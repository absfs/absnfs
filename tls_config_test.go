package absnfs

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Helper function to generate a self-signed certificate for testing
func generateTestCertificate(t *testing.T, commonName string, isCA bool) (*x509.Certificate, *rsa.PrivateKey) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatalf("failed to generate serial number: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   commonName,
			Organization: []string{"Test Organization"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              []string{"localhost"},
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}

	if isCA {
		template.IsCA = true
		template.KeyUsage |= x509.KeyUsageCertSign
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("failed to parse certificate: %v", err)
	}

	return cert, privateKey
}

// Helper function to save a certificate to a file
func saveCertToFile(t *testing.T, cert *x509.Certificate, filename string) {
	t.Helper()

	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	})

	if err := os.WriteFile(filename, certPEM, 0644); err != nil {
		t.Fatalf("failed to write certificate file: %v", err)
	}
}

// Helper function to save a private key to a file
func saveKeyToFile(t *testing.T, key *rsa.PrivateKey, filename string) {
	t.Helper()

	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(key),
	})

	if err := os.WriteFile(filename, keyPEM, 0600); err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}
}

func TestDefaultTLSConfig(t *testing.T) {
	config := DefaultTLSConfig()

	if config.Enabled {
		t.Error("DefaultTLSConfig should have TLS disabled by default")
	}

	if config.MinVersion != tls.VersionTLS12 {
		t.Errorf("DefaultTLSConfig should use TLS 1.2 as minimum version, got %d", config.MinVersion)
	}

	if config.MaxVersion != tls.VersionTLS13 {
		t.Errorf("DefaultTLSConfig should use TLS 1.3 as maximum version, got %d", config.MaxVersion)
	}

	if config.ClientAuth != tls.NoClientCert {
		t.Errorf("DefaultTLSConfig should not require client certs by default, got %d", config.ClientAuth)
	}

	if !config.PreferServerCipherSuites {
		t.Error("DefaultTLSConfig should prefer server cipher suites")
	}

	if config.InsecureSkipVerify {
		t.Error("DefaultTLSConfig should not skip verification")
	}

	if len(config.CipherSuites) == 0 {
		t.Error("DefaultTLSConfig should have cipher suites configured")
	}
}

func TestTLSConfigValidation(t *testing.T) {
	tests := []struct {
		name      string
		config    *TLSConfig
		wantError bool
	}{
		{
			name: "disabled TLS is valid",
			config: &TLSConfig{
				Enabled: false,
			},
			wantError: false,
		},
		{
			name: "missing certificate file",
			config: &TLSConfig{
				Enabled:  true,
				CertFile: "",
				KeyFile:  "key.pem",
			},
			wantError: true,
		},
		{
			name: "missing key file",
			config: &TLSConfig{
				Enabled:  true,
				CertFile: "cert.pem",
				KeyFile:  "",
			},
			wantError: true,
		},
		{
			name: "invalid TLS version range",
			config: &TLSConfig{
				Enabled:    true,
				CertFile:   "cert.pem",
				KeyFile:    "key.pem",
				MinVersion: tls.VersionTLS13,
				MaxVersion: tls.VersionTLS12,
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if tt.wantError && err == nil {
				t.Error("expected validation error, got nil")
			}
			if !tt.wantError && err != nil {
				t.Errorf("unexpected validation error: %v", err)
			}
		})
	}
}

func TestTLSConfigBuildConfig(t *testing.T) {
	// Create a temporary directory for test certificates
	tmpDir := t.TempDir()

	// Generate test certificate and key
	cert, key := generateTestCertificate(t, "test-server", false)
	certFile := filepath.Join(tmpDir, "server.crt")
	keyFile := filepath.Join(tmpDir, "server.key")
	saveCertToFile(t, cert, certFile)
	saveKeyToFile(t, key, keyFile)

	tests := []struct {
		name      string
		config    *TLSConfig
		wantError bool
	}{
		{
			name: "disabled TLS",
			config: &TLSConfig{
				Enabled: false,
			},
			wantError: false,
		},
		{
			name: "valid TLS configuration",
			config: &TLSConfig{
				Enabled:    true,
				CertFile:   certFile,
				KeyFile:    keyFile,
				MinVersion: tls.VersionTLS12,
				MaxVersion: tls.VersionTLS13,
			},
			wantError: false,
		},
		{
			name: "non-existent certificate file",
			config: &TLSConfig{
				Enabled:  true,
				CertFile: "/nonexistent/cert.pem",
				KeyFile:  keyFile,
			},
			wantError: true,
		},
		{
			name: "non-existent key file",
			config: &TLSConfig{
				Enabled:  true,
				CertFile: certFile,
				KeyFile:  "/nonexistent/key.pem",
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tlsConfig, err := tt.config.BuildConfig()
			if tt.wantError {
				if err == nil {
					t.Error("expected error building TLS config, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error building TLS config: %v", err)
				}
				if tt.config.Enabled && tlsConfig == nil {
					t.Error("expected TLS config, got nil")
				}
				if !tt.config.Enabled && tlsConfig != nil {
					t.Error("expected nil TLS config for disabled TLS")
				}
			}
		})
	}
}

func TestTLSConfigReloadCertificates(t *testing.T) {
	tmpDir := t.TempDir()

	// Generate initial certificate
	cert1, key1 := generateTestCertificate(t, "test-server-1", false)
	certFile := filepath.Join(tmpDir, "server.crt")
	keyFile := filepath.Join(tmpDir, "server.key")
	saveCertToFile(t, cert1, certFile)
	saveKeyToFile(t, key1, keyFile)

	config := &TLSConfig{
		Enabled:  true,
		CertFile: certFile,
		KeyFile:  keyFile,
	}

	// Build initial config
	_, err := config.BuildConfig()
	if err != nil {
		t.Fatalf("failed to build initial TLS config: %v", err)
	}

	// Generate new certificate and replace files
	cert2, key2 := generateTestCertificate(t, "test-server-2", false)
	saveCertToFile(t, cert2, certFile)
	saveKeyToFile(t, key2, keyFile)

	// Reload certificates
	err = config.ReloadCertificates()
	if err != nil {
		t.Fatalf("failed to reload certificates: %v", err)
	}

	// Verify config was updated
	tlsConfig, err := config.GetConfig()
	if err != nil {
		t.Fatalf("failed to get TLS config: %v", err)
	}

	if len(tlsConfig.Certificates) == 0 {
		t.Fatal("no certificates in TLS config after reload")
	}
}

func TestParseClientAuthType(t *testing.T) {
	tests := []struct {
		input    string
		expected tls.ClientAuthType
		wantErr  bool
	}{
		{"NoClientCert", tls.NoClientCert, false},
		{"none", tls.NoClientCert, false},
		{"", tls.NoClientCert, false},
		{"RequestClientCert", tls.RequestClientCert, false},
		{"request", tls.RequestClientCert, false},
		{"RequireAnyClientCert", tls.RequireAnyClientCert, false},
		{"require-any", tls.RequireAnyClientCert, false},
		{"VerifyClientCertIfGiven", tls.VerifyClientCertIfGiven, false},
		{"verify-if-given", tls.VerifyClientCertIfGiven, false},
		{"RequireAndVerifyClientCert", tls.RequireAndVerifyClientCert, false},
		{"require-and-verify", tls.RequireAndVerifyClientCert, false},
		{"require", tls.RequireAndVerifyClientCert, false},
		{"invalid", tls.NoClientCert, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParseClientAuthType(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result != tt.expected {
					t.Errorf("expected %d, got %d", tt.expected, result)
				}
			}
		})
	}
}

func TestParseTLSVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected uint16
		wantErr  bool
	}{
		{"1.0", tls.VersionTLS10, false},
		{"TLS1.0", tls.VersionTLS10, false},
		{"TLSv1.0", tls.VersionTLS10, false},
		{"1.1", tls.VersionTLS11, false},
		{"TLS1.1", tls.VersionTLS11, false},
		{"TLSv1.1", tls.VersionTLS11, false},
		{"1.2", tls.VersionTLS12, false},
		{"TLS1.2", tls.VersionTLS12, false},
		{"TLSv1.2", tls.VersionTLS12, false},
		{"", tls.VersionTLS12, false}, // Default
		{"1.3", tls.VersionTLS13, false},
		{"TLS1.3", tls.VersionTLS13, false},
		{"TLSv1.3", tls.VersionTLS13, false},
		{"invalid", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result, err := ParseTLSVersion(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if result != tt.expected {
					t.Errorf("expected 0x%04x, got 0x%04x", tt.expected, result)
				}
			}
		})
	}
}

func TestTLSVersionString(t *testing.T) {
	tests := []struct {
		version  uint16
		expected string
	}{
		{tls.VersionTLS10, "TLS 1.0"},
		{tls.VersionTLS11, "TLS 1.1"},
		{tls.VersionTLS12, "TLS 1.2"},
		{tls.VersionTLS13, "TLS 1.3"},
		{0x9999, "Unknown(0x9999)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := TLSVersionString(tt.version)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestExtractCertificateIdentity(t *testing.T) {
	tests := []struct {
		name     string
		cert     *x509.Certificate
		expected string
	}{
		{
			name:     "nil certificate",
			cert:     nil,
			expected: "",
		},
		{
			name: "certificate with CN",
			cert: &x509.Certificate{
				Subject: pkix.Name{
					CommonName: "test-user",
				},
			},
			expected: "test-user",
		},
		{
			name: "certificate with DNS SAN",
			cert: &x509.Certificate{
				Subject: pkix.Name{
					CommonName: "",
				},
				DNSNames: []string{"user.example.com"},
			},
			expected: "user.example.com",
		},
		{
			name: "certificate with email",
			cert: &x509.Certificate{
				Subject: pkix.Name{
					CommonName: "",
				},
				EmailAddresses: []string{"user@example.com"},
			},
			expected: "user@example.com",
		},
		{
			name: "certificate with no identity",
			cert: &x509.Certificate{
				Subject: pkix.Name{},
			},
			expected: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ExtractCertificateIdentity(tt.cert)
			if result != tt.expected {
				t.Errorf("expected %s, got %s", tt.expected, result)
			}
		})
	}
}

func TestGetCertificateInfo(t *testing.T) {
	cert, _ := generateTestCertificate(t, "test-server", false)

	info := GetCertificateInfo(cert)
	if info == "" {
		t.Error("expected non-empty certificate info")
	}

	// Check that info contains expected fields
	expectedFields := []string{"CN=", "Issuer=", "Serial=", "NotAfter="}
	for _, field := range expectedFields {
		if !contains(info, field) {
			t.Errorf("certificate info missing field: %s", field)
		}
	}

	// Test with nil certificate
	info = GetCertificateInfo(nil)
	if info != "no certificate" {
		t.Errorf("expected 'no certificate', got %s", info)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
