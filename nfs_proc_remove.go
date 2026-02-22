// nfs_proc_remove.go: NFSv3 object removal and renaming operations.
//
// Implements the REMOVE, RMDIR, RENAME, and LINK procedures as defined
// in RFC 1813 sections 3.3.12, 3.3.13, 3.3.14, and 3.3.15. These
// handlers delete files and directories, rename entries, and create
// hard links within the exported filesystem.
package absnfs

import (
	"bytes"
	"io"
	"os"
	"path"
)

// handleRemove handles NFSPROC3_REMOVE - remove a file
func (h *NFSProcedureHandler) handleRemove(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	if h.server.handler.policy.Load().ReadOnly {
		return nfsErrorWithWcc(reply, NFSERR_ROFS), nil
	}

	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	name, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	// R6: Validate filename for REMOVE
	if status := validateFilename(name); status != NFS_OK {
		return nfsErrorWithWcc(reply, status), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithWcc(reply, NFSERR_STALE), nil
	}

	node.mu.RLock()
	isDir := node.attrs.Mode&os.ModeDir != 0
	node.mu.RUnlock()

	if !isDir {
		return nfsErrorWithWcc(reply, NFSERR_NOTDIR), nil
	}

	// R23: Return NFS error instead of nil,err
	dirPreAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	if h.server.options.Debug {
		h.server.logger.Printf("REMOVE: Removing '%s' from directory '%s'", name, node.path)
	}

	if err := h.server.handler.Remove(node, name); err != nil {
		if h.server.options.Debug {
			h.server.logger.Printf("REMOVE: Failed to remove '%s': %v", name, err)
		}
		dirPostAttrs, _ := h.server.handler.GetAttr(node)
		if dirPostAttrs == nil {
			dirPostAttrs = dirPreAttrs
		}

		var buf bytes.Buffer
		xdrEncodeUint32(&buf, mapError(err))
		if wccErr := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); wccErr != nil {
			return nfsErrorWithWcc(reply, mapError(err)), nil
		}
		reply.Data = buf.Bytes()
		return reply, nil
	}

	if h.server.options.Debug {
		h.server.logger.Printf("REMOVE: Successfully removed '%s' from '%s'", name, node.path)
	}

	dirPostAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleRmdir handles NFSPROC3_RMDIR - remove a directory
func (h *NFSProcedureHandler) handleRmdir(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	if h.server.handler.policy.Load().ReadOnly {
		return nfsErrorWithWcc(reply, NFSERR_ROFS), nil
	}

	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	name, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	// C5: Validate filename to prevent directory traversal
	if status := validateFilename(name); status != NFS_OK {
		return nfsErrorWithWcc(reply, NFSERR_ACCES), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithWcc(reply, NFSERR_STALE), nil
	}

	node.mu.RLock()
	isDir := node.attrs.Mode&os.ModeDir != 0
	node.mu.RUnlock()

	if !isDir {
		return nfsErrorWithWcc(reply, NFSERR_NOTDIR), nil
	}

	// R23: Return NFS error instead of nil,err
	dirPreAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	targetPath := path.Join(node.path, name)
	targetInfo, err := h.server.handler.fs.Stat(targetPath)
	if err != nil {
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFSERR_NOENT)
		if wccErr := encodeWccData(&buf, dirPreAttrs, dirPreAttrs); wccErr != nil {
			return nfsErrorWithWcc(reply, NFSERR_NOENT), nil
		}
		reply.Data = buf.Bytes()
		return reply, nil
	}

	if !targetInfo.IsDir() {
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFSERR_NOTDIR)
		if wccErr := encodeWccData(&buf, dirPreAttrs, dirPreAttrs); wccErr != nil {
			return nfsErrorWithWcc(reply, NFSERR_NOTDIR), nil
		}
		reply.Data = buf.Bytes()
		return reply, nil
	}

	if err := h.server.handler.fs.Remove(targetPath); err != nil {
		dirPostAttrs, _ := h.server.handler.GetAttr(node)
		if dirPostAttrs == nil {
			dirPostAttrs = dirPreAttrs
		}

		var errCode uint32
		if os.IsPermission(err) {
			errCode = NFSERR_ACCES
		} else if os.IsNotExist(err) {
			errCode = NFSERR_NOENT
		} else {
			errCode = mapError(err)
			// mapError maps os.IsExist errors to NFSERR_EXIST, but for directory
			// removal the most common cause of os.IsExist is "directory not empty"
			if errCode == NFSERR_EXIST || errCode == NFSERR_IO {
				errCode = NFSERR_NOTEMPTY
			}
		}

		var buf bytes.Buffer
		xdrEncodeUint32(&buf, errCode)
		if wccErr := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); wccErr != nil {
			return nfsErrorWithWcc(reply, errCode), nil
		}
		reply.Data = buf.Bytes()
		return reply, nil
	}

	// Invalidate caches for removed directory and parent
	h.server.handler.attrCache.Invalidate(targetPath)
	h.server.handler.attrCache.Invalidate(node.path)
	if h.server.handler.dirCache != nil {
		h.server.handler.dirCache.Invalidate(node.path)
		h.server.handler.dirCache.Invalidate(targetPath)
	}

	dirPostAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleRename handles NFSPROC3_RENAME - rename a file or directory
func (h *NFSProcedureHandler) handleRename(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	if h.server.handler.policy.Load().ReadOnly {
		return nfsErrorWithDoubleWcc(reply, NFSERR_ROFS), nil
	}

	srcHandleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithDoubleWcc(reply, GARBAGE_ARGS), nil
	}

	srcName, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorWithDoubleWcc(reply, GARBAGE_ARGS), nil
	}

	// R7: Validate source filename
	if status := validateFilename(srcName); status != NFS_OK {
		return nfsErrorWithDoubleWcc(reply, status), nil
	}

	dstHandleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithDoubleWcc(reply, GARBAGE_ARGS), nil
	}

	dstName, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorWithDoubleWcc(reply, GARBAGE_ARGS), nil
	}

	// R7: Validate destination filename
	if status := validateFilename(dstName); status != NFS_OK {
		return nfsErrorWithDoubleWcc(reply, status), nil
	}

	srcDir, ok := h.lookupNode(srcHandleVal)
	if !ok {
		return nfsErrorWithDoubleWcc(reply, NFSERR_STALE), nil
	}

	dstDir, ok := h.lookupNode(dstHandleVal)
	if !ok {
		return nfsErrorWithDoubleWcc(reply, NFSERR_STALE), nil
	}

	// R23: Return NFS error instead of nil,err
	srcDirPreAttrs, err := h.server.handler.GetAttr(srcDir)
	if err != nil {
		return nfsErrorWithDoubleWcc(reply, mapError(err)), nil
	}

	dstDirPreAttrs, err := h.server.handler.GetAttr(dstDir)
	if err != nil {
		return nfsErrorWithDoubleWcc(reply, mapError(err)), nil
	}

	if err := h.server.handler.Rename(srcDir, srcName, dstDir, dstName); err != nil {
		srcDirPostAttrs, _ := h.server.handler.GetAttr(srcDir)
		if srcDirPostAttrs == nil {
			srcDirPostAttrs = srcDirPreAttrs
		}
		dstDirPostAttrs, _ := h.server.handler.GetAttr(dstDir)
		if dstDirPostAttrs == nil {
			dstDirPostAttrs = dstDirPreAttrs
		}

		errCode := mapError(err)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, errCode)
		if wccErr := encodeWccData(&buf, srcDirPreAttrs, srcDirPostAttrs); wccErr != nil {
			return nfsErrorWithDoubleWcc(reply, errCode), nil
		}
		if wccErr := encodeWccData(&buf, dstDirPreAttrs, dstDirPostAttrs); wccErr != nil {
			return nfsErrorWithDoubleWcc(reply, errCode), nil
		}
		reply.Data = buf.Bytes()
		return reply, nil
	}

	srcDirPostAttrs, err := h.server.handler.GetAttr(srcDir)
	if err != nil {
		return nfsErrorWithDoubleWcc(reply, mapError(err)), nil
	}

	dstDirPostAttrs, err := h.server.handler.GetAttr(dstDir)
	if err != nil {
		return nfsErrorWithDoubleWcc(reply, mapError(err)), nil
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeWccData(&buf, srcDirPreAttrs, srcDirPostAttrs); err != nil {
		return nfsErrorWithDoubleWcc(reply, NFSERR_IO), nil
	}
	if err := encodeWccData(&buf, dstDirPreAttrs, dstDirPostAttrs); err != nil {
		return nfsErrorWithDoubleWcc(reply, NFSERR_IO), nil
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleLink handles NFSPROC3_LINK - create hard link (not supported)
// R2: LINK3resfail format is status + post_op_attr + wcc_data (not just wcc_data)
func (h *NFSProcedureHandler) handleLink(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	// Consume the LINK arguments to prevent stream desync
	// LINK3args: nfs_fh3 file + diropargs3(nfs_fh3 dir + filename3 name)
	xdrDecodeFileHandle(body) // file handle
	xdrDecodeFileHandle(body) // dir handle
	xdrDecodeString(body)     // name

	notSupported := &NotSupportedError{
		Operation: "LINK",
		Reason:    "hard links are not supported by this NFS implementation",
	}
	return nfsErrorWithPostOpAndWcc(reply, mapError(notSupported)), nil
}
