package srv

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"docman/internal/dock"
)

// The status monitor.
//
// Listing containers (with update detection), images (with what uses them) and
// volumes, and measuring disk usage, takes Docker long enough that building the
// Overview, Containers and Images pages from scratch on every visit was slow.
// So one background goroutine keeps a snapshot of all of it up to date: it
// refreshes when Docker reports a change (the event stream) and at least every
// few seconds regardless. The snapshot is written to <data>/status.json whenever
// it changes, REST handlers answer from it, and WebSocket subscribers are told
// the moment a new one is published.
//
// A change made through the API marks the snapshot dirty, so the next reader
// rebuilds it first and never sees its own change missing.

const (
	statusFileName  = "status.json"
	statusTick      = 10 * time.Second
	storageTick     = 60 * time.Second
	statusDebounce  = 250 * time.Millisecond
	storageDebounce = 3 * time.Second
)

type statusCounts struct {
	Containers int `json:"containers"`
	Running    int `json:"running"`
	Images     int `json:"images"`
	Volumes    int `json:"volumes"`
}

type storageUsage struct {
	LayersBytes  int64 `json:"layers_bytes"`
	ImagesBytes  int64 `json:"images_bytes"`
	VolumesBytes int64 `json:"volumes_bytes"`
	ImageCount   int   `json:"image_count"`
	VolumeCount  int   `json:"volume_count"`
}

// statusSnapshot is what status.json holds. Published snapshots are never
// modified, so readers may share them.
type statusSnapshot struct {
	Version    int64            `json:"version"`
	UpdatedAt  time.Time        `json:"updated_at"`
	Counts     statusCounts     `json:"counts"`
	Storage    *storageUsage    `json:"storage,omitempty"`
	Containers []*containerView `json:"containers"`
	Images     []imageView      `json:"images"`
}

type monitor struct {
	s    *Server
	path string

	refreshMu sync.Mutex // one rebuild at a time
	dirty     atomic.Bool

	mu      sync.RWMutex
	snap    *statusSnapshot
	sum     [sha256.Size]byte // of the snapshot's content, without version and time
	storage *storageUsage

	subMu sync.Mutex
	subs  map[chan struct{}]struct{}

	kick        chan struct{}
	storageKick chan struct{}
	lastErr     string
}

func newMonitor(s *Server) *monitor {
	m := &monitor{
		s:           s,
		subs:        map[chan struct{}]struct{}{},
		kick:        make(chan struct{}, 1),
		storageKick: make(chan struct{}, 1),
	}
	if s.cfg.DataDir != "" {
		m.path = filepath.Join(s.cfg.DataDir, statusFileName)
	}
	m.dirty.Store(true)
	return m
}

// invalidate says something may have changed, so the next reader rebuilds the
// snapshot, and the background loop does too.
func (m *monitor) invalidate() {
	m.dirty.Store(true)
	nudge(m.kick)
}

func nudge(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// current returns an up-to-date snapshot: the published one, unless a change is
// pending, in which case it is rebuilt first.
func (m *monitor) current(ctx context.Context) (*statusSnapshot, error) {
	if snap := m.peek(); snap != nil && !m.dirty.Load() {
		return snap, nil
	}
	return m.refresh(ctx, false)
}

func (m *monitor) peek() *statusSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.snap
}

// subscribe returns a channel that receives a value whenever a new snapshot is
// published, and a function to stop listening.
func (m *monitor) subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	m.subMu.Lock()
	m.subs[ch] = struct{}{}
	m.subMu.Unlock()
	return ch, func() {
		m.subMu.Lock()
		delete(m.subs, ch)
		m.subMu.Unlock()
	}
}

func (m *monitor) notify() {
	m.subMu.Lock()
	defer m.subMu.Unlock()
	for ch := range m.subs {
		nudge(ch)
	}
}

// refresh rebuilds the snapshot from Docker. Unless force is set, it does
// nothing when another caller already rebuilt it while this one waited.
func (m *monitor) refresh(ctx context.Context, force bool) (*statusSnapshot, error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	if snap := m.peek(); !force && snap != nil && !m.dirty.Load() {
		return snap, nil
	}
	// Cleared before reading Docker, so a change that lands during the read
	// leaves it set and is picked up by the next one.
	m.dirty.Store(false)
	clearRecentLists()
	next, err := m.build(ctx)
	if err != nil {
		m.dirty.Store(true)
		return nil, err
	}
	m.publish(next)
	return m.peek(), nil
}

func (m *monitor) build(ctx context.Context) (*statusSnapshot, error) {
	s := m.s
	var (
		wg               sync.WaitGroup
		list             []*dock.ContainerSummary
		images           []*dock.ImageSummary
		volumes          []*dock.Volume
		errC, errI, errV error
	)
	wg.Add(3)
	go func() { defer wg.Done(); list, errC = s.dc.ListContainers(ctx, true) }()
	go func() { defer wg.Done(); images, errI = s.dc.ListImages(ctx, false) }()
	go func() { defer wg.Done(); volumes, errV = s.dc.ListVolumes(ctx) }()
	wg.Wait()
	for _, err := range []error{errC, errI, errV} {
		if err != nil {
			return nil, err
		}
	}
	list = withoutHelpers(list)

	views := s.containerViews(ctx, list)
	running := 0
	for _, c := range list {
		if c.State == "running" {
			running++
		}
	}
	m.mu.RLock()
	storage := m.storage
	m.mu.RUnlock()
	return &statusSnapshot{
		Counts:     statusCounts{Containers: len(list), Running: running, Images: len(images), Volumes: len(volumes)},
		Storage:    storage,
		Containers: views,
		Images:     s.imageViews(images, list),
	}, nil
}

// publish makes next the current snapshot if its content differs from the
// current one, writes it to disk, and tells the subscribers. It reports whether
// anything changed.
func (m *monitor) publish(next *statusSnapshot) bool {
	body, err := json.Marshal(next)
	if err != nil {
		return false
	}
	sum := sha256.Sum256(body)
	m.mu.Lock()
	if m.snap != nil && sum == m.sum {
		m.mu.Unlock()
		return false
	}
	version := int64(1)
	if m.snap != nil {
		version = m.snap.Version + 1
	}
	next.Version = version
	next.UpdatedAt = time.Now().UTC()
	m.snap, m.sum = next, sum
	m.mu.Unlock()

	m.writeFile(next)
	m.notify()
	return true
}

// writeFile replaces status.json atomically, so a reader never sees half a file.
func (m *monitor) writeFile(snap *statusSnapshot) {
	if m.path == "" {
		return
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(snap); err != nil {
		return
	}
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, buf.Bytes(), 0o600); err != nil {
		m.s.logf("status: could not write %s: %v", m.path, err)
		return
	}
	if err := os.Rename(tmp, m.path); err != nil {
		m.s.logf("status: could not replace %s: %v", m.path, err)
		_ = os.Remove(tmp)
	}
}

// refreshStorage measures disk usage, which Docker takes a while over, and
// folds it into the snapshot when it has changed.
func (m *monitor) refreshStorage(ctx context.Context) {
	df, err := m.s.dc.DiskUsage(ctx)
	if err != nil {
		return
	}
	u := &storageUsage{LayersBytes: df.LayersSize, ImageCount: len(df.Images), VolumeCount: len(df.Volumes)}
	for _, img := range df.Images {
		u.ImagesBytes += img.Size
	}
	for _, v := range df.Volumes {
		if v.UsageData != nil && v.UsageData.Size > 0 {
			u.VolumesBytes += v.UsageData.Size
		}
	}
	m.mu.Lock()
	changed := m.storage == nil || *m.storage != *u
	m.storage = u
	m.mu.Unlock()
	if changed {
		nudge(m.kick)
	}
}

// storageNow is the last disk usage measured, or nil before the first.
func (m *monitor) storageNow() *storageUsage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.storage
}

// Run keeps the snapshot current until ctx ends.
func (m *monitor) Run(ctx context.Context) {
	go m.watchEvents(ctx)
	go m.storageLoop(ctx)

	rebuild := func() {
		rctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		_, err := m.refresh(rctx, true)
		switch {
		case err != nil && err.Error() != m.lastErr:
			m.lastErr = err.Error()
			m.s.logf("status: could not refresh: %v", err)
		case err == nil:
			m.lastErr = ""
		}
	}
	rebuild()
	t := time.NewTicker(statusTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			rebuild()
		case <-m.kick:
			// Events arrive in bursts (a recreate is a dozen); let them settle.
			if !sleepCtx(ctx, statusDebounce) {
				return
			}
			select {
			case <-m.kick:
			default:
			}
			rebuild()
		}
	}
}

func (m *monitor) storageLoop(ctx context.Context) {
	m.refreshStorage(ctx)
	t := time.NewTicker(storageTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refreshStorage(ctx)
		case <-m.storageKick:
			if !sleepCtx(ctx, storageDebounce) {
				return
			}
			select {
			case <-m.storageKick:
			default:
			}
			m.refreshStorage(ctx)
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// watchEvents follows Docker's event stream and refreshes on anything that can
// change what the snapshot shows.
func (m *monitor) watchEvents(ctx context.Context) {
	for ctx.Err() == nil {
		body, err := m.s.dc.Events(ctx)
		if err == nil {
			dec := json.NewDecoder(body)
			for {
				var ev struct {
					Type   string
					Action string
				}
				if dec.Decode(&ev) != nil {
					break
				}
				if ev.Type == "container" && (strings.HasPrefix(ev.Action, "exec_") || ev.Action == "attach" || ev.Action == "resize" || ev.Action == "top") {
					continue
				}
				switch ev.Type {
				case "container", "image", "volume":
					m.invalidate()
				}
				if ev.Type == "image" || ev.Type == "volume" || ev.Action == "destroy" {
					nudge(m.storageKick)
				}
			}
			body.Close()
		}
		if !sleepCtx(ctx, 3*time.Second) {
			return
		}
		// The stream was down, so changes may have been missed.
		m.invalidate()
	}
}

// ---------- builders shared with the handlers ----------

// containerViews turns Docker's container list into the views the UI shows,
// with update flags, running containers first.
func (s *Server) containerViews(ctx context.Context, list []*dock.ContainerSummary) []*containerView {
	pins := s.pinMap()
	selfID := s.selfID()
	views := make([]*containerView, 0, len(list))
	for _, c := range list {
		views = append(views, s.toView(c, pins, selfID))
	}
	s.markUpdates(views, s.updatesFor(ctx, list))
	sort.Slice(views, func(i, j int) bool {
		if (views[i].State == "running") != (views[j].State == "running") {
			return views[i].State == "running"
		}
		return strings.ToLower(views[i].Name) < strings.ToLower(views[j].Name)
	})
	return views
}
