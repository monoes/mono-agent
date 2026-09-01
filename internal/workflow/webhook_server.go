package workflow

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/rs/zerolog"
)

// webhookTLSCertEnv/webhookTLSKeyEnv let an operator supply a real
// certificate/key pair for the webhook listener instead of the
// auto-generated self-signed one — see resolveTLSConfig.
const (
	webhookTLSCertEnv = "MONOAGENT_WEBHOOK_TLS_CERT"
	webhookTLSKeyEnv  = "MONOAGENT_WEBHOOK_TLS_KEY"
)

// WebhookRegistration holds the config for a registered webhook.
type WebhookRegistration struct {
	WorkflowID string
	NodeID     string
	Path       string             // unique URL path segment, e.g. UUID
	Method     string             // "GET", "POST", "ANY"
	HMACSecret string             // if non-empty, validate X-Hub-Signature-256 header
	AuthHeader string             // if non-empty, validate this header equals AuthToken
	AuthToken  string             // secret value expected in AuthHeader
	TriggerFn  func(items []Item) // called when the webhook fires
}

// WebhookServer is a standalone net/http server on a configurable port.
type WebhookServer struct {
	addr string
	// allowedOrigins is the CORS allowlist parsed once from
	// MONOAGENT_WEBHOOK_ALLOWED_ORIGINS. nil (unset/empty env) means NO CORS
	// headers are ever emitted — cross-origin browser callers are locked out,
	// which is correct for loopback/server-to-server webhook use.
	allowedOrigins map[string]struct{}
	mu             sync.RWMutex
	routes         map[string]*WebhookRegistration // path → registration
	server         *http.Server
	logger         zerolog.Logger
}

// NewWebhookServer creates a server that will listen on addr (e.g. ":9321").
func NewWebhookServer(addr string, logger zerolog.Logger) *WebhookServer {
	s := &WebhookServer{
		addr:           addr,
		allowedOrigins: parseWebhookAllowedOrigins(os.Getenv("MONOAGENT_WEBHOOK_ALLOWED_ORIGINS")),
		routes:         make(map[string]*WebhookRegistration),
		logger:         logger,
	}
	s.server = &http.Server{
		Addr:         addr,
		Handler:      s,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	return s
}

// Start starts the HTTP server in a goroutine. Returns immediately.
//
// A loopback bind (the default, "127.0.0.1:9321") serves plain HTTP with no
// extra ceremony — same-user trust, mirroring the Chrome extension bridge.
// A non-loopback bind is only ever served over TLS: see resolveTLSConfig
// for the cert resolution order. Webhook payloads can carry caller-attached
// auth headers/tokens, so a non-loopback bind must never be served in the
// clear.
func (s *WebhookServer) Start() error {
	ln, err := net.Listen("tcp", s.addr)
	if err != nil {
		return fmt.Errorf("webhook server: listen on %s: %w", s.addr, err)
	}

	tlsConfig, err := s.resolveTLSConfig()
	if err != nil {
		ln.Close()
		return err
	}

	scheme := "http"
	if tlsConfig != nil {
		ln = tls.NewListener(ln, tlsConfig)
		scheme = "https"
	}

	s.logger.Info().Str("addr", s.addr).Str("scheme", scheme).Msg("webhook server starting")
	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			s.logger.Error().Err(err).Msg("webhook server error")
		}
	}()
	return nil
}

// resolveTLSConfig decides whether the webhook listener should serve TLS
// and, if so, builds the *tls.Config to use. Returns (nil, nil) for plain
// HTTP, which is only valid for a loopback-only bind (see isLoopbackAddr).
//
// Resolution order for a non-loopback bind:
//  1. MONOAGENT_WEBHOOK_TLS_CERT + MONOAGENT_WEBHOOK_TLS_KEY, if both set —
//     an operator-supplied cert/key pair (a real certificate for production
//     or LAN use).
//  2. An auto-generated, disk-cached self-signed certificate (see
//     loadOrGenerateSelfSignedCert) — covers localhost/127.0.0.1/::1 only;
//     clients connecting by a LAN hostname or IP must skip verification or
//     the operator should supply a real cert via option 1.
//
// A non-loopback bind never falls through to plain HTTP: if self-signed
// generation itself fails, Start returns an error instead of silently
// serving webhook payloads (which can carry caller-attached auth
// headers/tokens) unencrypted to the network.
func (s *WebhookServer) resolveTLSConfig() (*tls.Config, error) {
	certPath := os.Getenv(webhookTLSCertEnv)
	keyPath := os.Getenv(webhookTLSKeyEnv)
	if certPath != "" || keyPath != "" {
		if certPath == "" || keyPath == "" {
			return nil, fmt.Errorf("webhook server: %s and %s must both be set to use an explicit TLS certificate", webhookTLSCertEnv, webhookTLSKeyEnv)
		}
		cert, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			return nil, fmt.Errorf("webhook server: loading TLS cert/key from %s/%s: %w", certPath, keyPath, err)
		}
		return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
	}

	if isLoopbackAddr(s.addr) {
		return nil, nil
	}

	cert, err := loadOrGenerateSelfSignedCert()
	if err != nil {
		return nil, fmt.Errorf("webhook server: bound to non-loopback address %q with no TLS configured, and self-signed certificate generation failed: %w (set %s/%s to use a real certificate instead)", s.addr, err, webhookTLSCertEnv, webhookTLSKeyEnv)
	}
	s.logger.Warn().Str("addr", s.addr).
		Msg("webhook server bound to a non-loopback address with no explicit TLS cert configured — using an auto-generated self-signed certificate; set " + webhookTLSCertEnv + "/" + webhookTLSKeyEnv + " for a real certificate")
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

// isLoopbackAddr reports whether addr (a "host:port" bind address) resolves
// to loopback only. An address with no host (e.g. ":9321", which binds
// every interface) is NOT loopback — that is the "remote-bind" case TLS
// hardening exists for.
func isLoopbackAddr(addr string) bool {
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

// selfSignedCertValidity is deliberately under two years — long enough that
// local/LAN use won't hit renewal often, short enough to stay well inside
// browser/OS maximum leaf-certificate lifetimes.
const selfSignedCertValidity = 397 * 24 * time.Hour

// webhookTLSDir returns ~/.monoagent/webhook-tls/, where the auto-generated
// self-signed certificate is cached — a process-global location (the
// webhook server itself is process-global, not per-profile), following the
// same ~/.monoagent/<subdir>/ convention internal/secrets' file-keyring
// fallback and internal/ai's debug log use.
func webhookTLSDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".monoagent", "webhook-tls"), nil
}

// loadOrGenerateSelfSignedCert returns the cached self-signed certificate
// for the webhook server, generating and caching one (key file mode 0600)
// on first use. A cached certificate that has expired is regenerated
// rather than reused.
func loadOrGenerateSelfSignedCert() (tls.Certificate, error) {
	dir, err := webhookTLSDir()
	if err != nil {
		return tls.Certificate{}, err
	}
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if cert, loadErr := tls.LoadX509KeyPair(certPath, keyPath); loadErr == nil {
		if leaf, parseErr := x509.ParseCertificate(cert.Certificate[0]); parseErr == nil && time.Now().Before(leaf.NotAfter) {
			return cert, nil
		}
	}

	cert, certPEM, keyPEM, err := generateSelfSignedCert()
	if err != nil {
		return tls.Certificate{}, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return tls.Certificate{}, fmt.Errorf("creating %s: %w", dir, err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0600); err != nil {
		return tls.Certificate{}, fmt.Errorf("writing %s: %w", keyPath, err)
	}
	if err := os.WriteFile(certPath, certPEM, 0644); err != nil {
		return tls.Certificate{}, fmt.Errorf("writing %s: %w", certPath, err)
	}
	return cert, nil
}

// generateSelfSignedCert creates a fresh ECDSA P-256 self-signed
// certificate covering localhost/127.0.0.1/::1 only — a caller connecting
// by a LAN hostname or IP must either skip verification or the operator
// must supply a real certificate via MONOAGENT_WEBHOOK_TLS_CERT/_KEY.
func generateSelfSignedCert() (cert tls.Certificate, certPEM, keyPEM []byte, err error) {
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
		Subject:               pkix.Name{CommonName: "monoagentcli webhook server (self-signed)"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(selfSignedCertValidity),
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

// isAddrInUse reports whether err is a "port already taken" listen failure.
// syscall.EADDRINUSE covers unix; Windows reports WSAEADDRINUSE, which does not
// compare equal to it, so its message is matched as well.
func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "only one usage of each socket address")
}

// Stop gracefully shuts down the server with a 5-second timeout.
func (s *WebhookServer) Stop(ctx context.Context) error {
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	s.logger.Info().Msg("webhook server shutting down")
	return s.server.Shutdown(shutdownCtx)
}

// Register adds a webhook route. Path must be unique.
func (s *WebhookServer) Register(reg *WebhookRegistration) error {
	if reg == nil {
		return fmt.Errorf("webhook server: registration must not be nil")
	}
	if reg.Path == "" {
		return fmt.Errorf("webhook server: registration path must not be empty")
	}
	if reg.TriggerFn == nil {
		return fmt.Errorf("webhook server: registration TriggerFn must not be nil")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.routes[reg.Path]; exists {
		return fmt.Errorf("webhook server: path %q is already registered", reg.Path)
	}
	s.routes[reg.Path] = reg
	s.logger.Info().
		Str("path", reg.Path).
		Str("workflow_id", reg.WorkflowID).
		Str("node_id", reg.NodeID).
		Msg("webhook registered")
	return nil
}

// Deregister removes a webhook route by path.
func (s *WebhookServer) Deregister(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.routes, path)
	s.logger.Info().Str("path", path).Msg("webhook deregistered")
}

// ServeHTTP handles all incoming webhook requests.
// Routes: POST/GET /webhook/{path}
// Returns 404 if path not found, 405 if method doesn't match, 200 on success.
func (s *WebhookServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Parse path: must be /webhook/{path}
	urlPath := r.URL.Path
	const prefix = "/webhook/"
	if !strings.HasPrefix(urlPath, prefix) {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}
	pathKey := strings.TrimPrefix(urlPath, prefix)
	pathKey = strings.Trim(pathKey, "/")
	if pathKey == "" {
		writeJSONError(w, http.StatusNotFound, "not found")
		return
	}

	s.mu.RLock()
	reg, ok := s.routes[pathKey]
	s.mu.RUnlock()

	// CORS (hardened, RA2-8): emit NO CORS headers by default. Webhooks are
	// meant for loopback/server-to-server callers, so browsers cannot call
	// them cross-origin unless the operator configures an explicit origin
	// allowlist via MONOAGENT_WEBHOOK_ALLOWED_ORIGINS — in which case only
	// listed origins are reflected, never arbitrary ones.
	if origin := r.Header.Get("Origin"); origin != "" {
		if _, allowed := s.allowedOrigins[origin]; allowed {
			allowedHeaders := "Content-Type, X-Hub-Signature-256"
			if ok && reg.AuthHeader != "" {
				allowedHeaders += ", " + reg.AuthHeader
			}
			// The response varies with the Origin header; without Vary a
			// shared cache could replay this at a disallowed origin.
			w.Header().Set("Vary", "Origin")
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
		}
	}

	// Handle CORS preflight
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	if !ok {
		writeJSONError(w, http.StatusNotFound, "webhook not found")
		return
	}

	// Validate HTTP method
	if reg.Method != "ANY" {
		if r.Method != reg.Method {
			writeJSONError(w, http.StatusMethodNotAllowed, fmt.Sprintf("method not allowed; expected %s", reg.Method))
			return
		}
	} else {
		// ANY: only allow GET and POST
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
	}

	// Limit body size to 1MB
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	// Read body
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSONError(w, http.StatusRequestEntityTooLarge, "request body too large or read error")
		return
	}

	// HMAC validation
	if reg.HMACSecret != "" {
		sigHeader := r.Header.Get("X-Hub-Signature-256")
		if sigHeader == "" {
			writeJSONError(w, http.StatusUnauthorized, "missing X-Hub-Signature-256 header")
			return
		}
		if !validateHMAC(bodyBytes, reg.HMACSecret, sigHeader) {
			writeJSONError(w, http.StatusUnauthorized, "invalid signature")
			return
		}
	}

	// Static header token validation
	if reg.AuthHeader != "" {
		got := r.Header.Get(reg.AuthHeader)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(reg.AuthToken)) != 1 {
			writeJSONError(w, http.StatusUnauthorized, fmt.Sprintf("missing or invalid %s header", reg.AuthHeader))
			return
		}
	}

	// Parse body as JSON into map[string]interface{}
	var data map[string]interface{}
	if len(bodyBytes) == 0 {
		data = make(map[string]interface{})
	} else {
		if err := json.Unmarshal(bodyBytes, &data); err != nil {
			writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON body: %s", err.Error()))
			return
		}
	}

	item := NewItem(data)

	s.logger.Debug().
		Str("path", pathKey).
		Str("workflow_id", reg.WorkflowID).
		Str("method", r.Method).
		Msg("webhook triggered")

	// Call the trigger function
	reg.TriggerFn([]Item{item})

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"success":true}`))
}

// parseWebhookAllowedOrigins parses MONOAGENT_WEBHOOK_ALLOWED_ORIGINS — a
// comma-separated list of origins (e.g. "https://app.example.com") that may
// call the webhooks from a browser. Empty/unset (or all-empty entries)
// yields nil: no CORS headers at all.
func parseWebhookAllowedOrigins(v string) map[string]struct{} {
	var out map[string]struct{}
	for _, o := range strings.Split(v, ",") {
		if o = strings.TrimSpace(o); o != "" {
			if out == nil {
				out = make(map[string]struct{})
			}
			out[o] = struct{}{}
		}
	}
	return out
}

// validateHMAC checks the X-Hub-Signature-256 header against the body and secret.
// The header format is: "sha256=<hex digest>"
func validateHMAC(body []byte, secret, sigHeader string) bool {
	const sigPrefix = "sha256="
	if !strings.HasPrefix(sigHeader, sigPrefix) {
		return false
	}
	sigHex := strings.TrimPrefix(sigHeader, sigPrefix)
	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return false
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := mac.Sum(nil)

	return hmac.Equal(expected, sigBytes)
}

// writeJSONError writes a JSON error response with the given status code and message.
func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	resp, _ := json.Marshal(map[string]string{"error": msg})
	_, _ = w.Write(resp)
}
