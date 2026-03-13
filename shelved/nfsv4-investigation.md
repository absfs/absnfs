# NFSv4 Support Investigation

Investigated March 2026. This document analyzes the feasibility and implications
of supporting NFSv4 (the latest NFS protocol family: v4.0/v4.1/v4.2) in absnfs.

## Current State

The codebase implements NFSv3 (RFC 1813) with 22 procedure handlers, a MOUNT v1/v3
protocol, and a portmapper/rpcbind shim. The RPC layer is ONC RPC (RFC 1831) with
XDR serialization. Total: ~39K lines of Go across 64 files.

## NFSv4 Protocol Overview

The latest stable version is NFSv4.2 (RFC 7862, November 2016), building on
NFSv4.0 (RFC 7530) and NFSv4.1 (RFC 5661). NFSv4 is a fundamentally different
protocol from NFSv3 — not an incremental extension.

## Key Architectural Differences

### 1. Stateful vs Stateless

NFSv3 is stateless. The server keeps no per-client session state. Clients can
retry any request and the server treats it identically.

NFSv4 is stateful. The server must track:
- **Client sessions** with lease-based expiry and renewal (RENEW/SEQUENCE)
- **Open file state** (OPEN/CLOSE with state IDs)
- **Lock state** (LOCK/LOCKT/LOCKU with lock-owner state)
- **Delegation state** (read/write delegations with callback channels)

This is the single largest impediment. The current architecture has no concept
of client identity beyond per-request AUTH_SYS credentials. Adding session state
management would be a new subsystem touching every layer of the server.

### 2. COMPOUND Procedure Model

NFSv3 has 22 individual RPC procedures (NULL through COMMIT), each a separate
RPC call with its own procedure number.

NFSv4 has exactly 2 RPC procedures: NULL and COMPOUND. All operations are
batched into COMPOUND requests containing a sequence of typed operations. The
server evaluates them sequentially, maintaining a "current filehandle" and
"saved filehandle" across operations within the compound.

This means:
- The RPC dispatch layer (`nfs_handlers.go`) would need a completely different
  structure — a COMPOUND decoder that iterates over an operation list
- Every existing procedure handler would become an "operation" handler with
  a different signature (receiving/producing current-filehandle state)
- The RPC layer can no longer identify the operation from the procedure number;
  it must parse the XDR payload to discover the operation list

NFSv4.0 defines ~39 operations. NFSv4.1 adds ~19 more. NFSv4.2 adds ~7 more.

### 3. No Separate Mount Protocol

NFSv3 uses MOUNT (program 100005) to obtain the initial root file handle.

NFSv4 eliminates the mount protocol entirely. Clients use PUTROOTFH to obtain
the root filehandle and navigate via LOOKUP within COMPOUND requests. The server
exposes a pseudo-filesystem (PseudoFS) that stitches exports into a unified
namespace.

The existing `mount_handlers.go` and portmapper registration would be unused
for NFSv4 clients.

### 4. Single Port, TCP Only

NFSv4 uses only TCP port 2049. No portmapper/rpcbind interaction is needed.
The existing portmapper code would only serve NFSv3 clients.

### 5. Integrated Locking (No NLM)

NFSv3 delegates locking to the Network Lock Manager (NLM) protocol, which this
codebase does not implement (absfs doesn't support advisory locking).

NFSv4 integrates locking into the protocol itself. The LOCK/LOCKT/LOCKU
operations are mandatory in the spec. A minimal implementation could return
NFS4ERR_NOTSUPP for lock operations, but many clients expect basic locking.

### 6. Security Model

NFSv3 uses AUTH_SYS (UID/GID) by default. This codebase adds TLS/mTLS and
IP filtering on top.

NFSv4 mandates RPCSEC_GSS (RFC 2203) support, typically with Kerberos V5.
AUTH_SYS is allowed but clients may require GSS negotiation. Implementing
RPCSEC_GSS is a substantial effort involving GSS-API integration (typically
via system GSSAPI libraries or a pure-Go implementation).

NFSv4 also uses string-based identity mapping (user@domain) instead of numeric
UID/GID, requiring an ID mapping subsystem.

### 7. File Handle Semantics

NFSv3 file handles are opaque, up to 64 bytes, and persistent.

NFSv4 file handles can be volatile (the server can expire them) and are up to
128 bytes. This is a smaller change but touches `filehandle.go` and every
operation that encodes/decodes handles.

### 8. ACLs and Named Attributes

NFSv4 introduces ACL support (beyond POSIX mode bits) and named attributes
(arbitrary key-value metadata on files). The absfs interface does not expose
either of these. Supporting them would require either extending absfs or
returning NFS4ERR_NOTSUPP.

## Effort Estimate

| Component | Scope |
|-----------|-------|
| COMPOUND procedure dispatcher | New subsystem, replaces per-procedure dispatch |
| Session/state management | New subsystem: leases, client tracking, state IDs |
| ~60 operation handlers (v4.0+v4.1+v4.2) | Each comparable to a current procedure handler |
| PseudoFS / namespace | Replaces mount protocol |
| File locking integration | New subsystem or stub with NFS4ERR_NOTSUPP |
| RPCSEC_GSS / Kerberos | Major new dependency, or stub with AUTH_SYS only |
| String-based ID mapping | New subsystem |
| Delegation / callback channel | NFSv4 server-to-client callbacks (optional but expected) |
| Updated XDR types | Hundreds of new type definitions |
| Test coverage | Comparable to existing test suite, plus interop testing |

This is conservatively a rewrite-scale effort — not an extension of the existing
NFSv3 code. The two protocols share the ONC RPC transport and XDR encoding, but
virtually everything above that layer differs.

## Existing Go Implementations

Several open-source Go NFSv4 implementations exist:

- **kuleuven/nfs4go**: NFSv4.0/v4.1/v4.2 server, built on libnfs-go. Most
  feature-complete. AUTH_SYS only, no file locking.
- **smallfz/libnfs-go**: Experimental NFSv4.0 server library.
- **willscott/go-nfs**: NFSv3 only (similar scope to this project).

None of these are production-grade or widely adopted. The kuleuven/nfs4go
project demonstrates that a minimal NFSv4 server in Go is feasible but
confirms the scope — it is a separate codebase, not a patch on an NFSv3 server.

## Options

### Option A: Do Nothing (Recommended for Now)

Keep NFSv3 only. NFSv3 remains widely supported and is the better fit for
the absfs use case (simple, stateless file serving). The current TLS/mTLS
support addresses the main security gap that NFSv4 would fill. This avoids
the speculative infrastructure that CLAUDE.md warns against.

### Option B: Dual-Stack (NFSv3 + Minimal NFSv4)

Implement a minimal NFSv4.0 server alongside the existing NFSv3 code, sharing
the absfs operations layer. This would support basic file operations (LOOKUP,
READ, WRITE, GETATTR, READDIR) with AUTH_SYS authentication and no locking,
no delegations, and no RPCSEC_GSS. Clients would negotiate the version at
connection time.

Rough scope: 10-15K new lines of Go, a new COMPOUND dispatcher, ~20 operation
handlers for the minimal set, session state tracking, and PseudoFS namespace.
The existing `operations.go` could be shared.

### Option C: NFSv4-Only Rewrite

Replace NFSv3 with NFSv4. This would be a new project that reuses the absfs
integration layer and options/auth infrastructure but replaces the protocol
stack entirely.

## Recommendation

Option A. The absfs ecosystem targets embedded and in-process use cases where
NFSv3's simplicity is an advantage. NFSv4's benefits (firewall traversal,
integrated locking, Kerberos) solve problems that are either already addressed
(TLS handles security) or not relevant (locking is unused). Adding NFSv4 would
roughly double the codebase complexity for unclear user demand.

If NFSv4 support becomes a concrete requirement, Option B with a minimal
operation set is the pragmatic path forward, potentially drawing on
kuleuven/nfs4go as a reference implementation.
