// Package dock is a small, dependency-free client for the Docker Engine API
// speaking HTTP over the local unix socket (or a TCP endpoint). It covers only
// what DocMan needs, but covers it fully: JSON calls, chunked streams and
// hijacked bidirectional attachments for interactive exec sessions.
package dock

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// APIVersion is the Engine API version DocMan negotiates against. 1.41 ships
// with Docker 20.10 and later, which covers every currently supported release.
const APIVersion = "1.41"

// Client talks to one Docker Engine.
type Client struct {
	network string // "unix" or "tcp"
	addr    string
	hc      *http.Client
}

// Error is a non-2xx response from the Engine.
type Error struct {
	Status  int
	Message string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("docker: http %d", e.Status)
	}
	return fmt.Sprintf("docker: %s (http %d)", e.Message, e.Status)
}

// NotFound reports whether err is a Docker 404.
func NotFound(err error) bool {
	var de *Error
	return errors.As(err, &de) && de.Status == http.StatusNotFound
}

// Conflict reports whether err is a Docker 409 (e.g. already started).
func Conflict(err error) bool {
	var de *Error
	return errors.As(err, &de) && de.Status == http.StatusConflict
}

// New builds a client for a docker host string: "unix:///var/run/docker.sock",
// "/var/run/docker.sock", "tcp://host:2375" or "host:2375".
func New(host string) (*Client, error) {
	network, addr := "unix", "/var/run/docker.sock"
	switch {
	case host == "":
	case strings.HasPrefix(host, "unix://"):
		network, addr = "unix", strings.TrimPrefix(host, "unix://")
	case strings.HasPrefix(host, "tcp://"):
		network, addr = "tcp", strings.TrimPrefix(host, "tcp://")
	case strings.HasPrefix(host, "http://"):
		network, addr = "tcp", strings.TrimPrefix(host, "http://")
	case strings.HasPrefix(host, "/"):
		network, addr = "unix", host
	default:
		network, addr = "tcp", host
	}
	if addr == "" {
		return nil, errors.New("dock: empty docker host address")
	}
	c := &Client{network: network, addr: addr}
	c.hc = &http.Client{
		// No client timeout: log/stat/event streams are long-lived and are
		// bounded by the request context instead.
		Transport: &http.Transport{
			DialContext:         c.dialContext,
			DisableCompression:  true,
			MaxIdleConns:        8,
			IdleConnTimeout:     30 * time.Second,
			TLSHandshakeTimeout: 5 * time.Second,
		},
	}
	return c, nil
}

// Endpoint describes where this client is pointed, for display purposes.
func (c *Client) Endpoint() string { return c.network + "://" + c.addr }

// SocketPath is the unix socket this client dials, or "" for a TCP endpoint.
func (c *Client) SocketPath() string {
	if c.network != "unix" {
		return ""
	}
	return c.addr
}

func (c *Client) dialContext(ctx context.Context, _, _ string) (net.Conn, error) {
	var d net.Dialer
	d.Timeout = 10 * time.Second
	return d.DialContext(ctx, c.network, c.addr)
}

func buildPath(path string, q url.Values) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	full := "/v" + APIVersion + path
	if len(q) > 0 {
		full += "?" + q.Encode()
	}
	return full
}

func (c *Client) newRequest(ctx context.Context, method, path string, q url.Values, body io.Reader, contentType string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+buildPath(path, q), body)
	if err != nil {
		return nil, err
	}
	req.Host = "docker"
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	return req, nil
}

func decodeError(resp *http.Response) error {
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	msg := strings.TrimSpace(string(raw))
	var payload struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &payload) == nil && payload.Message != "" {
		msg = payload.Message
	}
	return &Error{Status: resp.StatusCode, Message: msg}
}

// Do performs a JSON request. body may be nil; out may be nil.
func (c *Client) Do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	var reader io.Reader
	contentType := ""
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
		contentType = "application/json"
	}
	req, err := c.newRequest(ctx, method, path, q, reader, contentType)
	if err != nil {
		return err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		return decodeError(resp)
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// Get is a convenience wrapper for GET + JSON decode.
func (c *Client) Get(ctx context.Context, path string, q url.Values, out any) error {
	return c.Do(ctx, http.MethodGet, path, q, nil, out)
}

// Post is a convenience wrapper for POST + optional JSON decode.
func (c *Client) Post(ctx context.Context, path string, q url.Values, body, out any) error {
	return c.Do(ctx, http.MethodPost, path, q, body, out)
}

// Delete is a convenience wrapper for DELETE.
func (c *Client) Delete(ctx context.Context, path string, q url.Values) error {
	return c.Do(ctx, http.MethodDelete, path, q, nil, nil)
}

// Stream issues a request and hands back the raw response body for streaming
// endpoints (logs, stats, events, pull/load progress). The caller closes it.
func (c *Client) Stream(ctx context.Context, method, path string, q url.Values, body io.Reader, contentType string) (io.ReadCloser, error) {
	req, err := c.newRequest(ctx, method, path, q, body, contentType)
	if err != nil {
		return nil, err
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, decodeError(resp)
	}
	return resp.Body, nil
}

// Hijacked is a bidirectional attachment to a container or exec session.
type Hijacked struct {
	Conn net.Conn
	// Reader carries any bytes already buffered while reading the response
	// headers, so it must be used instead of Conn for reads.
	Reader *bufio.Reader
}

// Close tears the attachment down.
func (h *Hijacked) Close() error { return h.Conn.Close() }

// CloseWrite signals EOF on stdin where the transport supports it.
func (h *Hijacked) CloseWrite() error {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := h.Conn.(closeWriter); ok {
		return cw.CloseWrite()
	}
	return nil
}

// Hijack performs a POST that upgrades to a raw bidirectional stream. This is
// how the Engine exposes container attach and exec I/O.
func (c *Client) Hijack(ctx context.Context, path string, q url.Values, body any) (*Hijacked, error) {
	conn, err := c.dialContext(ctx, "", "")
	if err != nil {
		return nil, err
	}
	var payload []byte
	if body != nil {
		payload, err = json.Marshal(body)
		if err != nil {
			conn.Close()
			return nil, err
		}
	} else {
		payload = []byte("{}")
	}

	var head bytes.Buffer
	fmt.Fprintf(&head, "POST %s HTTP/1.1\r\n", buildPath(path, q))
	head.WriteString("Host: docker\r\n")
	head.WriteString("User-Agent: docman\r\n")
	head.WriteString("Content-Type: application/json\r\n")
	head.WriteString("Connection: Upgrade\r\n")
	head.WriteString("Upgrade: tcp\r\n")
	fmt.Fprintf(&head, "Content-Length: %d\r\n\r\n", len(payload))
	head.Write(payload)

	if err := conn.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := conn.Write(head.Bytes()); err != nil {
		conn.Close()
		return nil, err
	}
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols && resp.StatusCode != http.StatusOK {
		err := decodeError(resp)
		conn.Close()
		return nil, err
	}
	// Long-lived from here on; the caller's context governs the lifetime.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	go func() {
		<-ctx.Done()
		conn.Close()
	}()
	return &Hijacked{Conn: conn, Reader: br}, nil
}

// StdFrame is one demultiplexed chunk of a non-TTY container stream.
type StdFrame struct {
	// Stream is 0 for stdin, 1 for stdout, 2 for stderr.
	Stream byte
	Data   []byte
}

// ReadFrame reads one 8-byte-header framed chunk from a non-TTY docker stream.
func ReadFrame(r io.Reader) (StdFrame, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return StdFrame{}, err
	}
	size := binary.BigEndian.Uint32(hdr[4:8])
	if size > 16<<20 {
		return StdFrame{}, errors.New("dock: oversized stream frame")
	}
	data := make([]byte, size)
	if _, err := io.ReadFull(r, data); err != nil {
		return StdFrame{}, err
	}
	return StdFrame{Stream: hdr[0], Data: data}, nil
}
