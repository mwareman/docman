// Package wsx is a minimal RFC 6455 WebSocket server: enough to carry DocMan's
// log, statistics and terminal streams without pulling in a dependency.
package wsx

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const handshakeGUID = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

// Opcodes as defined by RFC 6455.
const (
	OpContinuation = 0x0
	OpText         = 0x1
	OpBinary       = 0x2
	OpClose        = 0x8
	OpPing         = 0x9
	OpPong         = 0xA
)

// MaxMessageSize caps a single inbound message.
const MaxMessageSize = 1 << 20

// ErrClosed is returned once the peer has closed the connection.
var ErrClosed = errors.New("wsx: connection closed")

// Conn is an upgraded WebSocket connection.
type Conn struct {
	conn net.Conn
	br   *bufio.Reader

	wmu    sync.Mutex
	closed bool
	cmu    sync.Mutex
}

// IsWebSocket reports whether r is a WebSocket upgrade request.
func IsWebSocket(r *http.Request) bool {
	return tokenListContains(r.Header.Get("Connection"), "upgrade") &&
		strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket")
}

func tokenListContains(header, want string) bool {
	for _, part := range strings.Split(header, ",") {
		if strings.EqualFold(strings.TrimSpace(part), want) {
			return true
		}
	}
	return false
}

// Upgrade completes the WebSocket handshake and takes over the connection.
func Upgrade(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if r.Method != http.MethodGet {
		return nil, errors.New("wsx: upgrade requires GET")
	}
	if !IsWebSocket(r) {
		return nil, errors.New("wsx: not a websocket upgrade")
	}
	if r.Header.Get("Sec-Websocket-Version") != "13" {
		return nil, errors.New("wsx: unsupported websocket version")
	}
	key := r.Header.Get("Sec-Websocket-Key")
	if key == "" {
		return nil, errors.New("wsx: missing Sec-WebSocket-Key")
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("wsx: response writer does not support hijacking")
	}
	conn, rw, err := hj.Hijack()
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(key + handshakeGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])

	var head strings.Builder
	head.WriteString("HTTP/1.1 101 Switching Protocols\r\n")
	head.WriteString("Upgrade: websocket\r\n")
	head.WriteString("Connection: Upgrade\r\n")
	head.WriteString("Sec-WebSocket-Accept: " + accept + "\r\n\r\n")

	if err := conn.SetWriteDeadline(time.Now().Add(10 * time.Second)); err != nil {
		conn.Close()
		return nil, err
	}
	if _, err := io.WriteString(conn, head.String()); err != nil {
		conn.Close()
		return nil, err
	}
	if err := conn.SetWriteDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}
	br := rw.Reader
	if br == nil {
		br = bufio.NewReader(conn)
	}
	return &Conn{conn: conn, br: br}, nil
}

// Close shuts the connection down.
func (c *Conn) Close() error {
	c.cmu.Lock()
	already := c.closed
	c.closed = true
	c.cmu.Unlock()
	if already {
		return nil
	}
	return c.conn.Close()
}

// RemoteAddr reports the peer address.
func (c *Conn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// SetReadDeadline bounds how long a read may block.
func (c *Conn) SetReadDeadline(t time.Time) error { return c.conn.SetReadDeadline(t) }

// ReadMessage returns the next data message, transparently answering pings and
// reassembling continuation frames. It returns ErrClosed on a clean close.
func (c *Conn) ReadMessage() (opcode byte, payload []byte, err error) {
	var msgOp byte
	var buf []byte
	for {
		fin, op, data, err := c.readFrame()
		if err != nil {
			return 0, nil, err
		}
		switch op {
		case OpPing:
			if err := c.write(OpPong, data); err != nil {
				return 0, nil, err
			}
			continue
		case OpPong:
			continue
		case OpClose:
			_ = c.write(OpClose, data)
			_ = c.Close()
			return 0, nil, ErrClosed
		case OpText, OpBinary:
			if len(buf) > 0 {
				return 0, nil, errors.New("wsx: interleaved data frame")
			}
			msgOp = op
			buf = data
		case OpContinuation:
			if msgOp == 0 {
				return 0, nil, errors.New("wsx: continuation without start")
			}
			buf = append(buf, data...)
		default:
			return 0, nil, errors.New("wsx: unknown opcode")
		}
		if len(buf) > MaxMessageSize {
			return 0, nil, errors.New("wsx: message too large")
		}
		if fin {
			return msgOp, buf, nil
		}
	}
}

func (c *Conn) readFrame() (fin bool, opcode byte, payload []byte, err error) {
	var hdr [2]byte
	if _, err := io.ReadFull(c.br, hdr[:]); err != nil {
		return false, 0, nil, err
	}
	fin = hdr[0]&0x80 != 0
	if hdr[0]&0x70 != 0 {
		return false, 0, nil, errors.New("wsx: reserved bits set")
	}
	opcode = hdr[0] & 0x0f
	masked := hdr[1]&0x80 != 0
	length := uint64(hdr[1] & 0x7f)
	switch length {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.br, ext[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(ext[:])
	}
	if length > MaxMessageSize {
		return false, 0, nil, errors.New("wsx: frame too large")
	}
	if !masked {
		// RFC 6455: every client-to-server frame must be masked.
		return false, 0, nil, errors.New("wsx: unmasked client frame")
	}
	var mask [4]byte
	if _, err := io.ReadFull(c.br, mask[:]); err != nil {
		return false, 0, nil, err
	}
	payload = make([]byte, length)
	if _, err := io.ReadFull(c.br, payload); err != nil {
		return false, 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i%4]
	}
	return fin, opcode, payload, nil
}

func (c *Conn) write(opcode byte, payload []byte) error {
	c.cmu.Lock()
	closed := c.closed
	c.cmu.Unlock()
	if closed {
		return ErrClosed
	}

	var head [10]byte
	head[0] = 0x80 | opcode
	n := 2
	switch {
	case len(payload) < 126:
		head[1] = byte(len(payload))
	case len(payload) <= 0xffff:
		head[1] = 126
		binary.BigEndian.PutUint16(head[2:4], uint16(len(payload)))
		n = 4
	default:
		head[1] = 127
		binary.BigEndian.PutUint64(head[2:10], uint64(len(payload)))
		n = 10
	}

	c.wmu.Lock()
	defer c.wmu.Unlock()
	if err := c.conn.SetWriteDeadline(time.Now().Add(20 * time.Second)); err != nil {
		return err
	}
	if _, err := c.conn.Write(head[:n]); err != nil {
		return err
	}
	if len(payload) > 0 {
		if _, err := c.conn.Write(payload); err != nil {
			return err
		}
	}
	return c.conn.SetWriteDeadline(time.Time{})
}

// WriteText sends a text message.
func (c *Conn) WriteText(b []byte) error { return c.write(OpText, b) }

// WriteBinary sends a binary message.
func (c *Conn) WriteBinary(b []byte) error { return c.write(OpBinary, b) }

// WriteJSON marshals v and sends it as a text message.
func (c *Conn) WriteJSON(v any) error {
	buf, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return c.write(OpText, buf)
}

// WritePing sends a ping frame.
func (c *Conn) WritePing() error { return c.write(OpPing, nil) }

// WriteClose sends a close frame with a status code and reason.
func (c *Conn) WriteClose(code uint16, reason string) error {
	payload := make([]byte, 2, 2+len(reason))
	binary.BigEndian.PutUint16(payload, code)
	payload = append(payload, reason...)
	return c.write(OpClose, payload)
}

// Pinger keeps the connection warm through proxies until stop is closed.
func (c *Conn) Pinger(interval time.Duration, stop <-chan struct{}) {
	if interval <= 0 {
		interval = 25 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			if err := c.WritePing(); err != nil {
				return
			}
		}
	}
}
