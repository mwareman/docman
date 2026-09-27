package srv

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"docman/internal/dock"
	"docman/internal/wsx"
)

// ---------- container statistics ----------

// ContainerSample is one derived statistics reading for a container.
type ContainerSample struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	At         int64   `json:"t"`
	CPUPercent float64 `json:"cpu_percent"`
	CPUCores   float64 `json:"cpu_cores"`
	MemUsage   uint64  `json:"mem_usage"`
	MemLimit   uint64  `json:"mem_limit"`
	MemPercent float64 `json:"mem_percent"`
	BlkRead    uint64  `json:"blk_read"`
	BlkWrite   uint64  `json:"blk_write"`
	BlkReadPS  float64 `json:"blk_read_ps"`
	BlkWritePS float64 `json:"blk_write_ps"`
	NetRx      uint64  `json:"net_rx"`
	NetTx      uint64  `json:"net_tx"`
	NetRxPS    float64 `json:"net_rx_ps"`
	NetTxPS    float64 `json:"net_tx_ps"`
	Pids       uint64  `json:"pids"`
	OnlineCPUs float64 `json:"online_cpus"`
}

// sampleFromStats derives a reading from one raw docker stats object. Rates
// that need two readings are filled in by the collector.
func sampleFromStats(st *dock.Stats, id, name string) *ContainerSample {
	out := &ContainerSample{ID: id, Name: name, At: time.Now().UnixMilli(), Pids: st.PidsStats.Current}

	cpus := float64(st.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(st.CPUStats.CPUUsage.PercpuUsage))
	}
	if cpus == 0 {
		cpus = 1
	}
	out.OnlineCPUs = cpus

	cpuDelta := float64(st.CPUStats.CPUUsage.TotalUsage) - float64(st.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(st.CPUStats.SystemUsage) - float64(st.PreCPUStats.SystemUsage)
	if cpuDelta > 0 && sysDelta > 0 {
		out.CPUPercent = cpuDelta / sysDelta * cpus * 100
		out.CPUCores = cpuDelta / sysDelta * cpus
	}

	out.MemUsage = workingSet(st.MemoryStats)
	out.MemLimit = st.MemoryStats.Limit
	if out.MemLimit > 0 {
		out.MemPercent = float64(out.MemUsage) / float64(out.MemLimit) * 100
	}

	out.BlkRead, out.BlkWrite = blkTotals(st.BlkioStats)
	for _, n := range st.Networks {
		out.NetRx += n.RxBytes
		out.NetTx += n.TxBytes
	}
	return out
}

// workingSet subtracts reclaimable page cache so the figure matches what
// `docker stats` reports rather than raw cgroup usage.
func workingSet(m dock.MemoryStats) uint64 {
	usage := m.Usage
	// cgroup v2 names it inactive_file; v1 uses total_inactive_file.
	for _, key := range []string{"inactive_file", "total_inactive_file"} {
		if v, ok := m.Stats[key]; ok {
			if usage > v {
				return usage - v
			}
			return 0
		}
	}
	if v, ok := m.Stats["cache"]; ok && usage > v {
		return usage - v
	}
	return usage
}

func blkTotals(b dock.BlkioStats) (read, write uint64) {
	for _, e := range b.IoServiceBytesRecursive {
		switch strings.ToLower(e.Op) {
		case "read":
			read += e.Value
		case "write":
			write += e.Value
		}
	}
	return read, write
}

// statsHub keeps one docker stats stream open per running container while at
// least one client is watching, and caches the latest derived reading. Docker
// only reports a usable CPU percentage across two readings, so a persistent
// stream is the only way to get correct numbers.
type statsHub struct {
	dc   *dock.Client
	logf func(string, ...any)

	mu      sync.Mutex
	samples map[string]*ContainerSample
	active  map[string]bool
	refs    int
	cancel  context.CancelFunc
}

func newStatsHub(dc *dock.Client, logf func(string, ...any)) *statsHub {
	return &statsHub{
		dc:      dc,
		logf:    logf,
		samples: map[string]*ContainerSample{},
		active:  map[string]bool{},
	}
}

func (h *statsHub) acquire() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.refs++
	if h.refs == 1 {
		ctx, cancel := context.WithCancel(context.Background())
		h.cancel = cancel
		go h.supervise(ctx)
	}
}

func (h *statsHub) release() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.refs--
	if h.refs > 0 {
		return
	}
	h.refs = 0
	if h.cancel != nil {
		h.cancel()
		h.cancel = nil
	}
	h.samples = map[string]*ContainerSample{}
}

func (h *statsHub) supervise(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	h.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			h.scan(ctx)
		}
	}
}

func (h *statsHub) scan(ctx context.Context) {
	list, err := h.dc.ListContainers(ctx, false)
	if err != nil {
		return
	}
	live := map[string]bool{}
	for _, c := range list {
		live[c.ID] = true
		h.mu.Lock()
		started := h.active[c.ID]
		if !started {
			h.active[c.ID] = true
		}
		h.mu.Unlock()
		if !started {
			go h.collect(ctx, c.ID, c.Name())
		}
	}
	h.mu.Lock()
	for id := range h.samples {
		if !live[id] {
			delete(h.samples, id)
		}
	}
	h.mu.Unlock()
}

func (h *statsHub) collect(ctx context.Context, id, name string) {
	defer func() {
		h.mu.Lock()
		delete(h.active, id)
		h.mu.Unlock()
	}()
	body, err := h.dc.ContainerStatsStream(ctx, id)
	if err != nil {
		if ctx.Err() == nil && h.logf != nil {
			h.logf("stats: could not follow %s: %v", name, err)
		}
		return
	}
	defer body.Close()

	dec := json.NewDecoder(body)
	var prev *ContainerSample
	for {
		var raw dock.Stats
		if err := dec.Decode(&raw); err != nil {
			return
		}
		sample := sampleFromStats(&raw, id, name)
		if prev != nil {
			elapsed := float64(sample.At-prev.At) / 1000
			if elapsed > 0 {
				sample.BlkReadPS = rate(sample.BlkRead, prev.BlkRead, elapsed)
				sample.BlkWritePS = rate(sample.BlkWrite, prev.BlkWrite, elapsed)
				sample.NetRxPS = rate(sample.NetRx, prev.NetRx, elapsed)
				sample.NetTxPS = rate(sample.NetTx, prev.NetTx, elapsed)
			}
		}
		prev = sample
		h.mu.Lock()
		h.samples[id] = sample
		h.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
	}
}

func rate(cur, old uint64, elapsed float64) float64 {
	if cur < old || elapsed <= 0 {
		return 0
	}
	return float64(cur-old) / elapsed
}

func (h *statsHub) snapshot() []*ContainerSample {
	h.mu.Lock()
	out := make([]*ContainerSample, 0, len(h.samples))
	cutoff := time.Now().Add(-30 * time.Second).UnixMilli()
	for _, s := range h.samples {
		if s.At >= cutoff {
			out = append(out, s)
		}
	}
	h.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ---------- websocket plumbing ----------

func (s *Server) originAllowed(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser clients do not send Origin; bearer auth governs those.
		return true
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := r.Host
	if s.cfg.TrustProxy {
		if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
			host = strings.TrimSpace(fwd)
		}
	}
	return strings.EqualFold(u.Host, host)
}

// upgrade completes a WebSocket handshake, refusing cross-origin attempts.
func (s *Server) upgrade(w http.ResponseWriter, r *http.Request) (*wsx.Conn, bool) {
	if !s.originAllowed(r) {
		fail(w, http.StatusForbidden, "cross-origin websocket refused")
		return nil, false
	}
	conn, err := wsx.Upgrade(w, r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return conn, true
}

// drainClient reads and discards client messages so pings are answered and a
// browser closing the tab cancels the stream.
func drainClient(conn *wsx.Conn, cancel context.CancelFunc) {
	defer cancel()
	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			return
		}
	}
}

func (s *Server) statsInterval() time.Duration {
	ms := 2000
	if v := s.st.Setting("stats_interval_ms", "2000"); v != "" {
		if n, err := time.ParseDuration(v + "ms"); err == nil && n >= 500*time.Millisecond && n <= 30*time.Second {
			return n
		}
	}
	return time.Duration(ms) * time.Millisecond
}

// ---------- host metrics stream ----------

func (s *Server) handleHostStream(w http.ResponseWriter, r *http.Request) {
	conn, ok := s.upgrade(w, r)
	if !ok {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go drainClient(conn, cancel)
	stop := make(chan struct{})
	defer close(stop)
	go conn.Pinger(25*time.Second, stop)

	s.hub.acquire()
	defer s.hub.release()

	interval := s.statsInterval()
	// Prime the reader so the first message already carries real rates.
	s.hm.Sample()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			payload := map[string]any{
				"type":       "host",
				"containers": s.hub.snapshot(),
			}
			if s.hm.Available() {
				payload["host"] = s.hm.Sample()
			}
			if err := conn.WriteJSON(payload); err != nil {
				return
			}
		}
	}
}

// ---------- container list stream ----------

func (s *Server) handleContainersStream(w http.ResponseWriter, r *http.Request) {
	conn, ok := s.upgrade(w, r)
	if !ok {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go drainClient(conn, cancel)
	stop := make(chan struct{})
	defer close(stop)
	go conn.Pinger(25*time.Second, stop)

	s.hub.acquire()
	defer s.hub.release()

	interval := s.statsInterval()
	t := time.NewTicker(interval)
	defer t.Stop()
	send := func() bool {
		list, err := s.dc.ListContainers(ctx, true)
		if err != nil {
			return conn.WriteJSON(map[string]any{"type": "error", "error": err.Error()}) == nil
		}
		list = withoutHelpers(list)
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
		return conn.WriteJSON(map[string]any{
			"type":       "containers",
			"containers": views,
			"stats":      s.hub.snapshot(),
		}) == nil
	}
	if !send() {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if !send() {
				return
			}
		}
	}
}

// ---------- single container statistics ----------

func (s *Server) handleStatsStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	conn, ok := s.upgrade(w, r)
	if !ok {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go drainClient(conn, cancel)
	stop := make(chan struct{})
	defer close(stop)
	go conn.Pinger(25*time.Second, stop)

	body, err := s.dc.ContainerStatsStream(ctx, id)
	if err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": err.Error()})
		return
	}
	defer body.Close()

	dec := json.NewDecoder(body)
	var prev *ContainerSample
	for {
		var raw dock.Stats
		if err := dec.Decode(&raw); err != nil {
			if ctx.Err() == nil && !errors.Is(err, io.EOF) {
				_ = conn.WriteJSON(map[string]any{"type": "error", "error": err.Error()})
			}
			return
		}
		sample := sampleFromStats(&raw, id, strings.TrimPrefix(raw.Name, "/"))
		if prev != nil {
			if elapsed := float64(sample.At-prev.At) / 1000; elapsed > 0 {
				sample.BlkReadPS = rate(sample.BlkRead, prev.BlkRead, elapsed)
				sample.BlkWritePS = rate(sample.BlkWrite, prev.BlkWrite, elapsed)
				sample.NetRxPS = rate(sample.NetRx, prev.NetRx, elapsed)
				sample.NetTxPS = rate(sample.NetTx, prev.NetTx, elapsed)
			}
		}
		prev = sample
		if err := conn.WriteJSON(map[string]any{"type": "stats", "sample": sample}); err != nil {
			return
		}
	}
}

// ---------- log stream ----------

func (s *Server) handleLogStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tail := r.URL.Query().Get("tail")
	if tail == "" {
		tail = s.st.Setting("log_tail", "500")
	}
	timestamps := boolQuery(r, "timestamps")
	stdout := r.URL.Query().Get("stdout") != "0"
	stderr := r.URL.Query().Get("stderr") != "0"
	if !stdout && !stderr {
		stdout, stderr = true, true
	}

	inspect, err := s.dc.InspectContainer(r.Context(), id)
	if err != nil {
		failDocker(w, err)
		return
	}
	tty := containerHasTTY(inspect)

	conn, ok := s.upgrade(w, r)
	if !ok {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go drainClient(conn, cancel)
	stop := make(chan struct{})
	defer close(stop)
	go conn.Pinger(25*time.Second, stop)

	body, err := s.dc.ContainerLogs(ctx, id, dock.LogOptions{
		Follow: true, Stdout: stdout, Stderr: stderr, Timestamps: timestamps, Tail: tail,
	})
	if err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": err.Error()})
		return
	}
	defer body.Close()

	if tty {
		buf := make([]byte, 16<<10)
		for {
			n, err := body.Read(buf)
			if n > 0 {
				if err := conn.WriteJSON(map[string]any{"type": "log", "stream": 1, "data": string(buf[:n])}); err != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
	for {
		frame, err := dock.ReadFrame(body)
		if err != nil {
			return
		}
		if len(frame.Data) == 0 {
			continue
		}
		which := 1
		if frame.Stream == 2 {
			which = 2
		}
		if err := conn.WriteJSON(map[string]any{"type": "log", "stream": which, "data": string(frame.Data)}); err != nil {
			return
		}
	}
}

// ---------- docker event stream ----------

func (s *Server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	conn, ok := s.upgrade(w, r)
	if !ok {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go drainClient(conn, cancel)
	stop := make(chan struct{})
	defer close(stop)
	go conn.Pinger(25*time.Second, stop)

	body, err := s.dc.Events(ctx)
	if err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": err.Error()})
		return
	}
	defer body.Close()

	dec := json.NewDecoder(body)
	for {
		var event map[string]any
		if err := dec.Decode(&event); err != nil {
			return
		}
		if err := conn.WriteJSON(map[string]any{"type": "event", "event": event}); err != nil {
			return
		}
	}
}

// ---------- interactive console ----------

// defaultShell prefers bash when the image has it and falls back to sh, in a
// single exec so no probing round trips are needed.
var defaultShell = []string{"/bin/sh", "-c",
	"if command -v bash >/dev/null 2>&1; then exec bash; else exec /bin/sh; fi"}

type execControl struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

func (s *Server) handleExecStream(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cmd := defaultShell
	if raw := r.URL.Query().Get("cmd"); raw != "" {
		cmd = []string{"/bin/sh", "-c", raw}
	}
	if raw := r.URL.Query().Get("shell"); raw != "" {
		cmd = []string{raw}
	}
	user := r.URL.Query().Get("user")
	workdir := r.URL.Query().Get("workdir")
	cols := intQuery(r, "cols", 80)
	rows := intQuery(r, "rows", 24)

	conn, ok := s.upgrade(w, r)
	if !ok {
		return
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	execID, err := s.dc.ExecCreate(ctx, id, dock.ExecConfig{
		AttachStdin:  true,
		AttachStdout: true,
		AttachStderr: true,
		Tty:          true,
		Cmd:          cmd,
		Env:          []string{"TERM=xterm-256color", "COLUMNS=" + itoa(cols), "LINES=" + itoa(rows)},
		User:         user,
		WorkingDir:   workdir,
	})
	if err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": err.Error()})
		return
	}
	attached, err := s.dc.ExecStart(ctx, execID, true)
	if err != nil {
		_ = conn.WriteJSON(map[string]any{"type": "error", "error": err.Error()})
		return
	}
	defer attached.Close()
	_ = s.dc.ExecResize(ctx, execID, rows, cols)
	s.st.Audit(identityOf(r).Actor(), "container.console", id, strings.Join(cmd, " "), true)
	_ = conn.WriteJSON(map[string]any{"type": "ready", "exec_id": execID})

	// Container output travels as binary frames; control messages as text.
	go func() {
		defer cancel()
		buf := make([]byte, 32<<10)
		for {
			n, err := attached.Reader.Read(buf)
			if n > 0 {
				if err := conn.WriteBinary(buf[:n]); err != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		op, payload, err := conn.ReadMessage()
		if err != nil {
			cancel()
			break
		}
		if op == wsx.OpBinary {
			if _, err := attached.Conn.Write(payload); err != nil {
				break
			}
			continue
		}
		var ctrl execControl
		if err := json.Unmarshal(payload, &ctrl); err != nil {
			continue
		}
		switch ctrl.Type {
		case "in":
			if _, err := attached.Conn.Write([]byte(ctrl.Data)); err != nil {
				cancel()
				return
			}
		case "resize":
			if ctrl.Cols > 0 && ctrl.Rows > 0 {
				_ = s.dc.ExecResize(ctx, execID, ctrl.Rows, ctrl.Cols)
			}
		case "eof":
			_ = attached.CloseWrite()
		case "close":
			cancel()
			return
		}
	}

	// Report how the command finished so the UI can say so.
	if info, err := s.dc.ExecInspect(context.Background(), execID); err == nil {
		code, _ := info["ExitCode"].(float64)
		_ = conn.WriteJSON(map[string]any{"type": "exit", "code": int(code)})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
