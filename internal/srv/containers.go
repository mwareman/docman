package srv

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"docman/internal/crypt"
	"docman/internal/dock"
)

// containerView is the shape the UI lists containers in.
type containerView struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Image     string            `json:"image"`
	ImageID   string            `json:"image_id"`
	Command   string            `json:"command"`
	Created   int64             `json:"created"`
	State     string            `json:"state"`
	Status    string            `json:"status"`
	Health    string            `json:"health,omitempty"`
	Ports     []dock.Port       `json:"ports"`
	Labels    map[string]string `json:"labels"`
	Networks  map[string]string `json:"networks"`
	PrimaryIP string            `json:"primary_ip"`
	PinnedIP  string            `json:"pinned_ip,omitempty"`
	Pinned    bool              `json:"pinned"`
	Mounts    []dock.MountPoint `json:"mounts"`
	Self      bool              `json:"self"`
	// ImageUploaded is set on single-container responses when the configured
	// image was last supplied by an archive upload rather than a registry.
	ImageUploaded bool `json:"image_uploaded,omitempty"`
	// UpdateAvailable is set when a newer image is on this host under the tag
	// the container was created from; recreating it picks that image up.
	UpdateAvailable bool   `json:"update_available,omitempty"`
	UpdateImage     string `json:"update_image,omitempty"`
	// UpdateSource is "host" when the newer image is already here, or
	// "registry" when it is still in the registry and a pull would fetch it.
	UpdateSource string `json:"update_source,omitempty"`
	// ImageOrigin is registry, uploaded, local or unknown.
	ImageOrigin string `json:"image_origin,omitempty"`
}

// errSelfReplacing reports that a recreate of DocMan's own container has been
// handed to a helper and DocMan is about to be stopped.
var errSelfReplacing = errors.New("docman is recreating itself")

// containerIDInMounts finds the /var/lib/docker/containers/<id>/ path Docker
// bind-mounts over /etc/hostname, /etc/hosts and /etc/resolv.conf.
var containerIDInMounts = regexp.MustCompile(`/containers/([0-9a-f]{64})/`)

// selfID is DocMan's own container id, or a prefix of it, or "" when DocMan is
// not running in a container. The full id comes from the mount table; the
// hostname fallback is only the short id, and only while it is the default.
func (s *Server) selfID() string {
	if raw, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
		if m := containerIDInMounts.FindSubmatch(raw); m != nil {
			return string(m[1])
		}
	}
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// isSelf reports whether id is DocMan's own container.
func (s *Server) isSelf(id string) bool {
	self := s.selfID()
	return self != "" && strings.HasPrefix(id, self)
}

func (s *Server) toView(c *dock.ContainerSummary, pins map[string]string, selfID string) *containerView {
	name := c.Name()
	v := &containerView{
		ID: c.ID, Name: name, Image: c.Image, ImageID: c.ImageID, Command: c.Command,
		Created: c.Created, State: c.State, Status: c.Status,
		Ports: c.Ports, Labels: c.Labels, Mounts: c.Mounts,
		Networks: map[string]string{},
	}
	if v.Ports == nil {
		v.Ports = []dock.Port{}
	}
	if v.Mounts == nil {
		v.Mounts = []dock.MountPoint{}
	}
	if v.Labels == nil {
		v.Labels = map[string]string{}
	}
	names := make([]string, 0, len(c.NetworkSettings.Networks))
	for n := range c.NetworkSettings.Networks {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		ip := c.NetworkSettings.Networks[n].IPAddress
		v.Networks[n] = ip
		if v.PrimaryIP == "" && ip != "" {
			v.PrimaryIP = ip
		}
	}
	if ip, ok := pins[name]; ok {
		v.PinnedIP = ip
		v.Pinned = true
	}
	if selfID != "" && strings.HasPrefix(c.ID, selfID) {
		v.Self = true
	}
	if i := strings.Index(c.Status, "(health"); i >= 0 {
		if j := strings.IndexByte(c.Status[i:], ')'); j > 0 {
			v.Health = strings.Trim(c.Status[i+1:i+j], "()")
		}
	}
	return v
}

func (s *Server) pinMap() map[string]string {
	out := map[string]string{}
	pins, err := s.st.ListPins()
	if err != nil {
		return out
	}
	for _, p := range pins {
		out[p.Container] = p.IP
	}
	return out
}

func (s *Server) handleListContainers(w http.ResponseWriter, r *http.Request) {
	all := true
	if r.URL.Query().Get("all") != "" {
		all = boolQuery(r, "all")
	}
	list, err := s.dc.ListContainers(r.Context(), all)
	if err != nil {
		failDocker(w, err)
		return
	}
	list = withoutHelpers(list)
	pins := s.pinMap()
	selfID := s.selfID()
	out := make([]*containerView, 0, len(list))
	for _, c := range list {
		out = append(out, s.toView(c, pins, selfID))
	}
	s.markUpdates(out, s.updatesFor(r.Context(), list))
	sort.Slice(out, func(i, j int) bool {
		if (out[i].State == "running") != (out[j].State == "running") {
			return out[i].State == "running"
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	writeJSON(w, http.StatusOK, map[string]any{"containers": out})
}

func (s *Server) handleGetContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	list, err := s.dc.ListContainers(r.Context(), true)
	if err != nil {
		failDocker(w, err)
		return
	}
	pins := s.pinMap()
	selfID := s.selfID()
	for _, c := range list {
		if c.ID == id || strings.HasPrefix(c.ID, id) || c.Name() == id {
			v := s.toView(c, pins, selfID)
			s.markUpdates([]*containerView{v}, s.updatesFor(r.Context(), []*dock.ContainerSummary{c}))
			// The list reports an image id once the tag has moved on, so read
			// the configured reference from the container itself.
			if inspect, err := s.dc.InspectContainer(r.Context(), c.ID); err == nil {
				if cfg, ok := inspect["Config"].(map[string]any); ok {
					ref, _ := cfg["Image"].(string)
					v.ImageUploaded = ref != "" && s.st.IsUploaded(normalizeImageRef(ref))
				}
			}
			writeJSON(w, http.StatusOK, v)
			return
		}
	}
	// A page or link can still hold the ID of a container that has since been
	// recreated; say which container took its place.
	if next := containerMoves.resolve(id); next != "" {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "this container was recreated", "moved_to": next})
		return
	}
	fail(w, http.StatusNotFound, "no such container")
}

func (s *Server) handleInspectContainer(w http.ResponseWriter, r *http.Request) {
	inspect, err := s.dc.InspectContainer(r.Context(), r.PathValue("id"))
	if err != nil {
		failDocker(w, err)
		return
	}
	writeJSON(w, http.StatusOK, inspect)
}

func (s *Server) handleContainerSpec(w http.ResponseWriter, r *http.Request) {
	inspect, err := s.dc.InspectContainer(r.Context(), r.PathValue("id"))
	if err != nil {
		failDocker(w, err)
		return
	}
	spec := dock.SpecFrom(inspect)
	pinned := ""
	if p, err := s.st.Pin(spec.Name); err == nil {
		pinned = p.IP
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"spec":      spec,
		"running":   containerRunning(inspect),
		"pinned_ip": pinned,
		"networks":  dock.NetworkAttachments(inspect),
	})
}

// ---------- lifecycle ----------

// handleContainerAction serves start, stop, restart, pause, unpause and kill.
func (s *Server) handleContainerAction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	action := lastSegment(r.URL.Path)
	who := identityOf(r).Actor()
	ctx := r.Context()

	var err error
	switch action {
	case "start":
		err = s.dc.StartContainer(ctx, id)
	case "stop":
		err = s.dc.StopContainer(ctx, id, intQuery(r, "t", 10))
	case "restart":
		err = s.dc.RestartContainer(ctx, id, intQuery(r, "t", 10))
	case "pause":
		err = s.dc.PauseContainer(ctx, id)
	case "unpause":
		err = s.dc.UnpauseContainer(ctx, id)
	case "kill":
		signal := r.URL.Query().Get("signal")
		err = s.dc.KillContainer(ctx, id, signal)
	default:
		fail(w, http.StatusNotFound, "unknown action")
		return
	}
	if err != nil {
		s.st.Audit(who, "container."+action, id, err.Error(), false)
		failDocker(w, err)
		return
	}
	s.st.Audit(who, "container."+action, id, "", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleRenameContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if err := validateContainerName(name); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	inspect, err := s.dc.InspectContainer(r.Context(), id)
	if err != nil {
		failDocker(w, err)
		return
	}
	oldName := containerNameOf(inspect)
	if err := s.dc.RenameContainer(r.Context(), id, name); err != nil {
		failDocker(w, err)
		return
	}
	_ = s.st.RenamePin(oldName, name)
	s.st.Audit(identityOf(r).Actor(), "container.rename", oldName, "renamed to "+name, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name})
}

func (s *Server) handleRemoveContainer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	force := boolQuery(r, "force")
	volumes := boolQuery(r, "volumes")
	name := id
	var anonymous []string
	if inspect, err := s.dc.InspectContainer(r.Context(), id); err == nil {
		name = containerNameOf(inspect)
		// A recreate reattaches a container's anonymous volumes by name, and
		// Docker then no longer counts them as anonymous. Note them, so asking
		// for anonymous volumes to go still removes them.
		mounts, _ := inspect["Mounts"].([]any)
		for _, m := range mounts {
			if mm, ok := m.(map[string]any); ok && mm["Type"] == "volume" {
				if v, _ := mm["Name"].(string); dock.IsAnonymousVolumeName(v) {
					anonymous = append(anonymous, v)
				}
			}
		}
	}
	if err := s.dc.RemoveContainer(r.Context(), id, force, volumes); err != nil {
		failDocker(w, err)
		return
	}
	if volumes {
		for _, v := range anonymous {
			// Refused, and left alone, if another container still uses it.
			_ = s.dc.RemoveVolume(r.Context(), v, false)
		}
	}
	_ = s.st.DeletePin(name)
	s.st.Audit(identityOf(r).Actor(), "container.remove", name, fmt.Sprintf("force=%v volumes=%v", force, volumes), true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- configuration changes ----------

func (s *Server) handleUpdateContainerSpec(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Spec  *dock.ContainerSpec `json:"spec"`
		Start *bool               `json:"start"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Spec == nil {
		fail(w, http.StatusBadRequest, "missing spec")
		return
	}
	// The spec replaces the container's whole configuration, so a partial one
	// would silently wipe everything it leaves out. The form always sends the
	// image; a spec without one is a client that sent the wrong document.
	if strings.TrimSpace(req.Spec.Image) == "" {
		failHint(w, http.StatusBadRequest, "the spec is incomplete: it has no image",
			"send the whole spec from GET /api/containers/{id}/spec with your changes made to it")
		return
	}
	if req.Spec.Name != "" {
		if err := validateContainerName(req.Spec.Name); err != nil {
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := validateSpec(req.Spec); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	who := identityOf(r).Actor()
	id := r.PathValue("id")
	current := ""
	if inspect, err := s.dc.InspectContainer(r.Context(), id); err == nil {
		current = dock.SpecFrom(inspect).NetworkMode
	}
	if err := s.checkNetworkMode(r.Context(), req.Spec.NetworkMode, current); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	inspect, err := s.dc.InspectContainer(r.Context(), id)
	if err != nil {
		failDocker(w, err)
		return
	}
	name, oldID := containerNameOf(inspect), stringAt(inspect, "Id")
	spec, start := req.Spec, req.Start
	// The change runs as a job, so it completes even if this connection is cut
	// off — as it is when the container being changed carries the connection.
	job := s.startJob("update", name, oldID, who, func(ctx context.Context) (string, error) {
		newID, err := s.recreateContainer(ctx, oldID, spec, start)
		switch {
		case errors.Is(err, errSelfReplacing):
			s.st.Audit(who, "container.update", name, "DocMan is applying its own configuration by recreating itself", true)
		case err != nil:
			s.st.Audit(who, "container.update", name, err.Error(), false)
		default:
			s.st.Audit(who, "container.update", name, "configuration applied by recreating the container", true)
		}
		return newID, err
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "job": job})
}

func stringAt(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func (s *Server) handleRecreateContainer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Start     *bool  `json:"start"`
		PullFirst bool   `json:"pull"`
		Image     string `json:"image"`
	}
	// A recreate with no body is valid.
	if r.ContentLength > 0 && !decodeBody(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	who := identityOf(r).Actor()
	inspect, err := s.dc.InspectContainer(r.Context(), id)
	if err != nil {
		failDocker(w, err)
		return
	}
	name, oldID := containerNameOf(inspect), stringAt(inspect, "Id")

	// The pull and the rebuild run as a job, so they finish even if this
	// connection is cut off — as it is when the container being upgraded is
	// the proxy or tunnel the browser reaches DocMan through.
	job := s.startJob("recreate", name, oldID, who, func(ctx context.Context) (string, error) {
		var spec *dock.ContainerSpec
		if req.Image != "" || req.PullFirst {
			spec = dock.SpecFrom(inspect)
			if req.Image != "" {
				spec.Image = req.Image
			}
		}
		if req.PullFirst {
			if err := s.pullForRecreate(ctx, spec.Image); err != nil {
				s.st.Audit(who, "container.recreate", name, err.Error(), false)
				return "", err
			}
		}
		newID, err := s.recreateContainer(ctx, oldID, spec, req.Start)
		switch {
		case errors.Is(err, errSelfReplacing):
			s.st.Audit(who, "container.recreate", name, "DocMan is recreating itself", true)
		case err != nil:
			s.st.Audit(who, "container.recreate", name, err.Error(), false)
		default:
			s.st.Audit(who, "container.recreate", name, "recreated", true)
		}
		return newID, err
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "job": job})
}

// pullForRecreate fetches the newest version of an image before a recreate.
func (s *Server) pullForRecreate(ctx context.Context, ref string) error {
	image, tag := splitImageTag(ref)
	if rec := s.st.RepoImages()[normalizeImageRef(image+":"+tag)]; rec != nil {
		// Installed from a GitHub repository: install its newest archive.
		progress := progressFrom(ctx)
		progress("Looking for a newer " + rec.Artifact + " in the repository")
		updated, note, err := s.updateFromRepo(ctx, rec, jobActor(ctx), progress)
		if err != nil {
			return err
		}
		if !updated {
			progress(note)
		}
		s.checkImages(ctx, image+":"+tag)
		return nil
	}
	progressFrom(ctx)("Pulling " + ref)
	body, err := s.dc.PullImage(ctx, image, tag, s.authFor(ctx, ref))
	if err != nil {
		return fmt.Errorf("could not pull %s: %w", ref, err)
	}
	defer body.Close()
	problem := ""
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		if e := streamLineError(sc.Bytes()); e != "" {
			problem = e
		}
	}
	if problem != "" {
		return fmt.Errorf("could not pull %s: %s", ref, problem)
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("could not pull %s: %w", ref, err)
	}
	_ = s.st.ClearUploaded(normalizeImageRef(image + ":" + tag))
	// Re-checked now, so the container list is right as soon as the job ends.
	s.checkImages(ctx, image+":"+tag)
	return nil
}

// recreateContainer applies a configuration change by rebuilding the container.
// When the container is DocMan itself it returns errSelfReplacing once a
// helper has taken over, and DocMan is about to be stopped.
//
// Docker cannot change a container's environment, command, mounts or ports in
// place, so DocMan stops it, sets it aside under a temporary name, creates a
// replacement from the same inspect document with the edits applied, and only
// then deletes the original. If the replacement cannot be created the original
// is renamed back and restarted, so a failed edit is not a lost container.
func (s *Server) recreateContainer(ctx context.Context, ref string, spec *dock.ContainerSpec, forceStart *bool) (string, error) {
	inspect, err := s.dc.InspectContainer(ctx, ref)
	if err != nil {
		return "", err
	}
	oldID, _ := inspect["Id"].(string)
	oldName := containerNameOf(inspect)
	if oldID == "" || oldName == "" {
		return "", errors.New("could not read the container's identity")
	}
	wasRunning := containerRunning(inspect)

	newName := oldName
	if spec != nil && spec.Name != "" {
		newName = spec.Name
	}

	body := dock.CreateBodyFrom(inspect, spec)
	// A pinned address must survive the rebuild.
	s.nm.ApplyPinToCreateBody(newName, body)

	primary := dock.PrimaryNetwork(inspect)
	extras := map[string]dock.EndpointSettings{}
	for name, ep := range dock.NetworkAttachments(inspect) {
		if name != primary {
			extras[name] = ep
		}
	}

	start := wasRunning
	if forceStart != nil {
		start = *forceStart
	}
	plan := &dock.ReplacePlan{
		OldID:      oldID,
		OldName:    oldName,
		NewName:    newName,
		Parked:     trimName(oldName) + ".docman-old-" + crypt.RandHex(3),
		Body:       body,
		Extras:     extras,
		WasRunning: wasRunning,
		Start:      start,
		// Keep a paused container paused, unless the caller chose otherwise.
		WasPaused: containerPaused(inspect) && forceStart == nil,
		Progress:  progressFrom(ctx),
	}

	// Stopping this container stops DocMan, so it cannot finish the job itself:
	// a helper container does the swap once this response is on its way.
	if s.isSelf(oldID) {
		if err := s.handOffSelfReplace(ctx, inspect, plan); err != nil {
			return "", err
		}
		if oldName != newName {
			_ = s.st.RenamePin(oldName, newName)
		}
		return "", errSelfReplacing
	}

	newID, err := dock.Replace(ctx, s.dc, plan, s.logf)
	if newID != "" && oldName != newName {
		_ = s.st.RenamePin(oldName, newName)
	}
	if newID != "" {
		containerMoves.record(oldID, newID)
	}
	return newID, err
}

// ---------- create from scratch ----------

func (s *Server) handleCreateContainer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Spec  *dock.ContainerSpec `json:"spec"`
		Start bool                `json:"start"`
		PinIP bool                `json:"pin_ip"`
		IP    string              `json:"ip"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Spec == nil {
		fail(w, http.StatusBadRequest, "missing spec")
		return
	}
	spec := req.Spec
	if err := validateContainerName(spec.Name); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(spec.Image) == "" {
		fail(w, http.StatusBadRequest, "an image is required")
		return
	}
	if err := validateSpec(spec); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if spec.NetworkMode == "" {
		spec.NetworkMode = "bridge"
	}
	who := identityOf(r).Actor()
	ctx := r.Context()
	if err := s.checkNetworkMode(ctx, spec.NetworkMode, ""); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}

	// Named volumes referenced by the spec are created up front so a deploy
	// never fails halfway through on a missing volume.
	for _, m := range spec.Mounts {
		if m.Type == "volume" && m.Source != "" && !strings.HasPrefix(m.Source, "/") {
			if _, err := s.dc.InspectVolume(ctx, m.Source); err != nil {
				if !dock.NotFound(err) {
					failDocker(w, err)
					return
				}
				if _, err := s.dc.CreateVolume(ctx, m.Source, "local",
					map[string]string{"io.docman.created-for": spec.Name}, nil); err != nil {
					failDocker(w, err)
					return
				}
			}
		}
	}

	body := dock.CreateBodyFrom(map[string]any{}, spec)
	created, err := s.dc.CreateContainer(ctx, spec.Name, body)
	if err != nil {
		s.st.Audit(who, "container.create", spec.Name, err.Error(), false)
		failDocker(w, err)
		return
	}

	result := map[string]any{"ok": true, "id": created.ID, "name": spec.Name}
	if len(created.Warnings) > 0 {
		result["warnings"] = created.Warnings
	}
	if req.PinIP {
		pin, err := s.nm.Pin(ctx, created.ID, req.IP)
		if err != nil {
			result["pin_error"] = err.Error()
		} else {
			result["pinned_ip"] = pin.IP
			result["network"] = pin.Network
		}
	}
	if req.Start {
		if err := s.dc.StartContainer(ctx, created.ID); err != nil {
			s.st.Audit(who, "container.create", spec.Name, "created but would not start: "+err.Error(), false)
			result["start_error"] = err.Error()
			writeJSON(w, http.StatusAccepted, result)
			return
		}
		result["started"] = true
	}
	s.st.Audit(who, "container.create", spec.Name, "created from "+spec.Image, true)
	writeJSON(w, http.StatusCreated, result)
}

// ---------- one-shot reads ----------

func (s *Server) handleContainerLogsOnce(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	inspect, err := s.dc.InspectContainer(r.Context(), id)
	if err != nil {
		failDocker(w, err)
		return
	}
	tail := strconv.Itoa(intQuery(r, "tail", 500))
	opt := dock.LogOptions{
		Stdout:     true,
		Stderr:     true,
		Tail:       tail,
		Timestamps: boolQuery(r, "timestamps"),
	}
	body, err := s.dc.ContainerLogs(r.Context(), id, opt)
	if err != nil {
		failDocker(w, err)
		return
	}
	defer body.Close()

	text, err := readLogText(body, containerHasTTY(inspect))
	if err != nil && text == "" {
		failDocker(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": text})
}

// readLogText drains a log stream, demultiplexing the 8-byte framing Docker
// uses whenever the container was not given a TTY.
func readLogText(body io.Reader, tty bool) (string, error) {
	if tty {
		raw, err := io.ReadAll(io.LimitReader(body, 8<<20))
		return string(raw), err
	}
	var out strings.Builder
	for out.Len() < 8<<20 {
		frame, err := dock.ReadFrame(body)
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return out.String(), nil
			}
			return out.String(), err
		}
		out.Write(frame.Data)
	}
	return out.String(), nil
}

func (s *Server) handleContainerStatsOnce(w http.ResponseWriter, r *http.Request) {
	stats, err := s.dc.ContainerStatsOnce(r.Context(), r.PathValue("id"))
	if err != nil {
		failDocker(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sampleFromStats(stats, r.PathValue("id"), ""))
}

func (s *Server) handleContainerTop(w http.ResponseWriter, r *http.Request) {
	out, err := s.dc.ContainerTop(r.Context(), r.PathValue("id"), r.URL.Query().Get("ps_args"))
	if err != nil {
		failDocker(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// ---------- fixed addresses ----------

func (s *Server) handlePinIP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IP string `json:"ip"`
	}
	if r.ContentLength > 0 && !decodeBody(w, r, &req) {
		return
	}
	id := r.PathValue("id")
	result, err := s.nm.Pin(r.Context(), id, req.IP)
	if err != nil {
		s.st.Audit(identityOf(r).Actor(), "address.pin", id, err.Error(), false)
		failHint(w, http.StatusBadRequest, err.Error(),
			"DocMan fixes addresses by moving the container onto its own bridge network")
		return
	}
	s.st.Audit(identityOf(r).Actor(), "address.pin", result.Container, "fixed at "+result.IP, true)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleUnpinIP(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.nm.Unpin(r.Context(), id); err != nil {
		s.st.Audit(identityOf(r).Actor(), "address.unpin", id, err.Error(), false)
		failDocker(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "address.unpin", id, "address released", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- helpers ----------

func lastSegment(path string) string {
	path = strings.TrimSuffix(path, "/")
	if i := strings.LastIndexByte(path, '/'); i >= 0 {
		return path[i+1:]
	}
	return path
}

func containerNameOf(inspect map[string]any) string {
	name, _ := inspect["Name"].(string)
	return strings.TrimPrefix(name, "/")
}

func containerPaused(inspect map[string]any) bool {
	state, ok := inspect["State"].(map[string]any)
	if !ok {
		return false
	}
	paused, _ := state["Paused"].(bool)
	return paused
}

func containerRunning(inspect map[string]any) bool {
	state, ok := inspect["State"].(map[string]any)
	if !ok {
		return false
	}
	running, _ := state["Running"].(bool)
	return running
}

func containerHasTTY(inspect map[string]any) bool {
	cfg, ok := inspect["Config"].(map[string]any)
	if !ok {
		return false
	}
	tty, _ := cfg["Tty"].(bool)
	return tty
}

// trimName keeps the parked name within Docker's 255 character limit.
func trimName(name string) string {
	if len(name) > 200 {
		return name[:200]
	}
	return name
}

func validateContainerName(name string) error {
	if name == "" {
		return errors.New("a container name is required")
	}
	if len(name) > 200 {
		return errors.New("container name is too long")
	}
	first := rune(name[0])
	if !(first >= 'a' && first <= 'z' || first >= 'A' && first <= 'Z' || first >= '0' && first <= '9') {
		return errors.New("container name must start with a letter or digit")
	}
	for _, c := range name {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '-', c == '_':
		default:
			return errors.New("container name may contain only letters, digits, dot, dash and underscore")
		}
	}
	return nil
}

// checkNetworkMode accepts only a network mode that exists on this host:
// Docker's built-in modes, a network by name or id, or another container's
// namespace. current is the container's present mode, which is always accepted
// unchanged, so a container whose network has since gone can still be edited.
func (s *Server) checkNetworkMode(ctx context.Context, mode, current string) error {
	mode = strings.TrimSpace(mode)
	switch mode {
	case "", "bridge", "default", "host", "none":
		return nil
	}
	if mode == current {
		return nil
	}
	if target, ok := strings.CutPrefix(mode, "container:"); ok {
		if target == "" {
			return errors.New("choose which container's network to share")
		}
		if _, err := s.dc.InspectContainer(ctx, target); err != nil {
			return fmt.Errorf("there is no container %q to share a network with", target)
		}
		return nil
	}
	networks, err := s.dc.ListNetworks(ctx)
	if err != nil {
		return err
	}
	for _, n := range networks {
		if n.Name == mode || n.ID == mode || (len(mode) >= 12 && strings.HasPrefix(n.ID, mode)) {
			return nil
		}
	}
	return fmt.Errorf("there is no network called %q on this host", mode)
}

func validateSpec(spec *dock.ContainerSpec) error {
	for _, e := range spec.Env {
		if !strings.Contains(e, "=") {
			return fmt.Errorf("environment entry %q must be in KEY=value form", e)
		}
		if strings.HasPrefix(e, "=") {
			return errors.New("an environment variable name cannot be empty")
		}
	}
	seen := map[string]bool{}
	for _, m := range spec.Mounts {
		if m.Target == "" {
			return errors.New("every mount needs a container path")
		}
		if !strings.HasPrefix(m.Target, "/") {
			return fmt.Errorf("mount path %q must be absolute", m.Target)
		}
		if seen[m.Target] {
			return fmt.Errorf("more than one mount targets %q", m.Target)
		}
		seen[m.Target] = true
		switch m.Type {
		case "", "volume", "bind", "tmpfs":
		default:
			return fmt.Errorf("unknown mount type %q", m.Type)
		}
		if m.Type == "bind" && !strings.HasPrefix(m.Source, "/") {
			return fmt.Errorf("bind mount source %q must be an absolute host path", m.Source)
		}
	}
	for _, p := range spec.Ports {
		if p.ContainerPort < 1 || p.ContainerPort > 65535 {
			return fmt.Errorf("container port %d is out of range", p.ContainerPort)
		}
		if p.HostPort != "" {
			n, err := strconv.Atoi(p.HostPort)
			if err != nil || n < 1 || n > 65535 {
				return fmt.Errorf("host port %q is not valid", p.HostPort)
			}
		}
		switch p.Protocol {
		case "", "tcp", "udp", "sctp":
		default:
			return fmt.Errorf("unknown protocol %q", p.Protocol)
		}
	}
	switch spec.RestartPolicy {
	case "", "no", "always", "unless-stopped", "on-failure":
	default:
		return fmt.Errorf("unknown restart policy %q", spec.RestartPolicy)
	}
	// Docker accepts capability names with or without the CAP_ prefix, in any
	// case, plus ALL. Which names exist is Docker's call; this only catches
	// text that cannot be a capability at all.
	for _, list := range [][]string{spec.CapAdd, spec.CapDrop} {
		for _, c := range list {
			name := strings.TrimPrefix(strings.ToUpper(strings.TrimSpace(c)), "CAP_")
			if name == "" || strings.Trim(name, "ABCDEFGHIJKLMNOPQRSTUVWXYZ_") != "" {
				return fmt.Errorf("%q is not a Linux capability name", c)
			}
		}
	}
	return nil
}

func splitImageTag(ref string) (string, string) {
	// A colon after the last slash introduces a tag; before it, a registry port.
	slash := strings.LastIndexByte(ref, '/')
	colon := strings.LastIndexByte(ref, ':')
	if colon > slash && colon >= 0 {
		return ref[:colon], ref[colon+1:]
	}
	return ref, "latest"
}
