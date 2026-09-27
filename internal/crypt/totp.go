package crypt

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// RFC 6238 time-based one-time passwords, in the shape every authenticator app
// expects: HMAC-SHA1, six digits, a thirty second step.
const (
	totpDigits = 6
	totpPeriod = 30
	// TOTPSkew is how many steps either side of now are accepted, which covers
	// a phone clock that is up to half a minute out.
	TOTPSkew = 1
)

var totpB32 = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewTOTPSecret returns a fresh 160-bit secret, base32 encoded.
func NewTOTPSecret() string {
	return totpB32.EncodeToString(RandBytes(20))
}

// FormatTOTPSecret groups a secret into blocks of four so it can be typed by
// hand without losing your place.
func FormatTOTPSecret(secret string) string {
	var b strings.Builder
	for i, r := range secret {
		if i > 0 && i%4 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func decodeTOTPSecret(secret string) ([]byte, error) {
	cleaned := strings.ToUpper(strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(strings.TrimSpace(secret)))
	if cleaned == "" {
		return nil, errors.New("no authenticator secret is set")
	}
	key, err := totpB32.DecodeString(cleaned)
	if err != nil {
		return nil, errors.New("the authenticator secret is not valid base32")
	}
	if len(key) < 10 {
		return nil, errors.New("the authenticator secret is too short")
	}
	return key, nil
}

// TOTPStep is the counter value for a point in time.
func TOTPStep(at time.Time) int64 { return at.Unix() / totpPeriod }

// TOTPCode returns the six digit code for a secret at a given step.
func TOTPCode(secret string, step int64) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))

	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)

	// Dynamic truncation, RFC 4226 section 5.3.
	offset := sum[len(sum)-1] & 0x0f
	value := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, value%1000000), nil
}

// NormalizeTOTPCode strips the spaces authenticator apps like to show.
func NormalizeTOTPCode(code string) string {
	return strings.NewReplacer(" ", "", "-", "", "\t", "").Replace(strings.TrimSpace(code))
}

// VerifyTOTP checks a code against a secret around the current time.
//
// lastStep is the step a previously accepted code came from; a code from that
// step or earlier is refused so an intercepted code cannot be replayed inside
// its validity window. The matched step is returned so the caller can store it.
func VerifyTOTP(secret, code string, at time.Time, lastStep int64) (int64, error) {
	cleaned := NormalizeTOTPCode(code)
	if len(cleaned) != totpDigits {
		return 0, fmt.Errorf("the code must be %d digits", totpDigits)
	}
	for _, r := range cleaned {
		if r < '0' || r > '9' {
			return 0, errors.New("the code must be digits only")
		}
	}
	now := TOTPStep(at)
	for step := now - TOTPSkew; step <= now+TOTPSkew; step++ {
		want, err := TOTPCode(secret, step)
		if err != nil {
			return 0, err
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(cleaned)) == 1 {
			if step <= lastStep {
				return 0, errors.New("that code has already been used; wait for the next one")
			}
			return step, nil
		}
	}
	return 0, errors.New("that code is not right, or your device clock has drifted")
}

// TOTPSecondsRemaining is how long the current code stays valid, for the UI.
func TOTPSecondsRemaining(at time.Time) int {
	return totpPeriod - int(at.Unix()%totpPeriod)
}

// TOTPURI builds the otpauth:// URI that authenticator apps enrol from, either
// by scanning it as a QR code or by following it as a link.
func TOTPURI(secret, issuer, account string) string {
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprintf("%d", totpDigits))
	query.Set("period", fmt.Sprintf("%d", totpPeriod))
	return "otpauth://totp/" + label + "?" + query.Encode()
}
