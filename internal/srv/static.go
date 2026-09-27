package srv

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// asset is one preloaded UI file, with a strong ETag and a gzip copy where
// compression is worthwhile.
type asset struct {
	body        []byte
	gzipped     []byte
	contentType string
	etag        string
}

var (
	assetsOnce sync.Once
	assets     map[string]*asset
	indexAsset *asset
)

// loadAssets reads the embedded UI into memory once. The whole UI is a handful
// of files, so serving it from memory keeps request handling trivial.
func (s *Server) loadAssets() {
	assetsOnce.Do(func() {
		assets = map[string]*asset{}
		if s.web == nil {
			return
		}
		_ = fs.WalkDir(s.web, ".", func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			body, err := fs.ReadFile(s.web, p)
			if err != nil {
				return nil
			}
			sum := sha256.Sum256(body)
			a := &asset{
				body:        body,
				contentType: contentTypeFor(p),
				etag:        `"` + hex.EncodeToString(sum[:16]) + `"`,
			}
			if compressible(a.contentType) && len(body) > 1024 {
				var buf bytes.Buffer
				zw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
				if err == nil {
					if _, err := zw.Write(body); err == nil && zw.Close() == nil && buf.Len() < len(body) {
						a.gzipped = buf.Bytes()
					}
				}
			}
			assets["/"+p] = a
			return nil
		})
		indexAsset = assets["/index.html"]
	})
}

func contentTypeFor(p string) string {
	switch strings.ToLower(path.Ext(p)) {
	case ".html":
		return "text/html; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".ico":
		return "image/x-icon"
	case ".woff2":
		return "font/woff2"
	}
	if t := mime.TypeByExtension(path.Ext(p)); t != "" {
		return t
	}
	return "application/octet-stream"
}

func compressible(contentType string) bool {
	for _, prefix := range []string{"text/", "application/json", "image/svg+xml", "text/javascript"} {
		if strings.HasPrefix(contentType, prefix) {
			return true
		}
	}
	return false
}

// handleStatic serves the single-page UI, falling back to index.html so that
// deep links work on reload.
func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	s.loadAssets()

	if strings.HasPrefix(r.URL.Path, "/api/") {
		fail(w, http.StatusNotFound, "no such endpoint")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		fail(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	clean := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/"))
	a := assets[clean]
	if a == nil && clean == "/" {
		a = indexAsset
	}
	if a == nil {
		// Unknown path: hand the SPA its shell and let the router decide.
		a = indexAsset
	}
	if a == nil {
		fail(w, http.StatusNotFound, "the DocMan UI is not embedded in this build")
		return
	}
	serveAsset(w, r, a, a == indexAsset)
}

func serveAsset(w http.ResponseWriter, r *http.Request, a *asset, isShell bool) {
	h := w.Header()
	h.Set("Content-Type", a.contentType)
	h.Set("ETag", a.etag)
	// Every file is revalidated on each load (a cheap 304 when unchanged). A
	// freshness window would let a browser run a mix of old and new scripts
	// for a while after DocMan is upgraded, which breaks pages.
	_ = isShell
	h.Set("Cache-Control", "no-cache")
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, a.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := a.body
	if a.gzipped != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		h.Add("Vary", "Accept-Encoding")
		body = a.gzipped
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, bytes.NewReader(body))
}
