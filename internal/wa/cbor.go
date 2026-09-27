package wa

import (
	"encoding/binary"
	"errors"
	"math"
)

// A deliberately small CBOR reader: enough to parse WebAuthn attestation
// objects and COSE_Key structures, and nothing more. Decoded values use
// map[any]any / []any / []byte / string / int64 / uint64 / bool / nil / float64.

var errCBOR = errors.New("webauthn: malformed CBOR")

type cborReader struct {
	buf []byte
	pos int
}

func (r *cborReader) byteAt() (byte, error) {
	if r.pos >= len(r.buf) {
		return 0, errCBOR
	}
	b := r.buf[r.pos]
	r.pos++
	return b, nil
}

func (r *cborReader) take(n int) ([]byte, error) {
	if n < 0 || r.pos+n > len(r.buf) {
		return nil, errCBOR
	}
	out := r.buf[r.pos : r.pos+n]
	r.pos += n
	return out, nil
}

// head reads the initial byte plus any following length/value argument.
// indefinite is true for the reserved 31 additional-information value.
func (r *cborReader) head() (major byte, arg uint64, indefinite bool, err error) {
	ib, err := r.byteAt()
	if err != nil {
		return 0, 0, false, err
	}
	major = ib >> 5
	ai := ib & 0x1f
	switch {
	case ai < 24:
		return major, uint64(ai), false, nil
	case ai == 24:
		b, err := r.byteAt()
		return major, uint64(b), false, err
	case ai == 25:
		b, err := r.take(2)
		if err != nil {
			return 0, 0, false, err
		}
		return major, uint64(binary.BigEndian.Uint16(b)), false, nil
	case ai == 26:
		b, err := r.take(4)
		if err != nil {
			return 0, 0, false, err
		}
		return major, uint64(binary.BigEndian.Uint32(b)), false, nil
	case ai == 27:
		b, err := r.take(8)
		if err != nil {
			return 0, 0, false, err
		}
		return major, binary.BigEndian.Uint64(b), false, nil
	case ai == 31:
		return major, 0, true, nil
	}
	return 0, 0, false, errCBOR
}

func (r *cborReader) value() (any, error) {
	major, arg, indefinite, err := r.head()
	if err != nil {
		return nil, err
	}
	switch major {
	case 0: // unsigned integer
		if arg > math.MaxInt64 {
			return arg, nil
		}
		return int64(arg), nil
	case 1: // negative integer
		if arg > math.MaxInt64 {
			return nil, errCBOR
		}
		return -1 - int64(arg), nil
	case 2: // byte string
		if indefinite {
			var out []byte
			for {
				b, err := r.peekBreak()
				if err != nil {
					return nil, err
				}
				if b {
					return out, nil
				}
				chunk, err := r.value()
				if err != nil {
					return nil, err
				}
				part, ok := chunk.([]byte)
				if !ok {
					return nil, errCBOR
				}
				out = append(out, part...)
			}
		}
		if arg > uint64(len(r.buf)) {
			return nil, errCBOR
		}
		return r.take(int(arg))
	case 3: // text string
		if indefinite {
			out := ""
			for {
				b, err := r.peekBreak()
				if err != nil {
					return nil, err
				}
				if b {
					return out, nil
				}
				chunk, err := r.value()
				if err != nil {
					return nil, err
				}
				part, ok := chunk.(string)
				if !ok {
					return nil, errCBOR
				}
				out += part
			}
		}
		if arg > uint64(len(r.buf)) {
			return nil, errCBOR
		}
		raw, err := r.take(int(arg))
		if err != nil {
			return nil, err
		}
		return string(raw), nil
	case 4: // array
		out := []any{}
		if indefinite {
			for {
				b, err := r.peekBreak()
				if err != nil {
					return nil, err
				}
				if b {
					return out, nil
				}
				item, err := r.value()
				if err != nil {
					return nil, err
				}
				out = append(out, item)
			}
		}
		if arg > uint64(len(r.buf)) {
			return nil, errCBOR
		}
		for i := uint64(0); i < arg; i++ {
			item, err := r.value()
			if err != nil {
				return nil, err
			}
			out = append(out, item)
		}
		return out, nil
	case 5: // map
		out := map[any]any{}
		if indefinite {
			for {
				b, err := r.peekBreak()
				if err != nil {
					return nil, err
				}
				if b {
					return out, nil
				}
				if err := r.pair(out); err != nil {
					return nil, err
				}
			}
		}
		if arg > uint64(len(r.buf)) {
			return nil, errCBOR
		}
		for i := uint64(0); i < arg; i++ {
			if err := r.pair(out); err != nil {
				return nil, err
			}
		}
		return out, nil
	case 6: // tagged value: DocMan ignores the tag itself
		return r.value()
	case 7:
		switch {
		case indefinite:
			return nil, errCBOR
		case arg == 20:
			return false, nil
		case arg == 21:
			return true, nil
		case arg == 22, arg == 23:
			return nil, nil
		case arg <= 19:
			return nil, nil
		}
		// Floats are encoded in the argument field; DocMan never needs them
		// but decoding must not desynchronise the reader.
		return float64(0), nil
	}
	return nil, errCBOR
}

func (r *cborReader) pair(into map[any]any) error {
	k, err := r.value()
	if err != nil {
		return err
	}
	v, err := r.value()
	if err != nil {
		return err
	}
	switch key := k.(type) {
	case string, int64, uint64:
		into[key] = v
	case []byte:
		into[string(key)] = v
	default:
		return errCBOR
	}
	return nil
}

// peekBreak consumes the 0xff break marker when present.
func (r *cborReader) peekBreak() (bool, error) {
	if r.pos >= len(r.buf) {
		return false, errCBOR
	}
	if r.buf[r.pos] == 0xff {
		r.pos++
		return true, nil
	}
	return false, nil
}

// cborDecode decodes one CBOR item and reports how many bytes it consumed.
func cborDecode(buf []byte) (any, int, error) {
	r := &cborReader{buf: buf}
	v, err := r.value()
	if err != nil {
		return nil, 0, err
	}
	return v, r.pos, nil
}

// cborMap decodes one CBOR item and requires it to be a map.
func cborMap(buf []byte) (map[any]any, error) {
	v, _, err := cborDecode(buf)
	if err != nil {
		return nil, err
	}
	m, ok := v.(map[any]any)
	if !ok {
		return nil, errCBOR
	}
	return m, nil
}

func cborInt(m map[any]any, key int64) (int64, bool) {
	v, ok := m[key]
	if !ok {
		return 0, false
	}
	switch n := v.(type) {
	case int64:
		return n, true
	case uint64:
		return int64(n), true
	}
	return 0, false
}

func cborBytes(m map[any]any, key int64) ([]byte, bool) {
	v, ok := m[key].([]byte)
	return v, ok
}
