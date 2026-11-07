package srv

import (
	"context"
	"strings"
	"sync"
	"time"

	"docman/internal/dock"
)

// Update detection.
//
// A container runs the exact image it was created from. When a newer image
// arrives under the same tag — uploaded as an archive, loaded on the host, or
// pulled — the tag moves on and the container is left on the old one until it
// is recreated. That is what "update available" means: the image the
// container's tag names now is newer than the one it runs.

// imageRefs remembers each container's configured image reference. It never
// changes for the life of a container ID, so it is inspected once, not on
// every refresh of the live list.
type imageRefs struct {
	mu   sync.Mutex
	refs map[string]string
}

func (c *imageRefs) get(id string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ref, ok := c.refs[id]
	return ref, ok
}

func (c *imageRefs) put(id, ref string, live map[string]bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.refs == nil {
		c.refs = map[string]string{}
	}
	c.refs[id] = ref
	// Forget containers that no longer exist so the cache cannot grow forever.
	// Only a full list says which those are.
	if live != nil && len(c.refs) > 2*len(live)+32 {
		for known := range c.refs {
			if !live[known] {
				delete(c.refs, known)
			}
		}
	}
}

var containerImageRefs imageRefs

// listCache holds a Docker list for a couple of seconds, so a burst of requests
// (page load, the live stream, the sidebar counts, several open tabs) shares one
// call. Any change made through the API clears it (see Server.route), so the
// UI never shows its own change as stale; only changes made outside DocMan can
// lag, by at most listTTL.
type listCache[T any] struct {
	mu    sync.Mutex
	at    time.Time
	items []T
	ok    bool
}

const listTTL = 3 * time.Second

func (c *listCache[T]) get(fetch func() ([]T, error)) ([]T, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ok && time.Since(c.at) < listTTL {
		return c.items, nil
	}
	items, err := fetch()
	if err != nil {
		return nil, err
	}
	c.items, c.at, c.ok = items, time.Now(), true
	return items, nil
}

func (c *listCache[T]) clear() {
	c.mu.Lock()
	c.ok = false
	c.mu.Unlock()
}

// recentImages is the list of images in use or tagged (all=false);
// recentContainers is every container, stopped ones included. Callers must
// treat both as read-only.
var (
	recentImages     listCache[*dock.ImageSummary]
	recentContainers listCache[*dock.ContainerSummary]
)

func clearRecentLists() {
	recentImages.clear()
	recentContainers.clear()
}

func cachedImages(ctx context.Context, dc *dock.Client) ([]*dock.ImageSummary, error) {
	return recentImages.get(func() ([]*dock.ImageSummary, error) { return dc.ListImages(ctx, false) })
}

func cachedContainers(ctx context.Context, dc *dock.Client) ([]*dock.ContainerSummary, error) {
	return recentContainers.get(func() ([]*dock.ContainerSummary, error) { return dc.ListContainers(ctx, true) })
}

// imageUpdate is what updatesFor found for one container: either a newer image
// on this host under its tag, or (runsTagged) that it runs exactly the image
// its tag names, so a newer version in the registry would be an update.
type imageUpdate struct {
	Ref        string // the tag, as the container names it
	ImageID    string // the image the tag now points at
	runsTagged bool
}

// updatesFor works out which of the listed containers have an update waiting.
func (s *Server) updatesFor(ctx context.Context, list []*dock.ContainerSummary) map[string]imageUpdate {
	out := map[string]imageUpdate{}
	images, err := cachedImages(ctx, s.dc)
	if err != nil {
		return out
	}
	byTag := map[string]*dock.ImageSummary{}
	byID := map[string]*dock.ImageSummary{}
	for _, img := range images {
		byID[img.ID] = img
		for _, tag := range img.RepoTags {
			if tag != "" && tag != "<none>:<none>" {
				byTag[normalizeImageRef(tag)] = img
			}
		}
	}
	var live map[string]bool
	if len(list) > 1 {
		live = map[string]bool{}
		for _, c := range list {
			live[c.ID] = true
		}
	}

	// Inspect the containers not seen before in parallel: on a cold cache this
	// is one call per container, and doing them in turn made the first load slow.
	var unknown []*dock.ContainerSummary
	for _, c := range list {
		if _, ok := containerImageRefs.get(c.ID); !ok {
			unknown = append(unknown, c)
		}
	}
	if len(unknown) > 0 {
		var wg sync.WaitGroup
		sem := make(chan struct{}, 8)
		for _, c := range unknown {
			wg.Add(1)
			sem <- struct{}{}
			go func(c *dock.ContainerSummary) {
				defer wg.Done()
				defer func() { <-sem }()
				inspect, err := s.dc.InspectContainer(ctx, c.ID)
				if err != nil {
					return
				}
				var ref string
				if cfg, ok := inspect["Config"].(map[string]any); ok {
					ref, _ = cfg["Image"].(string)
				}
				containerImageRefs.put(c.ID, ref, live)
			}(c)
		}
		wg.Wait()
	}

	for _, c := range list {
		ref, ok := containerImageRefs.get(c.ID)
		if !ok {
			continue
		}
		// A container created from an image ID or a digest is pinned to that
		// exact image; nothing can be newer under it.
		if ref == "" || strings.HasPrefix(ref, "sha256:") || strings.Contains(ref, "@") {
			continue
		}
		tagged := byTag[normalizeImageRef(ref)]
		if tagged != nil && tagged.ID == c.ImageID {
			out[c.ID] = imageUpdate{Ref: ref, runsTagged: true}
			continue
		}
		if tagged == nil {
			continue
		}
		// The tag points elsewhere. Only call it an update when that image is
		// newer, not when an older version was deliberately tagged back.
		if running := byID[c.ImageID]; running != nil && tagged.Created < running.Created {
			continue
		}
		out[c.ID] = imageUpdate{Ref: ref, ImageID: tagged.ID}
	}
	return out
}

// markUpdates fills in the update fields of the views: a newer image already
// on this host (from updatesFor), or failing that a newer version waiting in
// the image's registry (from the daily check). It also shows each container's
// image by the name it was created with — once the tag has moved on, Docker's
// list reports the old image's ID instead — and says where that image came
// from, which decides whether a recreate pulls first.
func (s *Server) markUpdates(views []*containerView, updates map[string]imageUpdate) {
	checks := s.st.ImageChecks()
	uploaded := s.st.UploadedRefs()
	fromRepo := s.st.RepoImages()
	for _, v := range views {
		ref, known := containerImageRefs.get(v.ID)
		if known && ref != "" && strings.HasPrefix(v.Image, "sha256:") {
			v.Image = ref
		}
		if !known || ref == "" || strings.HasPrefix(ref, "sha256:") || strings.Contains(ref, "@") {
			v.ImageOrigin = originLocal
			continue
		}
		v.ImageOrigin = imageOrigin(ref, checks, uploaded, fromRepo)
		u, ok := updates[v.ID]
		switch {
		case ok && !u.runsTagged:
			// A newer image is already on this host; a recreate switches to it.
			v.UpdateAvailable = true
			v.UpdateImage = u.Ref
			v.UpdateSource = "host"
		case ok && u.runsTagged:
			// The container runs the image its tag names here. If the registry
			// has moved that tag on, a pull and a recreate would update it.
			if c := checks[normalizeImageRef(ref)]; c != nil && c.Status == checkStatusUpdate {
				v.UpdateAvailable = true
				v.UpdateImage = ref
				v.UpdateSource = "registry"
				if fromRepo[normalizeImageRef(ref)] != nil {
					v.UpdateSource = originRepo
				}
			}
		}
	}
}
