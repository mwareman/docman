package srv

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"docman/internal/crypt"
	"docman/internal/dock"
	"docman/internal/store"
)

// Registries.
//
// DocMan keeps a list of container registries — Docker Hub, GitHub, GitHub
// Enterprise, GitLab, Quay, AWS, Google, Azure or any other — each with its own
// sign-in. The deploy wizard pulls from and searches the one chosen, and every
// other pull (Images → Pull, a recreate, the daily update check) signs in to
// whichever registry an image's name points at.
//
// Secrets are sealed with a key kept in secrets.key beside the database and
// never leave the server once saved.

// Sign-in methods.
const (
	authNone  = "none"  // anonymous
	authBasic = "basic" // a username with a password or access token
	authToken = "token" // an identity (refresh) token
	authAWS   = "aws"   // AWS access key; DocMan fetches the 12-hour ECR token
	authGCP   = "gcp"   // a Google service-account JSON key
)

// registryKind describes one kind of registry the UI offers.
type registryKind struct {
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`      // default display name
	Host       string   `json:"host"`      // fixed host, or "" when the user gives one
	HostHint   string   `json:"host_hint"` // example host
	Auth       []string `json:"auth"`      // sign-in methods, the first suggested
	AuthHelp   string   `json:"auth_help"` // what credentials to use
	Search     bool     `json:"search"`    // whether DocMan can search it
	SearchNote string   `json:"search_note,omitempty"`
	Options    []string `json:"options,omitempty"` // extra fields: region, api_url, repo, ref, path
	// Archives marks a GitHub repository of pre-built image archives, which
	// DocMan installs rather than pulls.
	Archives bool `json:"archives,omitempty"`
}

var registryKinds = []registryKind{
	{Kind: "dockerhub", Name: "Docker Hub", Host: "docker.io", Auth: []string{authNone, authBasic},
		AuthHelp: "Optional for public images. Your Docker Hub username and a personal access token (Account settings → Personal access tokens). Signing in also raises Docker Hub's pull limits.",
		Search:   true},
	{Kind: "ghcr", Name: "GitHub Container Registry", Host: "ghcr.io", Auth: []string{authBasic, authNone},
		AuthHelp: "Your GitHub username and a personal access token (classic) with the read:packages scope. Public images need none.",
		Search:   true, SearchNote: "Search lists the packages the token can see."},
	{Kind: "ghes", Name: "GitHub Enterprise", HostHint: "containers.github.example.com", Auth: []string{authBasic},
		AuthHelp: "Your GitHub Enterprise username and a personal access token with read:packages.",
		Search:   true, SearchNote: "Search lists the packages the token can see.", Options: []string{"api_url"}},
	{Kind: "gitlab", Name: "GitLab Container Registry", Host: "registry.gitlab.com", Auth: []string{authBasic, authNone},
		AuthHelp:   "A GitLab username with a personal access token (read_registry), or a deploy token's username and token. For self-managed GitLab, change the address.",
		SearchNote: "GitLab does not offer registry search; enter the full image path, such as group/project/image."},
	{Kind: "quay", Name: "Quay.io", Host: "quay.io", Auth: []string{authNone, authBasic},
		AuthHelp: "Your Quay username and password, or a robot account's name (owner+robot) and token.",
		Search:   true},
	{Kind: "ecr", Name: "Amazon ECR", HostHint: "123456789012.dkr.ecr.eu-west-1.amazonaws.com", Auth: []string{authAWS},
		AuthHelp: "An AWS access key ID and secret access key allowed ecr:GetAuthorizationToken (and ecr:DescribeRepositories to search). DocMan fetches the 12-hour registry token itself.",
		Search:   true, Options: []string{"region"}},
	{Kind: "gar", Name: "Google Artifact Registry", HostHint: "europe-west2-docker.pkg.dev", Auth: []string{authGCP, authBasic},
		AuthHelp:   "A service account's JSON key with the Artifact Registry Reader role. Also works for gcr.io.",
		SearchNote: "Search is not available; enter the full path, such as project/repository/image."},
	{Kind: "acr", Name: "Azure Container Registry", HostHint: "myregistry.azurecr.io", Auth: []string{authBasic, authToken},
		AuthHelp: "A service principal's application ID and secret, a repository-scoped token, or the registry's admin user. An identity (refresh) token also works.",
		Search:   true},
	{Kind: kindGitHubRepo, Name: "GitHub repository", Host: "github.com", Auth: []string{authNone, authBasic},
		AuthHelp:   "None for a public repository. For a private one, your GitHub username and a personal access token that can read it (fine-grained: Contents read-only; classic: repo). For GitHub Enterprise, change the address.",
		SearchNote: "Lists the pre-built image archives (docker save .tar, .tar.gz or .tgz) in the repository's files and releases. DocMan installs and updates only those; it does not build from a Dockerfile or compose file.",
		Options:    []string{"repo", "ref", "path", "api_url"}, Archives: true},
	{Kind: "other", Name: "Other registry", HostHint: "registry.example.com", Auth: []string{authBasic, authToken, authNone},
		AuthHelp: "Harbor, Nexus, Artifactory, a self-hosted registry or any other: a username with a password or token, or an identity token.",
		Search:   true, SearchNote: "Search works where the registry allows listing its catalogue."},
}

func kindByName(k string) *registryKind {
	for i := range registryKinds {
		if registryKinds[i].Kind == k {
			return &registryKinds[i]
		}
	}
	return nil
}

// ---------- secrets ----------

var (
	secretKeyOnce sync.Once
	secretKey     []byte
	secretKeyErr  error
)

func (s *Server) sealKey() ([]byte, error) {
	secretKeyOnce.Do(func() {
		secretKey, secretKeyErr = crypt.LoadOrCreateKey(filepath.Join(s.cfg.DataDir, "secrets.key"))
	})
	return secretKey, secretKeyErr
}

func (s *Server) openSecret(r *store.Registry) string {
	key, err := s.sealKey()
	if err != nil {
		return ""
	}
	plain, err := crypt.Open(key, r.Secret)
	if err != nil {
		s.logf("registries: could not read the secret of %q: %v", r.Name, err)
		return ""
	}
	return plain
}

func registryOptions(r *store.Registry) map[string]string {
	out := map[string]string{}
	_ = json.Unmarshal([]byte(r.Options), &out)
	return out
}

// ---------- the list ----------

// registries returns every registry, adding Docker Hub as the default the
// first time: it is where Docker itself pulls from when a name has no host.
func (s *Server) registries() ([]*store.Registry, error) {
	list, err := s.st.ListRegistries()
	if err != nil || len(list) > 0 {
		return list, err
	}
	hub := &store.Registry{Name: "Docker Hub", Kind: "dockerhub", Host: "docker.io", AuthType: authNone, Options: "{}", IsDefault: true}
	if err := s.st.SaveRegistry(hub); err != nil {
		return nil, err
	}
	return []*store.Registry{hub}, nil
}

type registryView struct {
	*store.Registry
	HasSecret bool              `json:"has_secret"`
	Options   map[string]string `json:"options"`
}

func viewRegistry(r *store.Registry) registryView {
	return registryView{Registry: r, HasSecret: r.Secret != "", Options: registryOptions(r)}
}

func (s *Server) handleListRegistries(w http.ResponseWriter, r *http.Request) {
	list, err := s.registries()
	if err != nil {
		failStore(w, err)
		return
	}
	out := make([]registryView, 0, len(list))
	for _, reg := range list {
		out = append(out, viewRegistry(reg))
	}
	writeJSON(w, http.StatusOK, map[string]any{"registries": out, "kinds": registryKinds})
}

type registryRequest struct {
	Name     string            `json:"name"`
	Kind     string            `json:"kind"`
	Host     string            `json:"host"`
	AuthType string            `json:"auth_type"`
	Username string            `json:"username"`
	Secret   *string           `json:"secret"` // nil keeps the stored one
	Options  map[string]string `json:"options"`
}

// normalizeHost strips a scheme and a trailing path from what the user typed.
func normalizeHost(h string) string {
	h = strings.TrimSpace(strings.ToLower(h))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	switch h {
	case "index.docker.io", "registry-1.docker.io", "registry.hub.docker.com", "hub.docker.com":
		return "docker.io"
	}
	return h
}

func (s *Server) applyRegistryRequest(reg *store.Registry, req *registryRequest) error {
	kind := kindByName(req.Kind)
	if kind == nil {
		return errors.New("unknown registry kind")
	}
	reg.Kind = kind.Kind
	reg.Name = strings.TrimSpace(req.Name)
	if reg.Name == "" {
		reg.Name = kind.Name
	}
	if len(reg.Name) > 60 {
		return errors.New("the name can be at most 60 characters")
	}
	host := kind.Host
	if req.Host != "" {
		host = req.Host
	}
	reg.Host = normalizeHost(host)
	if reg.Host == "" || strings.ContainsAny(reg.Host, " \t") {
		return errors.New("enter the registry's address, such as " + kind.HostHint)
	}
	allowed := false
	for _, a := range kind.Auth {
		if a == req.AuthType {
			allowed = true
		}
	}
	if !allowed {
		return fmt.Errorf("%s does not support that sign-in method", kind.Name)
	}
	reg.AuthType = req.AuthType
	reg.Username = strings.TrimSpace(req.Username)
	if req.AuthType == authNone {
		reg.Username = ""
		reg.Secret = ""
	} else if req.Secret != nil {
		key, err := s.sealKey()
		if err != nil {
			return fmt.Errorf("could not open the secrets key: %w", err)
		}
		if reg.Secret, err = crypt.Seal(key, *req.Secret); err != nil {
			return err
		}
	}
	switch req.AuthType {
	case authBasic:
		if reg.Username == "" {
			return errors.New("enter a username")
		}
	case authAWS:
		if reg.Username == "" {
			return errors.New("enter the AWS access key ID")
		}
	case authGCP:
		reg.Username = "_json_key"
	}
	if reg.AuthType != authNone && reg.Secret == "" {
		return errors.New("enter the password, token or key")
	}
	opts := map[string]string{}
	for _, k := range kind.Options {
		if v := strings.TrimSpace(req.Options[k]); v != "" {
			opts[k] = v
		}
	}
	if kind.Kind == "ecr" && opts["region"] == "" {
		// The region is part of an ECR address: 1234.dkr.ecr.<region>.amazonaws.com.
		if parts := strings.Split(reg.Host, "."); len(parts) >= 6 && parts[1] == "dkr" && parts[2] == "ecr" {
			opts["region"] = parts[3]
		}
	}
	if kind.Kind == "ecr" && opts["region"] == "" {
		return errors.New("enter the AWS region")
	}
	if kind.Kind == kindGitHubRepo {
		host, repo := parseRepoOption(opts["repo"])
		if !repoNameRe.MatchString(repo) {
			return errors.New("enter the repository as owner/name, or paste its address")
		}
		opts["repo"] = repo
		if host != "" {
			reg.Host = normalizeHost(host) // a pasted address names the host too
		}
	}
	raw, _ := json.Marshal(opts)
	reg.Options = string(raw)
	return nil
}

func (s *Server) handleCreateRegistry(w http.ResponseWriter, r *http.Request) {
	var req registryRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if _, err := s.registries(); err != nil {
		failStore(w, err)
		return
	}
	reg := &store.Registry{}
	if err := s.applyRegistryRequest(reg, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.SaveRegistry(reg); err != nil {
		failStore(w, err)
		return
	}
	s.st.Audit(identityOf(r).Actor(), "registry.create", reg.Name, reg.Host+", sign-in "+reg.AuthType, true)
	writeJSON(w, http.StatusCreated, viewRegistry(reg))
}

func (s *Server) handleUpdateRegistry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "invalid registry id")
		return
	}
	reg, err := s.st.RegistryByID(id)
	if err != nil {
		failStore(w, err)
		return
	}
	var req registryRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.applyRegistryRequest(reg, &req); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.st.SaveRegistry(reg); err != nil {
		failStore(w, err)
		return
	}
	ecrTokens.forget(reg.ID)
	s.st.Audit(identityOf(r).Actor(), "registry.update", reg.Name, reg.Host+", sign-in "+reg.AuthType, true)
	writeJSON(w, http.StatusOK, viewRegistry(reg))
}

func (s *Server) handleDeleteRegistry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "invalid registry id")
		return
	}
	reg, err := s.st.RegistryByID(id)
	if err != nil {
		failStore(w, err)
		return
	}
	if reg.IsDefault {
		fail(w, http.StatusConflict, "this is the default registry; make another one the default first")
		return
	}
	if err := s.st.DeleteRegistry(id); err != nil {
		failStore(w, err)
		return
	}
	ecrTokens.forget(id)
	s.st.Audit(identityOf(r).Actor(), "registry.delete", reg.Name, reg.Host, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDefaultRegistry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "invalid registry id")
		return
	}
	if err := s.st.SetDefaultRegistry(id); err != nil {
		failStore(w, err)
		return
	}
	reg, _ := s.st.RegistryByID(id)
	if reg != nil {
		s.st.Audit(identityOf(r).Actor(), "registry.default", reg.Name, "made the default registry", true)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTestRegistry signs in to the registry through the Engine, as docker
// login would.
func (s *Server) handleTestRegistry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "invalid registry id")
		return
	}
	reg, err := s.st.RegistryByID(id)
	if err != nil {
		failStore(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	if isArchiveKind(reg.Kind) {
		listing, _, err := s.listRepoImages(ctx, reg)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		fits := 0
		for _, a := range listing.Artifacts {
			if a.Fits {
				fits++
			}
		}
		msg := fmt.Sprintf("%s (%s): %d pre-built image archives, %d for this host.", listing.Repo, listing.Ref, len(listing.Artifacts), fits)
		if len(listing.Artifacts) == 0 && len(listing.BuildFiles) > 0 {
			msg = fmt.Sprintf("%s has %s but no pre-built image archives. DocMan installs only pre-built images.", listing.Repo, strings.Join(listing.BuildFiles, ", "))
		}
		s.st.Audit(identityOf(r).Actor(), "registry.test", reg.Name, msg, true)
		writeJSON(w, http.StatusOK, map[string]any{"ok": len(listing.Artifacts) > 0, "message": msg})
		return
	}
	if reg.AuthType == authNone {
		// Nothing to sign in with: check the registry answers at all.
		if err := newRegistryClient(reg, nil).ping(ctx); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "The registry answered. No sign-in is set, so only public images can be pulled."})
		return
	}
	auth, err := s.authForRegistry(ctx, reg)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	status, err := s.dc.CheckAuth(ctx, auth)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": "Sign-in failed: " + err.Error()})
		return
	}
	s.st.Audit(identityOf(r).Actor(), "registry.test", reg.Name, status, true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": status})
}

// ---------- credentials ----------

// authForRegistry turns a registry's stored sign-in into what the Engine
// needs, fetching a fresh token where the method requires one.
func (s *Server) authForRegistry(ctx context.Context, reg *store.Registry) (*dock.RegistryAuth, error) {
	server := reg.Host
	if reg.Kind == "dockerhub" {
		server = "https://index.docker.io/v1/"
	}
	secret := s.openSecret(reg)
	switch reg.AuthType {
	case authNone:
		return nil, nil
	case authBasic, authGCP:
		return &dock.RegistryAuth{Username: reg.Username, Password: secret, ServerAddress: server}, nil
	case authToken:
		return &dock.RegistryAuth{Username: reg.Username, IdentityToken: secret, ServerAddress: server}, nil
	case authAWS:
		user, pass, err := ecrTokens.get(ctx, reg, secret)
		if err != nil {
			return nil, err
		}
		return &dock.RegistryAuth{Username: user, Password: pass, ServerAddress: server}, nil
	}
	return nil, errors.New("unknown sign-in method")
}

// refHost splits an image reference into its registry host and the rest,
// the way Docker reads it: a first part with a dot or a colon, or
// "localhost", is a host; otherwise the image is on Docker Hub.
func refHost(ref string) (host, rest string) {
	first, remainder, found := strings.Cut(ref, "/")
	if found && (strings.ContainsAny(first, ".:") || first == "localhost") {
		return normalizeHost(first), remainder
	}
	return "docker.io", ref
}

// registryFor finds the stored registry an image reference points at.
func (s *Server) registryFor(ref string) *store.Registry {
	host, _ := refHost(ref)
	list, err := s.registries()
	if err != nil {
		return nil
	}
	for _, reg := range list {
		if reg.Host == host && !isArchiveKind(reg.Kind) {
			return reg
		}
	}
	return nil
}

// authFor is the sign-in for pulling ref, or nil to pull anonymously.
func (s *Server) authFor(ctx context.Context, ref string) *dock.RegistryAuth {
	reg := s.registryFor(ref)
	if reg == nil || reg.AuthType == authNone {
		return nil
	}
	auth, err := s.authForRegistry(ctx, reg)
	if err != nil {
		s.logf("registries: could not sign in to %s for %s: %v", reg.Name, ref, err)
		return nil
	}
	return auth
}

// ---------- AWS ECR ----------

type ecrToken struct {
	user, pass string
	expires    time.Time
}

type ecrTokenCache struct {
	mu     sync.Mutex
	tokens map[int64]ecrToken
}

var ecrTokens = &ecrTokenCache{tokens: map[int64]ecrToken{}}

func (c *ecrTokenCache) forget(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tokens, id)
}

// get returns a registry username and password for ECR, fetching a new
// 12-hour token with the access key when the cached one is near expiry.
func (c *ecrTokenCache) get(ctx context.Context, reg *store.Registry, secretKey string) (string, string, error) {
	c.mu.Lock()
	if t, ok := c.tokens[reg.ID]; ok && time.Until(t.expires) > 10*time.Minute {
		c.mu.Unlock()
		return t.user, t.pass, nil
	}
	c.mu.Unlock()
	region := registryOptions(reg)["region"]
	var out struct {
		AuthorizationData []struct {
			AuthorizationToken string  `json:"authorizationToken"`
			ExpiresAt          float64 `json:"expiresAt"`
		} `json:"authorizationData"`
	}
	if err := awsCall(ctx, region, reg.Username, secretKey, "GetAuthorizationToken", map[string]any{}, &out); err != nil {
		return "", "", err
	}
	if len(out.AuthorizationData) == 0 {
		return "", "", errors.New("AWS returned no registry token")
	}
	raw, err := base64.StdEncoding.DecodeString(out.AuthorizationData[0].AuthorizationToken)
	if err != nil {
		return "", "", err
	}
	user, pass, ok := strings.Cut(string(raw), ":")
	if !ok {
		return "", "", errors.New("AWS returned an unreadable registry token")
	}
	exp := time.Unix(int64(out.AuthorizationData[0].ExpiresAt), 0)
	c.mu.Lock()
	c.tokens[reg.ID] = ecrToken{user: user, pass: pass, expires: exp}
	c.mu.Unlock()
	return user, pass, nil
}

// awsCall makes one signed (SigV4) call to the ECR API.
func awsCall(ctx context.Context, region, keyID, secret, action string, body, out any) error {
	if region == "" {
		return errors.New("the AWS region is not set")
	}
	payload, _ := json.Marshal(body)
	host := "api.ecr." + region + ".amazonaws.com"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+host+"/", strings.NewReader(string(payload)))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	day := now.Format("20060102")
	target := "AmazonEC2ContainerRegistry_V20150921." + action
	req.Header.Set("Content-Type", "application/x-amz-json-1.1")
	req.Header.Set("X-Amz-Target", target)
	req.Header.Set("X-Amz-Date", amzDate)
	hash := sha256.Sum256(payload)
	payloadHash := hex.EncodeToString(hash[:])
	signed := "content-type;host;x-amz-date;x-amz-target"
	canonical := strings.Join([]string{
		"POST", "/", "",
		"content-type:application/x-amz-json-1.1\nhost:" + host + "\nx-amz-date:" + amzDate + "\nx-amz-target:" + target + "\n",
		signed, payloadHash,
	}, "\n")
	scope := day + "/" + region + "/ecr/aws4_request"
	ch := sha256.Sum256([]byte(canonical))
	toSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(ch[:])
	mac := func(key []byte, data string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(data))
		return h.Sum(nil)
	}
	k := mac(mac(mac(mac([]byte("AWS4"+secret), day), region), "ecr"), "aws4_request")
	sig := hex.EncodeToString(mac(k, toSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+keyID+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
	resp, err := externalClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach AWS: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 400 {
		var e struct {
			Message string `json:"message"`
			Type    string `json:"__type"`
		}
		_ = json.Unmarshal(raw, &e)
		msg := e.Message
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("AWS refused: %s", msg)
	}
	return json.Unmarshal(raw, out)
}

// externalClient is for DocMan's own calls out to registries and their APIs.
// It honours HTTPS_PROXY and friends.
var externalClient = &http.Client{Timeout: 30 * time.Second}

// ---------- the registry protocol (v2) ----------

type registryClient struct {
	base   string // https://host
	user   string
	pass   string
	token  string // identity (refresh) token
	bearer map[string]string
}

func newRegistryClient(reg *store.Registry, auth *dock.RegistryAuth) *registryClient {
	host := reg.Host
	if reg.Kind == "dockerhub" {
		host = "registry-1.docker.io"
	}
	c := &registryClient{base: "https://" + host, bearer: map[string]string{}}
	if auth != nil {
		c.user, c.pass, c.token = auth.Username, auth.Password, auth.IdentityToken
	}
	return c
}

// get fetches a registry path, answering the registry's sign-in challenge.
func (c *registryClient) get(ctx context.Context, path string, out any) error {
	_, err := c.getPage(ctx, path, out)
	return err
}

// getPage is get that also returns the path of the next page, from the
// Link header registries use to page long lists.
func (c *registryClient) getPage(ctx context.Context, path string, out any) (string, error) {
	// Pages of one list share a token: the scope is the same.
	scopeKey, _, _ := strings.Cut(path, "?")
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
		if err != nil {
			return "", err
		}
		if t := c.bearer[scopeKey]; t != "" {
			req.Header.Set("Authorization", "Bearer "+t)
		} else if attempt > 0 && c.user != "" {
			req.SetBasicAuth(c.user, c.pass)
		}
		resp, err := externalClient.Do(req)
		if err != nil {
			return "", fmt.Errorf("could not reach the registry: %w", err)
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			challenge := resp.Header.Get("WWW-Authenticate")
			if strings.HasPrefix(strings.ToLower(challenge), "bearer") {
				token, err := c.fetchToken(ctx, challenge)
				if err != nil {
					return "", err
				}
				c.bearer[scopeKey] = token
			}
			continue
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return "", errRefused
		case resp.StatusCode == http.StatusNotFound:
			return "", errNotFoundAtRegistry
		case resp.StatusCode >= 400:
			return "", fmt.Errorf("the registry answered %s", resp.Status)
		}
		next := nextLink(resp.Header.Get("Link"))
		if out == nil {
			return next, nil
		}
		return next, json.Unmarshal(raw, out)
	}
	return "", errRefused
}

// Registries such as Docker Hub answer "refused" rather than "not found" for
// an image that does not exist, so as not to reveal private ones.
var errRefused = errors.New("the registry refused: the image may not exist, or it is private and needs a sign-in that allows it")

// nextLink reads the next page from a Link header: </v2/x/tags/list?last=a&n=1000>; rel="next".
func nextLink(link string) string {
	for _, part := range strings.Split(link, ",") {
		target, params, ok := strings.Cut(strings.TrimSpace(part), ";")
		if !ok || !strings.Contains(strings.ReplaceAll(params, " ", ""), `rel="next"`) {
			continue
		}
		target = strings.Trim(strings.TrimSpace(target), "<>")
		if u, err := url.Parse(target); err == nil {
			if u.IsAbs() {
				return u.RequestURI()
			}
			return target
		}
	}
	return ""
}

var errNotFoundAtRegistry = errors.New("the registry does not have that")

// fetchToken follows a Bearer challenge to the registry's token service.
func (c *registryClient) fetchToken(ctx context.Context, challenge string) (string, error) {
	params := map[string]string{}
	for _, part := range strings.Split(strings.TrimSpace(challenge[len("bearer"):]), ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			params[strings.ToLower(k)] = strings.Trim(v, `"`)
		}
	}
	realm := params["realm"]
	if realm == "" {
		return "", errors.New("the registry's sign-in challenge has no token service")
	}
	q := url.Values{}
	if params["service"] != "" {
		q.Set("service", params["service"])
	}
	if params["scope"] != "" {
		q.Set("scope", params["scope"])
	}
	var req *http.Request
	var err error
	if c.token != "" {
		// An identity token is an OAuth2 refresh token.
		form := url.Values{"grant_type": {"refresh_token"}, "refresh_token": {c.token}, "service": {params["service"]}, "scope": {params["scope"]}, "client_id": {"docman"}}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, realm, strings.NewReader(form.Encode()))
		if err == nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, realm+"?"+q.Encode(), nil)
		if err == nil && c.user != "" {
			req.SetBasicAuth(c.user, c.pass)
		}
	}
	if err != nil {
		return "", err
	}
	resp, err := externalClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach the registry's sign-in service: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", errors.New("the registry refused the sign-in; check the username and token")
	}
	var tok struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&tok); err != nil {
		return "", err
	}
	if tok.Token != "" {
		return tok.Token, nil
	}
	return tok.AccessToken, nil
}

func (c *registryClient) ping(ctx context.Context) error {
	err := c.get(ctx, "/v2/", nil)
	if err != nil && !errors.Is(err, errRefused) {
		return err
	}
	return nil
}

// tags lists a repository's tags, newest-looking first.
func (c *registryClient) tags(ctx context.Context, repo string) ([]string, error) {
	var all []string
	path := "/v2/" + repo + "/tags/list?n=1000"
	// Registries return tags in name order, a page at a time; the newest of a
	// long list are often on the last page.
	for page := 0; path != "" && page < 20; page++ {
		var out struct {
			Tags []string `json:"tags"`
		}
		next, err := c.getPage(ctx, path, &out)
		if err != nil {
			if page > 0 {
				break // keep what was listed
			}
			return nil, err
		}
		for _, t := range out.Tags {
			if !signatureTag.MatchString(t) {
				all = append(all, t)
			}
		}
		path = next
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if ga, gb := tagGroup(a), tagGroup(b); ga != gb {
			return ga < gb
		}
		return compareVersions(a, b) > 0
	})
	return all, nil
}

// signatureTag matches the tags signing tools (cosign) store signatures,
// attestations and SBOMs under; they are not images to run.
var signatureTag = regexp.MustCompile(`^sha256-[0-9a-f]{64}(\.[a-z]+)?$`)

// tagGroup orders tags: "latest"; then versions such as 3.22 or v1.12.1;
// then date- and year-numbered ones such as 20260805 or 2025.9.1; then
// names without a number.
func tagGroup(tag string) int {
	if tag == "latest" {
		return 0
	}
	parts := strings.FieldsFunc(tag, notDigit)
	if len(parts) == 0 {
		return 3
	}
	if n, _ := strconv.Atoi(parts[0]); n >= 1000 || len(parts[0]) > 4 {
		return 2
	}
	return 1
}

// compareVersions orders tags like 1.10.2 above 1.9, falling back to text.
func compareVersions(a, b string) int {
	pa, pb := strings.FieldsFunc(a, notDigit), strings.FieldsFunc(b, notDigit)
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			if x > y {
				return 1
			}
			return -1
		}
	}
	if len(pa) != len(pb) {
		if len(pa) > len(pb) {
			return 1
		}
		return -1
	}
	return strings.Compare(a, b)
}

func notDigit(r rune) bool { return r < '0' || r > '9' }

// catalog lists repositories, for registries that allow it.
func (c *registryClient) catalog(ctx context.Context) ([]string, error) {
	var out struct {
		Repositories []string `json:"repositories"`
	}
	if err := c.get(ctx, "/v2/_catalog?n=1000", &out); err != nil {
		return nil, err
	}
	return out.Repositories, nil
}

// ---------- search and tags ----------

type searchHit struct {
	Name        string `json:"name"` // as it is typed after the host
	Description string `json:"description,omitempty"`
	Stars       int    `json:"stars,omitempty"`
	Official    bool   `json:"official,omitempty"`
}

func (s *Server) handleSearchRegistry(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "invalid registry id")
		return
	}
	reg, err := s.st.RegistryByID(id)
	if err != nil {
		failStore(w, err)
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeJSON(w, http.StatusOK, map[string]any{"results": []searchHit{}})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	auth, err := s.authForRegistry(ctx, reg)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	hits, err := s.search(ctx, reg, auth, q)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if len(hits) > 50 {
		hits = hits[:50]
	}
	for i := range hits {
		hits[i].Description = plainSummary(hits[i].Description)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": hits})
}

func (s *Server) search(ctx context.Context, reg *store.Registry, auth *dock.RegistryAuth, q string) ([]searchHit, error) {
	if isArchiveKind(reg.Kind) {
		return nil, errors.New("a GitHub repository is not searched; list its pre-built images instead")
	}
	lower := strings.ToLower(q)
	match := func(name string) bool { return strings.Contains(strings.ToLower(name), lower) }
	switch reg.Kind {
	case "dockerhub":
		found, err := s.dc.SearchImages(ctx, q, 25)
		if err != nil {
			return nil, err
		}
		out := make([]searchHit, 0, len(found))
		for _, f := range found {
			out = append(out, searchHit{Name: f.Name, Description: f.Description, Stars: f.StarCount, Official: f.IsOfficial})
		}
		return out, nil
	case "ghcr", "ghes":
		return githubPackages(ctx, reg, auth, match)
	case "quay":
		var out struct {
			Results []struct {
				Name        string                `json:"name"`
				Namespace   struct{ Name string } `json:"namespace"`
				Description string                `json:"description"`
				Stars       int                   `json:"stars"`
			} `json:"results"`
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+reg.Host+"/api/v1/find/repositories?query="+url.QueryEscape(q), nil)
		if err := doJSON(req, &out); err != nil {
			return nil, err
		}
		hits := []searchHit{}
		for _, r := range out.Results {
			hits = append(hits, searchHit{Name: r.Namespace.Name + "/" + r.Name, Description: r.Description, Stars: r.Stars})
		}
		return hits, nil
	case "ecr":
		var out struct {
			Repositories []struct {
				RepositoryName string `json:"repositoryName"`
			} `json:"repositories"`
		}
		secret := s.openSecret(reg)
		if err := awsCall(ctx, registryOptions(reg)["region"], reg.Username, secret, "DescribeRepositories", map[string]any{"maxResults": 1000}, &out); err != nil {
			return nil, err
		}
		hits := []searchHit{}
		for _, r := range out.Repositories {
			if match(r.RepositoryName) {
				hits = append(hits, searchHit{Name: r.RepositoryName})
			}
		}
		return hits, nil
	case "gitlab", "gar":
		return nil, errors.New("this registry does not offer search; enter the full image path")
	default:
		names, err := newRegistryClient(reg, auth).catalog(ctx)
		if err != nil {
			return nil, errors.New("this registry does not allow listing its images; enter the full image path")
		}
		hits := []searchHit{}
		for _, n := range names {
			if match(n) {
				hits = append(hits, searchHit{Name: n})
			}
		}
		return hits, nil
	}
}

// githubPackages lists the container packages a GitHub token can see.
func githubPackages(ctx context.Context, reg *store.Registry, auth *dock.RegistryAuth, match func(string) bool) ([]searchHit, error) {
	if auth == nil || auth.Password == "" {
		return nil, errors.New("searching GitHub needs a sign-in with a token; enter the full image path, such as owner/image")
	}
	api := "https://api.github.com"
	if reg.Kind == "ghes" {
		api = registryOptions(reg)["api_url"]
		if api == "" {
			host := strings.TrimPrefix(reg.Host, "containers.")
			api = "https://" + host + "/api/v3"
		}
	}
	var pkgs []struct {
		Name        string                 `json:"name"`
		Owner       struct{ Login string } `json:"owner"`
		Description string                 `json:"description"`
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(api, "/")+"/user/packages?package_type=container&per_page=100", nil)
	req.Header.Set("Authorization", "Bearer "+auth.Password)
	req.Header.Set("Accept", "application/vnd.github+json")
	if err := doJSON(req, &pkgs); err != nil {
		return nil, err
	}
	hits := []searchHit{}
	for _, p := range pkgs {
		name := strings.ToLower(p.Owner.Login + "/" + p.Name)
		if match(name) {
			hits = append(hits, searchHit{Name: name, Description: p.Description})
		}
	}
	return hits, nil
}

func doJSON(req *http.Request, out any) error {
	resp, err := externalClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach the registry: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errors.New("the registry refused: check the credentials allow listing")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("the registry answered %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(out)
}

// handleRegistryTags lists the tags of one repository.
func (s *Server) handleRegistryTags(w http.ResponseWriter, r *http.Request) {
	id, ok := pathInt(r, "id")
	if !ok {
		fail(w, http.StatusBadRequest, "invalid registry id")
		return
	}
	reg, err := s.st.RegistryByID(id)
	if err != nil {
		failStore(w, err)
		return
	}
	repo := strings.Trim(strings.TrimSpace(r.URL.Query().Get("repo")), "/")
	if repo == "" {
		fail(w, http.StatusBadRequest, "repo is required")
		return
	}
	if isArchiveKind(reg.Kind) {
		fail(w, http.StatusBadRequest, "a GitHub repository has no tags; list its pre-built images instead")
		return
	}
	if reg.Kind == "dockerhub" && !strings.Contains(repo, "/") {
		repo = "library/" + repo // official images live under library/
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	auth, err := s.authForRegistry(ctx, reg)
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	tags, err := newRegistryClient(reg, auth).tags(ctx, repo)
	if errors.Is(err, errNotFoundAtRegistry) {
		fail(w, http.StatusNotFound, "the registry has no image called "+repo)
		return
	}
	if err != nil {
		fail(w, http.StatusBadGateway, err.Error())
		return
	}
	if len(tags) > 300 {
		tags = tags[:300]
	}
	writeJSON(w, http.StatusOK, map[string]any{"tags": tags})
}

// imageRef builds the reference to pull from a registry, a repository and a tag.
func imageRef(reg *store.Registry, repo, tag string) string {
	repo = strings.Trim(repo, "/")
	if tag == "" {
		tag = "latest"
	}
	if reg.Kind == "dockerhub" {
		return strings.TrimPrefix(repo, "library/") + ":" + tag
	}
	return reg.Host + "/" + repo + ":" + tag
}

var (
	htmlTag    = regexp.MustCompile(`<[^>]*>`)
	mdImage    = regexp.MustCompile(`!\[[^\]]*\]\([^)]*\)`)               // badges
	mdMarkup   = regexp.MustCompile(`\]\([^)]*\)|[#*_>\[\]` + "`" + `]+`) // link targets, then markup
	whitespace = regexp.MustCompile(`\s+`)
)

// plainSummary turns a description that may be HTML or Markdown (Quay's are
// whole README files) into one short line of text.
func plainSummary(text string) string {
	text = htmlTag.ReplaceAllString(text, " ")
	text = mdImage.ReplaceAllString(text, " ")
	text = mdMarkup.ReplaceAllString(text, " ")
	text = strings.TrimSpace(whitespace.ReplaceAllString(html.UnescapeString(text), " "))
	if r := []rune(text); len(r) > 160 {
		text = strings.TrimSpace(string(r[:157])) + "…"
	}
	return text
}
