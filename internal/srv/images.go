package srv

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"docman/internal/dock"
	"docman/internal/store"
)

func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request) {
	images, err := s.dc.ListImages(r.Context(), boolQuery(r, "all"))
	if err != nil {
		failDocker(w, err)
		return
	}
	// Which images are in use, so the UI can warn before a delete.
	inUse := map[string]int{}
	usedBy := map[string][]string{}
	if containers, err := s.dc.ListContainers(r.Context(), true); err == nil {
		for _, c := range withoutHelpers(containers) {
			inUse[c.ImageID]++
			usedBy[c.ImageID] = append(usedBy[c.ImageID], c.Name())
		}
	}
	type view struct {
		*dock.ImageSummary
		InUse     int      `json:"in_use"`
		InUseBy   []string `json:"in_use_by"`
		Reference string   `json:"reference"`
		// Origin is where the image came from: registry, uploaded, local or
		// unknown (not checked yet). Update is the latest registry check of
		// its reference, when there is one.
		Origin string            `json:"origin"`
		Update *store.ImageCheck `json:"update,omitempty"`
	}
	checks := s.st.ImageChecks()
	uploaded := s.st.UploadedRefs()
	fromRepo := s.st.RepoImages()
	out := make([]view, 0, len(images))
	for _, img := range images {
		ref := "<none>"
		if len(img.RepoTags) > 0 && img.RepoTags[0] != "<none>:<none>" {
			ref = img.RepoTags[0]
		} else if len(img.RepoDigests) > 0 {
			ref = img.RepoDigests[0]
		}
		if img.RepoTags == nil {
			img.RepoTags = []string{}
		}
		names := usedBy[img.ID]
		sort.Strings(names)
		if names == nil {
			names = []string{}
		}
		v := view{ImageSummary: img, InUse: inUse[img.ID], InUseBy: names, Reference: ref, Origin: originLocal}
		if ref != "<none>" && !strings.Contains(ref, "@") {
			v.Origin = imageOrigin(ref, checks, uploaded, fromRepo)
			// Of an image's tags, report the one whose check matters most.
			for _, tag := range img.RepoTags {
				if c := checks[normalizeImageRef(tag)]; c != nil && (v.Update == nil || c.Status == checkStatusUpdate) {
					v.Update = c
				}
			}
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	writeJSON(w, http.StatusOK, map[string]any{"images": out})
}

func (s *Server) handleInspectImage(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		fail(w, http.StatusBadRequest, "image name required")
		return
	}
	inspect, err := s.dc.InspectImage(r.Context(), name)
	if err != nil {
		failDocker(w, err)
		return
	}
	history, _ := s.dc.ImageHistory(r.Context(), name)
	writeJSON(w, http.StatusOK, map[string]any{
		"inspect":  inspect,
		"defaults": dock.DefaultsFromImage(inspect),
		"history":  history,
	})
}

func (s *Server) handlePullImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Image    string `json:"image"`
		Tag      string `json:"tag"`
		Username string `json:"username"`
		Password string `json:"password"`
		// RegistryID names a registry from Settings whose sign-in to use; without
		// it, the registry is found from the image's host.
		RegistryID int64 `json:"registry_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	ref := strings.TrimSpace(req.Image)
	if ref == "" {
		fail(w, http.StatusBadRequest, "an image name is required")
		return
	}
	tag := strings.TrimSpace(req.Tag)
	if tag == "" {
		ref, tag = splitImageTag(ref)
	}
	if rec := s.st.RepoImages()[normalizeImageRef(ref+":"+tag)]; rec != nil && req.RegistryID == 0 && req.Username == "" {
		// Installed from a GitHub repository: look there for a newer archive.
		s.streamRepoUpdate(w, r, rec)
		return
	}
	var auth *dock.RegistryAuth
	switch {
	case req.Username != "" || req.Password != "":
		auth = &dock.RegistryAuth{Username: req.Username, Password: req.Password}
	case req.RegistryID != 0:
		reg, err := s.st.RegistryByID(req.RegistryID)
		if err != nil {
			fail(w, http.StatusNotFound, "no such registry")
			return
		}
		if reg.AuthType != authNone {
			if auth, err = s.authForRegistry(r.Context(), reg); err != nil {
				fail(w, http.StatusBadGateway, "could not sign in to "+reg.Name+": "+err.Error())
				return
			}
		}
	default:
		auth = s.authFor(r.Context(), ref)
	}
	body, err := s.dc.PullImage(r.Context(), ref, tag, auth)
	if err != nil {
		failDocker(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "image.pull", ref+":"+tag, "", true)
	failed := false
	pipeStream(w, body, func(line []byte) {
		if streamLineError(line) != "" {
			failed = true
		}
	})
	if !failed {
		_ = s.st.ClearUploaded(normalizeImageRef(ref + ":" + tag))
		s.recheckAfterPull(ref + ":" + tag)
	}
}

// normalizeImageRef writes an image reference the one way DocMan stores it,
// so "nginx", "nginx:latest" and "docker.io/library/nginx:latest" all match.
func normalizeImageRef(ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || strings.Contains(ref, "@") || strings.HasPrefix(ref, "sha256:") {
		return ref
	}
	name, tag := splitImageTag(ref)
	for _, prefix := range []string{"docker.io/", "index.docker.io/"} {
		name = strings.TrimPrefix(name, prefix)
	}
	if rest, ok := strings.CutPrefix(name, "library/"); ok && !strings.Contains(rest, "/") {
		name = rest
	}
	return name + ":" + tag
}

// streamLineError returns the error carried by one docker progress line, if any.
func streamLineError(line []byte) string {
	var msg struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(line, &msg) != nil {
		return ""
	}
	return msg.Error
}

// handleUploadImage streams a docker image tarball straight into the Engine.
// The request body is the tar itself, so a multi-gigabyte image never has to
// be buffered anywhere.
func (s *Server) handleUploadImage(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	if r.ContentLength == 0 {
		fail(w, http.StatusBadRequest, "the request body must be a docker image tar archive")
		return
	}
	body, err := s.dc.LoadImage(r.Context(), r.Body)
	if err != nil {
		failDocker(w, err)
		return
	}
	loaded := []string{}
	pipeStream(w, body, func(line []byte) {
		if ref := loadedImageRef(line); ref != "" {
			loaded = append(loaded, ref)
		}
	})
	detail := "no image reference reported"
	if len(loaded) > 0 {
		detail = strings.Join(loaded, ", ")
	}
	// Remember what came from an archive, so a later recreate knows not to
	// look for it in a registry. An archive with no tags reports only an id.
	for _, ref := range loaded {
		if !strings.HasPrefix(ref, "sha256:") {
			_ = s.st.MarkUploaded(normalizeImageRef(ref))
		}
	}
	s.st.Audit(identityOf(r).Actor(), "image.upload", detail, "image archive imported", true)
}

// loadedImageRef picks the image reference out of a docker load progress line.
func loadedImageRef(line []byte) string {
	var msg struct {
		Stream string `json:"stream"`
	}
	if err := json.Unmarshal(line, &msg); err != nil {
		return ""
	}
	text := strings.TrimSpace(msg.Stream)
	for _, prefix := range []string{"Loaded image: ", "Loaded image ID: "} {
		if strings.HasPrefix(text, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(text, prefix))
		}
	}
	return ""
}

var newline = []byte{'\n'}

// pipeStream forwards a docker progress stream to the client as newline
// delimited JSON, flushing as it goes so the UI shows live progress. When
// onLine is set each complete line is also handed to it.
func pipeStream(w http.ResponseWriter, body io.ReadCloser, onLine func([]byte)) {
	defer body.Close()
	h := w.Header()
	h.Set("Content-Type", "application/x-ndjson")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if onLine != nil {
			onLine(line)
		}
		// sc.Bytes() aliases the scanner's buffer, so the newline goes out separately.
		if _, err := w.Write(line); err != nil {
			return
		}
		if _, err := w.Write(newline); err != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
	if err := sc.Err(); err != nil {
		payload, _ := json.Marshal(map[string]string{"error": err.Error()})
		_, _ = w.Write(append(payload, '\n'))
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (s *Server) handleTagImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Image string `json:"image"`
		Repo  string `json:"repo"`
		Tag   string `json:"tag"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Image == "" || req.Repo == "" {
		fail(w, http.StatusBadRequest, "image and repo are required")
		return
	}
	repo, tag := req.Repo, req.Tag
	if tag == "" {
		repo, tag = splitImageTag(req.Repo)
	}
	if err := s.dc.TagImage(r.Context(), req.Image, repo, tag); err != nil {
		failDocker(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "image.tag", req.Image, "tagged "+repo+":"+tag, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "reference": repo + ":" + tag})
}

func (s *Server) handleRemoveImage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Image string `json:"image"`
		Force bool   `json:"force"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Image == "" {
		fail(w, http.StatusBadRequest, "an image is required")
		return
	}
	if err := s.dc.RemoveImage(r.Context(), req.Image, req.Force, false); err != nil {
		failDocker(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "image.remove", req.Image, fmt.Sprintf("force=%v", req.Force), true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handlePruneImages(w http.ResponseWriter, r *http.Request) {
	report, err := s.dc.PruneImages(r.Context(), !boolQuery(r, "unused"))
	if err != nil {
		failDocker(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "image.prune", "",
		fmt.Sprintf("reclaimed %d bytes", report.SpaceReclaimed), true)
	writeJSON(w, http.StatusOK, report)
}

// ---------- deploy planning ----------

// EnvSuggestion annotates one environment variable the image declares.
type EnvSuggestion struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Secret   bool   `json:"secret"`
	Required bool   `json:"required"`
	Note     string `json:"note,omitempty"`
}

// MountSuggestion is a proposed mount for a path the image declares as a volume.
type MountSuggestion struct {
	Target   string `json:"target"`
	Type     string `json:"type"`
	Source   string `json:"source"`
	ReadOnly bool   `json:"read_only"`
	Note     string `json:"note,omitempty"`
}

// PortSuggestion is a proposed publication for a port the image exposes.
type PortSuggestion struct {
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
	HostPort      string `json:"host_port"`
	Publish       bool   `json:"publish"`
	Note          string `json:"note,omitempty"`
}

// DeployPlan is everything the deploy form needs, already filled in.
type DeployPlan struct {
	Image    string              `json:"image"`
	Name     string              `json:"name"`
	Defaults *dock.ImageDefaults `json:"defaults"`
	Env      []EnvSuggestion     `json:"env"`
	Mounts   []MountSuggestion   `json:"mounts"`
	Ports    []PortSuggestion    `json:"ports"`
	Notes    []string            `json:"notes"`
	Spec     *dock.ContainerSpec `json:"spec"`
}

// secretHints name environment variables that should be masked in the UI.
var secretHints = []string{"PASSWORD", "PASSWD", "SECRET", "TOKEN", "APIKEY", "API_KEY", "PRIVATE_KEY", "CREDENTIAL"}

// noiseEnv are variables every image inherits from its base and that nobody
// needs to see in a deploy form.
var noiseEnv = map[string]bool{
	"PATH": true, "HOME": true, "TERM": true, "HOSTNAME": true,
	"LANG": true, "LC_ALL": true, "DEBIAN_FRONTEND": true,
	"GOPATH": true, "GOLANG_VERSION": true, "SSL_CERT_DIR": true,
}

func (s *Server) handleDeployPlan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Image string `json:"image"`
		Name  string `json:"name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	ref := strings.TrimSpace(req.Image)
	if ref == "" {
		fail(w, http.StatusBadRequest, "an image is required")
		return
	}
	ctx := r.Context()
	inspect, err := s.dc.InspectImage(ctx, ref)
	if err != nil {
		failDocker(w, err)
		return
	}
	defaults := dock.DefaultsFromImage(inspect)
	// Keep the name that was chosen: an image with several tags, such as
	// app:1.2 and app:latest, would otherwise be deployed by its first one.
	if defaults.Image == "" || strings.HasPrefix(defaults.Image, "sha256:") || !looksLikeImageID(ref) {
		defaults.Image = ref
	}

	containers, err := s.dc.ListContainers(ctx, true)
	if err != nil {
		failDocker(w, err)
		return
	}
	usedNames := map[string]bool{}
	usedHostPorts := map[int]bool{}
	for _, c := range containers {
		usedNames[c.Name()] = true
		for _, p := range c.Ports {
			if p.PublicPort > 0 {
				usedHostPorts[p.PublicPort] = true
			}
		}
	}

	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = uniqueName(suggestName(defaults.Image), usedNames)
	}

	plan := &DeployPlan{
		Image:    defaults.Image,
		Name:     name,
		Defaults: defaults,
		Env:      []EnvSuggestion{},
		Mounts:   []MountSuggestion{},
		Ports:    []PortSuggestion{},
		Notes:    []string{},
	}

	for _, entry := range defaults.Env {
		key, value, _ := strings.Cut(entry, "=")
		if noiseEnv[key] {
			continue
		}
		sug := EnvSuggestion{Key: key, Value: value}
		upper := strings.ToUpper(key)
		for _, hint := range secretHints {
			if strings.Contains(upper, hint) {
				sug.Secret = true
				break
			}
		}
		if value == "" {
			sug.Required = true
			sug.Note = "the image leaves this empty, so it probably needs a value"
		}
		if sug.Secret && value != "" {
			sug.Note = "the image ships a default secret; replace it"
			sug.Required = true
		}
		plan.Env = append(plan.Env, sug)
	}

	for _, target := range defaults.Volumes {
		source := name + volumeSuffix(target)
		plan.Mounts = append(plan.Mounts, MountSuggestion{
			Target: target, Type: "volume", Source: source,
			Note: "the image stores data here, so it needs a volume to survive a restart",
		})
	}

	for _, p := range defaults.Ports {
		sug := PortSuggestion{ContainerPort: p.ContainerPort, Protocol: p.Protocol, Publish: true}
		host := p.ContainerPort
		if usedHostPorts[host] || !hostPortFree(host) {
			host = nextFreeHostPort(p.ContainerPort, usedHostPorts)
			sug.Note = fmt.Sprintf("port %d is already taken on this host", p.ContainerPort)
		}
		usedHostPorts[host] = true
		sug.HostPort = strconv.Itoa(host)
		plan.Ports = append(plan.Ports, sug)
	}

	if len(defaults.Entrypoint) == 0 && len(defaults.Cmd) == 0 {
		plan.Notes = append(plan.Notes, "this image declares no command, so one must be supplied or the container will exit immediately")
	}
	if defaults.User == "" {
		plan.Notes = append(plan.Notes, "this image runs as root; consider setting a user")
	}
	if defaults.Healthcheck {
		plan.Notes = append(plan.Notes, "this image has a built-in health check")
	}

	plan.Spec = planToSpec(plan)
	writeJSON(w, http.StatusOK, plan)
}

// planToSpec turns the suggestions into a ready-to-submit container spec.
func planToSpec(p *DeployPlan) *dock.ContainerSpec {
	spec := &dock.ContainerSpec{
		Name:          p.Name,
		Image:         p.Image,
		Cmd:           p.Defaults.Cmd,
		Entrypoint:    p.Defaults.Entrypoint,
		Env:           []string{},
		Labels:        map[string]string{},
		WorkingDir:    p.Defaults.WorkingDir,
		User:          p.Defaults.User,
		StopSignal:    p.Defaults.StopSignal,
		RestartPolicy: "unless-stopped",
		NetworkMode:   "bridge",
		Ports:         []dock.PortMapping{},
		Mounts:        []dock.MountSpec{},
	}
	for _, e := range p.Env {
		spec.Env = append(spec.Env, e.Key+"="+e.Value)
	}
	for _, m := range p.Mounts {
		spec.Mounts = append(spec.Mounts, dock.MountSpec{
			Type: m.Type, Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly,
		})
	}
	for _, port := range p.Ports {
		if !port.Publish {
			continue
		}
		spec.Ports = append(spec.Ports, dock.PortMapping{
			ContainerPort: port.ContainerPort, Protocol: port.Protocol, HostPort: port.HostPort,
		})
	}
	return spec
}

// suggestName derives a container name from an image reference.
func suggestName(ref string) string {
	ref, _ = splitImageTag(ref)
	if i := strings.LastIndexByte(ref, '/'); i >= 0 {
		ref = ref[i+1:]
	}
	out := make([]rune, 0, len(ref))
	for _, c := range strings.ToLower(ref) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	name := strings.Trim(string(out), "-._")
	if name == "" {
		name = "container"
	}
	return name
}

func uniqueName(base string, used map[string]bool) string {
	if !used[base] {
		return base
	}
	for i := 2; i < 1000; i++ {
		candidate := base + "-" + strconv.Itoa(i)
		if !used[candidate] {
			return candidate
		}
	}
	return base + "-new"
}

// volumeSuffix turns /var/lib/postgresql/data into -data, or -srv-www.
func volumeSuffix(target string) string {
	parts := strings.Split(strings.Trim(target, "/"), "/")
	if len(parts) == 0 {
		return "-data"
	}
	tail := parts[len(parts)-1]
	if (tail == "data" || tail == "db") && len(parts) > 1 {
		tail = parts[len(parts)-2] + "-" + tail
	}
	out := make([]rune, 0, len(tail)+1)
	out = append(out, '-')
	for _, c := range strings.ToLower(tail) {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
			out = append(out, c)
		default:
			out = append(out, '-')
		}
	}
	return strings.TrimRight(string(out), "-")
}

// hostPortFree checks whether a TCP port can be bound right now. DocMan
// usually sits in its own network namespace, so this only rules out ports it
// holds itself; the authoritative signal is the set of ports other containers
// already publish, which the caller checks first.
func hostPortFree(port int) bool {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func nextFreeHostPort(from int, used map[int]bool) int {
	start := from
	if start < 1024 {
		// Suggesting a privileged host port rarely works out.
		start += 8000
	}
	for port := start; port < 65535; port++ {
		if used[port] {
			continue
		}
		if hostPortFree(port) {
			return port
		}
	}
	return from
}

// looksLikeImageID reports whether ref is an image ID rather than a name.
func looksLikeImageID(ref string) bool {
	id := strings.TrimPrefix(ref, "sha256:")
	if len(id) < 12 || len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return false
		}
	}
	return true
}
