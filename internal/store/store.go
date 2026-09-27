// Package store is DocMan's persistence layer: a single SQLite file holding
// users, passkey credentials, sessions, API tokens, pinned container IPs,
// settings and an audit trail.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver: no cgo, builds for amd64/arm64/armv7/386
)

// ErrNotFound is returned by lookups that match no row.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if necessary) the database at path and applies migrations.
func Open(path string) (*Store, error) {
	// temp_store(memory) keeps SQLite from needing a writable /tmp, which the
	// scratch-based container image does not have.
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)"+
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_pragma=temp_store(2)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// The workload is tiny and SQLite serialises writers anyway; a single
	// connection removes any chance of SQLITE_BUSY under concurrent streams.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	const schema = `
CREATE TABLE IF NOT EXISTS settings (
  key        TEXT PRIMARY KEY,
  value      TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS users (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  username       TEXT NOT NULL UNIQUE,
  pw_hash        TEXT NOT NULL DEFAULT '',
  totp_secret    TEXT NOT NULL DEFAULT '',
  totp_enabled   INTEGER NOT NULL DEFAULT 0,
  totp_last_step INTEGER NOT NULL DEFAULT 0,
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL,
  last_login_at  INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS credentials (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  cred_id      BLOB NOT NULL UNIQUE,
  public_key   BLOB NOT NULL,
  aaguid       BLOB NOT NULL,
  sign_count   INTEGER NOT NULL DEFAULT 0,
  name         TEXT NOT NULL DEFAULT '',
  transports   TEXT NOT NULL DEFAULT '',
  backed_up    INTEGER NOT NULL DEFAULT 0,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS sessions (
  id         TEXT PRIMARY KEY,
  user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  method     TEXT NOT NULL DEFAULT 'password',
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  seen_at    INTEGER NOT NULL DEFAULT 0,
  ip         TEXT NOT NULL DEFAULT '',
  ua         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE TABLE IF NOT EXISTS api_tokens (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  name         TEXT NOT NULL,
  prefix       TEXT NOT NULL UNIQUE,
  token_hash   TEXT NOT NULL,
  scope        TEXT NOT NULL DEFAULT 'admin',
  created_at   INTEGER NOT NULL,
  expires_at   INTEGER NOT NULL DEFAULT 0,
  last_used_at INTEGER NOT NULL DEFAULT 0,
  revoked      INTEGER NOT NULL DEFAULT 0,
  created_by_id INTEGER NOT NULL DEFAULT 0,
  created_by   TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS pinned_ips (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  container  TEXT NOT NULL UNIQUE,
  network    TEXT NOT NULL,
  ip         TEXT NOT NULL,
  created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS audit (
  id     INTEGER PRIMARY KEY AUTOINCREMENT,
  at     INTEGER NOT NULL,
  actor  TEXT NOT NULL DEFAULT '',
  action TEXT NOT NULL,
  target TEXT NOT NULL DEFAULT '',
  detail TEXT NOT NULL DEFAULT '',
  ok     INTEGER NOT NULL DEFAULT 1
);
CREATE INDEX IF NOT EXISTS idx_audit_at ON audit(at DESC);
CREATE TABLE IF NOT EXISTS uploaded_images (
  reference   TEXT PRIMARY KEY,
  uploaded_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS registries (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  name        TEXT NOT NULL,
  kind        TEXT NOT NULL,
  host        TEXT NOT NULL,
  auth_type   TEXT NOT NULL DEFAULT 'none',
  username    TEXT NOT NULL DEFAULT '',
  secret      TEXT NOT NULL DEFAULT '',
  options     TEXT NOT NULL DEFAULT '{}',
  is_default  INTEGER NOT NULL DEFAULT 0,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS repo_images (
  reference    TEXT PRIMARY KEY,
  registry_id  INTEGER NOT NULL,
  artifact     TEXT NOT NULL,
  arch         TEXT NOT NULL DEFAULT '',
  version      TEXT NOT NULL DEFAULT '',
  file         TEXT NOT NULL DEFAULT '',
  file_id      TEXT NOT NULL DEFAULT '',
  installed_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS image_checks (
  reference     TEXT PRIMARY KEY,
  status        TEXT NOT NULL,
  local_digest  TEXT NOT NULL DEFAULT '',
  remote_digest TEXT NOT NULL DEFAULT '',
  detail        TEXT NOT NULL DEFAULT '',
  checked_at    INTEGER NOT NULL
);
`
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	return s.addMissingColumns()
}

// addMissingColumns brings a database created by an older DocMan up to date.
// Column names here are internal constants, never user input.
func (s *Store) addMissingColumns() error {
	wanted := []struct{ table, column, decl string }{
		{"users", "totp_secret", "TEXT NOT NULL DEFAULT ''"},
		{"users", "totp_enabled", "INTEGER NOT NULL DEFAULT 0"},
		{"users", "totp_last_step", "INTEGER NOT NULL DEFAULT 0"},
		{"users", "last_login_at", "INTEGER NOT NULL DEFAULT 0"},
		// Tokens belong to DocMan, not to an account; these only record who
		// issued one. Tokens from before accounts were tracked read as unknown.
		{"api_tokens", "created_by_id", "INTEGER NOT NULL DEFAULT 0"},
		{"api_tokens", "created_by", "TEXT NOT NULL DEFAULT ''"},
	}
	for _, want := range wanted {
		present, err := s.hasColumn(want.table, want.column)
		if err != nil {
			return err
		}
		if present {
			continue
		}
		if _, err := s.db.Exec("ALTER TABLE " + want.table + " ADD COLUMN " + want.column + " " + want.decl); err != nil {
			return fmt.Errorf("add column %s.%s: %w", want.table, want.column, err)
		}
	}
	return nil
}

func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notNull   int
			dfltValue any
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, rows.Err()
		}
	}
	return false, rows.Err()
}

func now() int64 { return time.Now().Unix() }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// ---------- settings ----------

// Setting returns the value for key, or def when unset.
func (s *Store) Setting(key, def string) string {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

// SetSetting stores a setting value.
func (s *Store) SetSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings(key, value) VALUES(?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// DeleteSetting removes a setting.
func (s *Store) DeleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

// ---------- uploaded images ----------

// Docker records registry digests even for images loaded from an archive, so
// DocMan keeps its own note of the references it imported by upload. A pull of
// the same reference clears the note.

// MarkUploaded records that reference was last supplied by an archive upload.
func (s *Store) MarkUploaded(reference string) error {
	_, _ = s.db.Exec(`DELETE FROM repo_images WHERE reference = ?`, reference)
	_, err := s.db.Exec(`INSERT INTO uploaded_images(reference, uploaded_at) VALUES(?, ?)
		ON CONFLICT(reference) DO UPDATE SET uploaded_at = excluded.uploaded_at`, reference, now())
	return err
}

// ClearUploaded forgets where reference came from (an upload or a
// repository), after it has been pulled from a registry.
func (s *Store) ClearUploaded(reference string) error {
	_, _ = s.db.Exec(`DELETE FROM repo_images WHERE reference = ?`, reference)
	_, err := s.db.Exec(`DELETE FROM uploaded_images WHERE reference = ?`, reference)
	return err
}

// IsUploaded reports whether reference was last supplied by an archive upload.
func (s *Store) IsUploaded(reference string) bool {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM uploaded_images WHERE reference = ?`, reference).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// UploadedRefs returns every reference last supplied by an upload.
func (s *Store) UploadedRefs() map[string]bool {
	out := map[string]bool{}
	rows, err := s.db.Query(`SELECT reference FROM uploaded_images`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var ref string
		if rows.Scan(&ref) == nil {
			out[ref] = true
		}
	}
	return out
}

// ---------- images installed from a repository ----------

// RepoImage records that a reference was last supplied by a pre-built image
// archive from a GitHub repository registry, so updates are looked for there.
type RepoImage struct {
	Reference   string `json:"reference"`
	RegistryID  int64  `json:"registry_id"`
	Artifact    string `json:"artifact"` // the archive's name without version and architecture
	Arch        string `json:"arch"`
	Version     string `json:"version"`
	File        string `json:"file"`    // where it came from, as shown to people
	FileID      string `json:"file_id"` // what identifies its content: a blob SHA or release asset
	InstalledAt int64  `json:"installed_at"`
}

// MarkFromRepo records r, replacing any upload note for the same reference.
func (s *Store) MarkFromRepo(r *RepoImage) error {
	if _, err := s.db.Exec(`DELETE FROM uploaded_images WHERE reference = ?`, r.Reference); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO repo_images(reference, registry_id, artifact, arch, version, file, file_id, installed_at)
		VALUES(?,?,?,?,?,?,?,?)
		ON CONFLICT(reference) DO UPDATE SET registry_id = excluded.registry_id, artifact = excluded.artifact,
		arch = excluded.arch, version = excluded.version, file = excluded.file, file_id = excluded.file_id,
		installed_at = excluded.installed_at`,
		r.Reference, r.RegistryID, r.Artifact, r.Arch, r.Version, r.File, r.FileID, now())
	return err
}

// ClearFromRepo forgets reference, after it was pulled or uploaded some other way.
func (s *Store) ClearFromRepo(reference string) error {
	_, err := s.db.Exec(`DELETE FROM repo_images WHERE reference = ?`, reference)
	return err
}

// RepoImages returns every reference last supplied from a repository.
func (s *Store) RepoImages() map[string]*RepoImage {
	out := map[string]*RepoImage{}
	rows, err := s.db.Query(`SELECT reference, registry_id, artifact, arch, version, file, file_id, installed_at FROM repo_images`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		r := &RepoImage{}
		if rows.Scan(&r.Reference, &r.RegistryID, &r.Artifact, &r.Arch, &r.Version, &r.File, &r.FileID, &r.InstalledAt) == nil {
			out[r.Reference] = r
		}
	}
	return out
}

// ---------- registries ----------

// Registry is a container registry DocMan pulls from and searches. Secret is
// sealed (see crypt.Seal) and never leaves the server.
type Registry struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Kind      string `json:"kind"`
	Host      string `json:"host"`
	AuthType  string `json:"auth_type"`
	Username  string `json:"username"`
	Secret    string `json:"-"`
	Options   string `json:"-"` // JSON: region, api_url and the like
	IsDefault bool   `json:"is_default"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

const registryCols = `id, name, kind, host, auth_type, username, secret, options, is_default, created_at, updated_at`

func scanRegistry(sc scanner) (*Registry, error) {
	var r Registry
	var def int
	err := sc.Scan(&r.ID, &r.Name, &r.Kind, &r.Host, &r.AuthType, &r.Username, &r.Secret, &r.Options, &def, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	r.IsDefault = def != 0
	return &r, nil
}

// ListRegistries returns every registry, the default first.
func (s *Store) ListRegistries() ([]*Registry, error) {
	rows, err := s.db.Query(`SELECT ` + registryCols + ` FROM registries ORDER BY is_default DESC, name COLLATE NOCASE`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Registry{}
	for rows.Next() {
		r, err := scanRegistry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RegistryByID looks one registry up.
func (s *Store) RegistryByID(id int64) (*Registry, error) {
	return scanRegistry(s.db.QueryRow(`SELECT `+registryCols+` FROM registries WHERE id = ?`, id))
}

// SaveRegistry inserts r when r.ID is 0, or updates it.
func (s *Store) SaveRegistry(r *Registry) error {
	t := now()
	if r.ID == 0 {
		res, err := s.db.Exec(`INSERT INTO registries(name, kind, host, auth_type, username, secret, options, is_default, created_at, updated_at)
			VALUES(?,?,?,?,?,?,?,?,?,?)`,
			r.Name, r.Kind, r.Host, r.AuthType, r.Username, r.Secret, r.Options, boolInt(r.IsDefault), t, t)
		if err != nil {
			return err
		}
		r.ID, err = res.LastInsertId()
		r.CreatedAt, r.UpdatedAt = t, t
		return err
	}
	_, err := s.db.Exec(`UPDATE registries SET name = ?, kind = ?, host = ?, auth_type = ?, username = ?, secret = ?, options = ?, updated_at = ?
		WHERE id = ?`, r.Name, r.Kind, r.Host, r.AuthType, r.Username, r.Secret, r.Options, t, r.ID)
	r.UpdatedAt = t
	return err
}

// SetDefaultRegistry makes one registry the default and no other.
func (s *Store) SetDefaultRegistry(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE registries SET is_default = 0`); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE registries SET is_default = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return tx.Commit()
}

// DeleteRegistry removes a registry.
func (s *Store) DeleteRegistry(id int64) error {
	res, err := s.db.Exec(`DELETE FROM registries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- registry update checks ----------

// ImageCheck is the outcome of asking a registry whether an image tag has a
// newer version than the one on this host.
type ImageCheck struct {
	Reference    string `json:"reference"`
	Status       string `json:"status"` // current, update or unavailable
	LocalDigest  string `json:"local_digest,omitempty"`
	RemoteDigest string `json:"remote_digest,omitempty"`
	Detail       string `json:"detail,omitempty"`
	CheckedAt    int64  `json:"checked_at"`
}

// SaveImageCheck records the latest check of a reference.
func (s *Store) SaveImageCheck(c *ImageCheck) error {
	_, err := s.db.Exec(`INSERT INTO image_checks(reference, status, local_digest, remote_digest, detail, checked_at)
		VALUES(?,?,?,?,?,?)
		ON CONFLICT(reference) DO UPDATE SET status = excluded.status, local_digest = excluded.local_digest,
		remote_digest = excluded.remote_digest, detail = excluded.detail, checked_at = excluded.checked_at`,
		c.Reference, c.Status, c.LocalDigest, c.RemoteDigest, c.Detail, c.CheckedAt)
	return err
}

// ImageChecks returns every recorded check, by reference.
func (s *Store) ImageChecks() map[string]*ImageCheck {
	out := map[string]*ImageCheck{}
	rows, err := s.db.Query(`SELECT reference, status, local_digest, remote_digest, detail, checked_at FROM image_checks`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var c ImageCheck
		if rows.Scan(&c.Reference, &c.Status, &c.LocalDigest, &c.RemoteDigest, &c.Detail, &c.CheckedAt) == nil {
			out[c.Reference] = &c
		}
	}
	return out
}

// DeleteImageCheck forgets a reference, when its image is no longer on the host.
func (s *Store) DeleteImageCheck(reference string) error {
	_, err := s.db.Exec(`DELETE FROM image_checks WHERE reference = ?`, reference)
	return err
}

// ---------- users ----------

// User is a DocMan operator account.
type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PwHash       string `json:"-"`
	TOTPSecret   string `json:"-"`
	TOTPEnabled  bool   `json:"totp_enabled"`
	TOTPLastStep int64  `json:"-"`
	CreatedAt    int64  `json:"created_at"`
	UpdatedAt    int64  `json:"updated_at"`
	LastLoginAt  int64  `json:"last_login_at"`
}

const userCols = `id, username, pw_hash, totp_secret, totp_enabled, totp_last_step, created_at, updated_at, last_login_at`

func scanUser(sc scanner) (*User, error) {
	var u User
	var totpEnabled int
	err := sc.Scan(&u.ID, &u.Username, &u.PwHash, &u.TOTPSecret, &totpEnabled,
		&u.TOTPLastStep, &u.CreatedAt, &u.UpdatedAt, &u.LastLoginAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u.TOTPEnabled = totpEnabled != 0
	return &u, nil
}

// CreateUser inserts a user with the given encoded password hash.
func (s *Store) CreateUser(username, pwHash string) (*User, error) {
	t := now()
	res, err := s.db.Exec(`INSERT INTO users(username, pw_hash, created_at, updated_at) VALUES(?,?,?,?)`,
		username, pwHash, t, t)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &User{ID: id, Username: username, PwHash: pwHash, CreatedAt: t, UpdatedAt: t}, nil
}

// UserByName looks a user up by username (case-insensitive).
func (s *Store) UserByName(username string) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE username = ? COLLATE NOCASE`, username))
}

// UserByID looks a user up by id.
func (s *Store) UserByID(id int64) (*User, error) {
	return scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// ListUsers returns every account, oldest first.
func (s *Store) ListUsers() ([]*User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// DeleteUser removes an account. Its passkeys and sessions go with it.
func (s *Store) DeleteUser(id int64) error {
	res, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ResetSignIn gives an account a new password and removes every other way
// in: its passkeys, its authenticator app and its sessions. It is how one
// operator recovers another who is locked out.
func (s *Store) ResetSignIn(id int64, pwHash string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE users SET pw_hash = ?, totp_secret = '', totp_enabled = 0, totp_last_step = 0,
		updated_at = ? WHERE id = ?`, pwHash, now(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(`DELETE FROM credentials WHERE user_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// RecordLogin notes when an account last signed in.
func (s *Store) RecordLogin(id int64) error {
	_, err := s.db.Exec(`UPDATE users SET last_login_at = ? WHERE id = ?`, now(), id)
	return err
}

// CountUsers returns the number of accounts.
func (s *Store) CountUsers() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// SetPassword replaces a user's password hash.
func (s *Store) SetPassword(userID int64, pwHash string) error {
	_, err := s.db.Exec(`UPDATE users SET pw_hash = ?, updated_at = ? WHERE id = ?`, pwHash, now(), userID)
	return err
}

// SetUsername renames a user.
func (s *Store) SetUsername(userID int64, username string) error {
	_, err := s.db.Exec(`UPDATE users SET username = ?, updated_at = ? WHERE id = ?`, username, now(), userID)
	return err
}

// EnableTOTP stores a confirmed authenticator secret for a user.
func (s *Store) EnableTOTP(userID int64, secret string, step int64) error {
	_, err := s.db.Exec(
		`UPDATE users SET totp_secret = ?, totp_enabled = 1, totp_last_step = ?, updated_at = ? WHERE id = ?`,
		secret, step, now(), userID)
	return err
}

// DisableTOTP removes a user's authenticator secret.
func (s *Store) DisableTOTP(userID int64) error {
	_, err := s.db.Exec(
		`UPDATE users SET totp_secret = '', totp_enabled = 0, totp_last_step = 0, updated_at = ? WHERE id = ?`,
		now(), userID)
	return err
}

// RecordTOTPStep remembers the step a code was accepted from, so the same code
// cannot be presented twice.
func (s *Store) RecordTOTPStep(userID, step int64) error {
	_, err := s.db.Exec(`UPDATE users SET totp_last_step = ? WHERE id = ? AND totp_last_step < ?`,
		step, userID, step)
	return err
}

// ---------- credentials (passkeys) ----------

// Credential is a stored WebAuthn credential.
type Credential struct {
	ID         int64  `json:"id"`
	UserID     int64  `json:"user_id"`
	CredID     []byte `json:"-"`
	PublicKey  []byte `json:"-"`
	AAGUID     []byte `json:"-"`
	SignCount  uint32 `json:"sign_count"`
	Name       string `json:"name"`
	Transports string `json:"transports"`
	BackedUp   bool   `json:"backed_up"`
	CreatedAt  int64  `json:"created_at"`
	LastUsedAt int64  `json:"last_used_at"`
}

const credCols = `id, user_id, cred_id, public_key, aaguid, sign_count, name, transports, backed_up, created_at, last_used_at`

func scanCred(sc scanner) (*Credential, error) {
	var c Credential
	var backed int
	err := sc.Scan(&c.ID, &c.UserID, &c.CredID, &c.PublicKey, &c.AAGUID, &c.SignCount,
		&c.Name, &c.Transports, &backed, &c.CreatedAt, &c.LastUsedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.BackedUp = backed != 0
	return &c, nil
}

// AddCredential stores a newly registered passkey.
func (s *Store) AddCredential(c *Credential) error {
	c.CreatedAt = now()
	res, err := s.db.Exec(`INSERT INTO credentials(user_id, cred_id, public_key, aaguid, sign_count, name, transports, backed_up, created_at)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		c.UserID, c.CredID, c.PublicKey, c.AAGUID, c.SignCount, c.Name, c.Transports, boolInt(c.BackedUp), c.CreatedAt)
	if err != nil {
		return err
	}
	c.ID, err = res.LastInsertId()
	return err
}

// CredentialByCredID finds a credential by its raw WebAuthn credential id.
func (s *Store) CredentialByCredID(credID []byte) (*Credential, error) {
	return scanCred(s.db.QueryRow(`SELECT `+credCols+` FROM credentials WHERE cred_id = ?`, credID))
}

// ListCredentials returns every passkey for a user, oldest first.
func (s *Store) ListCredentials(userID int64) ([]*Credential, error) {
	rows, err := s.db.Query(`SELECT `+credCols+` FROM credentials WHERE user_id = ? ORDER BY created_at`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Credential{}
	for rows.Next() {
		c, err := scanCred(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CountAllCredentials returns how many passkeys exist across every account.
func (s *Store) CountAllCredentials() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM credentials`).Scan(&n)
	return n, err
}

// CountCredentials returns how many passkeys a user has registered.
func (s *Store) CountCredentials(userID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM credentials WHERE user_id = ?`, userID).Scan(&n)
	return n, err
}

// TouchCredential records a successful assertion.
func (s *Store) TouchCredential(id int64, signCount uint32) error {
	_, err := s.db.Exec(`UPDATE credentials SET sign_count = ?, last_used_at = ? WHERE id = ?`, signCount, now(), id)
	return err
}

// RenameCredential updates a passkey's friendly name.
func (s *Store) RenameCredential(userID, id int64, name string) error {
	_, err := s.db.Exec(`UPDATE credentials SET name = ? WHERE id = ? AND user_id = ?`, name, id, userID)
	return err
}

// DeleteCredential removes a passkey belonging to userID.
func (s *Store) DeleteCredential(userID, id int64) error {
	res, err := s.db.Exec(`DELETE FROM credentials WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- sessions ----------

// Session is a browser login session.
type Session struct {
	ID        string `json:"id"`
	UserID    int64  `json:"user_id"`
	Method    string `json:"method"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	SeenAt    int64  `json:"seen_at"`
	IP        string `json:"ip"`
	UA        string `json:"ua"`
}

const sessCols = `id, user_id, method, created_at, expires_at, seen_at, ip, ua`

func scanSess(sc scanner) (*Session, error) {
	var v Session
	err := sc.Scan(&v.ID, &v.UserID, &v.Method, &v.CreatedAt, &v.ExpiresAt, &v.SeenAt, &v.IP, &v.UA)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// CreateSession stores a new session.
func (s *Store) CreateSession(id string, userID int64, method string, ttl time.Duration, ip, ua string) (*Session, error) {
	t := now()
	sess := &Session{ID: id, UserID: userID, Method: method, CreatedAt: t, ExpiresAt: t + int64(ttl.Seconds()), SeenAt: t, IP: ip, UA: ua}
	_, err := s.db.Exec(`INSERT INTO sessions(`+sessCols+`) VALUES(?,?,?,?,?,?,?,?)`,
		sess.ID, sess.UserID, sess.Method, sess.CreatedAt, sess.ExpiresAt, sess.SeenAt, sess.IP, sess.UA)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// SessionByID returns a live session, or ErrNotFound when missing or expired.
func (s *Store) SessionByID(id string) (*Session, error) {
	sess, err := scanSess(s.db.QueryRow(`SELECT `+sessCols+` FROM sessions WHERE id = ?`, id))
	if err != nil {
		return nil, err
	}
	if sess.ExpiresAt <= now() {
		_ = s.DeleteSession(id)
		return nil, ErrNotFound
	}
	return sess, nil
}

// TouchSession slides the expiry window forward.
func (s *Store) TouchSession(id string, ttl time.Duration) error {
	t := now()
	_, err := s.db.Exec(`UPDATE sessions SET seen_at = ?, expires_at = ? WHERE id = ?`, t, t+int64(ttl.Seconds()), id)
	return err
}

// DeleteSession logs a session out.
func (s *Store) DeleteSession(id string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE id = ?`, id)
	return err
}

// DeleteUserSessions logs out every session for a user except keep.
func (s *Store) DeleteUserSessions(userID int64, keep string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, keep)
	return err
}

// ListSessions returns live sessions for a user.
func (s *Store) ListSessions(userID int64) ([]*Session, error) {
	rows, err := s.db.Query(`SELECT `+sessCols+` FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY seen_at DESC`,
		userID, now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Session{}
	for rows.Next() {
		v, err := scanSess(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PurgeExpired removes stale sessions.
func (s *Store) PurgeExpired() error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE expires_at <= ?`, now())
	return err
}

// ---------- api tokens ----------

// APIToken is a bearer credential for the REST API.
type APIToken struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Prefix     string `json:"prefix"`
	Hash       string `json:"-"`
	Scope      string `json:"scope"`
	CreatedAt  int64  `json:"created_at"`
	ExpiresAt  int64  `json:"expires_at"`
	LastUsedAt int64  `json:"last_used_at"`
	Revoked    bool   `json:"revoked"`
	// Who issued the token. A token is not tied to that account: it keeps
	// working if the account is renamed or removed.
	CreatedByID int64  `json:"created_by_id"`
	CreatedBy   string `json:"created_by"`
}

const tokenCols = `id, name, prefix, token_hash, scope, created_at, expires_at, last_used_at, revoked, created_by_id, created_by`

func scanToken(sc scanner) (*APIToken, error) {
	var t APIToken
	var revoked int
	err := sc.Scan(&t.ID, &t.Name, &t.Prefix, &t.Hash, &t.Scope, &t.CreatedAt, &t.ExpiresAt, &t.LastUsedAt, &revoked,
		&t.CreatedByID, &t.CreatedBy)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	t.Revoked = revoked != 0
	return &t, nil
}

// CreateAPIToken records a new bearer token. expiresAt of 0 means no expiry.
// createdByID and createdBy record who issued it, for display only.
func (s *Store) CreateAPIToken(name, prefix, hash, scope string, expiresAt, createdByID int64, createdBy string) (*APIToken, error) {
	t := now()
	res, err := s.db.Exec(`INSERT INTO api_tokens(name, prefix, token_hash, scope, created_at, expires_at, created_by_id, created_by)
		VALUES(?,?,?,?,?,?,?,?)`,
		name, prefix, hash, scope, t, expiresAt, createdByID, createdBy)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &APIToken{ID: id, Name: name, Prefix: prefix, Hash: hash, Scope: scope, CreatedAt: t, ExpiresAt: expiresAt,
		CreatedByID: createdByID, CreatedBy: createdBy}, nil
}

// APITokenByPrefix looks a token up by its non-secret prefix.
func (s *Store) APITokenByPrefix(prefix string) (*APIToken, error) {
	return scanToken(s.db.QueryRow(`SELECT `+tokenCols+` FROM api_tokens WHERE prefix = ?`, prefix))
}

// ListAPITokens returns all tokens, newest first.
func (s *Store) ListAPITokens() ([]*APIToken, error) {
	rows, err := s.db.Query(`SELECT ` + tokenCols + ` FROM api_tokens ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*APIToken{}
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TouchAPIToken records token usage (best effort).
func (s *Store) TouchAPIToken(id int64) {
	_, _ = s.db.Exec(`UPDATE api_tokens SET last_used_at = ? WHERE id = ?`, now(), id)
}

// RevokeAPIToken marks a token unusable.
func (s *Store) RevokeAPIToken(id int64) error {
	res, err := s.db.Exec(`UPDATE api_tokens SET revoked = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteAPIToken removes a token row entirely.
func (s *Store) DeleteAPIToken(id int64) error {
	res, err := s.db.Exec(`DELETE FROM api_tokens WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---------- pinned ips ----------

// PinnedIP records a container whose address DocMan keeps stable.
type PinnedIP struct {
	ID        int64  `json:"id"`
	Container string `json:"container"`
	Network   string `json:"network"`
	IP        string `json:"ip"`
	CreatedAt int64  `json:"created_at"`
}

// SavePin stores or updates the pin for a container name.
func (s *Store) SavePin(container, network, ip string) error {
	_, err := s.db.Exec(`INSERT INTO pinned_ips(container, network, ip, created_at) VALUES(?,?,?,?)
		ON CONFLICT(container) DO UPDATE SET network = excluded.network, ip = excluded.ip`,
		container, network, ip, now())
	return err
}

// Pin returns the pin for a container name.
func (s *Store) Pin(container string) (*PinnedIP, error) {
	var p PinnedIP
	err := s.db.QueryRow(`SELECT id, container, network, ip, created_at FROM pinned_ips WHERE container = ?`, container).
		Scan(&p.ID, &p.Container, &p.Network, &p.IP, &p.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// ListPins returns every pin.
func (s *Store) ListPins() ([]*PinnedIP, error) {
	rows, err := s.db.Query(`SELECT id, container, network, ip, created_at FROM pinned_ips ORDER BY ip`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PinnedIP{}
	for rows.Next() {
		var p PinnedIP
		if err := rows.Scan(&p.ID, &p.Container, &p.Network, &p.IP, &p.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, &p)
	}
	return out, rows.Err()
}

// DeletePin removes a pin.
func (s *Store) DeletePin(container string) error {
	_, err := s.db.Exec(`DELETE FROM pinned_ips WHERE container = ?`, container)
	return err
}

// RenamePin follows a container rename.
func (s *Store) RenamePin(oldName, newName string) error {
	_, err := s.db.Exec(`UPDATE pinned_ips SET container = ? WHERE container = ?`, newName, oldName)
	return err
}

// ---------- audit ----------

// AuditEntry is one recorded administrative action.
type AuditEntry struct {
	ID     int64  `json:"id"`
	At     int64  `json:"at"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Target string `json:"target"`
	Detail string `json:"detail"`
	OK     bool   `json:"ok"`
}

// Audit appends an audit record (best effort; never fails a request).
func (s *Store) Audit(actor, action, target, detail string, ok bool) {
	_, _ = s.db.Exec(`INSERT INTO audit(at, actor, action, target, detail, ok) VALUES(?,?,?,?,?,?)`,
		now(), actor, action, target, detail, boolInt(ok))
}

// ListAudit returns the most recent audit entries.
func (s *Store) ListAudit(limit int) ([]*AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	rows, err := s.db.Query(`SELECT id, at, actor, action, target, detail, ok FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		var ok int
		if err := rows.Scan(&e.ID, &e.At, &e.Actor, &e.Action, &e.Target, &e.Detail, &ok); err != nil {
			return nil, err
		}
		e.OK = ok != 0
		out = append(out, &e)
	}
	return out, rows.Err()
}

// TrimAudit keeps the audit table bounded to roughly keep rows.
func (s *Store) TrimAudit(keep int) {
	_, _ = s.db.Exec(`DELETE FROM audit WHERE id <= (SELECT MAX(id) - ? FROM audit)`, keep)
}
