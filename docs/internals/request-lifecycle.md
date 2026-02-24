# Request Lifecycle

This document traces a single NFS request from TCP accept to reply,
covering every layer the request passes through.

## 1. Connection Accept

The accept loop in `server.go` (`acceptLoop`) runs in a goroutine. For each
incoming connection:

1. **IP filtering**: `isIPAllowed` checks the client IP against `PolicyOptions.AllowedIPs`.
   Rejected connections are closed immediately. This is the first security check
   and happens before any bytes are read.

2. **Connection registration**: `registerConnection` checks `MaxConnections`. If
   the limit is reached, the connection is closed. Otherwise, it is added to
   `activeConns` with a timestamp and `sync.Once` for safe unregistration.

3. **TCP tuning**: If the connection is TCP, keepalive, Nagle disable, and
   buffer sizes from `TuningOptions` are applied.

4. **Connection handler goroutine**: A new goroutine is started with `defer`
   chains for unregistration, connection close, and panic recovery.

## 2. Connection Loop

The connection handler calls `handleConnectionLoop` with a `connIO` implementation
(either `rawConnIO` for direct TCP or `recordMarkingConnIO` for RFC 1831 framing).

The loop repeats:

1. Set a read deadline on the connection.
2. `cio.ReadCall()` reads and decodes an RPC call.

### Raw TCP mode (`rawConnIO`)

Reads directly from the TCP stream via `DecodeRPCCall`.

### Record marking mode (`recordMarkingConnIO`)

1. `rmConn.ReadRecord()` reads fragment headers, reassembles fragments into a
   complete record, enforcing `MaxRecordSize`.
2. The complete record is decoded via `DecodeRPCCall` from a `bytes.Reader`.
3. The remaining bytes after the RPC header become the procedure body reader.

## 3. RPC Decode

`DecodeRPCCall` in `rpc_types.go` parses:

- XID (transaction ID for request/reply matching)
- Message type (must be RPC_CALL)
- RPC version, program number, version, procedure number
- Credential (flavor + opaque body, bounded by `MAX_RPC_AUTH_LENGTH`)
- Verifier (flavor + opaque body, bounded by `MAX_RPC_AUTH_LENGTH`)

All length fields are validated before allocation.

## 4. Pre-dispatch Checks

Back in `handleConnectionLoop`:

1. **Activity tracking**: `updateConnectionActivity` refreshes the connection's
   last-activity timestamp for idle timeout management.

2. **AuthContext construction**: The client IP and port are extracted from the
   connection's remote address. The RPC credential is attached.

3. **Rate limiting**: If `EnableRateLimiting` is true, `rateLimiter.AllowRequest`
   checks per-IP and per-connection limits. Rejected requests receive `MSG_DENIED`.

## 5. Worker Dispatch

If a `WorkerPool` is configured, the request is submitted to a worker goroutine.
Otherwise, `HandleCall` is invoked directly. Both paths produce an `RPCReply`.

## 6. HandleCall -- Policy Lock and Authentication

`HandleCall` in `nfs_handlers.go` is the central dispatch point:

### Policy Read Lock

```go
if !handler.policyRWMu.TryRLock() {
    // Policy drain in progress -- return NFSERR_JUKEBOX
}
```

`TryRLock` is non-blocking. If a policy update is draining in-flight requests
(holding the write lock), this returns false and the client gets JUKEBOX, which
per RFC 1813 means "try again later." The client will retry automatically.

The read lock is NOT deferred here. Instead, it is released inside the goroutine
that does the actual work, so the lock is held for the full duration of the
filesystem operation, not just until HandleCall returns on timeout.

### Options Snapshot

```go
opts := handler.snapshotOptions()
```

Both tuning and policy options are loaded atomically and captured in a
`RequestOptions` struct. All subsequent reads within this request use this
snapshot, ensuring consistent behavior even if options change mid-request.

### Authentication

```go
authResult := ValidateAuthentication(authCtx, opts.Policy)
```

`ValidateAuthentication` in `auth.go` performs:

1. **IP filtering** (redundant with connection-level check, but covers RPC-level
   policy changes that happened after connection establishment).
2. **Secure port check**: If `Policy.Secure` is true, the client port must be < 1024.
3. **Credential validation**: AUTH_NONE maps to nobody (65534/65534). AUTH_SYS
   parses the credential body into UID, GID, machine name, and auxiliary GIDs.
4. **UID/GID squashing**: Applied based on `Policy.Squash`:
   - `"none"`: No squashing, credentials pass through.
   - `"root"`: UID 0 and GID 0 are mapped to 65534 (nobody).
   - `"all"`: All UIDs and GIDs are mapped to 65534.

If authentication fails, the policy read lock is released and `MSG_DENIED` is returned.

### Timeout Context

A `context.WithTimeout` is created using `opts.Tuning.Timeouts.DefaultTimeout`.
The actual handler runs in a goroutine; if it does not complete before the
timeout, HandleCall returns a timeout error.

### Program Dispatch

The goroutine (which holds the policy read lock) dispatches based on the RPC
program number:

- `MOUNT_PROGRAM` (100005) -> `handleMountCall`
- `NFS_PROGRAM` (100003) -> `handleNFSCall`
- Unknown -> `PROG_UNAVAIL`

## 7. NFS Procedure Handler

`handleNFSCall` checks the NFS version (must be v3) and looks up the procedure
in the `nfsHandlers` dispatch table. If the procedure is not found, `PROC_UNAVAIL`
is returned.

Each handler follows a common pattern:

1. **Decode arguments**: Read file handle(s) and procedure-specific XDR arguments
   from the body reader. `xdrDecodeFileHandle` validates handle length (must be
   exactly 8 bytes, capped at 64 bytes). `xdrDecodeString` enforces
   `MAX_XDR_STRING_LENGTH`.

2. **Validate inputs**: `validateFilename` rejects empty names, names > 255 bytes,
   names with NUL bytes, path separators, "." and "..", and Windows reserved names.
   `sanitizePath` prevents directory traversal by verifying the resolved path stays
   within the base directory.

3. **Look up the node**: `lookupNode` retrieves the `NFSNode` from `fileMap`.
   If the handle is not found, `NFSERR_STALE` is returned.

4. **Perform the operation**: Calls into `operations.go` (Lookup, GetAttr, Read,
   Write, Create, etc.).

5. **Encode the response**: Builds an XDR-encoded byte buffer with the status code,
   attributes (post_op_attr or wcc_data as appropriate), and procedure-specific
   results. The buffer is assigned to `reply.Data`.

## 8. Reply Encoding and Transmission

The `RPCReply` returns through the goroutine channel to HandleCall, which returns
it to the connection loop.

`EncodeRPCReply` in `rpc_types.go` writes:

- XID (echoed from the request)
- Message type (RPC_REPLY)
- Reply status (MSG_ACCEPTED or MSG_DENIED)
- Verifier (null verifier for AUTH_NONE)
- Accept status (SUCCESS, PROG_UNAVAIL, etc.)
- Procedure-specific data (the pre-encoded byte buffer from `reply.Data`)

The encoded reply is written through the `connIO` interface:

- **Raw TCP**: Written directly to the connection.
- **Record marking**: Serialized to a buffer, then written via
  `RecordMarkingWriter.WriteRecord`, which adds the fragment header(s).

## The Drain-and-Swap Mechanism

When `UpdatePolicyOptions` is called to change security-sensitive configuration
at runtime:

1. `policyMu.Lock()` serializes concurrent policy updates.
2. `policyRWMu.Lock()` acquires the write lock. This blocks until all goroutines
   holding read locks (in-flight requests) finish and release them.
3. While the write lock is held, any new request calling `TryRLock` fails and
   returns `NFSERR_JUKEBOX`, causing clients to retry.
4. The new policy is stored atomically via `n.policy.Store(&snapshot)`.
5. `policyRWMu.Unlock()` resumes normal request processing under the new policy.

This ensures there is never a request executing under a mix of old and new policy
settings. The JUKEBOX response is a standard NFS mechanism for temporary server
unavailability; clients retry transparently.
