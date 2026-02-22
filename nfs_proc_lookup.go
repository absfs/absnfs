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

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithPostOp(reply, NFSERR_STALE), nil
	}

	node.mu.RLock()
	isDir := node.attrs.Mode&os.ModeDir != 0
	node.mu.RUnlock()

	if !isDir {
		// R4: Copy attrs under RLock
		node.mu.RLock()
		if node.attrs == nil {
			node.mu.RUnlock()
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}
		nodeAttrsCopy := *node.attrs
		node.mu.RUnlock()
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFSERR_NOTDIR)
		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, &nodeAttrsCopy); err != nil {
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
		// R4: Copy attrs under RLock
		node.mu.RLock()
		if node.attrs == nil {
			node.mu.RUnlock()
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}
		nodeAttrsCopy := *node.attrs
		node.mu.RUnlock()
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, mapError(err))
		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, &nodeAttrsCopy); err != nil {
			return nfsErrorWithPostOp(reply, NFSERR_IO), nil
		}
		reply.Data = buf.Bytes()
		return reply, nil
	}

	handle := h.server.handler.fileMap.Allocate(lookupNode)
	if h.server.options.Debug {
		h.server.logger.Printf("LOOKUP: Found '%s', allocated handle %d", lookupPath, handle)
	}

	// R4: Copy attrs under RLock
	lookupNode.mu.RLock()
	lookupAttrsCopy := *lookupNode.attrs
	lookupNode.mu.RUnlock()
	node.mu.RLock()
	nodeAttrsCopy := *node.attrs
	node.mu.RUnlock()

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeFileHandle(&buf, handle)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, &lookupAttrsCopy); err != nil {
		return nfsErrorWithPostOp(reply, NFSERR_IO), nil
	}
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, &nodeAttrsCopy); err != nil {
		return nfsErrorWithPostOp(reply, NFSERR_IO), nil
	}
	reply.Data = buf.Bytes()
	return reply, nil
}

// handleReadlink handles NFSPROC3_READLINK - read symbolic link
func (h *NFSProcedureHandler) handleReadlink(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithPostOp(reply, NFSERR_STALE), nil
	}

	node.mu.RLock()
	isSymlink := node.attrs.Mode&os.ModeSymlink != 0
	node.mu.RUnlock()

	if !isSymlink {
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
