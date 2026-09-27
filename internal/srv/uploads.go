package srv

import (
	"archive/tar"
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"docman/internal/crypt"
)

// Chunked uploads.
//
// Proxies and WAFs in front of DocMan — Cloudflare and cloudflared, nginx,
// lighttpd, Traefik and the rest — limit how large one request may be, and
// the limit is rarely known. A multi-gigabyte image archive sent as a single
// request is cut off and leaves a broken import. So large files arrive as a
// series of pieces: the browser sends each piece as its own small request,
// DocMan appends it to a temporary file, and only once every byte is there is
// the file handed to Docker. The browser finds a piece size the path allows
// by itself, shrinking after a refusal and growing while pieces go through,
// so nobody has to know where the limit is.
//
// The import then runs as a background job, so a long docker load cannot run
// into a proxy's response time limit either.

const (
	uploadIdle   = 24 * time.Hour // unfinished uploads are discarded after this
	maxChunkSize = 256 << 20      // a single piece may not exceed this
)

type uploadSession struct {
	ID        string
	Kind      string // image or file
	Name      string
	Size      int64
	Received  int64
	Volume    string // for files
	Dir       string
	Overwrite bool
	Actor     string
	Path      string // temporary file
	Updated   time.Time
	Busy      bool // a piece is being written, or it is being imported
	mu        sync.Mutex
}

type uploadRegistry struct {
	mu       sync.Mutex
	sessions map[string]*uploadSession
}

var uploads = &uploadRegistry{sessions: map[string]*uploadSession{}}

func (s *Server) uploadDir() string { return filepath.Join(s.cfg.DataDir, "uploads") }

func (u *uploadRegistry) get(id string) *uploadSession {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.sessions[id]
}

func (u *uploadRegistry) drop(id string) {
	u.mu.Lock()
	sess := u.sessions[id]
	delete(u.sessions, id)
	u.mu.Unlock()
	if sess != nil {
		_ = os.Remove(sess.Path)
	}
}

// UploadJanitor clears temporary files from before a restart, then any
// upload left unfinished for a day.
func (s *Server) UploadJanitor(ctx context.Context) {
	_ = os.RemoveAll(s.uploadDir())
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		uploads.mu.Lock()
		var stale []string
		for id, sess := range uploads.sessions {
			if !sess.Busy && time.Since(sess.Updated) > uploadIdle {
				stale = append(stale, id)
			}
		}
		uploads.mu.Unlock()
		for _, id := range stale {
			uploads.drop(id)
		}
	}
}

func uploadView(sess *uploadSession) map[string]any {
	return map[string]any{
		"id": sess.ID, "kind": sess.Kind, "name": sess.Name,
		"size": sess.Size, "received": sess.Received, "max_chunk": maxChunkSize,
	}
}

// handleStartUpload opens an upload: an image archive, or a file for a
// volume folder.
func (s *Server) handleStartUpload(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Size      int64  `json:"size"`
		Volume    string `json:"volume"`
		Path      string `json:"path"`
		Overwrite bool   `json:"overwrite"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if req.Size <= 0 {
		fail(w, http.StatusBadRequest, "the file is empty")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		fail(w, http.StatusBadRequest, "invalid file name")
		return
	}
	sess := &uploadSession{
		ID: crypt.RandHex(12), Kind: req.Kind, Name: name, Size: req.Size,
		Actor: identityOf(r).Actor(), Updated: time.Now(), Overwrite: req.Overwrite,
	}
	switch req.Kind {
	case "image":
	case "file":
		if req.Volume == "" {
			fail(w, http.StatusBadRequest, "choose a volume")
			return
		}
		sess.Volume = req.Volume
		sess.Dir = path.Clean("/" + strings.ReplaceAll(req.Path, "\\", "/"))
		// Refuse to overwrite before any bytes are sent, not after.
		l, err := s.listing(r.Context(), sess.Volume, sess.Dir)
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
				if !req.Overwrite {
					writeJSON(w, http.StatusConflict, map[string]any{"error": name + " already exists", "exists": true})
					return
				}
			}
		}
	default:
		fail(w, http.StatusBadRequest, "kind must be image or file")
		return
	}
	if err := os.MkdirAll(s.uploadDir(), 0o700); err != nil {
		fail(w, http.StatusInternalServerError, "could not prepare the upload: "+err.Error())
		return
	}
	sess.Path = filepath.Join(s.uploadDir(), sess.ID+".part")
	f, err := os.OpenFile(sess.Path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not prepare the upload: "+err.Error())
		return
	}
	f.Close()
	uploads.mu.Lock()
	uploads.sessions[sess.ID] = sess
	uploads.mu.Unlock()
	writeJSON(w, http.StatusCreated, uploadView(sess))
}

func (s *Server) handleGetUpload(w http.ResponseWriter, r *http.Request) {
	sess := uploads.get(r.PathValue("id"))
	if sess == nil {
		fail(w, http.StatusNotFound, "no such upload; it may have expired or been cancelled")
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	writeJSON(w, http.StatusOK, uploadView(sess))
}

// handleUploadChunk appends one piece at ?offset=. A piece that arrives
// incomplete — cut off by a proxy — is discarded, so the file only ever holds
// whole pieces; the client resends from the offset DocMan reports.
func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	sess := uploads.get(r.PathValue("id"))
	if sess == nil {
		fail(w, http.StatusNotFound, "no such upload; it may have expired or been cancelled")
		return
	}
	offset, err := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		fail(w, http.StatusBadRequest, "offset is required")
		return
	}
	want := r.ContentLength
	if want <= 0 || want > maxChunkSize {
		fail(w, http.StatusBadRequest, fmt.Sprintf("each piece must be between 1 byte and %d bytes, with a Content-Length", maxChunkSize))
		return
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.Busy {
		fail(w, http.StatusConflict, "this upload is being imported")
		return
	}
	if offset != sess.Received {
		// Out of step, usually after a retry: say where to carry on from.
		writeJSON(w, http.StatusConflict, map[string]any{"error": "unexpected offset", "received": sess.Received})
		return
	}
	if offset+want > sess.Size {
		fail(w, http.StatusBadRequest, "the piece goes past the end of the file")
		return
	}
	f, err := os.OpenFile(sess.Path, os.O_WRONLY, 0o600)
	if err != nil {
		fail(w, http.StatusGone, "the upload's temporary file is gone; start again")
		return
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	n, copyErr := io.Copy(f, io.LimitReader(r.Body, want))
	if copyErr != nil || n != want {
		// Throw away the partial piece so the file ends on a whole one.
		_ = f.Truncate(offset)
		msg := "the piece arrived incomplete"
		if copyErr != nil {
			msg += ": " + copyErr.Error()
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": msg, "received": sess.Received})
		return
	}
	if err := f.Sync(); err != nil {
		_ = f.Truncate(offset)
		fail(w, http.StatusInsufficientStorage, "could not store the piece: "+err.Error())
		return
	}
	sess.Received = offset + n
	sess.Updated = time.Now()
	writeJSON(w, http.StatusOK, map[string]any{"received": sess.Received, "size": sess.Size})
}

func (s *Server) handleCancelUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if sess := uploads.get(id); sess != nil {
		sess.mu.Lock()
		busy := sess.Busy
		sess.mu.Unlock()
		if busy {
			fail(w, http.StatusConflict, "the upload is already being imported and can no longer be cancelled")
			return
		}
	}
	uploads.drop(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleCompleteUpload checks that every byte is there, then imports the file
// as a background job and returns the job to follow.
func (s *Server) handleCompleteUpload(w http.ResponseWriter, r *http.Request) {
	sess := uploads.get(r.PathValue("id"))
	if sess == nil {
		fail(w, http.StatusNotFound, "no such upload; it may have expired or been cancelled")
		return
	}
	sess.mu.Lock()
	if sess.Busy {
		sess.mu.Unlock()
		fail(w, http.StatusConflict, "this upload is already being imported")
		return
	}
	if sess.Received != sess.Size {
		received := sess.Received
		sess.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": fmt.Sprintf("the upload is incomplete: %d of %d bytes received", received, sess.Size), "received": received,
		})
		return
	}
	if info, err := os.Stat(sess.Path); err != nil || info.Size() != sess.Size {
		sess.mu.Unlock()
		fail(w, http.StatusGone, "the upload's temporary file is incomplete; start again")
		return
	}
	sess.Busy = true
	sess.mu.Unlock()

	kind := "upload"
	job := s.startJob(kind, sess.Name, "", sess.Actor, func(ctx context.Context) (string, error) {
		defer uploads.drop(sess.ID)
		if sess.Kind == "file" {
			return "", s.importUploadedFile(ctx, sess)
		}
		loaded, err := s.importUploadedImage(ctx, sess)
		if err == nil {
			backgroundJobs.update(jobIDFromCtx(ctx), func(j *Job) { j.Result = loaded })
		}
		return "", err
	})
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "job": job})
}

// importUploadedImage streams the finished archive into docker load,
// reporting how much of it Docker has read.
func (s *Server) importUploadedImage(ctx context.Context, sess *uploadSession) ([]string, error) {
	f, err := os.Open(sess.Path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	progress := progressFrom(ctx)
	counted := &countingReader{r: f, total: sess.Size, report: func(pct int) {
		progress(fmt.Sprintf("Importing into Docker: %d%%", pct))
	}}
	progress("Importing into Docker: 0%")
	loaded, err := s.loadArchive(ctx, counted)
	if err != nil {
		s.st.Audit(sess.Actor, "image.upload", sess.Name, err.Error(), false)
		return nil, err
	}
	for _, ref := range loaded {
		if !strings.HasPrefix(ref, "sha256:") {
			_ = s.st.MarkUploaded(normalizeImageRef(ref))
		}
	}
	s.st.Audit(sess.Actor, "image.upload", strings.Join(loaded, ", "),
		fmt.Sprintf("image archive imported (%d bytes, in pieces)", sess.Size), true)
	progress("Imported")
	return loaded, nil
}

// loadArchive streams an image archive into docker load and returns the
// references Docker reports loading.
func (s *Server) loadArchive(ctx context.Context, archive io.Reader) ([]string, error) {
	body, err := s.dc.LoadImage(ctx, archive)
	if err != nil {
		return nil, fmt.Errorf("Docker could not import the archive: %w", err)
	}
	defer body.Close()
	loaded := []string{}
	problem := ""
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 0, 64*1024), 4<<20)
	for sc.Scan() {
		if ref := loadedImageRef(sc.Bytes()); ref != "" {
			loaded = append(loaded, ref)
		}
		if e := streamLineError(sc.Bytes()); e != "" {
			problem = e
		}
	}
	if problem != "" {
		return nil, fmt.Errorf("Docker could not import the archive: %s", problem)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("the archive could not be read to the end: %w", err)
	}
	if len(loaded) == 0 {
		return nil, errors.New("Docker read the archive but reported no image in it; is it a docker save archive?")
	}
	return loaded, nil
}

// importUploadedFile moves a finished file into its volume folder.
func (s *Server) importUploadedFile(ctx context.Context, sess *uploadSession) error {
	f, err := os.Open(sess.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	progressFrom(ctx)("Saving into the volume")
	hdr := &tar.Header{Name: sess.Name, Mode: 0o644, Typeflag: tar.TypeReg, Size: sess.Size, ModTime: time.Now()}
	if l, err := s.listing(ctx, sess.Volume, sess.Dir); err == nil {
		hdr.Uid, hdr.Gid = l.Dir.UID, l.Dir.GID
	}
	if err := s.putFile(ctx, sess.Volume, sess.Dir, hdr, f); err != nil {
		return err
	}
	s.st.Audit(sess.Actor, "volume.upload", sess.Volume+":"+path.Join(sess.Dir, sess.Name),
		fmt.Sprintf("%d bytes, in pieces", sess.Size), true)
	return nil
}

// countingReader reports, at most once a second, how far through a file a
// reader has got.
type countingReader struct {
	r      io.Reader
	read   int64
	total  int64
	last   time.Time
	report func(pct int)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.read += int64(n)
	if c.total > 0 && time.Since(c.last) > time.Second {
		c.last = time.Now()
		c.report(int(c.read * 100 / c.total))
	}
	return n, err
}
