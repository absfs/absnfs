// nfs_proc_dir.go: NFSv3 directory listing operations.
//
// Implements the READDIR and READDIRPLUS procedures as defined in
// RFC 1813 sections 3.3.16 and 3.3.17. READDIR returns directory entry
// names and file IDs; READDIRPLUS additionally returns full attributes
// and file handles, reducing the need for follow-up LOOKUP/GETATTR calls.
package absnfs

import (
	"bytes"
	"cmp"
	"encoding/binary"
	"io"
	"os"
	"path"
	"slices"
)

// dirEntry is one directory entry as READDIR and READDIRPLUS report it.
type dirEntry struct {
	node   *NFSNode
	name   string
	attrs  NFSAttrs // copied under the node's lock
	cookie uint64
}

// dirCookie returns the READDIR cookie for the entry at p.
//
// Cookies are derived from the entry's path, not its position in the
// listing, and entries are returned in cookie order. A client that resumes
// after a cookie therefore continues where it left off even when entries
// before it were removed in the meantime (an incremental rm -rf), the way
// hash-ordered directories on local filesystems behave. The value is kept
// below 2^62 so clients that store cookies as signed offsets are safe, and it
// is never 0, which means "start of directory".
func dirCookie(p string) uint64 {
	return fileID(p)>>2 + 1
}

// entriesAfter returns the entries of a listing whose cookie is greater than
// cookie, in cookie order. Entries without attributes are left out.
func entriesAfter(nodes []*NFSNode, cookie uint64) []dirEntry {
	out := make([]dirEntry, 0, len(nodes))
	for _, n := range nodes {
		n.mu.RLock()
		if n.attrs == nil {
			n.mu.RUnlock()
			continue
		}
		e := dirEntry{node: n, name: path.Base(n.path), attrs: *n.attrs, cookie: dirCookie(n.path)}
		n.mu.RUnlock()
		if e.cookie > cookie {
			out = append(out, e)
		}
	}
	slices.SortFunc(out, func(a, b dirEntry) int {
		return cmp.Or(cmp.Compare(a.cookie, b.cookie), cmp.Compare(a.name, b.name))
	})
	return out
}

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

	for _, entry := range entriesAfter(entries, cookie) {
		if buf.Len() >= maxReplySize {
			reachedLimit = true
			break
		}

		xdrEncodeUint32(&buf, 1)
		if err := xdrEncodeUint64(&buf, entry.attrs.FileId); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}
		if err := xdrEncodeString(&buf, entry.name); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}
		if err := xdrEncodeUint64(&buf, entry.cookie); err != nil {
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

	for _, entry := range entriesAfter(entries, cookie) {
		if buf.Len() >= maxReplySize && entryCount > 0 {
			reachedLimit = true
			break
		}

		xdrEncodeUint32(&buf, 1)
		if err := xdrEncodeUint64(&buf, entry.attrs.FileId); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}
		if err := xdrEncodeString(&buf, entry.name); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}
		if err := xdrEncodeUint64(&buf, entry.cookie); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}

		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, &entry.attrs); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}

		// C3: Allocate handle only for the post_op_fh3 field in READDIRPLUS
		entryHandle := h.server.handler.fileMap.Allocate(entry.node)
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
