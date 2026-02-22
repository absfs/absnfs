// nfs_proc_dir.go: NFSv3 directory listing operations.
//
// Implements the READDIR and READDIRPLUS procedures as defined in
// RFC 1813 sections 3.3.16 and 3.3.17. READDIR returns directory entry
// names and file IDs; READDIRPLUS additionally returns full attributes
// and file handles, reducing the need for follow-up LOOKUP/GETATTR calls.
package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path"
)

// handleReaddir handles NFSPROC3_READDIR - read directory entries
func (h *NFSProcedureHandler) handleReaddir(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	var cookie uint64
	var cookieVerf [8]byte
	if err := binary.Read(body, binary.BigEndian, &cookie); err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}
	if _, err := io.ReadFull(body, cookieVerf[:]); err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	var count uint32
	if err := binary.Read(body, binary.BigEndian, &count); err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	// Rate limiting (after body consumption to prevent stream desync)
	if h.server.handler.rateLimiter != nil && h.server.handler.policy.Load().EnableRateLimiting {
		if !h.server.handler.rateLimiter.AllowOperation(authCtx.ClientIP, OpTypeReaddir) {
			if h.server.handler.metrics != nil {
				h.server.handler.metrics.RecordRateLimitExceeded()
			}
			return nfsErrorWithPostOp(reply, NFSERR_DELAY), nil
		}
	}

	dir, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithPostOp(reply, NFSERR_STALE), nil
	}

	// R4: Copy attrs under RLock for dir check
	dir.mu.RLock()
	dirMode := dir.attrs.Mode
	dir.mu.RUnlock()

	if dirMode&os.ModeDir == 0 {
		return nfsErrorWithPostOp(reply, NFSERR_NOTDIR), nil
	}

	// R22: Return NFS error instead of nil,err
	entries, err := h.server.handler.ReadDir(dir)
	if err != nil {
		return nfsErrorWithPostOp(reply, mapError(err)), nil
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)

	attrs, err := h.server.handler.GetAttr(dir)
	if err != nil {
		return nfsErrorWithPostOp(reply, mapError(err)), nil
	}
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nfsErrorWithPostOp(reply, NFSERR_IO), nil
	}

	buf.Write(cookieVerf[:])

	entryCount := 0
	maxReplySize := int(count) - 100
	if maxReplySize < 128 {
		maxReplySize = 128
	}
	reachedLimit := false

	for i, entry := range entries {
		if uint64(i) < cookie {
			continue
		}

		if buf.Len() >= maxReplySize {
			reachedLimit = true
			break
		}

		// Skip entries with nil attrs
		entry.mu.RLock()
		if entry.attrs == nil {
			entry.mu.RUnlock()
			continue
		}
		fileId := entry.attrs.FileId
		entry.mu.RUnlock()

		xdrEncodeUint32(&buf, 1)

		// R4: Copy fileId under RLock
		if err := xdrEncodeUint64(&buf, fileId); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}

		// M1: Use path.Base() for name extraction
		name := path.Base(entry.path)
		if entry.path == "/" {
			name = "/"
		}
		if err := xdrEncodeString(&buf, name); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}

		entryCookie := uint64(i + 1)
		if err := xdrEncodeUint64(&buf, entryCookie); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}

		entryCount++
	}

	xdrEncodeUint32(&buf, 0)

	if !reachedLimit {
		xdrEncodeUint32(&buf, 1) // EOF
	} else {
		xdrEncodeUint32(&buf, 0)
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleReaddirplus handles NFSPROC3_READDIRPLUS - read directory with attributes
func (h *NFSProcedureHandler) handleReaddirplus(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	var cookie uint64
	var cookieVerf [8]byte
	if err := binary.Read(body, binary.BigEndian, &cookie); err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}
	if _, err := io.ReadFull(body, cookieVerf[:]); err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	var dirCount, maxCount uint32
	if err := binary.Read(body, binary.BigEndian, &dirCount); err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}
	if err := binary.Read(body, binary.BigEndian, &maxCount); err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	// Rate limiting (after body consumption to prevent stream desync)
	if h.server.handler.rateLimiter != nil && h.server.handler.policy.Load().EnableRateLimiting {
		if !h.server.handler.rateLimiter.AllowOperation(authCtx.ClientIP, OpTypeReaddir) {
			if h.server.handler.metrics != nil {
				h.server.handler.metrics.RecordRateLimitExceeded()
			}
			return nfsErrorWithPostOp(reply, NFSERR_DELAY), nil
		}
	}

	dir, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithPostOp(reply, NFSERR_STALE), nil
	}

	// R4: Copy attrs under RLock for dir check
	dir.mu.RLock()
	dirMode := dir.attrs.Mode
	dir.mu.RUnlock()

	if dirMode&os.ModeDir == 0 {
		return nfsErrorWithPostOp(reply, NFSERR_NOTDIR), nil
	}

	// R22: Return NFS error instead of nil,err
	entries, err := h.server.handler.ReadDirPlus(dir)
	if err != nil {
		return nfsErrorWithPostOp(reply, mapError(err)), nil
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)

	attrs, err := h.server.handler.GetAttr(dir)
	if err != nil {
		return nfsErrorWithPostOp(reply, mapError(err)), nil
	}
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nfsErrorWithPostOp(reply, NFSERR_IO), nil
	}

	buf.Write(cookieVerf[:])

	entryCount := 0
	reachedLimit := false
	maxReplySize := int(maxCount) - 200
	if maxReplySize < 256 {
		maxReplySize = 256
	}

	for i, entry := range entries {
		if uint64(i) < cookie {
			continue
		}

		if buf.Len() >= maxReplySize && entryCount > 0 {
			reachedLimit = true
			break
		}

		// Skip entries with nil attrs
		entry.mu.RLock()
		if entry.attrs == nil {
			entry.mu.RUnlock()
			continue
		}
		entryAttrsCopy := *entry.attrs
		entry.mu.RUnlock()

		xdrEncodeUint32(&buf, 1)

		entryCookie := uint64(i + 1)

		if err := xdrEncodeUint64(&buf, entryAttrsCopy.FileId); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}

		// M1: Use path.Base() for name extraction
		name := path.Base(entry.path)
		if entry.path == "/" {
			name = "/"
		}
		if err := xdrEncodeString(&buf, name); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}

		if err := xdrEncodeUint64(&buf, entryCookie); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}

		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, &entryAttrsCopy); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}

		// C3: Allocate handle only for the post_op_fh3 field in READDIRPLUS
		entryHandle := h.server.handler.fileMap.Allocate(entry)
		xdrEncodeUint32(&buf, 1)
		xdrEncodeFileHandle(&buf, entryHandle)

		entryCount++
	}

	xdrEncodeUint32(&buf, 0)

	if !reachedLimit {
		xdrEncodeUint32(&buf, 1)
	} else {
		xdrEncodeUint32(&buf, 0)
	}

	reply.Data = buf.Bytes()
	return reply, nil
}
