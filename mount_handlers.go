package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
)

// handleMountCall handles mount protocol operations
func (h *NFSProcedureHandler) handleMountCall(call *RPCCall, body io.Reader, reply *RPCReply) (*RPCReply, error) {
	// Check version first
	if call.Header.Version != MOUNT_V3 {
		reply.Status = PROG_MISMATCH
		return reply, nil
	}

	switch call.Header.Procedure {
	case 0: // NULL
		return reply, nil

	case 1: // MNT
		mountPath, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			reply.Status = GARBAGE_ARGS
			return reply, nil
		}

		// Create mount point with timeout
		node, err := h.server.handler.Lookup(mountPath)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			reply.Status = MSG_ACCEPTED
			return reply, nil
		}

		// Allocate file handle for root
		handle := h.server.handler.fileMap.Allocate(node)

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		binary.Write(&buf, binary.BigEndian, handle)
		reply.Data = buf.Bytes()
		return reply, nil

	case 2: // DUMP
		// Return empty list of mounts
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, 0) // No entries
		reply.Data = buf.Bytes()
		return reply, nil

	case 3: // UMNT
		unmountPath, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			reply.Status = GARBAGE_ARGS
			return reply, nil
		}

		// Find and release any file handles for this path
		// Note: In a real implementation, we would track mount points separately
		// Reduced logging - only log in debug mode
		if h.server.options.Debug {
			h.server.logger.Printf("Unmounting %s", unmountPath)
		}
		return reply, nil

	default:
		reply.Status = PROC_UNAVAIL
		return reply, nil
	}
}
