package absnfs

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"sync"
)

// TLSConfig holds the TLS configuration for the NFS server
type TLSConfig struct {
	// Enabled indicates whether TLS is enabled
	Enabled bool

	// CertFile is the path to the server certificate file (PEM format)
	CertFile string

	// KeyFile is the path to the server private key file (PEM format)
	KeyFile string

	// CAFile is the path to the CA certificate file for client verification (optional)
	CAFile string

	// ClientAuth specifies the client authentication policy
	// Options: NoClientCert, RequestClientCert, RequireAnyClientCert,
	// VerifyClientCertIfGiven, RequireAndVerifyClientCert
	ClientAuth tls.ClientAuthType

	// MinVersion specifies the minimum TLS version to accept
	// Default: TLS 1.2
	MinVersion uint16

	// MaxVersion specifies the maximum TLS version to accept
	// Default: TLS 1.3
	MaxVersion uint16

	// CipherSuites is a list of enabled cipher suites
	// If empty, a secure default list will be used
	CipherSuites []uint16

	// PreferServerCipherSuites controls whether the server's cipher suite
	// preferences should be used instead of the client's
	PreferServerCipherSuites bool

	// InsecureSkipVerify controls whether the client should skip verification
	// of the server's certificate chain and host name
	// WARNING: Only for testing purposes
	InsecureSkipVerify bool

	// tlsConfig is the internal Go TLS configuration
	tlsConfig *tls.Config
	mu        sync.RWMutex
}

// DefaultTLSConfig returns a TLS configuration with secure defaults
func DefaultTLSConfig() *TLSConfig {
	return &TLSConfig{
		Enabled:                  false,
		ClientAuth:               tls.NoClientCert,
		MinVersion:               tls.VersionTLS12,
		MaxVersion:               tls.VersionTLS13,
		PreferServerCipherSuites: true,
		InsecureSkipVerify:       false,
		CipherSuites: []uint16{
			// TLS 1.3 cipher suites (don't need to be configured, but listed for reference)
			tls.TLS_AES_128_GCM_SHA256,
			tls.TLS_AES_256_GCM_SHA384,
			tls.TLS_CHACHA20_POLY1305_SHA256,

			// TLS 1.2 secure cipher suites
			tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
		},
	}
}

// Validate checks if the TLS configuration is valid
func (tc *TLSConfig) Validate() error {
	if !tc.Enabled {
		return nil
	}

	// Check required fields
	if tc.CertFile == "" {
		return fmt.Errorf("TLS certificate file is required when TLS is enabled")
	}
	if tc.KeyFile == "" {
		return fmt.Errorf("TLS key file is required when TLS is enabled")
	}

	// Check if certificate files exist
	if _, err := os.Stat(tc.CertFile); err != nil {
		return fmt.Errorf("TLS certificate file not found: %w", err)
	}
	if _, err := os.Stat(tc.KeyFile); err != nil {
		return fmt.Errorf("TLS key file not found: %w", err)
	}

	// Check CA file if client authentication is required
	if tc.ClientAuth >= tls.VerifyClientCertIfGiven && tc.CAFile != "" {
		if _, err := os.Stat(tc.CAFile); err != nil {
			return fmt.Errorf("TLS CA file not found: %w", err)
		}
	}

	// Validate TLS version
	if tc.MinVersion > tc.MaxVersion {
		return fmt.Errorf("TLS min version (%d) cannot be greater than max version (%d)",
			tc.MinVersion, tc.MaxVersion)
	}

	// Warn about insecure settings (but don't error)
	if tc.InsecureSkipVerify {
		// This will be logged by the server
	}

	return nil
}

// BuildConfig creates and returns a Go TLS configuration
func (tc *TLSConfig) BuildConfig() (*tls.Config, error) {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if !tc.Enabled {
		return nil, nil
	}

	// Validate configuration first
	if err := tc.Validate(); err != nil {
		return nil, err
	}

	// Load server certificate and key
	cert, err := tls.LoadX509KeyPair(tc.CertFile, tc.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load server certificate and key: %w", err)
	}

	// Create base TLS config
	config := &tls.Config{
		Certificates:             []tls.Certificate{cert},
		MinVersion:               tc.MinVersion,
		MaxVersion:               tc.MaxVersion,
		PreferServerCipherSuites: tc.PreferServerCipherSuites,
		InsecureSkipVerify:       tc.InsecureSkipVerify,
		ClientAuth:               tc.ClientAuth,
	}

	// Add cipher suites if specified
	if len(tc.CipherSuites) > 0 {
		config.CipherSuites = tc.CipherSuites
	}

	// Load CA certificate pool for client verification if needed
	if tc.ClientAuth >= tls.VerifyClientCertIfGiven && tc.CAFile != "" {
		caCert, err := os.ReadFile(tc.CAFile)
		if err != nil {
			return nil, fmt.Errorf("failed to read CA certificate: %w", err)
		}

		caCertPool := x509.NewCertPool()
		if !caCertPool.AppendCertsFromPEM(caCert) {
			return nil, fmt.Errorf("failed to parse CA certificate")
		}

		config.ClientCAs = caCertPool
	}

	// Cache the built config
	tc.tlsConfig = config

	return config, nil
}

// GetConfig returns the cached TLS configuration
// If not built yet, it builds and caches it
func (tc *TLSConfig) GetConfig() (*tls.Config, error) {
	tc.mu.RLock()
	if tc.tlsConfig != nil {
		defer tc.mu.RUnlock()
		return tc.tlsConfig, nil
	}
	tc.mu.RUnlock()

	return tc.BuildConfig()
}

// ReloadCertificates reloads the server certificates without changing other settings
// This is useful for certificate rotation without restarting the server
func (tc *TLSConfig) ReloadCertificates() error {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	if !tc.Enabled {
		return fmt.Errorf("TLS is not enabled")
	}

	// Load new certificate and key
	cert, err := tls.LoadX509KeyPair(tc.CertFile, tc.KeyFile)
	if err != nil {
		return fmt.Errorf("failed to reload certificate and key: %w", err)
	}

	// Update the cached config if it exists
	if tc.tlsConfig != nil {
		tc.tlsConfig.Certificates = []tls.Certificate{cert}
	}

	return nil
}

// GetClientAuthString returns a string representation of the ClientAuth setting
func (tc *TLSConfig) GetClientAuthString() string {
	switch tc.ClientAuth {
	case tls.NoClientCert:
		return "NoClientCert"
	case tls.RequestClientCert:
		return "RequestClientCert"
	case tls.RequireAnyClientCert:
		return "RequireAnyClientCert"
	case tls.VerifyClientCertIfGiven:
		return "VerifyClientCertIfGiven"
	case tls.RequireAndVerifyClientCert:
		return "RequireAndVerifyClientCert"
	default:
		return fmt.Sprintf("Unknown(%d)", tc.ClientAuth)
	}
}

// ParseClientAuthType parses a string into a tls.ClientAuthType
func ParseClientAuthType(s string) (tls.ClientAuthType, error) {
	switch s {
	case "NoClientCert", "none", "":
		return tls.NoClientCert, nil
	case "RequestClientCert", "request":
		return tls.RequestClientCert, nil
	case "RequireAnyClientCert", "require-any":
		return tls.RequireAnyClientCert, nil
	case "VerifyClientCertIfGiven", "verify-if-given":
		return tls.VerifyClientCertIfGiven, nil
	case "RequireAndVerifyClientCert", "require-and-verify", "require":
		return tls.RequireAndVerifyClientCert, nil
	default:
		return tls.NoClientCert, fmt.Errorf("unknown client auth type: %s", s)
	}
}

// ParseTLSVersion parses a string into a TLS version constant
func ParseTLSVersion(s string) (uint16, error) {
	switch s {
	case "1.0", "TLS1.0", "TLSv1.0":
		return tls.VersionTLS10, nil
	case "1.1", "TLS1.1", "TLSv1.1":
		return tls.VersionTLS11, nil
	case "1.2", "TLS1.2", "TLSv1.2", "":
		return tls.VersionTLS12, nil
	case "1.3", "TLS1.3", "TLSv1.3":
		return tls.VersionTLS13, nil
	default:
		return 0, fmt.Errorf("unknown TLS version: %s", s)
	}
}

// TLSVersionString returns the string representation of a TLS version
func TLSVersionString(version uint16) string {
	switch version {
	case tls.VersionTLS10:
		return "TLS 1.0"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS13:
		return "TLS 1.3"
	default:
		return fmt.Sprintf("Unknown(0x%04x)", version)
	}
}

// Clone returns a deep copy of the TLSConfig without copying the mutex
func (tc *TLSConfig) Clone() *TLSConfig {
	if tc == nil {
		return nil
	}
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	clone := &TLSConfig{
		Enabled:                  tc.Enabled,
		CertFile:                 tc.CertFile,
		KeyFile:                  tc.KeyFile,
		CAFile:                   tc.CAFile,
		ClientAuth:               tc.ClientAuth,
		MinVersion:               tc.MinVersion,
		MaxVersion:               tc.MaxVersion,
		PreferServerCipherSuites: tc.PreferServerCipherSuites,
		InsecureSkipVerify:       tc.InsecureSkipVerify,
	}

	// Copy cipher suites slice
	if len(tc.CipherSuites) > 0 {
		clone.CipherSuites = make([]uint16, len(tc.CipherSuites))
		copy(clone.CipherSuites, tc.CipherSuites)
	}

	// Note: tlsConfig is not copied - the clone will rebuild it when needed
	return clone
}
