// Package wa implements the slice of WebAuthn level 2 that DocMan needs:
// building credential creation and request options, and verifying the
// registration and assertion responses a browser sends back.
//
// Attestation is requested as "none" and not verified. DocMan cares that a
// credential is bound to its origin and that later assertions are signed by the
// same key, not which vendor made the authenticator.
package wa

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// Flag bits in the authenticator data flags byte.
const (
	flagUserPresent    = 0x01
	flagUserVerified   = 0x04
	flagBackupEligible = 0x08
	flagBackedUp       = 0x10
	flagAttestedData   = 0x40
	flagExtensionData  = 0x80
)

// RP identifies this relying party.
type RP struct {
	// ID is the relying party id: the site's registrable domain, e.g. "docman.lan".
	// It must be a domain name; bare IP addresses are not valid RP IDs.
	ID string
	// Name is shown to the user by the platform authenticator UI.
	Name string
	// Origins lists the exact origins allowed to present credentials.
	Origins []string
}

// CredentialDescriptor names a credential to the browser. ID is base64url.
type CredentialDescriptor struct {
	Type       string   `json:"type"`
	ID         string   `json:"id"`
	Transports []string `json:"transports,omitempty"`
}

type credParam struct {
	Type string `json:"type"`
	Alg  int    `json:"alg"`
}

type rpEntity struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type userEntity struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}

type authenticatorSelection struct {
	ResidentKey        string `json:"residentKey"`
	RequireResidentKey bool   `json:"requireResidentKey"`
	UserVerification   string `json:"userVerification"`
}

// CreationOptions is the PublicKeyCredentialCreationOptions payload.
type CreationOptions struct {
	RP                     rpEntity               `json:"rp"`
	User                   userEntity             `json:"user"`
	Challenge              string                 `json:"challenge"`
	PubKeyCredParams       []credParam            `json:"pubKeyCredParams"`
	Timeout                int                    `json:"timeout"`
	Attestation            string                 `json:"attestation"`
	AuthenticatorSelection authenticatorSelection `json:"authenticatorSelection"`
	ExcludeCredentials     []CredentialDescriptor `json:"excludeCredentials"`
	Extensions             map[string]any         `json:"extensions,omitempty"`
}

// RequestOptions is the PublicKeyCredentialRequestOptions payload.
type RequestOptions struct {
	Challenge        string                 `json:"challenge"`
	Timeout          int                    `json:"timeout"`
	RPID             string                 `json:"rpId"`
	AllowCredentials []CredentialDescriptor `json:"allowCredentials"`
	UserVerification string                 `json:"userVerification"`
}

// Algorithms offered at registration, most preferred first.
var supportedAlgs = []credParam{
	{Type: "public-key", Alg: -7},   // ES256
	{Type: "public-key", Alg: -8},   // Ed25519
	{Type: "public-key", Alg: -257}, // RS256
	{Type: "public-key", Alg: -35},  // ES384
	{Type: "public-key", Alg: -36},  // ES512
	{Type: "public-key", Alg: -37},  // PS256
}

// NewCreationOptions builds registration options. Resident (discoverable)
// credentials are required so that sign-in works without typing a username.
func NewCreationOptions(rp RP, challenge, userID []byte, username, display string, exclude []CredentialDescriptor) *CreationOptions {
	if display == "" {
		display = username
	}
	if exclude == nil {
		exclude = []CredentialDescriptor{}
	}
	return &CreationOptions{
		RP:               rpEntity{ID: rp.ID, Name: rp.Name},
		User:             userEntity{ID: B64(userID), Name: username, DisplayName: display},
		Challenge:        B64(challenge),
		PubKeyCredParams: supportedAlgs,
		Timeout:          120000,
		Attestation:      "none",
		AuthenticatorSelection: authenticatorSelection{
			ResidentKey:        "required",
			RequireResidentKey: true,
			UserVerification:   "preferred",
		},
		ExcludeCredentials: exclude,
		Extensions:         map[string]any{"credProps": true},
	}
}

// NewRequestOptions builds sign-in options. An empty allow list lets the
// platform offer any discoverable DocMan credential it holds.
func NewRequestOptions(rp RP, challenge []byte, allow []CredentialDescriptor) *RequestOptions {
	if allow == nil {
		allow = []CredentialDescriptor{}
	}
	return &RequestOptions{
		Challenge:        B64(challenge),
		Timeout:          120000,
		RPID:             rp.ID,
		AllowCredentials: allow,
		UserVerification: "preferred",
	}
}

// ---------- browser responses ----------

// RegistrationResponse is navigator.credentials.create() serialised by the UI.
type RegistrationResponse struct {
	ID       string `json:"id"`
	RawID    string `json:"rawId"`
	Type     string `json:"type"`
	Response struct {
		ClientDataJSON    string   `json:"clientDataJSON"`
		AttestationObject string   `json:"attestationObject"`
		Transports        []string `json:"transports"`
	} `json:"response"`
	AuthenticatorAttachment string `json:"authenticatorAttachment"`
}

// AssertionResponse is navigator.credentials.get() serialised by the UI.
type AssertionResponse struct {
	ID       string `json:"id"`
	RawID    string `json:"rawId"`
	Type     string `json:"type"`
	Response struct {
		ClientDataJSON    string `json:"clientDataJSON"`
		AuthenticatorData string `json:"authenticatorData"`
		Signature         string `json:"signature"`
		UserHandle        string `json:"userHandle"`
	} `json:"response"`
}

// RegistrationResult is what to persist after a successful registration.
type RegistrationResult struct {
	CredentialID []byte
	PublicKey    []byte // raw COSE_Key
	AAGUID       []byte
	SignCount    uint32
	UserVerified bool
	BackedUp     bool
	Transports   []string
	Attachment   string
}

// AssertionResult is the outcome of a verified sign-in.
type AssertionResult struct {
	CredentialID []byte
	SignCount    uint32
	UserVerified bool
	BackedUp     bool
	UserHandle   []byte
}

// AuthData is parsed authenticator data.
type AuthData struct {
	RPIDHash     []byte
	Flags        byte
	SignCount    uint32
	AAGUID       []byte
	CredentialID []byte
	COSEKey      []byte
}

// B64 encodes bytes the way the WebAuthn JSON mapping expects.
func B64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

// UnB64 decodes base64url, tolerating padding and the standard alphabet.
func UnB64(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	s = strings.NewReplacer("+", "-", "/", "_").Replace(s)
	return base64.RawURLEncoding.DecodeString(s)
}

type clientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

func (rp RP) checkOrigin(origin string) error {
	for _, allowed := range rp.Origins {
		if strings.EqualFold(strings.TrimSuffix(allowed, "/"), strings.TrimSuffix(origin, "/")) {
			return nil
		}
	}
	return fmt.Errorf("webauthn: origin %q is not allowed for this relying party", origin)
}

func (rp RP) checkClientData(raw []byte, wantType string, challenge []byte) error {
	var cd clientData
	if err := json.Unmarshal(raw, &cd); err != nil {
		return errors.New("webauthn: unreadable clientDataJSON")
	}
	if cd.Type != wantType {
		return fmt.Errorf("webauthn: expected ceremony %q, got %q", wantType, cd.Type)
	}
	got, err := UnB64(cd.Challenge)
	if err != nil {
		return errors.New("webauthn: unreadable challenge")
	}
	if subtle.ConstantTimeCompare(got, challenge) != 1 {
		return errors.New("webauthn: challenge mismatch")
	}
	return rp.checkOrigin(cd.Origin)
}

// ParseAuthData parses raw authenticator data, including attested credential
// data when the AT flag is set.
func ParseAuthData(raw []byte) (*AuthData, error) {
	if len(raw) < 37 {
		return nil, errors.New("webauthn: authenticator data too short")
	}
	ad := &AuthData{
		RPIDHash:  raw[0:32],
		Flags:     raw[32],
		SignCount: binary.BigEndian.Uint32(raw[33:37]),
	}
	rest := raw[37:]
	if ad.Flags&flagAttestedData != 0 {
		if len(rest) < 18 {
			return nil, errors.New("webauthn: truncated attested credential data")
		}
		ad.AAGUID = rest[0:16]
		idLen := int(binary.BigEndian.Uint16(rest[16:18]))
		rest = rest[18:]
		if idLen <= 0 || idLen > 1023 || len(rest) < idLen {
			return nil, errors.New("webauthn: bad credential id length")
		}
		ad.CredentialID = rest[:idLen]
		rest = rest[idLen:]
		_, n, err := cborDecode(rest)
		if err != nil {
			return nil, errors.New("webauthn: bad COSE public key")
		}
		ad.COSEKey = rest[:n]
	}
	return ad, nil
}

// VerifyRegistration checks a create() response against the expected challenge.
func VerifyRegistration(rp RP, challenge []byte, resp *RegistrationResponse) (*RegistrationResult, error) {
	if resp == nil || resp.Response.ClientDataJSON == "" || resp.Response.AttestationObject == "" {
		return nil, errors.New("webauthn: incomplete registration response")
	}
	clientDataRaw, err := UnB64(resp.Response.ClientDataJSON)
	if err != nil {
		return nil, errors.New("webauthn: unreadable clientDataJSON")
	}
	if err := rp.checkClientData(clientDataRaw, "webauthn.create", challenge); err != nil {
		return nil, err
	}
	attRaw, err := UnB64(resp.Response.AttestationObject)
	if err != nil {
		return nil, errors.New("webauthn: unreadable attestationObject")
	}
	att, err := cborMap(attRaw)
	if err != nil {
		return nil, errors.New("webauthn: unreadable attestation statement")
	}
	authDataRaw, ok := att["authData"].([]byte)
	if !ok {
		return nil, errors.New("webauthn: attestation object has no authData")
	}
	ad, err := ParseAuthData(authDataRaw)
	if err != nil {
		return nil, err
	}
	if err := rp.checkRPIDHash(ad); err != nil {
		return nil, err
	}
	if ad.Flags&flagUserPresent == 0 {
		return nil, errors.New("webauthn: user presence was not confirmed")
	}
	if len(ad.CredentialID) == 0 || len(ad.COSEKey) == 0 {
		return nil, errors.New("webauthn: authenticator returned no credential")
	}
	if _, err := parseCOSE(ad.COSEKey); err != nil {
		return nil, err
	}
	rawID, err := UnB64(resp.RawID)
	if err != nil || subtle.ConstantTimeCompare(rawID, ad.CredentialID) != 1 {
		// Trust the authenticator data, but a mismatch means the client is broken.
		return nil, errors.New("webauthn: credential id mismatch")
	}
	return &RegistrationResult{
		CredentialID: append([]byte(nil), ad.CredentialID...),
		PublicKey:    append([]byte(nil), ad.COSEKey...),
		AAGUID:       append([]byte(nil), ad.AAGUID...),
		SignCount:    ad.SignCount,
		UserVerified: ad.Flags&flagUserVerified != 0,
		BackedUp:     ad.Flags&flagBackedUp != 0,
		Transports:   resp.Response.Transports,
		Attachment:   resp.AuthenticatorAttachment,
	}, nil
}

// VerifyAssertion checks a get() response against the stored credential.
func VerifyAssertion(rp RP, challenge, coseKey []byte, storedSignCount uint32, resp *AssertionResponse) (*AssertionResult, error) {
	if resp == nil || resp.Response.ClientDataJSON == "" || resp.Response.AuthenticatorData == "" || resp.Response.Signature == "" {
		return nil, errors.New("webauthn: incomplete assertion response")
	}
	clientDataRaw, err := UnB64(resp.Response.ClientDataJSON)
	if err != nil {
		return nil, errors.New("webauthn: unreadable clientDataJSON")
	}
	if err := rp.checkClientData(clientDataRaw, "webauthn.get", challenge); err != nil {
		return nil, err
	}
	authDataRaw, err := UnB64(resp.Response.AuthenticatorData)
	if err != nil {
		return nil, errors.New("webauthn: unreadable authenticatorData")
	}
	ad, err := ParseAuthData(authDataRaw)
	if err != nil {
		return nil, err
	}
	if err := rp.checkRPIDHash(ad); err != nil {
		return nil, err
	}
	if ad.Flags&flagUserPresent == 0 {
		return nil, errors.New("webauthn: user presence was not confirmed")
	}
	sig, err := UnB64(resp.Response.Signature)
	if err != nil {
		return nil, errors.New("webauthn: unreadable signature")
	}
	clientHash := sha256.Sum256(clientDataRaw)
	signed := make([]byte, 0, len(authDataRaw)+len(clientHash))
	signed = append(signed, authDataRaw...)
	signed = append(signed, clientHash[:]...)
	if err := verifySignature(coseKey, signed, sig); err != nil {
		return nil, err
	}
	// A rolling counter of zero means the authenticator does not keep one,
	// which is normal for synced passkeys.
	if ad.SignCount != 0 && storedSignCount != 0 && ad.SignCount <= storedSignCount {
		return nil, errors.New("webauthn: signature counter went backwards; credential may be cloned")
	}
	credID, err := UnB64(resp.RawID)
	if err != nil {
		return nil, errors.New("webauthn: unreadable credential id")
	}
	var handle []byte
	if resp.Response.UserHandle != "" {
		handle, _ = UnB64(resp.Response.UserHandle)
	}
	return &AssertionResult{
		CredentialID: credID,
		SignCount:    ad.SignCount,
		UserVerified: ad.Flags&flagUserVerified != 0,
		BackedUp:     ad.Flags&flagBackedUp != 0,
		UserHandle:   handle,
	}, nil
}

func (rp RP) checkRPIDHash(ad *AuthData) error {
	want := sha256.Sum256([]byte(rp.ID))
	if subtle.ConstantTimeCompare(want[:], ad.RPIDHash) != 1 {
		return fmt.Errorf("webauthn: credential is not bound to relying party %q", rp.ID)
	}
	return nil
}

// ---------- COSE keys ----------

type coseKey struct {
	kty int64
	alg int64
	crv int64
	x   []byte
	y   []byte
	n   []byte
	e   []byte
}

func parseCOSE(raw []byte) (*coseKey, error) {
	m, err := cborMap(raw)
	if err != nil {
		return nil, errors.New("webauthn: unreadable COSE key")
	}
	k := &coseKey{}
	var ok bool
	if k.kty, ok = cborInt(m, 1); !ok {
		return nil, errors.New("webauthn: COSE key has no type")
	}
	if k.alg, ok = cborInt(m, 3); !ok {
		return nil, errors.New("webauthn: COSE key has no algorithm")
	}
	switch k.kty {
	case 2: // EC2
		k.crv, _ = cborInt(m, -1)
		if k.x, ok = cborBytes(m, -2); !ok {
			return nil, errors.New("webauthn: EC2 key missing x")
		}
		if k.y, ok = cborBytes(m, -3); !ok {
			return nil, errors.New("webauthn: EC2 key missing y")
		}
	case 3: // RSA
		if k.n, ok = cborBytes(m, -1); !ok {
			return nil, errors.New("webauthn: RSA key missing modulus")
		}
		if k.e, ok = cborBytes(m, -2); !ok {
			return nil, errors.New("webauthn: RSA key missing exponent")
		}
	case 1: // OKP (Ed25519)
		k.crv, _ = cborInt(m, -1)
		if k.x, ok = cborBytes(m, -2); !ok {
			return nil, errors.New("webauthn: OKP key missing x")
		}
	default:
		return nil, fmt.Errorf("webauthn: unsupported key type %d", k.kty)
	}
	return k, nil
}

func digestFor(alg int64, msg []byte) ([]byte, crypto.Hash, error) {
	switch alg {
	case -7, -257, -37:
		sum := sha256.Sum256(msg)
		return sum[:], crypto.SHA256, nil
	case -35, -258:
		sum := sha512.Sum384(msg)
		return sum[:], crypto.SHA384, nil
	case -36, -259:
		sum := sha512.Sum512(msg)
		return sum[:], crypto.SHA512, nil
	}
	return nil, 0, fmt.Errorf("webauthn: unsupported algorithm %d", alg)
}

func verifySignature(coseRaw, msg, sig []byte) error {
	k, err := parseCOSE(coseRaw)
	if err != nil {
		return err
	}
	switch k.kty {
	case 2:
		var curve elliptic.Curve
		switch k.crv {
		case 1:
			curve = elliptic.P256()
		case 2:
			curve = elliptic.P384()
		case 3:
			curve = elliptic.P521()
		default:
			return fmt.Errorf("webauthn: unsupported curve %d", k.crv)
		}
		pub := &ecdsa.PublicKey{
			Curve: curve,
			X:     new(big.Int).SetBytes(k.x),
			Y:     new(big.Int).SetBytes(k.y),
		}
		if !pub.Curve.IsOnCurve(pub.X, pub.Y) {
			return errors.New("webauthn: public key point is not on the curve")
		}
		digest, _, err := digestFor(k.alg, msg)
		if err != nil {
			return err
		}
		// WebAuthn ECDSA signatures are ASN.1 DER encoded.
		if !ecdsa.VerifyASN1(pub, digest, sig) {
			return errors.New("webauthn: signature verification failed")
		}
		return nil
	case 3:
		pub := &rsa.PublicKey{
			N: new(big.Int).SetBytes(k.n),
			E: int(new(big.Int).SetBytes(k.e).Int64()),
		}
		if pub.N.Sign() <= 0 || pub.E < 3 {
			return errors.New("webauthn: malformed RSA key")
		}
		digest, hash, err := digestFor(k.alg, msg)
		if err != nil {
			return err
		}
		if k.alg == -37 || k.alg == -38 || k.alg == -39 {
			if err := rsa.VerifyPSS(pub, hash, digest, sig, nil); err != nil {
				return errors.New("webauthn: signature verification failed")
			}
			return nil
		}
		if err := rsa.VerifyPKCS1v15(pub, hash, digest, sig); err != nil {
			return errors.New("webauthn: signature verification failed")
		}
		return nil
	case 1:
		if k.crv != 6 || len(k.x) != ed25519.PublicKeySize {
			return errors.New("webauthn: unsupported OKP key")
		}
		if !ed25519.Verify(ed25519.PublicKey(k.x), msg, sig) {
			return errors.New("webauthn: signature verification failed")
		}
		return nil
	}
	return errors.New("webauthn: unsupported key type")
}

// Describe returns a short human label for a stored COSE key, for the UI.
func Describe(coseRaw []byte) string {
	k, err := parseCOSE(coseRaw)
	if err != nil {
		return "unknown key"
	}
	switch k.alg {
	case -7:
		return "ES256"
	case -8:
		return "Ed25519"
	case -257:
		return "RS256"
	case -35:
		return "ES384"
	case -36:
		return "ES512"
	case -37:
		return "PS256"
	}
	return fmt.Sprintf("alg %d", k.alg)
}

// BackupEligible reports whether authenticator data marked the credential as
// eligible for multi-device backup, i.e. a synced passkey.
func BackupEligible(flags byte) bool { return flags&flagBackupEligible != 0 }

// HasExtensions reports whether authenticator data carried extension output.
func HasExtensions(flags byte) bool { return flags&flagExtensionData != 0 }
