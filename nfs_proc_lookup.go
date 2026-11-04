// nfs_proc_lookup.go: NFSv3 name resolution operations.
//
// Implements the LOOKUP and READLINK procedures as defined in RFC 1813
// sections 3.3.3 and 3.3.5. LOOKUP resolves a filename within a directory
// to a file handle; READLINK reads the target of a symbolic link.
package absnfs

import (
	"bytes"
	"io"
	"os"
	"path"
)

// handleLookup handles NFSPROC3_LOOKUP - look up filename
func (h *NFSProcedureHandler) handleLookup(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	name, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	// C4: Validate filename to prevent directory traversal
	if status := validateFilename(name); status != NFS_OK {
		return nfsErrorWithPostOp(reply, NFSERR_ACCES), nil
	}

	node, st := h.lookupNode(handleVal)
	if st != NFS_OK {
		return nfsErrorWithPostOp(reply, st), nil
	}

	// The directory as it is now, not as it was when its handle was
	// issued: one replaced by a symbolic link must not be looked through.
	// One that is gone reports the attributes the handle last had.
	dirAttrs, err := h.server.handler.freshAttrs(node)
	status := uint32(NFSERR_NOTDIR)
	if err != nil {
		status, dirAttrs = mapError(err), h.currentAttrs(node)
	}
	if err != nil || !dirAttrs.Mode.IsDir() {
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, status)
		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, dirAttrs); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}
		reply.Data = buf.Bytes()
		return reply, nil
	}

	lookupPath := path.Join(node.path, name)
	if h.server.options.Debug {
		h.server.logger.Printf("LOOKUP: Looking up '%s'", lookupPath)
	}

	lookupNode, err := h.server.handler.Lookup(lookupPath)
	if err != nil {
		if h.server.options.Debug {
			h.server.logger.Printf("LOOKUP: '%s' not found: %v", lookupPath, err)
		}
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, mapError(err))
		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, dirAttrs); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}
		reply.Data = buf.Bytes()
		return reply, nil
	}

	handle := h.server.handler.fileMap.issue(lookupNode)
	if h.server.options.Debug {
		h.server.logger.Printf("LOOKUP: Found '%s', handle %x", lookupPath, handle)
	}

	// R4: Copy attrs under RLock
	lookupNode.mu.RLock()
	lookupAttrsCopy := *lookupNode.attrs
	lookupNode.mu.RUnlock()

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeFileHandle(&buf, handle)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, &lookupAttrsCopy); err != nil {
		return nfsErrorWithPostOp(reply, NFSERR_IO), nil
	}
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, dirAttrs); err != nil {
		return nfsErrorWithPostOp(reply, NFSERR_IO), nil
	}
	reply.Data = buf.Bytes()
	return reply, nil
}

// currentAttrs returns the attributes to report for node, as GETATTR would
// (from the attribute cache within its TTL), or the snapshot the handle
// holds when they cannot be read, as when the object is gone.
func (h *NFSProcedureHandler) currentAttrs(node *NFSNode) *NFSAttrs {
	if current, err := h.server.handler.GetAttr(node); err == nil {
		return current
	}
	node.mu.RLock()
	defer node.mu.RUnlock()
	attrs := *node.attrs
	return &attrs
}

// handleReadlink handles NFSPROC3_READLINK - read symbolic link
func (h *NFSProcedureHandler) handleReadlink(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	node, st := h.lookupNode(handleVal)
	if st != NFS_OK {
		return nfsErrorWithPostOp(reply, st), nil
	}

	// Whether it is a symbolic link now, not when its handle was issued.
	current, err := h.server.handler.freshAttrs(node)
	if err != nil {
		return nfsErrorWithPostOp(reply, mapError(err)), nil
	}
	if current.Mode&os.ModeSymlink == 0 {
		return nfsErrorWithPostOp(reply, NFSERR_INVAL), nil
	}

	// R22: Return NFS error instead of nil,err
	target, err := h.server.handler.Readlink(node)
	if err != nil {
		return nfsErrorWithPostOp(reply, mapError(err)), nil
	}

	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithPostOp(reply, mapError(err)), nil
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nfsErrorWithPostOp(reply, NFSERR_IO), nil
	}
	if err := xdrEncodeString(&buf, target); err != nil {
		return nfsErrorWithPostOp(reply, NFSERR_IO), nil
	}
	reply.Data = buf.Bytes()
	return reply, nil
}
