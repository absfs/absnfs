package absnfs

import (
	"crypto/x509"
	"fmt"
	"net"
	"strings"
)

// AuthContext contains information about the client making a request
type AuthContext struct {
	ClientIP   string             // Client IP address
	ClientPort int                // Client port number
	Credential *RPCCredential     // RPC credential
	AuthSys    *AuthSysCredential // Parsed AUTH_SYS credential (if applicable)
	ClientCert *x509.Certificate  // Client certificate (if TLS with client auth)
	TLSEnabled bool               // Whether this connection is using TLS
}

// AuthResult contains the result of authentication validation
type AuthResult struct {
	Allowed bool   // Whether the request is allowed
	UID     uint32 // Effective UID after squashing
	GID     uint32 // Effective GID after squashing
	Reason  string // Reason for denial (if not allowed)
}

// ValidateAuthentication validates a client request against export options
func ValidateAuthentication(ctx *AuthContext, options ExportOptions) *AuthResult {
	result := &AuthResult{
		Allowed: false,
		UID:     65534, // Default to nobody
		GID:     65534, // Default to nobody
	}

	// Step 1: Validate client IP address
	if len(options.AllowedIPs) > 0 {
		if !isIPAllowed(ctx.ClientIP, options.AllowedIPs) {
			result.Reason = fmt.Sprintf("client IP %s not in allowed list", ctx.ClientIP)
			return result
		}
	}

	// Step 2: Validate secure port requirement
	if options.Secure {
		if ctx.ClientPort >= 1024 {
			result.Reason = fmt.Sprintf("client port %d is not a privileged port (required when Secure=true)", ctx.ClientPort)
			return result
		}
	}

	// Step 3: Validate credential flavor
	switch ctx.Credential.Flavor {
	case AUTH_NONE:
		// AUTH_NONE is always accepted but uses nobody/nobody
		result.Allowed = true
		result.UID = 65534
		result.GID = 65534

	case AUTH_SYS:
		// Parse AUTH_SYS credentials if not already parsed
		if ctx.AuthSys == nil {
			authSys, err := ParseAuthSysCredential(ctx.Credential.Body)
			if err != nil {
				result.Reason = fmt.Sprintf("invalid AUTH_SYS credentials: %v", err)
				return result
			}
			ctx.AuthSys = authSys
		}

		// AUTH_SYS is accepted
		result.Allowed = true
		result.UID = ctx.AuthSys.UID
		result.GID = ctx.AuthSys.GID

		// Step 4: Apply squashing (user mapping)
		applySquashing(result, ctx.AuthSys, options.Squash)

	default:
		// Other authentication flavors are not supported
		result.Reason = fmt.Sprintf("unsupported authentication flavor: %d", ctx.Credential.Flavor)
		return result
	}

	return result
}

// applySquashing applies user ID squashing/mapping according to the export options
func applySquashing(result *AuthResult, authSys *AuthSysCredential, squash string) {
	switch strings.ToLower(squash) {
	case "root":
		// Map root (UID 0) to nobody
		if authSys.UID == 0 {
			result.UID = 65534
		}
		if authSys.GID == 0 {
			result.GID = 65534
		}

	case "all":
		// Map all users to nobody
		result.UID = 65534
		result.GID = 65534

	case "none", "":
		// No squashing - use the credentials as provided
		// (already set in result)

	default:
		// Unknown squash mode - default to no squashing
		// Could log a warning here in the future
	}
}

// isIPAllowed checks if a client IP is in the allowed list
func isIPAllowed(clientIP string, allowedIPs []string) bool {
	// Parse client IP
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}

	// Check against each allowed IP/subnet
	for _, allowed := range allowedIPs {
		// Check if it's a CIDR notation
		if strings.Contains(allowed, "/") {
			_, subnet, err := net.ParseCIDR(allowed)
			if err != nil {
				continue // Invalid CIDR, skip
			}
			if subnet.Contains(ip) {
				return true
			}
		} else {
			// Direct IP comparison
			allowedIP := net.ParseIP(allowed)
			if allowedIP != nil && allowedIP.Equal(ip) {
				return true
			}
		}
	}

	return false
}

// ExtractCertificateIdentity extracts user identity from a client certificate
// It returns the Common Name (CN) from the certificate subject
// Can be extended to support other fields or custom mappings
func ExtractCertificateIdentity(cert *x509.Certificate) string {
	if cert == nil {
		return ""
	}

	// Primary: Use Common Name (CN) from subject
	if cert.Subject.CommonName != "" {
		return cert.Subject.CommonName
	}

	// Fallback: Use first DNS name from Subject Alternative Names
	if len(cert.DNSNames) > 0 {
		return cert.DNSNames[0]
	}

	// Fallback: Use first email address
	if len(cert.EmailAddresses) > 0 {
		return cert.EmailAddresses[0]
	}

	return "unknown"
}

// GetCertificateInfo returns a human-readable string with certificate details
func GetCertificateInfo(cert *x509.Certificate) string {
	if cert == nil {
		return "no certificate"
	}

	return fmt.Sprintf("CN=%s, Issuer=%s, Serial=%s, NotAfter=%s",
		cert.Subject.CommonName,
		cert.Issuer.CommonName,
		cert.SerialNumber.String(),
		cert.NotAfter.Format("2006-01-02"))
}
