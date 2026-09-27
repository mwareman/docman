package srv

import (
	"encoding/base64"
	"net/http"
	"sync"
	"time"

	"rsc.io/qr"

	"docman/internal/crypt"
)

// Authenticator-app enrolment.
//
// A secret is held in memory only while the operator is enrolling: it reaches
// the database once they have proved they can generate a code from it, so a
// half-finished enrolment can never lock anybody out.

type pendingTOTP struct {
	secret  string
	expires time.Time
}

type totpEnrolments struct {
	mu    sync.Mutex
	items map[int64]pendingTOTP
}

func newTOTPEnrolments() *totpEnrolments {
	return &totpEnrolments{items: map[int64]pendingTOTP{}}
}

func (t *totpEnrolments) put(userID int64, secret string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	for id, item := range t.items {
		if item.expires.Before(now) {
			delete(t.items, id)
		}
	}
	t.items[userID] = pendingTOTP{secret: secret, expires: now.Add(10 * time.Minute)}
}

func (t *totpEnrolments) get(userID int64) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	item, ok := t.items[userID]
	if !ok || item.expires.Before(time.Now()) {
		delete(t.items, userID)
		return "", false
	}
	return item.secret, true
}

func (t *totpEnrolments) clear(userID int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.items, userID)
}

// qrDataURI renders text as a QR code, inlined so the page needs no extra
// request and the secret never lands in a cacheable URL.
func qrDataURI(text string) string {
	code, err := qr.Encode(text, qr.M)
	if err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(code.PNG())
}

// handleTOTPBegin starts enrolment and returns everything the UI needs to show:
// a QR code, the secret in readable form, and the otpauth URI as a link.
func (s *Server) handleTOTPBegin(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	user, err := s.st.UserByID(id.UserID)
	if err != nil {
		failStore(w, err)
		return
	}
	if user.TOTPEnabled {
		fail(w, http.StatusConflict, "an authenticator app is already set up; remove it first to enrol a new one")
		return
	}
	secret := crypt.NewTOTPSecret()
	s.totp.put(id.UserID, secret)
	uri := crypt.TOTPURI(secret, "DocMan", user.Username)

	writeJSON(w, http.StatusOK, map[string]any{
		"secret":           secret,
		"secret_formatted": crypt.FormatTOTPSecret(secret),
		"uri":              uri,
		"qr":               qrDataURI(uri),
		"issuer":           "DocMan",
		"account":          user.Username,
		"digits":           6,
		"period":           30,
		"algorithm":        "SHA1",
	})
}

// handleTOTPEnable confirms enrolment with a code generated from the pending
// secret, then persists it.
func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	var req struct {
		Code string `json:"code"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	secret, ok := s.totp.get(id.UserID)
	if !ok {
		fail(w, http.StatusBadRequest, "this enrolment expired; start again")
		return
	}
	step, err := crypt.VerifyTOTP(secret, req.Code, time.Now(), 0)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.EnableTOTP(id.UserID, secret, step); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.totp.clear(id.UserID)
	s.st.Audit(id.Actor(), "totp.enable", id.Username, "authenticator app enrolled", true)
	s.logf("authenticator app enrolled for %q; password sign-in now requires a code", id.Username)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"totp_enabled":     true,
		"password_enabled": s.passwordEnabledForID(id.UserID),
	})
}

// handleTOTPDisable removes the authenticator, confirming with the account
// password so a borrowed session cannot quietly weaken sign-in.
func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	if !requireAccount(w, id) {
		return
	}
	var req struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	user, err := s.st.UserByID(id.UserID)
	if err != nil {
		failStore(w, err)
		return
	}
	if !user.TOTPEnabled {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "totp_enabled": false})
		return
	}
	if !s.limiter.allow("totp:"+s.clientIP(r), 0.2, 6) {
		fail(w, http.StatusTooManyRequests, "too many attempts; wait a moment and try again")
		return
	}

	confirmed := false
	if user.PwHash != "" && req.Password != "" && crypt.VerifyPassword(user.PwHash, req.Password) {
		confirmed = true
	}
	if !confirmed && req.Code != "" {
		if step, err := crypt.VerifyTOTP(user.TOTPSecret, req.Code, time.Now(), user.TOTPLastStep); err == nil {
			_ = s.st.RecordTOTPStep(user.ID, step)
			confirmed = true
		}
	}
	if !confirmed {
		s.st.Audit(id.Actor(), "totp.disable", id.Username, "confirmation failed", false)
		fail(w, http.StatusUnauthorized, "confirm with your password or a current authenticator code")
		return
	}

	if err := s.st.DisableTOTP(id.UserID); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.totp.clear(id.UserID)
	s.st.Audit(id.Actor(), "totp.disable", id.Username, "authenticator app removed", true)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"totp_enabled":     false,
		"password_enabled": s.passwordEnabledForID(id.UserID),
	})
}
