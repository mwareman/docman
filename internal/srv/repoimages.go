package srv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"docman/internal/store"
)

// Images from a GitHub repository.
//
// A "GitHub repository" registry is not a container registry: it is a
// repository (on github.com or GitHub Enterprise) that holds pre-built image
// archives, made with docker save, either as files in the repository — often
// in a folder such as dist/ — or attached to its releases. DocMan lists those
// archives, downloads the one for this host's architecture and loads it into
// Docker. It never builds anything: a Dockerfile or compose file in the
// repository is reported, but only pre-built images can be installed.
//
// Each reference installed this way remembers where it came from, so the daily
// update check, Images → Pull and Recreate look in the repository for a newer
// version of the same archive.

const kindGitHubRepo = "github-repo"

// isArchiveKind reports whether a registry kind holds image archives rather
// than being a registry Docker can pull from.
func isArchiveKind(kind string) bool { return kind == kindGitHubRepo }

// repoArtifact is one pre-built image archive.
type repoArtifact struct {
	Key     string `json:"key"`     // "file:<path>" or "asset:<id>", what to install
	Name    string `json:"name"`    // the archive's name without version and architecture
	Version string `json:"version"` // from the file name, else the release tag; may be empty
	Arch    string `json:"arch"`    // amd64, arm64, armv7, armv6, 386, multiarch, or "" when unnamed
	Fits    bool   `json:"fits"`    // whether it runs on this host
	Source  string `json:"source"`  // "repository" or "release"
	File    string `json:"file"`    // the path in the repository, or <tag>/<asset name>
	Release string `json:"release,omitempty"`
	Size    int64  `json:"size"`
	FileID  string `json:"-"` // blob SHA, or asset id and date: changes when the content does

	path    string // repository path
	assetID int64
}

// repoListing is what a repository offers.
type repoListing struct {
	Repo       string          `json:"repo"`
	Ref        string          `json:"ref"`
	HostArch   string          `json:"host_arch"`
	Artifacts  []*repoArtifact `json:"artifacts"`
	BuildFiles []string        `json:"build_files"` // Dockerfiles and compose files, which DocMan does not build
	Truncated  bool            `json:"truncated,omitempty"`
}

// ---------- the repository ----------

type githubRepo struct {
	api    string // https://api.github.com, or https://<host>/api/v3
	web    string // github.com or the Enterprise host
	repo   string // owner/name
	ref    string // branch or tag; the default branch when empty
	subdir string // only look under this folder
	token  string
}

var repoNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// parseRepoOption accepts owner/name or a repository URL, returning the host
// when a URL names one.
func parseRepoOption(v string) (host, repo string) {
	v = strings.TrimSpace(v)
	if strings.Contains(v, "://") {
		if u, err := url.Parse(v); err == nil {
			host = strings.ToLower(u.Host)
			v = u.Path
		}
	}
	v = strings.Trim(strings.TrimSuffix(strings.Trim(v, "/"), ".git"), "/")
	parts := strings.Split(v, "/")
	if len(parts) >= 2 {
		v = parts[0] + "/" + parts[1]
	}
	return host, v
}

func (s *Server) githubRepoFor(reg *store.Registry) (*githubRepo, error) {
	opts := registryOptions(reg)
	_, repo := parseRepoOption(opts["repo"])
	if !repoNameRe.MatchString(repo) {
		return nil, errors.New("the registry has no repository set; edit it and enter owner/name")
	}
	g := &githubRepo{
		web:    reg.Host,
		repo:   repo,
		ref:    strings.TrimSpace(opts["ref"]),
		subdir: strings.Trim(strings.TrimSpace(opts["path"]), "/"),
		api:    strings.TrimRight(strings.TrimSpace(opts["api_url"]), "/"),
	}
	if g.api == "" {
		if reg.Host == "github.com" {
			g.api = "https://api.github.com"
		} else {
			g.api = "https://" + reg.Host + "/api/v3"
		}
	}
	if reg.AuthType != authNone {
		g.token = s.openSecret(reg)
	}
	return g, nil
}

func (g *githubRepo) request(ctx context.Context, rawURL, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "DocMan")
	if g.token != "" {
		req.Header.Set("Authorization", "Bearer "+g.token)
	}
	resp, err := externalClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach GitHub: %w", err)
	}
	if resp.StatusCode < 300 {
		return resp, nil
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	var msg struct {
		Message string `json:"message"`
	}
	_ = json.Unmarshal(body, &msg)
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errNotFoundAtRegistry
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, errors.New("GitHub refused the token; check it has not expired and can read the repository")
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return nil, errors.New("GitHub's rate limit for anonymous use was reached; add a token to this registry to raise it")
		}
		return nil, fmt.Errorf("GitHub refused: %s", strings.TrimSpace(msg.Message))
	}
	return nil, fmt.Errorf("GitHub answered %s %s", resp.Status, strings.TrimSpace(msg.Message))
}

func (g *githubRepo) getJSON(ctx context.Context, apiPath string, out any) error {
	resp, err := g.request(ctx, g.api+apiPath, "application/vnd.github+json")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 32<<20)).Decode(out)
}

func (g *githubRepo) notFound() error {
	if g.token == "" {
		return fmt.Errorf("GitHub has no repository %s, or it is private: add a token to this registry", g.repo)
	}
	return fmt.Errorf("GitHub has no repository %s that this token can read", g.repo)
}

// list finds the image archives in the repository's files and releases.
func (g *githubRepo) list(ctx context.Context) (*repoListing, error) {
	out := &repoListing{Repo: g.repo, Artifacts: []*repoArtifact{}, BuildFiles: []string{}}
	ref := g.ref
	if ref == "" {
		var info struct {
			DefaultBranch string `json:"default_branch"`
		}
		if err := g.getJSON(ctx, "/repos/"+g.repo, &info); err != nil {
			if errors.Is(err, errNotFoundAtRegistry) {
				return nil, g.notFound()
			}
			return nil, err
		}
		ref = info.DefaultBranch
	}
	out.Ref = ref

	var tree struct {
		Tree []struct {
			Path string `json:"path"`
			Type string `json:"type"`
			SHA  string `json:"sha"`
			Size int64  `json:"size"`
		} `json:"tree"`
		Truncated bool `json:"truncated"`
	}
	if err := g.getJSON(ctx, "/repos/"+g.repo+"/git/trees/"+url.PathEscape(ref)+"?recursive=1", &tree); err != nil {
		if errors.Is(err, errNotFoundAtRegistry) {
			if g.ref != "" {
				return nil, fmt.Errorf("%s has no branch or tag called %s", g.repo, g.ref)
			}
			return nil, g.notFound()
		}
		return nil, err
	}
	out.Truncated = tree.Truncated
	for _, e := range tree.Tree {
		if e.Type != "blob" {
			continue
		}
		if isBuildFile(path.Base(e.Path)) {
			out.BuildFiles = append(out.BuildFiles, e.Path)
		}
		if g.subdir != "" && !strings.HasPrefix(e.Path, g.subdir+"/") {
			continue
		}
		name, version, arch, ok := parseArchiveName(path.Base(e.Path))
		if !ok {
			continue
		}
		out.Artifacts = append(out.Artifacts, &repoArtifact{
			Key: "file:" + e.Path, Name: name, Version: version, Arch: arch, Source: "repository",
			File: e.Path, Size: e.Size, FileID: e.SHA, path: e.Path,
		})
	}

	// Release assets. A repository without releases, or one whose releases
	// cannot be listed, still offers its files.
	var releases []struct {
		TagName    string `json:"tag_name"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
		Assets     []struct {
			ID        int64  `json:"id"`
			Name      string `json:"name"`
			Size      int64  `json:"size"`
			UpdatedAt string `json:"updated_at"`
		} `json:"assets"`
	}
	if err := g.getJSON(ctx, "/repos/"+g.repo+"/releases?per_page=30", &releases); err == nil {
		for _, rel := range releases {
			if rel.Draft || rel.Prerelease {
				continue
			}
			for _, a := range rel.Assets {
				name, version, arch, ok := parseArchiveName(a.Name)
				if !ok {
					continue
				}
				if version == "" {
					version = rel.TagName
				}
				out.Artifacts = append(out.Artifacts, &repoArtifact{
					Key: "asset:" + strconv.FormatInt(a.ID, 10), Name: name, Version: version, Arch: arch,
					Source: "release", Release: rel.TagName, File: rel.TagName + "/" + a.Name, Size: a.Size,
					FileID: "asset:" + strconv.FormatInt(a.ID, 10) + ":" + a.UpdatedAt, assetID: a.ID,
				})
			}
		}
	}
	for _, a := range out.Artifacts {
		if a.Name == "" {
			a.Name = strings.ToLower(path.Base(g.repo))
		}
	}
	return out, nil
}

// download opens an archive for reading, with its size when known.
func (g *githubRepo) download(ctx context.Context, ref string, a *repoArtifact) (io.ReadCloser, int64, error) {
	if a.assetID != 0 {
		// GitHub answers with a redirect to its file storage; the token is not
		// sent on to the other host.
		resp, err := g.request(ctx, fmt.Sprintf("%s/repos/%s/releases/assets/%d", g.api, g.repo, a.assetID), "application/octet-stream")
		if err != nil {
			return nil, 0, err
		}
		return resp.Body, a.Size, nil
	}
	escaped := escapePath(a.path)
	resp, err := g.request(ctx, g.api+"/repos/"+g.repo+"/contents/"+escaped+"?ref="+url.QueryEscape(ref), "application/vnd.github.raw")
	if err != nil {
		return nil, 0, err
	}
	// A file kept in Git LFS comes back as a small pointer to the real content.
	br := bufio.NewReaderSize(resp.Body, 512)
	head, _ := br.Peek(200)
	if !strings.HasPrefix(string(head), "version https://git-lfs") {
		return readCloser{br, resp.Body}, a.Size, nil
	}
	pointer, _ := io.ReadAll(io.LimitReader(br, 1024))
	resp.Body.Close()
	var size int64
	for _, line := range strings.Split(string(pointer), "\n") {
		if v, ok := strings.CutPrefix(line, "size "); ok {
			size, _ = strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		}
	}
	if g.web != "github.com" {
		return nil, 0, errors.New("this archive is kept in Git LFS, which DocMan can only download from github.com; attach it to a release instead")
	}
	lfs, err := g.request(ctx, "https://media.githubusercontent.com/media/"+g.repo+"/"+url.PathEscape(ref)+"/"+escaped, "application/octet-stream")
	if err != nil {
		return nil, 0, fmt.Errorf("could not download the Git LFS file: %w", err)
	}
	return lfs.Body, size, nil
}

type readCloser struct {
	io.Reader
	io.Closer
}

func escapePath(p string) string {
	parts := strings.Split(p, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

// ---------- archive names ----------

var (
	archiveExt = []string{".tar.gz", ".tgz", ".tar"}
	versionRe  = regexp.MustCompile(`^[vV]?\d+(\.\d+)*$`)
	archNames  = map[string]string{
		"amd64": "amd64", "x86_64": "amd64", "x64": "amd64",
		"arm64": "arm64", "aarch64": "arm64",
		"armv7": "armv7", "armv7l": "armv7", "armhf": "armv7", "arm": "armv7",
		"armv6": "armv6", "armv6l": "armv6",
		"386": "386", "i386": "386", "i686": "386", "x86": "386",
		"multiarch": "multiarch", "multi": "multiarch", "all": "multiarch",
	}
)

// parseArchiveName reads docman-1.9.1-amd64.tar.gz as docman, 1.9.1, amd64.
// Names without a version or architecture are accepted too.
func parseArchiveName(file string) (name, version, arch string, ok bool) {
	lower := strings.ToLower(file)
	base := ""
	for _, ext := range archiveExt {
		if strings.HasSuffix(lower, ext) {
			base = lower[:len(lower)-len(ext)]
			break
		}
	}
	if base == "" {
		return "", "", "", false
	}
	parts := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '_' })
	if n := len(parts); n > 0 {
		if a, known := archNames[parts[n-1]]; known {
			arch = a
			parts = parts[:n-1]
			if n := len(parts); n > 0 && parts[n-1] == "linux" {
				parts = parts[:n-1]
			}
		}
	}
	if n := len(parts); n > 0 && versionRe.MatchString(parts[n-1]) {
		version = parts[n-1]
		parts = parts[:n-1]
	}
	return strings.Join(parts, "-"), version, arch, true
}

func isBuildFile(base string) bool {
	b := strings.ToLower(base)
	if b == "dockerfile" || strings.HasPrefix(b, "dockerfile.") || strings.HasSuffix(b, ".dockerfile") || b == "containerfile" {
		return true
	}
	return (strings.Contains(b, "compose") || strings.Contains(b, "docker-stack")) &&
		(strings.HasSuffix(b, ".yml") || strings.HasSuffix(b, ".yaml"))
}

// hostArch is this host's architecture in the names archives use.
func (s *Server) hostArch(ctx context.Context) string {
	info, err := s.dc.Info(ctx)
	if err != nil {
		return ""
	}
	switch strings.ToLower(info.Architecture) {
	case "x86_64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	case "armv7l", "armv7", "armhf":
		return "armv7"
	case "armv6l":
		return "armv6"
	case "i386", "i686", "386":
		return "386"
	}
	return strings.ToLower(info.Architecture)
}

// archRank orders archives for a host: its own architecture first, then
// unnamed ones, then multi-architecture ones, then those that will not run.
func archRank(arch, host string) int {
	switch {
	case arch == host && host != "":
		return 0
	case arch == "":
		return 1
	case arch == "multiarch":
		return 2
	}
	return 3
}

// listRepoImages lists a repository registry's archives, best first.
func (s *Server) listRepoImages(ctx context.Context, reg *store.Registry) (*repoListing, *githubRepo, error) {
	g, err := s.githubRepoFor(reg)
	if err != nil {
		return nil, nil, err
	}
	listing, err := g.list(ctx)
	if err != nil {
		return nil, nil, err
	}
	listing.HostArch = s.hostArch(ctx)
	for _, a := range listing.Artifacts {
		a.Fits = archRank(a.Arch, listing.HostArch) < 3
	}
	sort.SliceStable(listing.Artifacts, func(i, j int) bool {
		a, b := listing.Artifacts[i], listing.Artifacts[j]
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if c := compareVersions(a.Version, b.Version); c != 0 {
			return c > 0
		}
		if ra, rb := archRank(a.Arch, listing.HostArch), archRank(b.Arch, listing.HostArch); ra != rb {
			return ra < rb
		}
		return a.Source == "repository" && b.Source != "repository"
	})
	return listing, g, nil
}

// newestFor picks the newest archive that replaces what rec installed: the
// same name, for the same architecture.
func newestFor(listing *repoListing, rec *store.RepoImage) *repoArtifact {
	for _, a := range listing.Artifacts { // already newest first
		if a.Name != rec.Artifact || !a.Fits {
			continue
		}
		if rec.Arch != "" && a.Arch != rec.Arch {
			continue
		}
		return a
	}
	return nil
}

// isNewer reports whether a replaces what rec installed.
func isNewer(a *repoArtifact, rec *store.RepoImage) bool {
	if a.Version != rec.Version && compareVersions(a.Version, rec.Version) > 0 {
		return true
	}
	// Same version and file, new content: the archive was replaced in place.
	return a.Version == rec.Version && a.File == rec.File && a.FileID != rec.FileID
}

// pinnedTo reports whether ref's tag is the version itself, such as
// docman:1.9.1, which a newer archive does not move.
func pinnedTo(ref, version string) bool {
	if version == "" {
		return false
	}
	_, tag := splitImageTag(ref)
	return strings.TrimPrefix(strings.ToLower(tag), "v") == strings.TrimPrefix(strings.ToLower(version), "v")
}

// ---------- installing ----------

// installArtifact downloads one archive and loads it into Docker, returning
// the references it provided.
func (s *Server) installArtifact(ctx context.Context, reg *store.Registry, g *githubRepo, listing *repoListing,
	a *repoArtifact, actor string, progress func(string)) ([]string, error) {
	body, size, err := g.download(ctx, listing.Ref, a)
	if err != nil {
		s.st.Audit(actor, "image.install", g.repo+"/"+a.File, err.Error(), false)
		return nil, err
	}
	defer body.Close()
	progress(fmt.Sprintf("Downloading %s from %s", path.Base(a.File), g.repo))
	counted := &countingReader{r: body, total: size, report: func(pct int) {
		progress(fmt.Sprintf("Downloading and importing: %d%% of %s", pct, humanBytes(size)))
	}}
	loaded, err := s.loadArchive(ctx, counted)
	if err != nil {
		s.st.Audit(actor, "image.install", g.repo+"/"+a.File, err.Error(), false)
		return nil, err
	}
	for _, ref := range loaded {
		if strings.HasPrefix(ref, "sha256:") {
			continue
		}
		_ = s.st.MarkFromRepo(&store.RepoImage{
			Reference: normalizeImageRef(ref), RegistryID: reg.ID, Artifact: a.Name, Arch: a.Arch,
			Version: a.Version, File: a.File, FileID: a.FileID,
		})
		_ = s.st.DeleteImageCheck(normalizeImageRef(ref))
	}
	detail := "from " + g.repo + " " + a.File
	if a.Version != "" {
		detail += ", version " + a.Version
	}
	s.st.Audit(actor, "image.install", strings.Join(loaded, ", "), detail, true)
	progress("Imported " + strings.Join(loaded, ", "))
	return loaded, nil
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d bytes", n)
}

// updateFromRepo installs the newest archive for a reference installed from
// a repository. When nothing is installed, note says why: the host already
// has the newest, or the reference is pinned to its version.
func (s *Server) updateFromRepo(ctx context.Context, rec *store.RepoImage, actor string, progress func(string)) (updated bool, note string, err error) {
	reg, err := s.st.RegistryByID(rec.RegistryID)
	if err != nil {
		return false, "", fmt.Errorf("%s was installed from a repository registry that has since been removed", rec.Reference)
	}
	lctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	listing, g, err := s.listRepoImages(lctx, reg)
	cancel()
	if err != nil {
		return false, "", fmt.Errorf("could not list %s: %w", reg.Name, err)
	}
	a := newestFor(listing, rec)
	if a == nil {
		return false, "", fmt.Errorf("%s no longer offers %s for this host", g.repo, rec.Artifact)
	}
	if !isNewer(a, rec) {
		return false, fmt.Sprintf("Image is up to date for %s (version %s in %s)", rec.Reference, orUnnamed(a.Version), g.repo), nil
	}
	if pinnedTo(rec.Reference, rec.Version) {
		return false, fmt.Sprintf("%s is pinned to version %s, so it stays as it is; version %s is in %s. Change the image to use it",
			rec.Reference, rec.Version, a.Version, g.repo), nil
	}
	if _, err := s.installArtifact(ctx, reg, g, listing, a, actor, progress); err != nil {
		return false, "", err
	}
	return true, "", nil
}

// ---------- update checks ----------

// repoListingCache shares one listing per registry across a check run.
type repoListingCache struct {
	mu   sync.Mutex
	done map[int64]*cachedListing
}

type cachedListing struct {
	once    sync.Once
	listing *repoListing
	err     error
}

func (c *repoListingCache) get(ctx context.Context, s *Server, reg *store.Registry) (*repoListing, error) {
	c.mu.Lock()
	if c.done == nil {
		c.done = map[int64]*cachedListing{}
	}
	entry := c.done[reg.ID]
	if entry == nil {
		entry = &cachedListing{}
		c.done[reg.ID] = entry
	}
	c.mu.Unlock()
	entry.once.Do(func() {
		cctx, cancel := context.WithTimeout(ctx, 45*time.Second)
		defer cancel()
		entry.listing, _, entry.err = s.listRepoImages(cctx, reg)
	})
	return entry.listing, entry.err
}

// checkRepoImage looks in the repository for a newer archive of a reference.
func (s *Server) checkRepoImage(ctx context.Context, ref string, rec *store.RepoImage, cache *repoListingCache) *store.ImageCheck {
	check := &store.ImageCheck{Reference: ref, CheckedAt: time.Now().Unix(), LocalDigest: rec.Version}
	reg, err := s.st.RegistryByID(rec.RegistryID)
	if err != nil {
		check.Status = checkStatusNone
		check.Detail = "installed from a repository registry that has since been removed"
		return check
	}
	listing, err := cache.get(ctx, s, reg)
	if err != nil {
		check.Status = checkStatusNone
		check.Detail = "the repository could not be checked: " + err.Error()
		return check
	}
	a := newestFor(listing, rec)
	if a == nil {
		check.Status = checkStatusNone
		check.Detail = listing.Repo + " no longer offers " + rec.Artifact + " for this host"
		return check
	}
	check.RemoteDigest = a.Version
	switch {
	case !isNewer(a, rec):
		check.Status = checkStatusOK
		check.Detail = "the newest " + rec.Artifact + " in " + listing.Repo
	case pinnedTo(ref, rec.Version):
		// The tag names the version, so a newer archive would not move it.
		check.Status = checkStatusOK
		check.Detail = fmt.Sprintf("pinned to version %s; version %s is in %s", rec.Version, a.Version, listing.Repo)
	default:
		check.Status = checkStatusUpdate
		check.Detail = fmt.Sprintf("version %s is in %s (%s)", orUnnamed(a.Version), listing.Repo, a.File)
	}
	return check
}

func orUnnamed(v string) string {
	if v == "" {
		return "a new build"
	}
	return v
}

// ---------- endpoints ----------

func (s *Server) repoRegistry(w http.ResponseWriter, r *http.Request) (*store.Registry, bool) {
	id, ok := pathInt(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "invalid registry id")
		return nil, false
	}
	reg, err := s.st.RegistryByID(id)
	if err != nil {
		failStore(w, err)
		return nil, false
	}
	if !isArchiveKind(reg.Kind) {
		fail(w, http.StatusBadRequest, reg.Name+" is a container registry; pull from it instead")
		return nil, false
	}
	return reg, true
}

// handleRepoArchives lists the pre-built images a repository registry offers.
func (s *Server) handleRepoArchives(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.repoRegistry(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	listing, _, err := s.listRepoImages(ctx, reg)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, listing)
}

// ndjsonWriter sends progress lines in the shape of Docker's pull output, so
// the UI shows an install the way it shows a pull.
type ndjsonWriter struct {
	w       http.ResponseWriter
	flusher http.Flusher
	mu      sync.Mutex
}

func newNDJSON(w http.ResponseWriter) *ndjsonWriter {
	h := w.Header()
	h.Set("Content-Type", "application/x-ndjson")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	f, _ := w.(http.Flusher)
	return &ndjsonWriter{w: w, flusher: f}
}

func (n *ndjsonWriter) send(v any) {
	n.mu.Lock()
	defer n.mu.Unlock()
	raw, _ := json.Marshal(v)
	_, _ = n.w.Write(append(raw, '\n'))
	if n.flusher != nil {
		n.flusher.Flush()
	}
}

func (n *ndjsonWriter) status(text string) { n.send(map[string]string{"status": text}) }

// handleRepoInstall downloads one archive from a repository registry and
// loads it, streaming progress.
func (s *Server) handleRepoInstall(w http.ResponseWriter, r *http.Request) {
	reg, ok := s.repoRegistry(w, r)
	if !ok {
		return
	}
	var req struct {
		Key string `json:"key"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	lctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	listing, g, err := s.listRepoImages(lctx, reg)
	cancel()
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	var chosen *repoArtifact
	for _, a := range listing.Artifacts {
		if a.Key == req.Key {
			chosen = a
		}
	}
	if chosen == nil {
		fail(w, http.StatusNotFound, "the repository no longer has that archive; reload the list")
		return
	}
	out := newNDJSON(w)
	loaded, err := s.installArtifact(r.Context(), reg, g, listing, chosen, identityOf(r).Actor(), out.status)
	if err != nil {
		out.send(map[string]string{"error": err.Error()})
		return
	}
	out.send(map[string]any{"loaded": loaded})
}

// streamRepoUpdate is Images → Pull for a reference installed from a
// repository: it installs the newest archive, if there is a newer one.
func (s *Server) streamRepoUpdate(w http.ResponseWriter, r *http.Request, rec *store.RepoImage) {
	out := newNDJSON(w)
	actor := identityOf(r).Actor()
	updated, note, err := s.updateFromRepo(r.Context(), rec, actor, out.status)
	if err != nil {
		out.send(map[string]string{"error": err.Error()})
		return
	}
	if !updated {
		out.status(note)
	}
	s.recheckAfterPull(rec.Reference)
}
