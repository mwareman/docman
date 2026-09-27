// Package crypt provides the small set of cryptographic helpers DocMan needs:
// password hashing (PBKDF2-HMAC-SHA256), random identifiers and API tokens.
// It intentionally depends only on the Go standard library.
package crypt

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// DefaultIterations follows current OWASP guidance for PBKDF2-HMAC-SHA256.
const DefaultIterations = 600000

var b64 = base64.RawStdEncoding

// RandBytes returns n cryptographically secure random bytes.
func RandBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypt: entropy source failed: " + err.Error())
	}
	return b
}

// RandHex returns a hex string carrying n bytes of entropy.
func RandHex(n int) string { return hex.EncodeToString(RandBytes(n)) }

// RandURL returns a URL-safe random string carrying n bytes of entropy.
func RandURL(n int) string { return base64.RawURLEncoding.EncodeToString(RandBytes(n)) }

// SetupKey returns the human-transcribable first-run key, formatted in groups
// of five so it is easy to read off a container log and type into the browser.
func SetupKey() string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no I/O/0/1
	raw := RandBytes(20)
	out := make([]byte, 0, 24)
	for i, b := range raw {
		if i > 0 && i%5 == 0 {
			out = append(out, '-')
		}
		out = append(out, alphabet[int(b)%len(alphabet)])
	}
	return string(out)
}

// NormalizeSetupKey makes key comparison forgiving about case and separators.
func NormalizeSetupKey(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.NewReplacer("-", "", " ", "", "\t", "").Replace(s)
}

// SHA256Hex returns the hex-encoded SHA-256 of s. Used for opaque token lookup.
func SHA256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// ConstantTimeEqualString compares two strings without leaking length-independent timing.
func ConstantTimeEqualString(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// HashPassword returns an encoded PBKDF2 hash: pbkdf2-sha256$iter$salt$key.
func HashPassword(password string) string {
	salt := RandBytes(16)
	dk := pbkdf2SHA256([]byte(password), salt, DefaultIterations, 32)
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", DefaultIterations, b64.EncodeToString(salt), b64.EncodeToString(dk))
}

// VerifyPassword checks password against an encoded hash produced by HashPassword.
func VerifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 1000 || iter > 20000000 {
		return false
	}
	salt, err := b64.DecodeString(parts[2])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[3])
	if err != nil || len(want) == 0 {
		return false
	}
	got := pbkdf2SHA256([]byte(password), salt, iter, len(want))
	return subtle.ConstantTimeCompare(got, want) == 1
}

var (
	dummyOnce sync.Once
	dummyHash string
)

// SpendVerifyWork does the same work as VerifyPassword against a throwaway
// hash. Call it when the username was not found, so how long a sign-in attempt
// takes does not reveal whether the account exists.
func SpendVerifyWork(password string) {
	dummyOnce.Do(func() {
		dummyHash = HashPassword("this-hash-matches-nothing-" + RandHex(16))
	})
	_ = VerifyPassword(dummyHash, password)
}

// APIToken returns a new bearer token and its lookup prefix.
// Format: dm_<8 char prefix><32 char secret>.
func APIToken() (token, prefix string) {
	prefix = strings.ToLower(hex.EncodeToString(RandBytes(4)))
	secret := base64.RawURLEncoding.EncodeToString(RandBytes(24))
	return "dm_" + prefix + "." + secret, prefix
}

// TokenPrefix extracts the lookup prefix from a presented bearer token.
func TokenPrefix(token string) (string, error) {
	if !strings.HasPrefix(token, "dm_") {
		return "", errors.New("malformed token")
	}
	rest := token[3:]
	i := strings.IndexByte(rest, '.')
	if i <= 0 {
		return "", errors.New("malformed token")
	}
	return rest[:i], nil
}

func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	prf := hmac.New(sha256.New, password)
	hashLen := prf.Size()
	numBlocks := (keyLen + hashLen - 1) / hashLen

	var buf [4]byte
	dk := make([]byte, 0, numBlocks*hashLen)
	u := make([]byte, hashLen)
	for block := 1; block <= numBlocks; block++ {
		prf.Reset()
		prf.Write(salt)
		buf[0] = byte(block >> 24)
		buf[1] = byte(block >> 16)
		buf[2] = byte(block >> 8)
		buf[3] = byte(block)
		prf.Write(buf[:4])
		dk = prf.Sum(dk)
		t := dk[len(dk)-hashLen:]
		copy(u, t)
		for n := 2; n <= iter; n++ {
			prf.Reset()
			prf.Write(u)
			u = u[:0]
			u = prf.Sum(u)
			for x := range u {
				t[x] ^= u[x]
			}
		}
	}
	return dk[:keyLen]
}
