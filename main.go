// Command docman runs the DocMan container manager: a single static binary
// that serves its own UI and talks to the local Docker Engine.
package main

import (
	"context"
	"crypto/tls"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata" // embed the zoneinfo database: the scratch image has none

	"docman/internal/crypt"
	"docman/internal/dock"
	"docman/internal/srv"
	"docman/internal/store"
	"docman/internal/volagent"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

//go:embed all:web
var webFS embed.FS

// The API reference is served from the binary at GET /api and /help/api.
//
//go:embed API.md
var apiDoc []byte

// The user guide is served from the binary at /help.
//
//go:embed help/*.md
var helpFS embed.FS

func main() {
	// DocMan cannot recreate its own container from inside it, so it runs a
	// copy of this binary in a helper container to do it.
	// Inside a volume explorer helper, the binary is the helper's only tool.
	if len(os.Args) > 1 && os.Args[1] == volagent.Command {
		os.Exit(volagent.Run(os.Args[2:], os.Stdout, os.Stderr))
	}
	if len(os.Args) > 1 && os.Args[1] == srv.SelfReplaceCommand {
		logger := log.New(os.Stdout, "", log.LstdFlags|log.LUTC)
		dc, err := dock.New(env("DOCMAN_DOCKER_HOST", "unix:///var/run/docker.sock"))
		if err == nil {
			err = srv.RunSelfReplace(context.Background(), dc, logger.Printf)
		}
		if err != nil {
			logger.Printf("fatal: %v", err)
			os.Exit(1)
		}
		return
	}
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--version", "-version", "-v":
			fmt.Println("docman", version)
			return
		case "--help", "-h", "help":
			usage()
			return
		}
	}
	logger := log.New(os.Stdout, "", log.LstdFlags|log.LUTC)
	if err := run(logger); err != nil {
		logger.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

// usage prints the environment DocMan reads. There are no flags: everything is
// configured through the environment so a compose file is the whole story.
func usage() {
	fmt.Println(`docman ` + version + ` — manage the containers on one docker host.

DocMan takes no flags. It is configured with environment variables:

  DOCMAN_DATA_DIR      where the SQLite database and TLS keys live (default /data)
  DOCMAN_DOCKER_HOST   docker endpoint (default unix:///var/run/docker.sock)
  DOCMAN_HTTPS_ADDR    HTTPS listen address (default :9444)
  DOCMAN_HTTP_ADDR     HTTP listen address, redirects to HTTPS (default :9080)
  DOCMAN_TLS_CERT      path to your own certificate (optional)
  DOCMAN_TLS_KEY       path to your own private key (optional)
  DOCMAN_DISABLE_TLS   serve plain HTTP only; passkeys then need localhost or a proxy
  DOCMAN_HOSTNAMES     comma separated names to put in the self-signed certificate
  DOCMAN_RP_ID         hostname passkeys are bound to (default: the browser's host)
  DOCMAN_SESSION_TTL   browser session lifetime, e.g. 12h (default 12h)
  DOCMAN_TRUST_PROXY   honour X-Forwarded-* headers (default false)
  DOCMAN_PROC_PATH     where to read host metrics from (default /proc)
  DOCMAN_SETUP_KEY     use a fixed first-run key instead of a generated one

Run it with the docker socket and a data volume:

  docker run -d --name docman -p 9444:9444
    -v /var/run/docker.sock:/var/run/docker.sock
    -v docman-data:/data docman:latest

The first-run setup key is printed to this log.`)
}

func run(logger *log.Logger) error {
	cfg := srv.Config{
		DataDir:     env("DOCMAN_DATA_DIR", "/data"),
		DockerHost:  env("DOCMAN_DOCKER_HOST", "unix:///var/run/docker.sock"),
		HTTPSAddr:   env("DOCMAN_HTTPS_ADDR", ":9444"),
		HTTPAddr:    env("DOCMAN_HTTP_ADDR", ":9080"),
		TLSCertFile: os.Getenv("DOCMAN_TLS_CERT"),
		TLSKeyFile:  os.Getenv("DOCMAN_TLS_KEY"),
		DisableTLS:  envBool("DOCMAN_DISABLE_TLS", false),
		RPID:        os.Getenv("DOCMAN_RP_ID"),
		Hostnames:   splitList(os.Getenv("DOCMAN_HOSTNAMES")),
		SessionTTL:  envDuration("DOCMAN_SESSION_TTL", 12*time.Hour),
		ProcPath:    os.Getenv("DOCMAN_PROC_PATH"),
		TrustProxy:  envBool("DOCMAN_TRUST_PROXY", false),
		Version:     version,
		APIDoc:      apiDoc,
		HelpFS:      mustSub(helpFS, "help"),
	}

	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("the data directory %s is not writable: %w", cfg.DataDir, err)
	}
	dbPath := filepath.Join(cfg.DataDir, "docman.db")
	st, err := store.Open(dbPath)
	if err != nil {
		return fmt.Errorf("open database %s: %w", dbPath, err)
	}
	defer st.Close()

	dc, err := dock.New(cfg.DockerHost)
	if err != nil {
		return err
	}
	if err := waitForDocker(dc, logger); err != nil {
		return err
	}

	server := srv.New(cfg, st, dc, mustSubFS(), logger)

	// First run: publish a setup key on the console. It is regenerated on every
	// start until an admin account exists, and is single use.
	if !server.SetupComplete() {
		key := os.Getenv("DOCMAN_SETUP_KEY")
		if key == "" {
			key = crypt.SetupKey()
		}
		server.SetSetupKey(key)
		printSetupBanner(logger, key, cfg)
	} else {
		logger.Printf("DocMan %s ready; sign in at the address below", version)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go server.Housekeeping(ctx)
	go server.SweepSelfReplaceHelpers(ctx)
	go server.ImageUpdateChecker(ctx)
	go server.ExplorerJanitor(ctx)
	go server.UploadJanitor(ctx)
	go func() {
		// Restore any fixed addresses that drifted while DocMan was down.
		time.Sleep(2 * time.Second)
		for _, note := range server.NetManager().Reconcile(ctx) {
			logger.Printf("addresses: %s", note)
		}
	}()

	handler := server.Handler()
	var servers []*http.Server

	if cfg.DisableTLS {
		logger.Printf("TLS is disabled; passkeys will only work over localhost or behind an HTTPS proxy")
		httpSrv := newHTTPServer(cfg.HTTPAddr, handler)
		servers = append(servers, httpSrv)
		go listen(httpSrv, "", "", "http://"+displayAddr(cfg.HTTPAddr), logger)
	} else {
		certFile, keyFile := cfg.TLSCertFile, cfg.TLSKeyFile
		if certFile == "" || keyFile == "" {
			var generated bool
			certFile, keyFile, generated, err = srv.EnsureTLS(cfg.DataDir, cfg.Hostnames)
			if err != nil {
				return fmt.Errorf("prepare TLS certificate: %w", err)
			}
			if generated {
				logger.Printf("generated a self-signed TLS certificate in %s", cfg.DataDir)
			}
		}
		httpsSrv := newHTTPServer(cfg.HTTPSAddr, handler)
		httpsSrv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			// The console and every live stream are WebSockets, which need
			// connection hijacking. Go's HTTP/2 server cannot hijack, so
			// HTTP/1.1 is negotiated deliberately rather than by luck.
			NextProtos: []string{"http/1.1"},
		}
		servers = append(servers, httpsSrv)
		go listen(httpsSrv, certFile, keyFile, "https://"+displayAddr(cfg.HTTPSAddr), logger)

		if cfg.HTTPAddr != "" {
			redirect := newHTTPServer(cfg.HTTPAddr, redirectToHTTPS(cfg.HTTPSAddr))
			servers = append(servers, redirect)
			go listen(redirect, "", "", "", logger)
		}
	}

	<-ctx.Done()
	logger.Printf("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for _, s := range servers {
		_ = s.Shutdown(shutdownCtx)
	}
	return nil
}

func newHTTPServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:    addr,
		Handler: handler,
		// Streams and terminals are long lived, so no write timeout here; the
		// read header timeout still protects against slow-loris clients.
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
		ErrorLog:          log.New(os.Stdout, "http: ", log.LstdFlags),
	}
}

func listen(s *http.Server, certFile, keyFile, announce string, logger *log.Logger) {
	if announce != "" {
		logger.Printf("listening on %s", announce)
	}
	var err error
	if certFile != "" {
		err = s.ListenAndServeTLS(certFile, keyFile)
	} else {
		err = s.ListenAndServe()
	}
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Printf("listener %s stopped: %v", s.Addr, err)
	}
}

func redirectToHTTPS(httpsAddr string) http.Handler {
	_, port, _ := strings.Cut(strings.TrimPrefix(httpsAddr, "http://"), ":")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, ok := strings.Cut(host, ":"); ok {
			host = h
		}
		target := "https://" + host
		if port != "" && port != "443" {
			target += ":" + port
		}
		target += r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusPermanentRedirect)
	})
}

func waitForDocker(dc *dock.Client, logger *log.Logger) error {
	var lastErr error
	for attempt := 0; attempt < 10; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := dc.Ping(ctx)
		cancel()
		if err == nil {
			if info, err := dc.Info(context.Background()); err == nil {
				logger.Printf("docker %s on %s/%s via %s (%d containers, %d images)",
					info.ServerVersion, info.OSType, info.Architecture, dc.Endpoint(),
					info.Containers, info.Images)
			}
			return nil
		}
		lastErr = err
		logger.Printf("waiting for the docker socket at %s: %v", dc.Endpoint(), err)
		time.Sleep(time.Duration(attempt+1) * time.Second)
	}
	return fmt.Errorf("cannot reach the docker engine at %s: %w\n"+
		"mount the socket into the container, for example:\n"+
		"  -v /var/run/docker.sock:/var/run/docker.sock", dc.Endpoint(), lastErr)
}

func printSetupBanner(logger *log.Logger, key string, cfg srv.Config) {
	scheme, addr := "https", cfg.HTTPSAddr
	if cfg.DisableTLS {
		scheme, addr = "http", cfg.HTTPAddr
	}
	line := strings.Repeat("=", 62)
	logger.Printf("\n%s\n"+
		"  DocMan %s first-run setup\n"+
		"%s\n"+
		"  Open   %s://<this-host>%s\n"+
		"  Key    %s\n"+
		"\n"+
		"  Enter that key in the browser, then choose an administrator\n"+
		"  username and password. The key works once and is replaced on\n"+
		"  every restart until setup is finished.\n"+
		"%s",
		line, version, line, scheme, displayPort(addr), key, line)
}

func displayAddr(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return "0.0.0.0" + addr
	}
	return addr
}

func displayPort(addr string) string {
	if strings.HasPrefix(addr, ":") {
		return addr
	}
	if _, port, ok := strings.Cut(addr, ":"); ok {
		return ":" + port
	}
	return addr
}

func mustSubFS() fs.FS { return mustSub(webFS, "web") }

func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		// The files are embedded at build time, so this cannot happen in a real build.
		return fsys
	}
	return sub
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	if d, err := time.ParseDuration(v); err == nil && d > 0 {
		return d
	}
	// A bare number is taken as hours, which is how people usually mean it.
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		return time.Duration(n) * time.Hour
	}
	return def
}

func splitList(v string) []string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
