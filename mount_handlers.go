package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
)

// handleMountCall handles mount protocol operations
// Supports both MOUNT v1 and v3 for compatibility with different clients
func (h *NFSProcedureHandler) handleMountCall(call *RPCCall, body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	// Check version - accept v1 or v3
	if call.Header.Version != 1 && call.Header.Version != MOUNT_V3 {
		reply.AcceptStatus = PROG_MISMATCH
		return reply, nil
	}

	switch call.Header.Procedure {
	case 0: // NULL
		return reply, nil

	case 1: // MNT
		// Apply rate limiting for mount operations
		if h.server.handler.rateLimiter != nil && h.server.handler.options.EnableRateLimiting {
			if !h.server.handler.rateLimiter.AllowOperation(authCtx.ClientIP, OpTypeMount) {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, NFSERR_DELAY) // Server is busy
				reply.Data = buf.Bytes()

				// Record rate limit exceeded
				if h.server.handler.metrics != nil {
					h.server.handler.metrics.RecordRateLimitExceeded()
				}

				return reply, nil
			}
		}

		mountPath, err := xdrDecodeString(body)
		if err != nil {
			reply.AcceptStatus = GARBAGE_ARGS
			return reply, nil
		}

		// Create mount point with timeout
		node, err := h.server.handler.Lookup(mountPath)
		if err != nil {
			// MNT3 response: fhs_status (MNT3ERR_NOENT = 2)
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, 2) // MNT3ERR_NOENT
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Allocate file handle for root
		handle := h.server.handler.fileMap.Allocate(node)
		if h.server.options.Debug {
			h.server.logger.Printf("MOUNT: Allocated handle %d for path '%s', fileMap count: %d", handle, mountPath, h.server.handler.fileMap.Count())
		}

		// Encode MNT3 response
		// fhs_status = 0 (MNT3_OK)
		// fhandle3 (variable length opaque handle)
		// auth_flavors (list of supported auth flavors)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, 0) // MNT3_OK
		// fhandle3 is opaque<FHSIZE3> - write length + data
		xdrEncodeUint32(&buf, 8) // handle size
		binary.Write(&buf, binary.BigEndian, handle)
		// auth_flavors - array of supported authentication flavors
		xdrEncodeUint32(&buf, 1)        // 1 flavor
		xdrEncodeUint32(&buf, AUTH_SYS) // AUTH_SYS
		reply.Data = buf.Bytes()
		return reply, nil

	case 2: // DUMP
		// Return empty list of mounts
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, 0) // No entries (NULL pointer in linked list)
		reply.Data = buf.Bytes()
		return reply, nil

	case 3: // UMNT
		_, err := xdrDecodeString(body)
		if err != nil {
			reply.AcceptStatus = GARBAGE_ARGS
			return reply, nil
		}

		// UMNT has no return value
		return reply, nil

	case 4: // UMNTALL
		// No arguments, no return value
		return reply, nil

	case 5: // EXPORT
		// Return list of exported filesystems
		// Each entry: ex_dir (string), ex_groups (list)
		// We export "/" to all
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, 1)   // Has entry (1 = true)
		xdrEncodeString(&buf, "/") // Export path
		xdrEncodeUint32(&buf, 0)   // No group restrictions (null pointer)
		xdrEncodeUint32(&buf, 0)   // End of list
		reply.Data = buf.Bytes()
		return reply, nil

	default:
		reply.AcceptStatus = PROC_UNAVAIL
		return reply, nil
	}
}
