package srv

import (
	"net/http"
	"strings"

	"docman/internal/crypt"
	"docman/internal/store"
)

// Accounts.
//
// Every account is a full administrator: DocMan manages a host through its
// Docker socket, which is root-equivalent, so finer roles would only look like
// protection. What each account has of its own is how it signs in: a password,
// passkeys and an authenticator app. Any account, or an API token, can add,
// reset and remove the others.

type userView struct {
	ID              int64  `json:"id"`
	Username        string `json:"username"`
	CreatedAt       int64  `json:"created_at"`
	LastLoginAt     int64  `json:"last_login_at"`
	HasPassword     bool   `json:"has_password"`
	PasswordEnabled bool   `json:"password_enabled"`
	TOTPEnabled     bool   `json:"totp_enabled"`
	PasskeyCount    int    `json:"passkey_count"`
	Current         bool   `json:"current"`
}

func (s *Server) viewUser(u *store.User, callerID int64) userView {
	n, _ := s.st.CountCredentials(u.ID)
	return userView{
		ID: u.ID, Username: u.Username, CreatedAt: u.CreatedAt, LastLoginAt: u.LastLoginAt,
		HasPassword: u.PwHash != "", PasswordEnabled: s.passwordEnabledFor(u),
		TOTPEnabled: u.TOTPEnabled, PasskeyCount: n, Current: u.ID == callerID,
	}
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	users, err := s.st.ListUsers()
	if err != nil {
		failStore(w, err)
		return
	}
	out := make([]userView, 0, len(users))
	for _, u := range users {
		out = append(out, s.viewUser(u, id.UserID))
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": out, "passkey_target": passkeyThreshold})
}

// handleCreateUser adds an account with a starting password. The new user signs
// in with it, then changes it and adds their own passkeys or authenticator.
func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &req) {
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
	if _, err := s.st.UserByName(username); err == nil {
		fail(w, http.StatusConflict, "an account called "+username+" already exists")
		return
	}
	user, err := s.st.CreateUser(username, crypt.HashPassword(req.Password))
	if err != nil {
		fail(w, http.StatusInternalServerError, "could not create the account: "+err.Error())
		return
	}
	s.st.Audit(id.Actor(), "user.create", username, "account created", true)
	writeJSON(w, http.StatusCreated, map[string]any{"ok": true, "user": s.viewUser(user, id.UserID)})
}

// handleResetUser recovers another account: it gets a new password, and its
// passkeys, authenticator app and sessions are removed so nothing else can
// still sign in as it. Your own account is changed from Settings → Account.
func (s *Server) handleResetUser(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	userID, ok := pathInt(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "invalid account id")
		return
	}
	if userID == id.UserID {
		fail(w, http.StatusConflict, "change your own sign-in from Settings → Account instead")
		return
	}
	var req struct {
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := validatePassword(req.Password); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	user, err := s.st.UserByID(userID)
	if err != nil {
		failStore(w, err)
		return
	}
	if err := s.st.ResetSignIn(user.ID, crypt.HashPassword(req.Password)); err != nil {
		failStore(w, err)
		return
	}
	s.totp.clear(user.ID)
	s.st.Audit(id.Actor(), "user.reset", user.Username,
		"new password set; passkeys, authenticator app and sessions removed", true)
	s.logf("sign-in reset for %q by %s", user.Username, id.Actor())
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleDeleteUser removes an account. It cannot remove the caller's own
// account, or the last one, which would leave DocMan with no way in.
func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := identityOf(r)
	userID, ok := pathInt(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "invalid account id")
		return
	}
	if userID == id.UserID {
		fail(w, http.StatusConflict, "you cannot remove the account you are signed in with; sign in as another user to remove it")
		return
	}
	user, err := s.st.UserByID(userID)
	if err != nil {
		failStore(w, err)
		return
	}
	if n, err := s.st.CountUsers(); err == nil && n <= 1 {
		fail(w, http.StatusConflict, "this is the only account; DocMan needs at least one")
		return
	}
	if err := s.st.DeleteUser(user.ID); err != nil {
		failStore(w, err)
		return
	}
	s.totp.clear(user.ID)
	s.st.Audit(id.Actor(), "user.delete", user.Username, "account removed", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
