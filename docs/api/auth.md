# Authentication

IP-based access control, UID/GID squashing, and credential validation for NFS requests.

## Types

### AuthContext

```go
type AuthContext struct {
    ClientIP     string
    ClientPort   int
    Credential   *RPCCredential
    AuthSys      *AuthSysCredential  // Parsed AUTH_SYS credential (if applicable)
    ClientCert   *x509.Certificate   // Client certificate (if TLS with client auth)
    TLSEnabled   bool
    EffectiveUID uint32
    EffectiveGID uint32
}
```

Built per-request by the connection handler from the TCP connection's remote address and the RPC call's credential block.

### AuthResult

```go
type AuthResult struct {
    Allowed bool   // Whether the request is allowed
    UID     uint32 // Effective UID after squashing
    GID     uint32 // Effective GID after squashing
    Reason  string // Reason for denial (empty if allowed)
}
```

## Functions

### ValidateAuthentication

```go
func ValidateAuthentication(ctx *AuthContext, policy *PolicyOptions) *AuthResult
```

Validates a client request against the current policy. Runs four checks in order:

1. **IP filtering**: If `policy.AllowedIPs` is non-empty, the client IP must match at least one entry. Supports individual IPs and CIDR notation (e.g., `"192.168.1.0/24"`). IPv4-mapped IPv6 addresses are normalized for correct comparison.

2. **Secure port**: If `policy.Secure` is true, the client port must be below 1024 (privileged port).

3. **Credential flavor**: Only `AUTH_NONE` and `AUTH_SYS` are accepted.
   - `AUTH_NONE` maps to nobody (UID/GID 65534).
   - `AUTH_SYS` parses the credential body to extract UID, GID, and auxiliary GIDs.

4. **UID/GID squashing**: Applied to `AUTH_SYS` credentials based on `policy.Squash`.

If any check fails, `AuthResult.Allowed` is false and `Reason` describes the failure.

## Squash Modes

The `Squash` field in `PolicyOptions` controls user ID mapping:

| Mode | Behavior |
|------|----------|
| `"none"` or `""` | No mapping. Credentials used as-is. |
| `"root"` | UID 0 is mapped to 65534 (nobody) along with its GID. GID 0 in auxiliary groups is also squashed. Non-root users keep their UIDs. |
| `"all"` | All UIDs and GIDs are mapped to 65534. |
| (unknown) | Fails closed -- all users mapped to 65534. |

Root squashing also handles the edge case of a non-root user whose primary GID is 0: the GID alone is squashed while the UID is preserved. Auxiliary GID arrays are copied before modification to avoid mutating shared slices.

## TLS Certificate Identity

### ExtractCertificateIdentity

```go
func ExtractCertificateIdentity(cert *x509.Certificate) string
```

Extracts a user identity string from a client certificate. Checks in order:
1. Subject Common Name (CN)
2. First DNS name from Subject Alternative Names
3. First email address
4. Falls back to `"unknown"`

### GetCertificateInfo

```go
func GetCertificateInfo(cert *x509.Certificate) string
```

Returns a human-readable summary: `CN=..., Issuer=..., Serial=..., NotAfter=...`.

## IP Filtering Details

The `isIPAllowed` function (used both at the connection level and in `ValidateAuthentication`) supports:

- Individual IPs: `"192.168.1.100"`
- CIDR subnets: `"10.0.0.0/8"`
- IPv4 and IPv6 addresses
- IPv4-mapped IPv6 normalization (e.g., `"::ffff:192.168.1.1"` matches `"192.168.1.1"`)

An empty `AllowedIPs` list means all IPs are permitted.
