// Package tlsserve decides how a server listener is secured: a loopback bind
// stays plain HTTP, and any other bind is only ever served over TLS, with an
// operator-supplied certificate from the environment or a cached self-signed
// one. The webhook server and the OpenAI-compatible API share these rules.
package tlsserve

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
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

// Config describes one listener's TLS needs.
type Config struct {
	// Addr is the bind address ("host:port").
	Addr string
	// CertEnv and KeyEnv name the environment variables that hold the paths
	// of an operator-supplied PEM certificate and key. Both or neither.
	CertEnv, KeyEnv string
	// CacheDir is the folder under ~/.monoagent where a generated
	// self-signed certificate is cached, for example "webhook-tls".
	CacheDir string
	// CommonName is the subject of a generated certificate.
	CommonName string
	// Label names the listener in messages, for example "webhook server".
	Label string
	// Warn receives the notice that a self-signed certificate is in use.
	// nil drops it.
	Warn func(msg string)
}

// Resolve returns the *tls.Config the listener must serve, or (nil, nil) for
// plain HTTP, which is only valid for a loopback-only bind.
//
// Order:
//  1. CertEnv and KeyEnv, when both are set: an operator-supplied pair. Setting
//     only one of them is an error, never a silent fallback.
//  2. A loopback bind: plain HTTP.
//  3. Otherwise a disk-cached self-signed certificate that covers
//     localhost, 127.0.0.1 and ::1 only. A remote client must skip
//     verification, so real deployments set option 1 or terminate TLS in a
//     proxy.
//
// A non-loopback bind never falls through to plain HTTP: if generating the
// self-signed certificate fails, Resolve returns an error.
func Resolve(c Config) (*tls.Config, error) {
	certPath, keyPath := os.Getenv(c.CertEnv), os.Getenv(c.KeyEnv)
	if certPath != "" || keyPath != "" {
		if certPath == "" || keyPath == "" {
			return nil, fmt.Errorf("%s: %s and %s must both be set to use an explicit TLS certificate", c.Label, c.CertEnv, c.KeyEnv)
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("%s: loading TLS cert/key from %s/%s: %w", c.Label, certPath, keyPath, err)
		}
		return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
	}

	if IsLoopbackAddr(c.Addr) {
		return nil, nil
	}

	cert, err := LoadOrGenerateSelfSigned(c.CacheDir, c.CommonName)
	if err != nil {
		return nil, fmt.Errorf("%s: bound to non-loopback address %q with no TLS configured, and self-signed certificate generation failed: %w (set %s/%s to use a real certificate instead)", c.Label, c.Addr, err, c.CertEnv, c.KeyEnv)
	}
	if c.Warn != nil {
		c.Warn(fmt.Sprintf("%s bound to a non-loopback address with no explicit TLS cert configured — using an auto-generated self-signed certificate; set %s/%s for a real certificate", c.Label, c.CertEnv, c.KeyEnv))
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

// IsLoopbackAddr reports whether addr (a "host:port" bind address) resolves
// to loopback only. An address with no host (":9322" binds every interface)
// is NOT loopback.
func IsLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// selfSignedValidity is deliberately under two years: long enough that
// local and LAN use won't hit renewal often, short enough to stay inside
// browser and OS maximum leaf-certificate lifetimes.
const selfSignedValidity = 397 * 24 * time.Hour

// LoadOrGenerateSelfSigned returns the self-signed certificate cached in
// ~/.monoagent/<cacheDir>/, generating and caching one (key file mode 0600)
// on first use. A cached certificate that has expired is regenerated.
func LoadOrGenerateSelfSigned(cacheDir, commonName string) (tls.Certificate, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("resolving home directory: %w", err)
	}
	dir := filepath.Join(home, ".monoagent", cacheDir)
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if cert, loadErr := tls.LoadX509KeyPair(certPath, keyPath); loadErr == nil {
		if leaf, parseErr := x509.ParseCertificate(cert.Certificate[0]); parseErr == nil && time.Now().Before(leaf.NotAfter) {
			// A key someone loosened (a restored backup, a copy) is tightened again.
			_ = os.Chmod(keyPath, 0o600)
			_ = os.Chmod(dir, 0o700)
			return cert, nil
		}
	}

	cert, certPEM, keyPEM, err := GenerateSelfSigned(commonName)
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return tls.Certificate{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	_ = os.Chmod(dir, 0o700) // MkdirAll leaves an existing folder's mode alone
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return tls.Certificate{}, fmt.Errorf("writing %s: %w", keyPath, err)
	}
	_ = os.Chmod(keyPath, 0o600) // WriteFile leaves an existing file's mode alone
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return tls.Certificate{}, fmt.Errorf("writing %s: %w", certPath, err)
	}
	return cert, nil
}

// GenerateSelfSigned creates a fresh ECDSA P-256 self-signed certificate
// covering localhost, 127.0.0.1 and ::1 only.
func GenerateSelfSigned(commonName string) (cert tls.Certificate, certPEM, keyPEM []byte, err error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("generating TLS key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("generating TLS certificate serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: commonName},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(selfSignedValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	derBytes, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("creating TLS certificate: %w", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("marshaling TLS key: %w", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	cert, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, nil, nil, fmt.Errorf("loading generated TLS certificate: %w", err)
	}
	return cert, certPEM, keyPEM, nil
}
