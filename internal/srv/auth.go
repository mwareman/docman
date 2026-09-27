package srv

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"docman/internal/crypt"
	"docman/internal/store"
	"docman/internal/wa"
)

const sessionCookie = "docman_session"

// passkeyThreshold is how many passkeys must exist before password sign-in is
// switched off. Two means losing one device never locks the operator out.
const passkeyThreshold = 2

type identity struct {
	UserID    int64
	Username  string
	Kind      string // "session" or "token"
	SessionID string
	TokenID   int64
	TokenName string
}

// Actor is the audit-log name for this caller.
func (i *identity) Actor() string {
	if i == nil {
		return "anonymous"
	}
	if i.Kind == "token" {
		return "token:" + i.TokenName
	}
	return i.Username
}

type ctxKey int

const identityKey ctxKey = 1

func identityOf(r *http.Request) *identity {
	v, _ := r.Context().Value(identityKey).(*identity)
	return v
}

// ---------- middleware ----------

func (s *Server) requireAuth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, err := s.authenticate(r)
		if err != nil {
			fail(w, http.StatusUnauthorized, err.Error())
			return
		}
		// Cookie-authenticated writes must carry the custom header, which a
		// cross-site form or image cannot set.
		if !isSafeMethod(r.Method) && id.Kind == "session" && !csrfOK(r) {
			fail(w, http.StatusForbidden, "missing DocMan request header")
			return
		}
		ctx := context.WithValue(r.Context(), identityKey, id)
		next(w, r.WithContext(ctx))
	})
}

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func csrfOK(r *http.Request) bool {
	return r.Header.Get("X-Docman-Csrf") == "1"
}

func (s *Server) authenticate(r *http.Request) (*identity, error) {
	if auth := r.Header.Get("Authorization"); auth != "" {
		token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer"))
		if token == auth {
			return nil, errors.New("unsupported authorization scheme; use Bearer")
		}
		return s.authenticateToken(token)
	}
	// WebSocket clients cannot set headers, so a query token is allowed there.
	if tok := r.URL.Query().Get("access_token"); tok != "" && strings.HasPrefix(r.URL.Path, "/api/stream/") {
		return s.authenticateToken(tok)
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return nil, errors.New("not signed in")
	}
	sess, err := s.st.SessionByID(c.Value)
	if err != nil {
		return nil, errors.New("session expired")
	}
	user, err := s.st.UserByID(sess.UserID)
	if err != nil {
		return nil, errors.New("account no longer exists")
	}
	// Slide the window only occasionally to avoid a write on every request.
	if time.Now().Unix()-sess.SeenAt > 60 {
		_ = s.st.TouchSession(sess.ID, s.cfg.SessionTTL)
	}
	return &identity{UserID: user.ID, Username: user.Username, Kind: "session", SessionID: sess.ID}, nil
}

func (s *Server) authenticateToken(token string) (*identity, error) {
	prefix, err := crypt.TokenPrefix(token)
	if err != nil {
		return nil, errors.New("malformed API token")
	}
	rec, err := s.st.APITokenByPrefix(prefix)
	if err != nil {
		return nil, errors.New("unknown API token")
	}
	if !crypt.ConstantTimeEqualString(rec.Hash, crypt.SHA256Hex(token)) {
		return nil, errors.New("invalid API token")
	}
	if rec.Revoked {
		return nil, errors.New("API token revoked")
	}
	if rec.ExpiresAt > 0 && rec.ExpiresAt < time.Now().Unix() {
		return nil, errors.New("API token expired")
	}
	s.st.TouchAPIToken(rec.ID)
	// A token belongs to DocMan, not to an account: it has full rights but no
	// account of its own, so it keeps working whoever issued it.
	return &identity{Kind: "token", TokenID: rec.ID, TokenName: rec.Name}, nil
}

// requireAccount stops account-specific requests made with an API token,
// which has no password, passkeys or sessions of its own.
func requireAccount(w http.ResponseWriter, id *identity) bool {
	if id == nil || id.UserID == 0 {
		fail(w, http.StatusForbidden, "API tokens are not tied to an account; sign in to manage your own account")
		return false
	}
	return true
}

// ---------- sign-in state ----------

// passwordEnabledFor reports whether an account accepts username-and-password
// sign-in.
//
// Two passkeys switch it off: at that point passkeys are both stronger and
// fully redundant. Enrolling an authenticator app brings it back, because a
// password plus a one-time code is a sound second route in and does not depend
// on owning a passkey-capable device. Each account follows the rule on its own.
func (s *Server) passwordEnabledFor(user *store.User) bool {
	if user == nil || user.PwHash == "" {
		return false
	}
	if user.TOTPEnabled {
		return true
	}
	n, err := s.st.CountCredentials(user.ID)
	if err != nil {
		return true
	}
	return n < passkeyThreshold
}

// totpEnabledForID reports whether an account has an authenticator app.
func (s *Server) totpEnabledForID(userID int64) bool {
	user, err := s.st.UserByID(userID)
	return err == nil && user.TOTPEnabled
}

// passwordEnabledForID is passwordEnabledFor by account id.
func (s *Server) passwordEnabledForID(userID int64) bool {
	user, err := s.st.UserByID(userID)
	if err != nil {
		return false
	}
	return s.passwordEnabledFor(user)
}

// signInSummary says which sign-in methods the sign-in page should offer:
// the password form while any account can use it, the code field while any
// account needs one, and the passkey button once any passkey exists. It is
// deliberately about DocMan as a whole, so it names no account.
func (s *Server) signInSummary() (password, totp, passkeys bool) {
	users, err := s.st.ListUsers()
	if err != nil {
		return true, false, false
	}
	for _, u := range users {
		if s.passwordEnabledFor(u) {
			password = true
		}
		if u.TOTPEnabled {
			totp = true
		}
	}
	if n, err := s.st.CountAllCredentials(); err == nil {
		passkeys = n > 0
	}
	return password, totp, passkeys
}

type stateResponse struct {
	SetupComplete   bool   `json:"setup_complete"`
	Authenticated   bool   `json:"authenticated"`
	Username        string `json:"username,omitempty"`
	AuthKind        string `json:"auth_kind,omitempty"`
	PasswordEnabled bool   `json:"password_enabled"`
	TOTPEnabled     bool   `json:"totp_enabled"`
	PasskeyCount    int    `json:"passkey_count"`
	PasskeyTarget   int    `json:"passkey_target"`
	PasskeyReady    bool   `json:"passkey_ready"`
	SecureContext   bool   `json:"secure_context"`
	RPID            string `json:"rp_id"`
	RPProblem       string `json:"rp_problem,omitempty"`
	Version         string `json:"version"`
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	resp := stateResponse{
		SetupComplete:   s.SetupComplete(),
		PasswordEnabled: true,
		PasskeyTarget:   passkeyThreshold,
		Version:         s.cfg.Version,
	}
	host := s.requestHost(r)
	resp.SecureContext = s.requestScheme(r) == "https" || host == "localhost" || host == "127.0.0.1" || host == "::1"

	if rp, err := s.rp(r); err != nil {
		resp.RPProblem = err.Error()
	} else {
		resp.RPID = rp.ID
	}
	if resp.SetupComplete {
		resp.PasswordEnabled, resp.TOTPEnabled, resp.PasskeyReady = s.signInSummary()
	}
	if id, err := s.authenticate(r); err == nil {
		resp.Authenticated = true
		resp.Username = id.Username
		resp.AuthKind = id.Kind
		if id.Kind == "token" {
			resp.Username = "token:" + id.TokenName
		}
		// Once signed in, the figures describe the caller's own account.
		if user, err := s.st.UserByID(id.UserID); err == nil {
			resp.PasswordEnabled = s.passwordEnabledFor(user)
			resp.TOTPEnabled = user.TOTPEnabled
			if n, err := s.st.CountCredentials(user.ID); err == nil {
				resp.PasskeyCount = n
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// rp resolves the WebAuthn relying party for this request.
func (s *Server) rp(r *http.Request) (wa.RP, error) {
	host := s.requestHost(r)
	id := s.cfg.RPID
	if id == "" {
		id = s.st.Setting("rp_id", "")
	}
	if id == "" {
		id = host
	}
	if id == "" {
		return wa.RP{}, errors.New("cannot determine a hostname for passkeys")
	}
	if net.ParseIP(id) != nil {
		return wa.RP{}, fmt.Errorf("passkeys need a hostname, but DocMan was reached at the IP address %s; browse to a DNS or hosts-file name instead, or set DOCMAN_RP_ID", id)
	}
	if host != id && !strings.HasSuffix(host, "."+id) {
		return wa.RP{}, fmt.Errorf("passkey identity %q does not match the address %q you used to reach DocMan", id, host)
	}
	return wa.RP{ID: id, Name: "DocMan", Origins: []string{s.origin(r)}}, nil
}

// ---------- first run ----------

type bootstrapRequest struct {
	Key      string `json:"key"`
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) checkSetupKey(w http.ResponseWriter, r *http.Request, key string) bool {
	if !s.limiter.allow("setup:"+s.clientIP(r), 0.2, 5) {
		fail(w, http.StatusTooManyRequests, "too many attempts; wait a moment and try again")
		return false
	}
	if s.setupKey == "" {
		fail(w, http.StatusConflict, "setup is not available")
		return false
	}
	got := crypt.NormalizeSetupKey(key)
	want := crypt.NormalizeSetupKey(s.setupKey)
	if got == "" || !crypt.ConstantTimeEqualString(got, want) {
		s.st.Audit("anonymous", "bootstrap.verify", "", "wrong setup key from "+s.clientIP(r), false)
		fail(w, http.StatusUnauthorized, "that setup key does not match the one in the container log")
		return false
	}
	return true
}

func (s *Server) handleBootstrapVerify(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		fail(w, http.StatusForbidden, "missing DocMan request header")
		return
	}
	if s.SetupComplete() {
		fail(w, http.StatusConflict, "DocMan is already set up")
		return
	}
	var req bootstrapRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.checkSetupKey(w, r, req.Key) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleBootstrapAdmin(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		fail(w, http.StatusForbidden, "missing DocMan request header")
		return
	}
	if s.SetupComplete() {
		fail(w, http.StatusConflict, "DocMan is already set up")
		return
	}
	var req bootstrapRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if !s.checkSetupKey(w, r, req.Key) {
		return
	}
	username := strings.TrimSpace(req.Username)
	if err := validateUsername(username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validatePassword(req.Password); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := s.st.CreateUser(username, crypt.HashPassword(req.Password))
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not create the account: "+err.Error())
		return
	}
	// The setup key is single use.
	s.setupKey = ""
	s.st.Audit(username, "bootstrap.complete", username, "admin account created", true)
	s.logf("setup complete: admin account %q created", username)
	s.startSession(w, r, user, "password")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": username})
}

func validateUsername(u string) error {
	if len(u) < 3 || len(u) > 32 {
		return errors.New("username must be 3 to 32 characters")
	}
	for _, c := range u {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
		default:
			return errors.New("username may contain only letters, digits, dot, dash and underscore")
		}
	}
	return nil
}

func validatePassword(p string) error {
	if len(p) < 12 {
		return errors.New("password must be at least 12 characters")
	}
	if len(p) > 256 {
		return errors.New("password must be at most 256 characters")
	}
	var hasLetter, hasOther bool
	for _, c := range p {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			hasLetter = true
		} else {
			hasOther = true
		}
	}
	if !hasLetter || !hasOther {
		return errors.New("password must mix letters with digits or symbols")
	}
	return nil
}

// ---------- sessions ----------

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, user *store.User, method string) {
	id := crypt.RandURL(32)
	if _, err := s.st.CreateSession(id, user.ID, method, s.cfg.SessionTTL, s.clientIP(r), r.UserAgent()); err != nil {
		fail(w, http.StatusInternalServerError, "could not start a session: "+err.Error())
		return
	}
	_ = s.st.RecordLogin(user.ID)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.requestScheme(r) == "https",
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.requestScheme(r) == "https",
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		fail(w, http.StatusForbidden, "missing DocMan request header")
		return
	}
	if !s.SetupComplete() {
		fail(w, http.StatusConflict, "DocMan has not been set up yet")
		return
	}
	if !s.limiter.allow("login:"+s.clientIP(r), 0.2, 8) {
		fail(w, http.StatusTooManyRequests, "too many sign-in attempts; wait a moment and try again")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	user, err := s.st.UserByName(strings.TrimSpace(req.Username))
	passwordOK := false
	if err == nil {
		passwordOK = crypt.VerifyPassword(user.PwHash, req.Password)
	} else {
		// Same hashing work for an unknown username, so the response time does
		// not say whether the account exists.
		crypt.SpendVerifyWork(req.Password)
	}

	// An account with two passkeys and no authenticator app has password
	// sign-in switched off. That is only said once the password is right, so
	// the answer never reveals which usernames exist.
	if passwordOK && !s.passwordEnabledFor(user) {
		s.st.Audit(user.Username, "auth.login", "", "password sign-in refused: switched off for this account", false)
		failHint(w, http.StatusForbidden,
			"password sign-in is switched off for this account because it has two passkeys",
			"sign in with a passkey, or ask another DocMan user to reset your sign-in")
		return
	}

	// With an authenticator enrolled the code travels in the same request, and
	// a wrong password and a wrong code are reported identically so this
	// endpoint cannot be used to test passwords on their own.
	if err == nil && user.TOTPEnabled {
		if crypt.NormalizeTOTPCode(req.Code) == "" {
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":         "an authenticator code is required",
				"hint":          "enter the current six digit code from your authenticator app",
				"totp_required": true,
			})
			return
		}
		step, codeErr := crypt.VerifyTOTP(user.TOTPSecret, req.Code, time.Now(), user.TOTPLastStep)
		if !passwordOK || codeErr != nil {
			detail := "failed password sign-in from " + s.clientIP(r)
			if passwordOK {
				detail = "wrong authenticator code from " + s.clientIP(r)
			}
			s.st.Audit(req.Username, "auth.login", "", detail, false)
			writeJSON(w, http.StatusUnauthorized, map[string]any{
				"error":         "incorrect username, password or authenticator code",
				"hint":          "each code works once; if you just used this one, wait for your app to show the next",
				"totp_required": true,
			})
			return
		}
		_ = s.st.RecordTOTPStep(user.ID, step)
		s.st.Audit(user.Username, "auth.login", "", "password and authenticator sign-in from "+s.clientIP(r), true)
		s.startSession(w, r, user, "password+totp")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": user.Username})
		return
	}

	if !passwordOK {
		s.st.Audit(req.Username, "auth.login", "", "failed password sign-in from "+s.clientIP(r), false)
		fail(w, http.StatusUnauthorized, "incorrect username or password")
		return
	}
	s.st.Audit(user.Username, "auth.login", "", "password sign-in from "+s.clientIP(r), true)
	s.startSession(w, r, user, "password")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": user.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = s.st.DeleteSession(c.Value)
	}
	s.clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- passkey sign-in ----------

func (s *Server) handlePasskeyLoginBegin(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		fail(w, http.StatusForbidden, "missing DocMan request header")
		return
	}
	if !s.SetupComplete() {
		fail(w, http.StatusConflict, "DocMan has not been set up yet")
		return
	}
	if !s.limiter.allow("passkey:"+s.clientIP(r), 1, 20) {
		fail(w, http.StatusTooManyRequests, "too many attempts; wait a moment and try again")
		return
	}
	rp, err := s.rp(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	id, challenge := s.challenges.put(0)
	// No allow-list: the platform offers whichever DocMan passkey it holds.
	writeJSON(w, http.StatusOK, map[string]any{
		"challenge_id": id,
		"options":      wa.NewRequestOptions(rp, challenge, nil),
	})
}

func (s *Server) handlePasskeyLoginFinish(w http.ResponseWriter, r *http.Request) {
	if !csrfOK(r) {
		fail(w, http.StatusForbidden, "missing DocMan request header")
		return
	}
	var req struct {
		ChallengeID string                `json:"challenge_id"`
		Response    *wa.AssertionResponse `json:"response"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	pending, ok := s.challenges.take(req.ChallengeID)
	if !ok {
		fail(w, http.StatusBadRequest, "this sign-in attempt expired; try again")
		return
	}
	rp, err := s.rp(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Response == nil {
		fail(w, http.StatusBadRequest, "missing passkey response")
		return
	}
	rawID, err := wa.UnB64(req.Response.RawID)
	if err != nil {
		fail(w, http.StatusBadRequest, "unreadable credential id")
		return
	}
	cred, err := s.st.CredentialByCredID(rawID)
	if err != nil {
		s.st.Audit("anonymous", "auth.passkey", "", "unknown credential from "+s.clientIP(r), false)
		fail(w, http.StatusUnauthorized, "that passkey is not registered with this DocMan")
		return
	}
	result, err := wa.VerifyAssertion(rp, pending.challenge, cred.PublicKey, cred.SignCount, req.Response)
	if err != nil {
		s.st.Audit("anonymous", "auth.passkey", cred.Name, err.Error(), false)
		fail(w, http.StatusUnauthorized, err.Error())
		return
	}
	if len(result.UserHandle) > 0 {
		if want := strconv.FormatInt(cred.UserID, 10); string(result.UserHandle) != want {
			fail(w, http.StatusUnauthorized, "passkey belongs to a different account")
			return
		}
	}
	user, err := s.st.UserByID(cred.UserID)
	if err != nil {
		failStore(w, err)
		return
	}
	_ = s.st.TouchCredential(cred.ID, result.SignCount)
	s.st.Audit(user.Username, "auth.passkey", cred.Name, "passkey sign-in from "+s.clientIP(r), true)
	s.startSession(w, r, user, "passkey")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": user.Username})
}

// ---------- passkey management ----------

type passkeyView struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Algorithm  string `json:"algorithm"`
	Transports string `json:"transports"`
	BackedUp   bool   `json:"backed_up"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"`
}

func (s *Server) handleListPasskeys(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	creds, err := s.st.ListCredentials(id.UserID)
	if err != nil {
		failStore(w, err)
		return
	}
	out := make([]passkeyView, 0, len(creds))
	for _, c := range creds {
		out = append(out, passkeyView{
			ID: c.ID, Name: c.Name, Algorithm: wa.Describe(c.PublicKey),
			Transports: c.Transports, BackedUp: c.BackedUp,
			CreatedAt: c.CreatedAt, LastUsedAt: c.LastUsedAt,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"passkeys":         out,
		"password_enabled": s.passwordEnabledForID(id.UserID),
		"totp_enabled":     s.totpEnabledForID(id.UserID),
		"target":           passkeyThreshold,
	})
}

func (s *Server) handlePasskeyRegisterBegin(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	rp, err := s.rp(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	creds, err := s.st.ListCredentials(id.UserID)
	if err != nil {
		failStore(w, err)
		return
	}
	exclude := make([]wa.CredentialDescriptor, 0, len(creds))
	for _, c := range creds {
		exclude = append(exclude, wa.CredentialDescriptor{Type: "public-key", ID: wa.B64(c.CredID)})
	}
	chID, challenge := s.challenges.put(id.UserID)
	userHandle := []byte(strconv.FormatInt(id.UserID, 10))
	writeJSON(w, http.StatusOK, map[string]any{
		"challenge_id": chID,
		"options":      wa.NewCreationOptions(rp, challenge, userHandle, id.Username, id.Username, exclude),
	})
}

func (s *Server) handlePasskeyRegisterFinish(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	var req struct {
		ChallengeID string                   `json:"challenge_id"`
		Name        string                   `json:"name"`
		Response    *wa.RegistrationResponse `json:"response"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	pending, ok := s.challenges.take(req.ChallengeID)
	if !ok || pending.userID != id.UserID {
		fail(w, http.StatusBadRequest, "this registration attempt expired; try again")
		return
	}
	rp, err := s.rp(r)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.Response == nil {
		fail(w, http.StatusBadRequest, "missing passkey response")
		return
	}
	result, err := wa.VerifyRegistration(rp, pending.challenge, req.Response)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, err := s.st.CredentialByCredID(result.CredentialID); err == nil {
		fail(w, http.StatusConflict, "that passkey is already registered")
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = passkeyLabel(result)
	}
	if len(name) > 60 {
		name = name[:60]
	}
	cred := &store.Credential{
		UserID:     id.UserID,
		CredID:     result.CredentialID,
		PublicKey:  result.PublicKey,
		AAGUID:     result.AAGUID,
		SignCount:  result.SignCount,
		Name:       name,
		Transports: strings.Join(result.Transports, ","),
		BackedUp:   result.BackedUp,
	}
	if err := s.st.AddCredential(cred); err != nil {
		fail(w, http.StatusInternalServerError, "could not save the passkey: "+err.Error())
		return
	}
	count, _ := s.st.CountCredentials(id.UserID)
	s.st.Audit(id.Actor(), "passkey.add", name, fmt.Sprintf("passkey %d of %d", count, passkeyThreshold), true)
	if count >= passkeyThreshold {
		s.st.Audit(id.Actor(), "auth.password.disabled", id.Username, "two passkeys registered", true)
		s.logf("password sign-in disabled for %q: %d passkeys registered", id.Username, count)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"id":               cred.ID,
		"name":             name,
		"passkey_count":    count,
		"password_enabled": s.passwordEnabledForID(id.UserID),
	})
}

func passkeyLabel(r *wa.RegistrationResult) string {
	switch r.Attachment {
	case "platform":
		return "This device"
	case "cross-platform":
		return "Security key"
	}
	for _, t := range r.Transports {
		switch t {
		case "internal":
			return "This device"
		case "usb":
			return "USB security key"
		case "nfc", "ble":
			return "Wireless security key"
		case "hybrid":
			return "Phone or tablet"
		}
	}
	return "Passkey"
}

func (s *Server) handleRenamePasskey(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	credID, ok := pathInt(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "invalid passkey id")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 60 {
		fail(w, http.StatusBadRequest, "name must be 1 to 60 characters")
		return
	}
	if err := s.st.RenameCredential(id.UserID, credID, name); err != nil {
		failStore(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	credID, ok := pathInt(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "invalid passkey id")
		return
	}
	user, err := s.st.UserByID(id.UserID)
	if err != nil {
		failStore(w, err)
		return
	}
	count, _ := s.st.CountCredentials(id.UserID)
	if count <= 1 && user.PwHash == "" {
		fail(w, http.StatusConflict, "this is the only way you can sign in; set a password first or add another passkey")
		return
	}
	if err := s.st.DeleteCredential(id.UserID, credID); err != nil {
		failStore(w, err)
		return
	}
	remaining, _ := s.st.CountCredentials(id.UserID)
	s.st.Audit(id.Actor(), "passkey.remove", strconv.FormatInt(credID, 10),
		fmt.Sprintf("%d passkeys remain", remaining), true)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"passkey_count":    remaining,
		"password_enabled": s.passwordEnabledForID(id.UserID),
	})
}

// ---------- account ----------

func (s *Server) handleAccount(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	user, err := s.st.UserByID(id.UserID)
	if err != nil {
		failStore(w, err)
		return
	}
	count, _ := s.st.CountCredentials(user.ID)
	writeJSON(w, http.StatusOK, map[string]any{
		"username":         user.Username,
		"created_at":       user.CreatedAt,
		"has_password":     user.PwHash != "",
		"password_enabled": s.passwordEnabledForID(id.UserID),
		"totp_enabled":     user.TOTPEnabled,
		"passkey_count":    count,
		"passkey_target":   passkeyThreshold,
		"auth_kind":        id.Kind,
	})
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	user, err := s.st.UserByID(id.UserID)
	if err != nil {
		failStore(w, err)
		return
	}
	if user.PwHash != "" && !crypt.VerifyPassword(user.PwHash, req.Current) {
		fail(w, http.StatusUnauthorized, "current password is incorrect")
		return
	}
	if err := validatePassword(req.New); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.SetPassword(user.ID, crypt.HashPassword(req.New)); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(id.Actor(), "account.password", user.Username, "password changed", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleChangeUsername(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	var req struct {
		Username string `json:"username"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	username := strings.TrimSpace(req.Username)
	if err := validateUsername(username); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	// Sign-in matches usernames regardless of case, so two accounts may not
	// differ only in case.
	if other, err := s.st.UserByName(username); err == nil && other.ID != id.UserID {
		fail(w, http.StatusConflict, "another account is already called "+other.Username)
		return
	}
	if err := s.st.SetUsername(id.UserID, username); err != nil {
		fail(w, http.StatusConflict, "could not rename the account: "+err.Error())
		return
	}
	s.st.Audit(id.Actor(), "account.username", username, "renamed from "+id.Username, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "username": username})
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	sessions, err := s.st.ListSessions(id.UserID)
	if err != nil {
		failStore(w, err)
		return
	}
	type view struct {
		Method    string `json:"method"`
		CreatedAt int64  `json:"created_at"`
		SeenAt    int64  `json:"seen_at"`
		ExpiresAt int64  `json:"expires_at"`
		IP        string `json:"ip"`
		UA        string `json:"ua"`
		Current   bool   `json:"current"`
	}
	out := make([]view, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, view{
			Method: sess.Method, CreatedAt: sess.CreatedAt, SeenAt: sess.SeenAt,
			ExpiresAt: sess.ExpiresAt, IP: sess.IP, UA: sess.UA,
			Current: sess.ID == id.SessionID,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

func (s *Server) handleRevokeSessions(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	if err := s.st.DeleteUserSessions(id.UserID, id.SessionID); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(id.Actor(), "account.sessions.revoke", id.Username, "other sessions signed out", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---------- API tokens ----------

// tokenView is a token as the UI lists it. created_by is the issuer's current
// username, or the name recorded at the time when that account is gone.
type tokenView struct {
	*store.APIToken
	CreatorRemoved bool `json:"created_by_removed,omitempty"`
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.st.ListAPITokens()
	if err != nil {
		failStore(w, err)
		return
	}
	names := map[int64]string{}
	if users, err := s.st.ListUsers(); err == nil {
		for _, u := range users {
			names[u.ID] = u.Username
		}
	}
	out := make([]tokenView, 0, len(tokens))
	for _, t := range tokens {
		v := tokenView{APIToken: t}
		switch current, ok := names[t.CreatedByID]; {
		case ok:
			v.CreatedBy = current
		case t.CreatedByID != 0:
			v.CreatorRemoved = true
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out})
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	var req struct {
		Name      string `json:"name"`
		ExpiresIn int64  `json:"expires_in_days"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || len(name) > 60 {
		fail(w, http.StatusBadRequest, "token name must be 1 to 60 characters")
		return
	}
	var expiresAt int64
	if req.ExpiresIn > 0 {
		expiresAt = time.Now().AddDate(0, 0, int(req.ExpiresIn)).Unix()
	}
	token, prefix := crypt.APIToken()
	// Record who issued it. A token issuing another is credited to the token.
	rec, err := s.st.CreateAPIToken(name, prefix, crypt.SHA256Hex(token), "admin", expiresAt, id.UserID, id.Actor())
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.st.Audit(id.Actor(), "token.create", name, "API token issued", true)
	// The secret is shown exactly once.
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "record": rec})
}

func (s *Server) handleDeleteToken(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	tokenID, ok := pathInt(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "invalid token id")
		return
	}
	if id.Kind == "token" && id.TokenID == tokenID {
		fail(w, http.StatusConflict, "a token cannot delete itself")
		return
	}
	purge := boolQuery(r, "purge")
	var err error
	if purge {
		err = s.st.DeleteAPIToken(tokenID)
	} else {
		err = s.st.RevokeAPIToken(tokenID)
	}
	if err != nil {
		failStore(w, err)
		return
	}
	s.st.Audit(id.Actor(), "token.revoke", strconv.FormatInt(tokenID, 10), "API token revoked", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
