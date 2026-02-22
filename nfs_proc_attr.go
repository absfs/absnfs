// nfs_proc_attr.go: NFSv3 attribute and access operations.
//
// Implements the GETATTR, SETATTR, and ACCESS procedures as defined
// in RFC 1813 sections 3.3.1, 3.3.2, and 3.3.4. These handlers read
// and modify file/directory metadata and check client access permissions
// against the exported filesystem.
package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"time"
)

// handleGetattr handles NFSPROC3_GETATTR - get file attributes
func (h *NFSProcedureHandler) handleGetattr(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		if h.server.options.Debug {
			h.server.logger.Printf("GETATTR: Failed to decode handle: %v", err)
		}
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	if h.server.options.Debug {
		h.server.logger.Printf("GETATTR: Looking up handle %d, fileMap count: %d", handleVal, h.server.handler.fileMap.Count())
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		if h.server.options.Debug {
			h.server.logger.Printf("GETATTR: Handle %d not found in fileMap", handleVal)
		}
		return nfsErrorReply(reply, NFSERR_STALE), nil
	}

	// R22: Return NFS error instead of nil,err
	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorReply(reply, mapError(err)), nil
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nfsErrorReply(reply, NFSERR_IO), nil
	}
	reply.Data = buf.Bytes()
	return reply, nil
}

// handleSetattr handles NFSPROC3_SETATTR - set file attributes
//
// Per RFC 1813, SETATTR3args contains a file handle, sattr3, and an optional
// guard (sattrguard3). The sattr3 includes mode, uid, gid, size, atime, and
// mtime — each with a set flag. Setting size to 0 truncates the file, which
// is how shell redirects (>) clear a file before writing.
func (h *NFSProcedureHandler) handleSetattr(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	// R18: Check read-only before processing
	if h.server.handler.policy.Load().ReadOnly {
		return nfsErrorWithWcc(reply, NFSERR_ROFS), nil
	}

	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	sattr, err := decodeSattr3(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	// R8: Read sattrguard3 (RFC 1813 section 3.3.2). The guard is a
	// discriminated union: 0 = no guard, 1 = check ctime before applying.
	var guardCheck uint32
	if err := binary.Read(body, binary.BigEndian, &guardCheck); err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}
	var guardSec, guardNsec uint32
	if guardCheck != 0 {
		if err := binary.Read(body, binary.BigEndian, &guardSec); err != nil {
			return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
		}
		if err := binary.Read(body, binary.BigEndian, &guardNsec); err != nil {
			return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
		}
	}

	if sattr.SetMode && sattr.Mode&0x8000 != 0 {
		return nfsErrorWithWcc(reply, NFSERR_INVAL), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithWcc(reply, NFSERR_STALE), nil
	}

	preAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	// R8: Enforce sattrguard3 - compare guard ctime with current ctime
	if guardCheck != 0 {
		// ctime is represented as mtime in this implementation
		ctimeSec := uint32(preAttrs.Mtime().Unix())
		ctimeNsec := uint32(preAttrs.Mtime().Nanosecond())
		if guardSec != ctimeSec || guardNsec != ctimeNsec {
			return nfsErrorWithWcc(reply, NFSERR_NOT_SYNC), nil
		}
	}

	// Apply truncation before other attribute changes.
	// This is critical for file overwrites: the NFS client sends
	// SETATTR(size=0) before WRITE(offset=0, data) to clear old content.
	if sattr.SetSize {
		if sattr.Size > uint64(math.MaxInt64) {
			return nfsErrorWithWcc(reply, NFSERR_INVAL), nil
		}
		if err := node.Truncate(int64(sattr.Size)); err != nil {
			return nfsErrorWithWcc(reply, mapError(err)), nil
		}
		h.server.handler.attrCache.Invalidate(node.path)
		h.server.handler.readBuf.ClearPath(node.path)
		info, statErr := h.server.handler.fs.Stat(node.path)
		if statErr == nil {
			node.mu.Lock()
			node.attrs.Size = info.Size()
			node.attrs.SetMtime(info.ModTime())
			node.attrs.Refresh()
			node.mu.Unlock()
		}
	}

	node.mu.RLock()
	if node.attrs == nil {
		node.mu.RUnlock()
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}
	attrs := &NFSAttrs{
		Mode: node.attrs.Mode,
		Uid:  node.attrs.Uid,
		Gid:  node.attrs.Gid,
	}
	node.mu.RUnlock()

	if sattr.SetMode {
		attrs.Mode = os.FileMode(sattr.Mode)
	}
	if sattr.SetUID {
		if authCtx.EffectiveUID == 0 {
			attrs.Uid = sattr.UID
		}
		// else: silently ignore, non-root can't change UID
	}
	if sattr.SetGID {
		if authCtx.EffectiveUID == 0 {
			attrs.Gid = sattr.GID
		}
	}

	if sattr.SetAtime == 1 {
		attrs.SetAtime(time.Now())
	} else if sattr.SetAtime == 2 {
		attrs.SetAtime(time.Unix(int64(sattr.AtimeSec), int64(sattr.AtimeNsec)))
	}

	if sattr.SetMtime == 1 {
		attrs.SetMtime(time.Now())
	} else if sattr.SetMtime == 2 {
		attrs.SetMtime(time.Unix(int64(sattr.MtimeSec), int64(sattr.MtimeNsec)))
	}

	if err := h.server.handler.SetAttr(node, attrs); err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	postAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeWccData(&buf, preAttrs, postAttrs); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}
	reply.Data = buf.Bytes()
	return reply, nil
}

// handleAccess handles NFSPROC3_ACCESS - check access permission
func (h *NFSProcedureHandler) handleAccess(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	var access uint32
	if err := binary.Read(body, binary.BigEndian, &access); err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithPostOp(reply, NFSERR_STALE), nil
	}

	// R22: Return NFS error instead of nil,err
	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithPostOp(reply, mapError(err)), nil
	}

	// Get file attributes for permission checking
	fileMode := attrs.Mode
	fileUid := attrs.Uid
	fileGid := attrs.Gid
	isDir := attrs.Mode&os.ModeDir != 0

	// Use effective (squashed) UID/GID set by HandleCall authentication
	effectiveUID := authCtx.EffectiveUID
	effectiveGID := authCtx.EffectiveGID

	// Determine which permission bits apply (owner, group, other)
	var permBits os.FileMode
	if effectiveUID == fileUid {
		permBits = (fileMode >> 6) & 7 // owner bits
	} else if effectiveGID == fileGid {
		permBits = (fileMode >> 3) & 7 // group bits
	} else {
		// Check auxiliary groups
		isGroupMember := false
		if authCtx.AuthSys != nil {
			for _, auxGID := range authCtx.AuthSys.AuxGIDs {
				if auxGID == fileGid {
					isGroupMember = true
					break
				}
			}
		}
		if isGroupMember {
			permBits = (fileMode >> 3) & 7 // group bits
		} else {
			permBits = fileMode & 7 // other bits
		}
	}
	// Root (UID 0) gets all permissions
	if effectiveUID == 0 {
		permBits = 7
	}

	var accessAllowed uint32
	if access&ACCESS3_READ != 0 && permBits&4 != 0 {
		accessAllowed |= ACCESS3_READ
	}
	if access&ACCESS3_LOOKUP != 0 && isDir && permBits&1 != 0 {
		accessAllowed |= ACCESS3_LOOKUP
	}
	if access&ACCESS3_EXECUTE != 0 && permBits&1 != 0 {
		accessAllowed |= ACCESS3_EXECUTE
	}
	if !h.server.handler.policy.Load().ReadOnly {
		if access&ACCESS3_MODIFY != 0 && permBits&2 != 0 {
			accessAllowed |= ACCESS3_MODIFY
		}
		if access&ACCESS3_EXTEND != 0 && permBits&2 != 0 {
			accessAllowed |= ACCESS3_EXTEND
		}
		if access&ACCESS3_DELETE != 0 && isDir && permBits&2 != 0 {
			accessAllowed |= ACCESS3_DELETE
		}
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nfsErrorWithPostOp(reply, NFSERR_IO), nil
	}
	binary.Write(&buf, binary.BigEndian, accessAllowed)

	reply.Data = buf.Bytes()
	return reply, nil
}
