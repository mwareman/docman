package srv

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"docman/internal/crypt"
	"docman/internal/dock"
	"docman/internal/volagent"
)

// The volume explorer.
//
// DocMan cannot see volume contents itself: it runs in its own container with
// only the Docker socket. So for each volume being explored it starts a small
// helper container from its own image, with the volume mounted at /volume and
// networking off. Listing, making folders, renaming and deleting run the
// docman binary inside the helper (see package volagent); downloads, uploads
// and edits stream through the Engine's archive API. A helper that has not
// been used for a while is removed, and leftovers are swept at startup.

const (
	explorerLabel   = "docman.helper"
	explorerKind    = "volume-explorer"
	explorerIdle    = 3 * time.Minute
	maxEditBytes    = 1 << 20 // files larger than this are downloaded, not edited
	agentOutputSize = 32 << 20
)

type explorerHelper struct {
	id       string
	lastUsed time.Time
}

type explorers struct {
	mu      sync.Mutex
	helpers map[string]*explorerHelper // by volume name
}

var volumeExplorers = &explorers{helpers: map[string]*explorerHelper{}}

// helperFor returns a running helper for the volume, starting one if needed.
func (s *Server) helperFor(ctx context.Context, volume string) (string, error) {
	volumeExplorers.mu.Lock()
	defer volumeExplorers.mu.Unlock()

	if h, ok := volumeExplorers.helpers[volume]; ok {
		if inspect, err := s.dc.InspectContainer(ctx, h.id); err == nil && containerRunning(inspect) {
			h.lastUsed = time.Now()
			return h.id, nil
		}
		_ = s.dc.RemoveContainer(context.Background(), h.id, true, true)
		delete(volumeExplorers.helpers, volume)
	}

	if _, err := s.dc.InspectVolume(ctx, volume); err != nil {
		return "", err
	}
	image, err := s.selfImage(ctx)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"Image":      image,
		"Entrypoint": []string{"/docman"},
		"Cmd":        []string{volagent.Command, "serve"},
		"User":       "0:0",
		"Labels":     map[string]string{explorerLabel: explorerKind, "docman.volume": volume},
		"HostConfig": map[string]any{
			"Mounts": []map[string]any{{"Type": "volume", "Source": volume, "Target": volagent.Root}},
			// DocMan's image declares /data a volume; a throwaway tmpfs there stops
			// Docker from creating an anonymous volume for every helper.
			"Tmpfs":         map[string]string{"/data": ""},
			"NetworkMode":   "none",
			"SecurityOpt":   []string{"no-new-privileges:true"},
			"RestartPolicy": map[string]any{"Name": "no"},
		},
	}
	name := "docman-explore-" + safeName(volume) + "-" + crypt.RandHex(3)
	created, err := s.dc.CreateContainer(ctx, name, body)
	if err != nil {
		return "", fmt.Errorf("could not start the volume explorer: %w", err)
	}
	if err := s.dc.StartContainer(ctx, created.ID); err != nil {
		_ = s.dc.RemoveContainer(context.Background(), created.ID, true, true)
		return "", fmt.Errorf("could not start the volume explorer: %w", err)
	}
	volumeExplorers.helpers[volume] = &explorerHelper{id: created.ID, lastUsed: time.Now()}
	return created.ID, nil
}

// selfImage is the image DocMan itself runs from, which carries the agent.
func (s *Server) selfImage(ctx context.Context) (string, error) {
	self := s.selfID()
	if self == "" {
		return "", errors.New("the volume explorer needs DocMan to run as a container")
	}
	inspect, err := s.dc.InspectContainer(ctx, self)
	if err != nil {
		return "", errors.New("the volume explorer needs DocMan to run as a container")
	}
	image, _ := inspect["Image"].(string)
	if image == "" {
		return "", errors.New("could not tell which image DocMan runs from")
	}
	return image, nil
}

func safeName(v string) string {
	var b strings.Builder
	for _, r := range v {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		}
		if b.Len() >= 40 {
			break
		}
	}
	return b.String()
}

// closeExplorers removes the helper for one volume, or every helper when
// volume is empty, so the volume can be deleted or pruned.
func (s *Server) closeExplorers(ctx context.Context, volume string) {
	volumeExplorers.mu.Lock()
	defer volumeExplorers.mu.Unlock()
	for v, h := range volumeExplorers.helpers {
		if volume == "" || v == volume {
			_ = s.dc.RemoveContainer(ctx, h.id, true, true)
			delete(volumeExplorers.helpers, v)
		}
	}
}

// removeEmptyAnonymousVolumes deletes anonymous volumes (Docker's 64-hex names)
// that no container uses and that hold nothing at all. Earlier DocMan versions
// left one behind for every volume explorer and self-recreate helper, and
// every recreate left the container's old anonymous volumes. A volume with
// anything in it is never touched, nor one any container still uses.
func (s *Server) removeEmptyAnonymousVolumes(ctx context.Context) {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	df, err := s.dc.DiskUsage(cctx)
	if err != nil {
		return
	}
	removed := 0
	for _, v := range df.Volumes {
		if v == nil || v.UsageData == nil || !dock.IsAnonymousVolumeName(v.Name) {
			continue
		}
		// Size -1 means Docker could not measure it: leave it alone.
		if v.UsageData.RefCount != 0 || v.UsageData.Size != 0 {
			continue
		}
		if err := s.dc.RemoveVolume(cctx, v.Name, false); err == nil {
			removed++
		}
	}
	if removed > 0 {
		s.logf("volumes: removed %d empty, unused anonymous volume(s)", removed)
		s.st.Audit("docman", "volume.cleanup", "", fmt.Sprintf("removed %d empty, unused anonymous volumes", removed), true)
	}
}

// withoutHelpers drops DocMan's own helper containers from a list.
func withoutHelpers(list []*dock.ContainerSummary) []*dock.ContainerSummary {
	out := list[:0:0]
	for _, c := range list {
		if !isInternalHelper(c) {
			out = append(out, c)
		}
	}
	return out
}

// isInternalHelper reports a container DocMan runs for its own purposes,
// which the container and volume lists leave out.
func isInternalHelper(c *dock.ContainerSummary) bool {
	return c.Labels[explorerLabel] == explorerKind
}

// ExplorerJanitor removes helpers left from before a restart, then any that
// sit idle. Once a day, and at start, it also clears away empty anonymous
// volumes that nothing uses.
func (s *Server) ExplorerJanitor(ctx context.Context) {
	if list, err := s.dc.ListContainers(ctx, true); err == nil {
		for _, c := range list {
			if c.Labels[explorerLabel] == explorerKind {
				_ = s.dc.RemoveContainer(ctx, c.ID, true, true)
			}
		}
	}
	s.removeEmptyAnonymousVolumes(ctx)
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	lastSweep := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if time.Since(lastSweep) > 24*time.Hour {
			s.removeEmptyAnonymousVolumes(ctx)
			lastSweep = time.Now()
		}
		volumeExplorers.mu.Lock()
		known := map[string]bool{}
		for volume, h := range volumeExplorers.helpers {
			if time.Since(h.lastUsed) > explorerIdle {
				_ = s.dc.RemoveContainer(ctx, h.id, true, true)
				delete(volumeExplorers.helpers, volume)
				continue
			}
			known[h.id] = true
		}
		// Helpers this process does not know about (left by an earlier DocMan,
		// or orphaned by a failed start) are removed too, once they are old
		// enough not to be one that is starting right now.
		if list, err := s.dc.ListContainers(ctx, true); err == nil {
			for _, c := range list {
				if c.Labels[explorerLabel] == explorerKind && !known[c.ID] &&
					time.Since(time.Unix(c.Created, 0)) > time.Minute {
					_ = s.dc.RemoveContainer(ctx, c.ID, true, true)
				}
			}
		}
		volumeExplorers.mu.Unlock()
	}
}

// handleVolumeExplorerClose stops the volume's helper as soon as the explorer
// is left, rather than waiting for it to sit idle.
func (s *Server) handleVolumeExplorerClose(w http.ResponseWriter, r *http.Request) {
	s.closeExplorers(r.Context(), r.PathValue("name"))
	w.WriteHeader(http.StatusNoContent)
}

// agent runs one volume-agent command in the volume's helper.
func (s *Server) agent(ctx context.Context, volume string, args ...string) ([]byte, error) {
	id, err := s.helperFor(ctx, volume)
	if err != nil {
		return nil, err
	}
	cmd := append([]string{"/docman", volagent.Command}, args...)
	out, errOut, code, err := s.dc.RunExec(ctx, id, cmd, agentOutputSize)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		msg := strings.TrimSpace(string(errOut))
		if msg == "" {
			msg = "the operation failed"
		}
		return nil, &agentError{msg}
	}
	return out, nil
}

type agentError struct{ msg string }

func (e *agentError) Error() string { return e.msg }

func failExplorer(w http.ResponseWriter, err error) {
	var ae *agentError
	if errors.As(err, &ae) {
		fail(w, http.StatusBadRequest, ae.msg)
		return
	}
	failDocker(w, err)
}

// volPath reads and cleans the path query parameter; "/" is the volume root.
func volPath(r *http.Request, key string) string {
	p := path.Clean("/" + strings.ReplaceAll(r.URL.Query().Get(key), "\\", "/"))
	return p
}

func (s *Server) listing(ctx context.Context, volume, dir string) (*volagent.Listing, error) {
	out, err := s.agent(ctx, volume, "ls", dir)
	if err != nil {
		return nil, err
	}
	var l volagent.Listing
	if err := json.Unmarshal(out, &l); err != nil {
		return nil, fmt.Errorf("unreadable listing: %w", err)
	}
	return &l, nil
}

// ---------- handlers ----------

func (s *Server) handleVolumeFiles(w http.ResponseWriter, r *http.Request) {
	volume := r.PathValue("name")
	l, err := s.listing(r.Context(), volume, volPath(r, "path"))
	if err != nil {
		failExplorer(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		*volagent.Listing
		// DocManData marks DocMan's own volume, where changes can break it.
		DocManData bool `json:"docman_data,omitempty"`
	}{l, s.mountedBySelf(r.Context(), volume)})
}

// mountedBySelf reports whether DocMan's own container mounts the volume.
func (s *Server) mountedBySelf(ctx context.Context, volume string) bool {
	self := s.selfID()
	if self == "" {
		return false
	}
	inspect, err := s.dc.InspectContainer(ctx, self)
	if err != nil {
		return false
	}
	mounts, _ := inspect["Mounts"].([]any)
	for _, m := range mounts {
		if mm, ok := m.(map[string]any); ok && mm["Name"] == volume {
			return true
		}
	}
	return false
}

// handleVolumeDownload streams a file as itself, or a folder as a tar.
func (s *Server) handleVolumeDownload(w http.ResponseWriter, r *http.Request) {
	volume := r.PathValue("name")
	p := volPath(r, "path")
	id, err := s.helperFor(r.Context(), volume)
	if err != nil {
		failExplorer(w, err)
		return
	}
	full, _ := volagent.Resolve(p)
	body, err := s.dc.GetArchive(r.Context(), id, full)
	if err != nil {
		failDocker(w, err)
		return
	}
	defer body.Close()

	base := path.Base(p)
	if p == "/" {
		base = volume
	}
	if r.URL.Query().Get("kind") == "dir" {
		w.Header().Set("Content-Type", "application/x-tar")
		w.Header().Set("Content-Disposition", attachment(base+".tar"))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, body)
		s.st.Audit(identityOf(r).Actor(), "volume.download", volume+":"+p, "folder as tar", true)
		return
	}
	tr := tar.NewReader(body)
	hdr, err := tr.Next()
	if err != nil {
		fail(w, http.StatusBadGateway, "could not read the file: "+err.Error())
		return
	}
	if hdr.Typeflag == tar.TypeDir {
		fail(w, http.StatusBadRequest, "that is a folder; download it as a folder instead")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", attachment(base))
	if hdr.Typeflag == tar.TypeReg {
		w.Header().Set("Content-Length", strconv.FormatInt(hdr.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, tr)
	s.st.Audit(identityOf(r).Actor(), "volume.download", volume+":"+p, strconv.FormatInt(hdr.Size, 10)+" bytes", true)
}

func attachment(name string) string {
	return `attachment; filename="` + strings.NewReplacer(`"`, "", "\r", "", "\n", "").Replace(name) + `"`
}

// fileHeader reads the tar header of one file in the volume, and its content
// when limit allows.
func (s *Server) fileHeader(ctx context.Context, volume, p string, limit int64) (*tar.Header, []byte, error) {
	id, err := s.helperFor(ctx, volume)
	if err != nil {
		return nil, nil, err
	}
	full, _ := volagent.Resolve(p)
	body, err := s.dc.GetArchive(ctx, id, full)
	if err != nil {
		return nil, nil, err
	}
	defer body.Close()
	tr := tar.NewReader(body)
	hdr, err := tr.Next()
	if err != nil {
		return nil, nil, err
	}
	if limit <= 0 || hdr.Typeflag != tar.TypeReg || hdr.Size > limit {
		return hdr, nil, nil
	}
	data, err := io.ReadAll(io.LimitReader(tr, limit+1))
	return hdr, data, err
}

// handleVolumeFileContent returns a text file for the editor.
func (s *Server) handleVolumeFileContent(w http.ResponseWriter, r *http.Request) {
	volume := r.PathValue("name")
	p := volPath(r, "path")
	hdr, data, err := s.fileHeader(r.Context(), volume, p, maxEditBytes)
	if err != nil {
		if dock.NotFound(err) {
			fail(w, http.StatusNotFound, "that file no longer exists")
			return
		}
		failExplorer(w, err)
		return
	}
	switch {
	case hdr.Typeflag != tar.TypeReg:
		fail(w, http.StatusBadRequest, "only regular files can be edited")
		return
	case hdr.Size > maxEditBytes:
		failHint(w, http.StatusRequestEntityTooLarge,
			fmt.Sprintf("the file is %d bytes; files over 1 MB are downloaded rather than edited", hdr.Size),
			"download it, edit it locally, and upload it again")
		return
	case bytes.IndexByte(data[:min(len(data), 8000)], 0) >= 0:
		failHint(w, http.StatusUnsupportedMediaType, "this looks like a binary file, which cannot be edited as text",
			"download it instead")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": p, "content": string(data), "size": hdr.Size,
		"mtime": hdr.ModTime.Unix(), "mode": fmt.Sprintf("%04o", hdr.Mode&0o7777),
		"uid": hdr.Uid, "gid": hdr.Gid,
	})
}

// handleVolumeFileSave writes a file from the editor. It refuses when the file
// changed since it was opened, so two editors cannot silently overwrite each
// other. Owner and permissions are kept; a new file takes its folder's owner.
func (s *Server) handleVolumeFileSave(w http.ResponseWriter, r *http.Request) {
	volume := r.PathValue("name")
	p := volPath(r, "path")
	if p == "/" {
		fail(w, http.StatusBadRequest, "choose a file")
		return
	}
	var req struct {
		Content string `json:"content"`
		MTime   int64  `json:"mtime"` // as opened; 0 for a new file
		Create  bool   `json:"create"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	ctx := r.Context()
	hdr := &tar.Header{Name: path.Base(p), Mode: 0o644, Typeflag: tar.TypeReg}
	existing, _, err := s.fileHeader(ctx, volume, p, 0)
	switch {
	case err == nil:
		if req.Create {
			fail(w, http.StatusConflict, "a file with that name already exists")
			return
		}
		if existing.Typeflag != tar.TypeReg {
			fail(w, http.StatusBadRequest, "only regular files can be edited")
			return
		}
		if req.MTime != 0 && existing.ModTime.Unix() != req.MTime {
			failHint(w, http.StatusConflict, "the file changed since you opened it",
				"reopen it to see the current version, then make your change again")
			return
		}
		hdr.Mode, hdr.Uid, hdr.Gid = existing.Mode, existing.Uid, existing.Gid
	case dock.NotFound(err) || req.Create:
		if l, err := s.listing(ctx, volume, path.Dir(p)); err == nil {
			hdr.Uid, hdr.Gid = l.Dir.UID, l.Dir.GID
		}
	default:
		failExplorer(w, err)
		return
	}
	hdr.Size = int64(len(req.Content))
	hdr.ModTime = time.Now()
	if err := s.putFile(ctx, volume, path.Dir(p), hdr, strings.NewReader(req.Content)); err != nil {
		failExplorer(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "volume.edit", volume+":"+p, strconv.Itoa(len(req.Content))+" bytes saved", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mtime": hdr.ModTime.Unix(), "size": hdr.Size})
}

// putFile streams one file into a folder of the volume.
func (s *Server) putFile(ctx context.Context, volume, dir string, hdr *tar.Header, content io.Reader) error {
	id, err := s.helperFor(ctx, volume)
	if err != nil {
		return err
	}
	pr, pw := io.Pipe()
	go func() {
		tw := tar.NewWriter(pw)
		if err := tw.WriteHeader(hdr); err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(tw, content); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.CloseWithError(tw.Close())
	}()
	full, _ := volagent.Resolve(dir)
	err = s.dc.PutArchive(ctx, id, full, pr)
	_ = pr.Close()
	return err
}

// handleVolumeUpload stores the request body as a file in a folder. The body
// is streamed, so size is limited only by the volume.
func (s *Server) handleVolumeUpload(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	volume := r.PathValue("name")
	dir := volPath(r, "path")
	name := r.URL.Query().Get("name")
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		fail(w, http.StatusBadRequest, "invalid file name")
		return
	}
	if r.ContentLength < 0 {
		fail(w, http.StatusLengthRequired, "the upload needs a Content-Length")
		return
	}
	ctx := r.Context()
	l, err := s.listing(ctx, volume, dir)
	if err != nil {
		failExplorer(w, err)
		return
	}
	for _, e := range l.Entries {
		if e.Name == name {
			if e.Type == "dir" {
				fail(w, http.StatusConflict, "a folder with that name already exists")
				return
			}
			if !boolQuery(r, "overwrite") {
				writeJSON(w, http.StatusConflict, map[string]any{"error": name + " already exists", "exists": true})
				return
			}
		}
	}
	hdr := &tar.Header{
		Name: name, Mode: 0o644, Typeflag: tar.TypeReg, Size: r.ContentLength,
		ModTime: time.Now(), Uid: l.Dir.UID, Gid: l.Dir.GID,
	}
	if err := s.putFile(ctx, volume, dir, hdr, r.Body); err != nil {
		failExplorer(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "volume.upload", volume+":"+path.Join(dir, name),
		strconv.FormatInt(r.ContentLength, 10)+" bytes", true)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func (s *Server) handleVolumeMkdir(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	volume := r.PathValue("name")
	if _, err := s.agent(r.Context(), volume, "mkdir", req.Path); err != nil {
		failExplorer(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "volume.mkdir", volume+":"+req.Path, "folder created", true)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true})
}

func (s *Server) handleVolumeRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	volume := r.PathValue("name")
	if _, err := s.agent(r.Context(), volume, "mv", req.From, req.To); err != nil {
		failExplorer(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "volume.rename", volume+":"+req.From, "renamed to "+req.To, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleVolumeDeleteFile(w http.ResponseWriter, r *http.Request) {
	volume := r.PathValue("name")
	p := volPath(r, "path")
	if _, err := s.agent(r.Context(), volume, "rm", p); err != nil {
		failExplorer(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "volume.delete", volume+":"+p, "deleted", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
