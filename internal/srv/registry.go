package srv

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"docman/internal/dock"
	"docman/internal/store"
)

// Registry update checks.
//
// For every image tag that came from a registry, DocMan asks the registry —
// through the Engine, which downloads nothing — which digest the tag points at
// now, and compares it with the digest recorded when the image was pulled. A
// difference means a newer version is waiting in the registry. The check runs
// once a day and whenever someone asks for it.
//
// Images uploaded as archives are not checked: they usually exist in no
// registry. Images with no registry digest at all, such as ones built on the
// host, are recorded as not from a registry.

const (
	checkEvery        = 24 * time.Hour
	settingLastCheck  = "image_check_at"
	checkStatusUpdate = "update"
	checkStatusOK     = "current"
	checkStatusNone   = "unavailable"
)

// Image origins, as the UI shows them.
const (
	originRegistry = "registry"   // pulled from a registry, which can be checked
	originUploaded = "uploaded"   // imported from an archive through DocMan
	originRepo     = "repository" // installed from a GitHub repository's pre-built archive
	originLocal    = "local"      // on the host with no registry to check against
	originUnknown  = "unknown"    // not checked yet
)

var checkMu sync.Mutex

// ImageUpdateChecker runs the daily check. A restart does not trigger an extra
// check: the next one is due a day after the last, whenever DocMan was running.
func (s *Server) ImageUpdateChecker(ctx context.Context) {
	for {
		last, _ := strconv.ParseInt(s.st.Setting(settingLastCheck, "0"), 10, 64)
		wait := time.Until(time.Unix(last, 0).Add(checkEvery))
		if wait < 2*time.Minute {
			// Give the host a moment after DocMan starts before calling out.
			wait = 2 * time.Minute
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		results := s.checkImages(ctx, "")
		updates := 0
		for _, c := range results {
			if c.Status == checkStatusUpdate {
				updates++
			}
		}
		s.logf("image update check: %d tags checked, %d with a newer version in their registry", len(results), updates)
	}
}

// checkImages checks every image tag on the host, or only the one given, and
// records the results.
func (s *Server) checkImages(ctx context.Context, only string) []*store.ImageCheck {
	checkMu.Lock()
	defer checkMu.Unlock()

	images, err := s.dc.ListImages(ctx, false)
	if err != nil {
		return nil
	}
	uploaded := s.st.UploadedRefs()
	fromRepo := s.st.RepoImages()
	cache := &repoListingCache{}
	want := normalizeImageRef(only)

	type job struct {
		ref string
		img *dock.ImageSummary
	}
	var jobs []job
	present := map[string]bool{}
	for _, img := range images {
		for _, tag := range img.RepoTags {
			if tag == "" || tag == "<none>:<none>" {
				continue
			}
			ref := normalizeImageRef(tag)
			present[ref] = true
			if only != "" && ref != want {
				continue
			}
			if uploaded[ref] {
				// An archive upload has no registry to ask; drop any old result.
				_ = s.st.DeleteImageCheck(ref)
				continue
			}
			jobs = append(jobs, job{ref: ref, img: img})
		}
	}

	results := make([]*store.ImageCheck, len(jobs))
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, j := range jobs {
		wg.Add(1)
		go func(i int, j job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if rec := fromRepo[j.ref]; rec != nil {
				results[i] = s.checkRepoImage(ctx, j.ref, rec, cache)
			} else {
				results[i] = s.checkImage(ctx, j.ref, j.img)
			}
			_ = s.st.SaveImageCheck(results[i])
		}(i, j)
	}
	wg.Wait()

	if only == "" {
		// Forget tags whose images have gone from the host.
		for ref := range s.st.ImageChecks() {
			if !present[ref] {
				_ = s.st.DeleteImageCheck(ref)
			}
		}
		for ref := range fromRepo {
			if !present[ref] {
				_ = s.st.ClearFromRepo(ref)
			}
		}
		_ = s.st.SetSetting(settingLastCheck, strconv.FormatInt(time.Now().Unix(), 10))
	}
	return results
}

// checkImage asks the registry about one tag.
func (s *Server) checkImage(ctx context.Context, ref string, img *dock.ImageSummary) *store.ImageCheck {
	check := &store.ImageCheck{Reference: ref, CheckedAt: time.Now().Unix()}
	local := localDigests(img)
	if len(local) == 0 {
		check.Status = checkStatusNone
		check.Detail = "not from a registry: this image was built or loaded on the host"
		return check
	}
	check.LocalDigest = local[0]

	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	remote, err := s.dc.RegistryDigest(cctx, ref, s.authFor(cctx, ref))
	if err != nil {
		check.Status = checkStatusNone
		check.Detail = registryProblem(err)
		return check
	}
	check.RemoteDigest = remote
	check.Status = checkStatusUpdate
	for _, d := range local {
		if d == remote {
			check.Status = checkStatusOK
			break
		}
	}
	return check
}

// localDigests are the registry digests recorded for an image when it was
// pulled, without the repository names in front.
func localDigests(img *dock.ImageSummary) []string {
	var out []string
	for _, rd := range img.RepoDigests {
		if _, digest, ok := strings.Cut(rd, "@"); ok && digest != "" {
			out = append(out, digest)
		}
	}
	return out
}

// notInRegistry starts the detail of a check whose registry says it has no
// such image (or will not say without credentials). Such an image is treated
// like one built on the host: there is nothing DocMan could pull.
const notInRegistry = "not in a registry DocMan can reach"

func registryProblem(err error) string {
	var de *dock.Error
	if errors.As(err, &de) {
		switch de.Status {
		case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
			return notInRegistry + ": the registry does not have this image, or needs credentials DocMan does not keep"
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "the registry did not answer in time"
	}
	return "the registry could not be reached: " + err.Error()
}

// imageOrigin says where the image behind a reference came from, which decides
// whether a recreate should pull first.
func imageOrigin(ref string, checks map[string]*store.ImageCheck, uploaded map[string]bool, fromRepo map[string]*store.RepoImage) string {
	ref = normalizeImageRef(ref)
	if uploaded[ref] {
		return originUploaded
	}
	if fromRepo[ref] != nil {
		return originRepo
	}
	c, ok := checks[ref]
	switch {
	case !ok:
		return originUnknown
	case c.Status == checkStatusNone &&
		(strings.HasPrefix(c.Detail, "not from a registry") || strings.HasPrefix(c.Detail, notInRegistry)):
		return originLocal
	}
	// Up to date, updatable, or a registry that was only unreachable for now:
	// the image comes from a registry, so pulling it is worth offering.
	return originRegistry
}

// ---------- endpoints ----------

func (s *Server) imageCheckSummary() map[string]any {
	last, _ := strconv.ParseInt(s.st.Setting(settingLastCheck, "0"), 10, 64)
	checks := s.st.ImageChecks()
	list := make([]*store.ImageCheck, 0, len(checks))
	for _, c := range checks {
		list = append(list, c)
	}
	next := int64(0)
	if last > 0 {
		next = last + int64(checkEvery.Seconds())
	}
	return map[string]any{"checked_at": last, "next_check_at": next, "checks": list}
}

func (s *Server) handleImageUpdates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.imageCheckSummary())
}

// handleCheckImageUpdates runs the check now, for every image or one reference.
func (s *Server) handleCheckImageUpdates(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reference string `json:"reference"`
	}
	if r.ContentLength > 0 && !decodeBody(w, r, &req) {
		return
	}
	results := s.checkImages(r.Context(), strings.TrimSpace(req.Reference))
	updates := 0
	for _, c := range results {
		if c != nil && c.Status == checkStatusUpdate {
			updates++
		}
	}
	target := req.Reference
	if target == "" {
		target = "all images"
	}
	s.st.Audit(identityOf(r).Actor(), "image.check", target,
		strconv.Itoa(len(results))+" tags checked, "+strconv.Itoa(updates)+" with a newer version", true)
	writeJSON(w, http.StatusOK, s.imageCheckSummary())
}

// recheckAfterPull refreshes one tag's result once a pull has changed it.
func (s *Server) recheckAfterPull(ref string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		s.checkImages(ctx, ref)
	}()
}
