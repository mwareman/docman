// Package srv is DocMan's HTTP layer: the JSON API, the WebSocket streams and
// the embedded single-page UI.
package srv

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"docman/internal/crypt"
	"docman/internal/dock"
	"docman/internal/hostmetrics"
	"docman/internal/netmgr"
	"docman/internal/store"
)

// Config is everything the server needs to run, resolved from the environment.
type Config struct {
	DataDir     string
	DockerHost  string
	HTTPSAddr   string
	HTTPAddr    string
	TLSCertFile string
	TLSKeyFile  string
	DisableTLS  bool
	RPID        string
	Hostnames   []string
	SessionTTL  time.Duration
	ProcPath    string
	TrustProxy  bool
	Version     string

	// APIDoc is the embedded API.md, served as documentation at GET /api.
	APIDoc []byte
	// HelpFS holds the user guide's Markdown pages, served under /help.
	HelpFS fs.FS
}

// Server owns every dependency the handlers need.
type Server struct {
	cfg  Config
	st   *store.Store
	dc   *dock.Client
	nm   *netmgr.Manager
	hm   *hostmetrics.Reader
	web  fs.FS
	mux  *http.ServeMux
	logs *log.Logger

	setupKey string

	challenges *challengeStore
	limiter    *limiter
	hub        *statsHub
	totp       *totpEnrolments
}

// New wires a server up. web is the embedded UI filesystem.
func New(cfg Config, st *store.Store, dc *dock.Client, web fs.FS, logger *log.Logger) *Server {
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 12 * time.Hour
	}
	s := &Server{
		cfg:        cfg,
		st:         st,
		dc:         dc,
		nm:         netmgr.New(dc, st),
		hm:         hostmetrics.New(cfg.ProcPath),
		web:        web,
		mux:        http.NewServeMux(),
		logs:       logger,
		challenges: newChallengeStore(),
		limiter:    newLimiter(),
		totp:       newTOTPEnrolments(),
	}
	s.hub = newStatsHub(dc, s.logf)
	s.routes()
	return s
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler { return s.withCommonHeaders(s.mux) }

// SetSetupKey records the first-run key so the bootstrap endpoint can check it.
func (s *Server) SetSetupKey(key string) { s.setupKey = key }

// SetupComplete reports whether an admin account exists.
func (s *Server) SetupComplete() bool {
	n, err := s.st.CountUsers()
	return err == nil && n > 0
}

// Docker exposes the engine client for startup checks.
func (s *Server) Docker() *dock.Client { return s.dc }

// NetManager exposes the address manager for startup reconciliation.
func (s *Server) NetManager() *netmgr.Manager { return s.nm }

// ---------- routing ----------

func (s *Server) routes() {
	// Public: bootstrap and sign-in.
	// Self-documenting: a browser gets a formatted page, anything else gets
	// the raw Markdown. Readable without signing in.
	s.mux.HandleFunc("GET /api", s.handleAPIDoc)
	s.mux.HandleFunc("GET /api.md", s.handleAPIDoc)
	// The help system: the user guide and the API reference. It documents
	// DocMan itself and nothing about this host, so it needs no sign-in.
	s.mux.HandleFunc("GET /help", s.handleHelp)
	s.mux.HandleFunc("GET /help/{page...}", s.handleHelp)
	s.mux.HandleFunc("GET /api/state", s.handleState)
	s.mux.HandleFunc("POST /api/bootstrap/verify", s.handleBootstrapVerify)
	s.mux.HandleFunc("POST /api/bootstrap/admin", s.handleBootstrapAdmin)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/passkey/login/begin", s.handlePasskeyLoginBegin)
	s.mux.HandleFunc("POST /api/auth/passkey/login/finish", s.handlePasskeyLoginFinish)
	s.mux.HandleFunc("POST /api/auth/logout", s.handleLogout)

	// Account and credentials.
	s.get("/api/account", s.handleAccount)
	s.post("/api/account/password", s.handleChangePassword)
	s.post("/api/account/username", s.handleChangeUsername)
	s.get("/api/account/sessions", s.handleListSessions)
	s.post("/api/account/sessions/revoke", s.handleRevokeSessions)
	s.post("/api/account/totp/begin", s.handleTOTPBegin)
	s.post("/api/account/totp/enable", s.handleTOTPEnable)
	s.post("/api/account/totp/disable", s.handleTOTPDisable)
	s.get("/api/passkeys", s.handleListPasskeys)
	s.post("/api/passkeys/register/begin", s.handlePasskeyRegisterBegin)
	s.post("/api/passkeys/register/finish", s.handlePasskeyRegisterFinish)
	s.patch("/api/passkeys/{id}", s.handleRenamePasskey)
	s.delete("/api/passkeys/{id}", s.handleDeletePasskey)

	// Accounts. Every account is an administrator; each signs in its own way.
	s.get("/api/users", s.handleListUsers)
	s.post("/api/users", s.handleCreateUser)
	s.post("/api/users/{id}/reset", s.handleResetUser)
	s.delete("/api/users/{id}", s.handleDeleteUser)

	// API tokens.
	s.get("/api/tokens", s.handleListTokens)
	s.post("/api/tokens", s.handleCreateToken)
	s.delete("/api/tokens/{id}", s.handleDeleteToken)

	// Host and system.
	s.get("/api/system", s.handleSystem)
	s.get("/api/system/df", s.handleDiskUsage)
	s.get("/api/system/metrics", s.handleHostMetricsOnce)
	s.get("/api/system/volume-drivers", s.handleVolumeDrivers)
	s.get("/api/audit", s.handleAudit)
	s.get("/api/settings", s.handleGetSettings)
	s.patch("/api/settings", s.handlePatchSettings)

	// Registries: where images are searched for and pulled from.
	s.get("/api/registries", s.handleListRegistries)
	s.post("/api/registries", s.handleCreateRegistry)
	s.put("/api/registries/{id}", s.handleUpdateRegistry)
	s.delete("/api/registries/{id}", s.handleDeleteRegistry)
	s.post("/api/registries/{id}/default", s.handleDefaultRegistry)
	s.post("/api/registries/{id}/test", s.handleTestRegistry)
	s.get("/api/registries/{id}/search", s.handleSearchRegistry)
	s.get("/api/registries/{id}/tags", s.handleRegistryTags)
	s.get("/api/registries/{id}/archives", s.handleRepoArchives)
	s.post("/api/registries/{id}/install", s.handleRepoInstall)

	// Containers.
	s.get("/api/containers", s.handleListContainers)
	s.post("/api/containers", s.handleCreateContainer)
	s.get("/api/containers/{id}", s.handleGetContainer)
	s.get("/api/containers/{id}/inspect", s.handleInspectContainer)
	s.get("/api/containers/{id}/spec", s.handleContainerSpec)
	s.put("/api/containers/{id}/spec", s.handleUpdateContainerSpec)
	s.post("/api/containers/{id}/start", s.handleContainerAction)
	s.post("/api/containers/{id}/stop", s.handleContainerAction)
	s.post("/api/containers/{id}/restart", s.handleContainerAction)
	s.post("/api/containers/{id}/pause", s.handleContainerAction)
	s.post("/api/containers/{id}/unpause", s.handleContainerAction)
	s.post("/api/containers/{id}/kill", s.handleContainerAction)
	s.post("/api/containers/{id}/recreate", s.handleRecreateContainer)
	// Uploads in pieces, which pass proxy and WAF request size limits.
	s.post("/api/uploads", s.handleStartUpload)
	s.get("/api/uploads/{id}", s.handleGetUpload)
	s.put("/api/uploads/{id}", s.handleUploadChunk)
	// POST as well: browsers silently resend a PUT whose connection drops,
	// which would hide from the uploader that a piece was too large.
	s.post("/api/uploads/{id}", s.handleUploadChunk)
	s.post("/api/uploads/{id}/complete", s.handleCompleteUpload)
	s.delete("/api/uploads/{id}", s.handleCancelUpload)
	s.get("/api/jobs", s.handleListJobs)
	s.get("/api/jobs/{id}", s.handleGetJob)
	s.post("/api/containers/{id}/rename", s.handleRenameContainer)
	s.delete("/api/containers/{id}", s.handleRemoveContainer)
	s.get("/api/containers/{id}/logs", s.handleContainerLogsOnce)
	s.get("/api/containers/{id}/stats", s.handleContainerStatsOnce)
	s.get("/api/containers/{id}/top", s.handleContainerTop)
	s.post("/api/containers/{id}/ip", s.handlePinIP)
	s.delete("/api/containers/{id}/ip", s.handleUnpinIP)

	// Images.
	s.get("/api/images", s.handleListImages)
	s.get("/api/images/updates", s.handleImageUpdates)
	s.post("/api/images/updates/check", s.handleCheckImageUpdates)
	s.get("/api/images/{name...}", s.handleInspectImage)
	s.post("/api/images/pull", s.handlePullImage)
	s.post("/api/images/upload", s.handleUploadImage)
	s.post("/api/images/tag", s.handleTagImage)
	s.post("/api/images/remove", s.handleRemoveImage)
	s.post("/api/images/prune", s.handlePruneImages)
	s.post("/api/images/deploy-plan", s.handleDeployPlan)

	// Volumes.
	s.get("/api/volumes", s.handleListVolumes)
	s.post("/api/volumes", s.handleCreateVolume)
	s.get("/api/volumes/{name}", s.handleInspectVolume)
	s.delete("/api/volumes/{name}", s.handleRemoveVolume)
	s.post("/api/volumes/prune", s.handlePruneVolumes)
	// Volume explorer: browse, download, upload and edit files in a volume.
	s.get("/api/volumes/{name}/files", s.handleVolumeFiles)
	s.delete("/api/volumes/{name}/files", s.handleVolumeDeleteFile)
	s.get("/api/volumes/{name}/files/download", s.handleVolumeDownload)
	s.get("/api/volumes/{name}/files/content", s.handleVolumeFileContent)
	s.put("/api/volumes/{name}/files/content", s.handleVolumeFileSave)
	s.post("/api/volumes/{name}/files/upload", s.handleVolumeUpload)
	s.post("/api/volumes/{name}/files/mkdir", s.handleVolumeMkdir)
	s.post("/api/volumes/{name}/files/rename", s.handleVolumeRename)
	s.delete("/api/volumes/{name}/explorer", s.handleVolumeExplorerClose)

	// Networks and fixed addresses.
	s.get("/api/networks", s.handleListNetworks)
	s.get("/api/networks/{id}", s.handleInspectNetwork)
	s.get("/api/addresses", s.handleAddresses)
	s.post("/api/addresses/reconcile", s.handleReconcileAddresses)

	// Streams.
	s.get("/api/stream/host", s.handleHostStream)
	s.get("/api/stream/containers", s.handleContainersStream)
	s.get("/api/stream/logs/{id}", s.handleLogStream)
	s.get("/api/stream/stats/{id}", s.handleStatsStream)
	s.get("/api/stream/exec/{id}", s.handleExecStream)
	s.get("/api/stream/events", s.handleEventStream)

	// UI.
	s.mux.HandleFunc("/", s.handleStatic)
}

func (s *Server) get(pattern string, h http.HandlerFunc)    { s.route(http.MethodGet, pattern, h) }
func (s *Server) post(pattern string, h http.HandlerFunc)   { s.route(http.MethodPost, pattern, h) }
func (s *Server) put(pattern string, h http.HandlerFunc)    { s.route(http.MethodPut, pattern, h) }
func (s *Server) patch(pattern string, h http.HandlerFunc)  { s.route(http.MethodPatch, pattern, h) }
func (s *Server) delete(pattern string, h http.HandlerFunc) { s.route(http.MethodDelete, pattern, h) }

func (s *Server) route(method, pattern string, h http.HandlerFunc) {
	s.mux.Handle(method+" "+pattern, s.requireAuth(h))
}

func (s *Server) withCommonHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		// The UI ships with the binary and loads nothing from anywhere else.
		h.Set("Content-Security-Policy",
			"default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; "+
				"script-src 'self'; connect-src 'self' ws: wss:; font-src 'self'; "+
				"base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- response helpers ----------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The response is already committed; nothing useful is left to do.
		return
	}
}

type errorBody struct {
	Error string `json:"error"`
	Hint  string `json:"hint,omitempty"`
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

func failHint(w http.ResponseWriter, status int, msg, hint string) {
	writeJSON(w, status, errorBody{Error: msg, Hint: hint})
}

// failDocker maps an Engine error onto a sensible HTTP status.
func failDocker(w http.ResponseWriter, err error) {
	var de *dock.Error
	if errors.As(err, &de) {
		status := de.Status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		fail(w, status, de.Message)
		return
	}
	if errors.Is(err, context.Canceled) {
		return
	}
	fail(w, http.StatusBadGateway, "docker: "+err.Error())
}

func failStore(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrNotFound) {
		fail(w, http.StatusNotFound, "not found")
		return
	}
	fail(w, http.StatusInternalServerError, err.Error())
}

// decodeBody reads a JSON request body with a size cap.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(dst); err != nil {
		fail(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

func pathInt(r *http.Request, name string) (int64, bool) {
	n, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func boolQuery(r *http.Request, name string) bool {
	switch strings.ToLower(r.URL.Query().Get(name)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func intQuery(r *http.Request, name string, def int) int {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// ---------- request context ----------

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
			if i := strings.IndexByte(fwd, ','); i > 0 {
				return strings.TrimSpace(fwd[:i])
			}
			return strings.TrimSpace(fwd)
		}
		if real := r.Header.Get("X-Real-Ip"); real != "" {
			return strings.TrimSpace(real)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// requestScheme reports whether the browser reached DocMan over TLS.
func (s *Server) requestScheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if s.cfg.TrustProxy {
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			return strings.ToLower(strings.TrimSpace(proto))
		}
	}
	return "http"
}

func (s *Server) requestHost(r *http.Request) string {
	host := r.Host
	if s.cfg.TrustProxy {
		if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
			host = strings.TrimSpace(fwd)
		}
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func (s *Server) origin(r *http.Request) string {
	host := r.Host
	if s.cfg.TrustProxy {
		if fwd := r.Header.Get("X-Forwarded-Host"); fwd != "" {
			host = strings.TrimSpace(fwd)
		}
	}
	return s.requestScheme(r) + "://" + host
}

func (s *Server) logf(format string, args ...any) {
	if s.logs != nil {
		s.logs.Printf(format, args...)
	}
}

// ---------- rate limiting ----------

// limiter is a small per-key token bucket, used on the unauthenticated paths.
type limiter struct {
	mu      sync.Mutex
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter() *limiter {
	return &limiter{buckets: map[string]*bucket{}}
}

// allow consumes a token for key, refilling at rate per second up to burst.
func (l *limiter) allow(key string, rate float64, burst float64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) > 4096 {
			l.buckets = map[string]*bucket{}
		}
		b = &bucket{tokens: burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * rate
	if b.tokens > burst {
		b.tokens = burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// ---------- WebAuthn challenge store ----------

type pendingChallenge struct {
	challenge []byte
	userID    int64
	expires   time.Time
}

type challengeStore struct {
	mu    sync.Mutex
	items map[string]pendingChallenge
}

func newChallengeStore() *challengeStore {
	return &challengeStore{items: map[string]pendingChallenge{}}
}

func (c *challengeStore) put(userID int64) (id string, challenge []byte) {
	challenge = crypt.RandBytes(32)
	id = crypt.RandURL(16)
	c.mu.Lock()
	defer c.mu.Unlock()
	now := time.Now()
	for k, v := range c.items {
		if v.expires.Before(now) {
			delete(c.items, k)
		}
	}
	c.items[id] = pendingChallenge{challenge: challenge, userID: userID, expires: now.Add(3 * time.Minute)}
	return id, challenge
}

// take consumes a challenge; a challenge is valid exactly once.
func (c *challengeStore) take(id string) (pendingChallenge, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.items[id]
	if !ok {
		return pendingChallenge{}, false
	}
	delete(c.items, id)
	if v.expires.Before(time.Now()) {
		return pendingChallenge{}, false
	}
	return v, true
}

// Housekeeping runs periodic maintenance until ctx is cancelled.
func (s *Server) Housekeeping(ctx context.Context) {
	t := time.NewTicker(30 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = s.st.PurgeExpired()
			s.st.TrimAudit(5000)
		}
	}
}
