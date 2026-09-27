package srv

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// EnsureTLS returns paths to a certificate and key, generating a self-signed
// pair in dataDir on first run.
//
// DocMan defaults to HTTPS because passkeys only work in a secure browser
// context. Browsers will warn about the self-signed certificate once; putting
// DocMan behind a reverse proxy with a real certificate, or supplying your own
// via DOCMAN_TLS_CERT and DOCMAN_TLS_KEY, avoids that.
func EnsureTLS(dataDir string, hostnames []string) (certPath, keyPath string, generated bool, err error) {
	certPath = filepath.Join(dataDir, "tls-cert.pem")
	keyPath = filepath.Join(dataDir, "tls-key.pem")

	if fileExists(certPath) && fileExists(keyPath) {
		if ok, _ := certStillValid(certPath); ok {
			return certPath, keyPath, false, nil
		}
	}
	if err := generateSelfSigned(certPath, keyPath, hostnames); err != nil {
		return "", "", false, err
	}
	return certPath, keyPath, true, nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir() && st.Size() > 0
}

func certStillValid(certPath string) (bool, error) {
	raw, err := os.ReadFile(certPath)
	if err != nil {
		return false, err
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return false, fmt.Errorf("%s is not a PEM certificate", certPath)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return false, err
	}
	// Renew a month before expiry so a long-lived install never serves an
	// expired certificate.
	return time.Now().Before(cert.NotAfter.Add(-30 * 24 * time.Hour)), nil
}

func generateSelfSigned(certPath, keyPath string, hostnames []string) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return err
	}

	dnsNames := map[string]bool{"localhost": true}
	ips := map[string]net.IP{
		"127.0.0.1": net.ParseIP("127.0.0.1"),
		"::1":       net.ParseIP("::1"),
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		dnsNames[h] = true
	}
	for _, h := range hostnames {
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			ips[h] = ip
			continue
		}
		dnsNames[h] = true
	}
	// The addresses DocMan itself answers on, so browsing by IP still works.
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
				ips[ipnet.IP.String()] = ipnet.IP
			}
		}
	}

	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "DocMan",
			Organization: []string{"DocMan"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(5, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for name := range dnsNames {
		tmpl.DNSNames = append(tmpl.DNSNames, name)
	}
	for _, ip := range ips {
		if ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		}
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return os.WriteFile(keyPath, keyPEM, 0o600)
}
