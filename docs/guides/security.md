---
layout: default
title: Security
---

# Security Guide

This guide covers security considerations and best practices when using ABSNFS. While NFS itself is not known for being highly secure, ABSNFS provides several features to improve security.

## Security Considerations

NFS has some inherent security limitations you should be aware of:

1. **Limited Authentication**: NFS primarily relies on IP-based authentication rather than user credentials
2. **Clear Text Protocol**: NFSv3 does not encrypt network traffic
3. **Trust Model**: NFS generally assumes trusted networks and clients

## Security Features in ABSNFS

ABSNFS provides several security features to mitigate these limitations:

### Read-Only Exports

The simplest security measure is to make exports read-only, which prevents any modification of data:

```go
options := absnfs.ExportOptions{
    ReadOnly: true,
}
```

This is particularly useful for sharing reference data or documentation.

### IP Restrictions

You can restrict access to specific IP addresses or ranges:

```go
options := absnfs.ExportOptions{
    Secure: true,
    AllowedIPs: []string{
        "192.168.1.0/24",  // All IPs in the 192.168.1.x range
        "10.0.0.5",        // A specific IP
        "10.0.1.0/28",     // IPs from 10.0.1.0 to 10.0.1.15
    },
}
```

### User Identity Mapping (Squashing)

NFS allows controlling how user identities from clients are mapped on the server:

```go
options := absnfs.ExportOptions{
    Squash: "root", // Options: "none", "root", "all"
}
```

The options are:

- `"none"`: No identity mapping, client UIDs/GIDs are used as-is
- `"root"`: Root (UID 0) is mapped to the anonymous user, other users are unchanged
- `"all"`: All users are mapped to the anonymous user

Root squashing (`"root"`) is the default and prevents remote root users from having root privileges on your server.

### Path Validation

When `Secure` is enabled (the default), ABSNFS performs path validation to prevent directory traversal attacks:

```go
options := absnfs.ExportOptions{
    Secure: true,
}
```

## Best Practices

### Network Security

1. **Use Firewalls**: Restrict NFS access at the network level using firewalls
2. **VPN or Private Network**: When possible, only expose NFS servers on private networks or VPNs
3. **NFS Ports**: Ensure your firewall allows traffic on all required NFS ports (typically 2049 for NFSv3)

### Deployment Security

1. **Minimal Permissions**: Export with the minimal permissions necessary
2. **Limited Scope**: Export only the specific directories needed, not entire filesystems
3. **Isolated Filesystems**: Use isolated filesystems (like memfs) when appropriate to limit exposure

### Example Secure Configuration

Here's an example of a security-focused configuration:

```go
options := absnfs.ExportOptions{
    // Limit access to specific trusted networks
    Secure: true,
    AllowedIPs: []string{"10.0.0.0/8", "192.168.1.0/24"},
    
    // Only allow read operations
    ReadOnly: true,
    
    // Map all users to anonymous user
    Squash: "all",
}

nfsServer, err := absnfs.New(fs, options)
```

## Securing Custom Filesystems

When implementing custom filesystems for use with ABSNFS:

1. **Validate Paths**: Ensure paths cannot escape the filesystem's root
2. **Implement Permissions**: Correctly implement and check file permissions
3. **Handle Symbolic Links**: Be careful with symlink handling to prevent link traversal attacks
4. **Validate Input**: Validate all input parameters, especially those from network clients

## Monitoring and Auditing

Consider implementing monitoring and auditing for your NFS server:

1. **Access Logs**: Log all access attempts, especially failures
2. **Periodic Scanning**: Regularly scan for unauthorized access attempts
3. **Resource Monitoring**: Monitor resource usage to detect abuse

## Limitations

Be aware of these security limitations:

1. **NFSv3 Protocol**: ABSNFS uses NFSv3, which does not include strong authentication or encryption
2. **UDP Support**: NFS can use UDP, which is vulnerable to IP spoofing attacks
3. **Lack of User Authentication**: NFS relies primarily on IP-based authentication

For highly sensitive data or environments with strict security requirements, consider using other protocols like SFTP or encrypted block storage solutions.

## Security Checklist

- [ ] Configure appropriate export options
- [ ] Restrict access using firewalls and network controls
- [ ] Use read-only exports when write access is not needed
- [ ] Enable user identity mapping (squashing)
- [ ] Limit exports to specific directories
- [ ] Monitor access and usage patterns
- [ ] Deploy only on trusted networks when possible