// nfs_proc_create.go: NFSv3 object creation operations.
//
// Implements the CREATE, MKDIR, SYMLINK, and MKNOD procedures as defined
// in RFC 1813 sections 3.3.8, 3.3.9, 3.3.10, and 3.3.11. These handlers
// create regular files, directories, symbolic links, and device nodes in
// the exported filesystem. All respect the read-only policy check.
package absnfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"time"
)

// handleCreate handles NFSPROC3_CREATE - create a file
func (h *NFSProcedureHandler) handleCreate(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	// R5: Check read-only before processing
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

	if status := validateFilename(name); status != NFS_OK {
		return nfsErrorWithWcc(reply, status), nil
	}

	var createHow uint32
	if err := binary.Read(body, binary.BigEndian, &createHow); err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	var mode uint32 = 0644
	// Use effective UID/GID from auth context as default for new files
	newUID := authCtx.EffectiveUID
	newGID := authCtx.EffectiveGID
	var verf [8]byte
	if createHow == 0 || createHow == 1 {
		sattr, err := decodeSattr3(body)
		if err != nil {
			return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
		}
		if sattr.SetMode {
			mode = sattr.Mode
		}
		// Only allow explicit UID/GID override if caller is root (not squashed)
		if sattr.SetUID && authCtx.EffectiveUID == 0 {
			newUID = sattr.UID
		}
		if sattr.SetGID && authCtx.EffectiveUID == 0 {
			newGID = sattr.GID
		}
	} else if createHow == 2 {
		// M14: Use io.ReadFull for the 8-byte EXCLUSIVE verifier
		if _, err := io.ReadFull(body, verf[:]); err != nil {
			return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
		}
	}

	if status := validateMode(mode, false); status != NFS_OK {
		return nfsErrorWithWcc(reply, status), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithWcc(reply, NFSERR_STALE), nil
	}

	// R23: Return NFS error instead of nil,err
	dirPreAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	attrs := &NFSAttrs{
		Mode: os.FileMode(mode),
		Uid:  newUID,
		Gid:  newGID,
	}

	// GUARDED (1) and EXCLUSIVE (2) must not touch an existing file.
	exclusive := createHow == 1 || createHow == 2
	newNode, err := h.server.handler.createWithContext(context.Background(), node, name, attrs, exclusive)
	if err == nil && createHow == 2 {
		newNode = h.stampVerifier(newNode, verf)
	}
	if err != nil && createHow == 2 && errors.Is(err, os.ErrExist) {
		// A retransmitted EXCLUSIVE CREATE finds the file it made: the
		// verifier is still in its times (RFC 1813 section 3.3.8).
		existing, lookupErr := h.server.handler.Lookup(path.Join(node.path, name))
		if lookupErr == nil && existing.attrs.Mtime().Equal(verifierTime(verf)) {
			newNode, err = existing, nil
		}
	}
	if err != nil {
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

	dirPostAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	handle := h.server.handler.fileMap.Allocate(newNode)

	// R4: Copy newNode attrs under RLock
	newNode.mu.RLock()
	newNodeAttrsCopy := *newNode.attrs
	newNode.mu.RUnlock()

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	xdrEncodeFileHandle(&buf, handle)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, &newNodeAttrsCopy); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}
	if err := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// verifierTime encodes an EXCLUSIVE CREATE verifier as a file time.
func verifierTime(verf [8]byte) time.Time {
	sec := binary.BigEndian.Uint32(verf[:4])
	nsec := binary.BigEndian.Uint32(verf[4:]) % 1e9
	return time.Unix(int64(sec), int64(nsec))
}

// stampVerifier stores the verifier of an EXCLUSIVE CREATE in the new file's
// times, so a retransmission of the same request can be told apart from a
// different client's create. The client sets the real times with SETATTR
// next. If the filesystem refuses, the create still succeeds; only a
// retransmission would then see NFS3ERR_EXIST.
func (h *NFSProcedureHandler) stampVerifier(n *NFSNode, verf [8]byte) *NFSNode {
	s := h.server.handler
	t := verifierTime(verf)
	if err := s.fs.Chtimes(n.path, t, t); err != nil {
		return n
	}
	s.attrCache.Invalidate(n.path)
	if stamped, err := s.Lookup(n.path); err == nil {
		return stamped
	}
	return n
}

// handleMkdir handles NFSPROC3_MKDIR - create a directory
func (h *NFSProcedureHandler) handleMkdir(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	// H2: Check read-only before processing
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

	if status := validateFilename(name); status != NFS_OK {
		return nfsErrorWithWcc(reply, status), nil
	}

	sattr, err := decodeSattr3(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	var mode uint32 = 0755
	if sattr.SetMode {
		mode = sattr.Mode
	}

	if status := validateMode(mode, true); status != NFS_OK {
		return nfsErrorWithWcc(reply, status), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithWcc(reply, NFSERR_STALE), nil
	}

	// R23: Return NFS error instead of nil,err
	dirPreAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	dirPath := path.Join(node.path, name)
	if err := h.server.handler.fs.Mkdir(dirPath, os.FileMode(mode)); err != nil {
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

	// Apply uid/gid: use effective UID/GID from auth context as default,
	// only allow explicit override if caller is root (not squashed).
	{
		chownUID := int(authCtx.EffectiveUID)
		chownGID := int(authCtx.EffectiveGID)
		if sattr.SetUID && authCtx.EffectiveUID == 0 {
			chownUID = int(sattr.UID)
		}
		if sattr.SetGID && authCtx.EffectiveUID == 0 {
			chownGID = int(sattr.GID)
		}
		if err := h.server.handler.fs.Chown(dirPath, chownUID, chownGID); err != nil {
			if h.server.options.Debug {
				h.server.logger.Printf("MKDIR: Chown failed for '%s': %v", dirPath, err)
			}
		}
	}
	h.server.handler.invalidate(node.path, dirPath)

	newNode, err := h.server.handler.Lookup(dirPath)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	dirPostAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	handle := h.server.handler.fileMap.Allocate(newNode)

	// R4: Copy newNode attrs under RLock
	newNode.mu.RLock()
	newNodeAttrsCopy := *newNode.attrs
	newNode.mu.RUnlock()

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	xdrEncodeFileHandle(&buf, handle)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, &newNodeAttrsCopy); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}
	if err := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleSymlink handles NFSPROC3_SYMLINK - create a symbolic link
func (h *NFSProcedureHandler) handleSymlink(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
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

	if status := validateFilename(name); status != NFS_OK {
		return nfsErrorWithWcc(reply, status), nil
	}

	sattr, err := decodeSattr3(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	target, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	if target == "" {
		return nfsErrorWithWcc(reply, NFSERR_INVAL), nil
	}

	// Unless the export allows any target, refuse targets that could lead
	// out of the export when the backend follows links on the host:
	// absolute ones and ones with ".." components.
	if !h.server.handler.policy.Load().AllowAnySymlinkTarget && !symlinkTargetContained(target) {
		return nfsErrorWithWcc(reply, NFSERR_ACCES), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithWcc(reply, NFSERR_STALE), nil
	}

	// R23: Return NFS error instead of nil,err
	dirPreAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	var mode uint32 = 0777
	if sattr.SetMode {
		// Strip type bits - symlink type is set by the operation itself
		mode = sattr.Mode & 07777
	}

	// Use effective UID/GID from auth context as default for new symlinks
	attrs := &NFSAttrs{
		Mode: os.FileMode(mode) | os.ModeSymlink,
		Uid:  authCtx.EffectiveUID,
		Gid:  authCtx.EffectiveGID,
	}
	// Only allow explicit UID/GID override if caller is root (not squashed)
	if sattr.SetUID && authCtx.EffectiveUID == 0 {
		attrs.Uid = sattr.UID
	}
	if sattr.SetGID && authCtx.EffectiveUID == 0 {
		attrs.Gid = sattr.GID
	}

	newNode, err := h.server.handler.Symlink(node, name, target, attrs)
	if err != nil {
		// H8: Include wcc_data in error response
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

	// R9: Use Lchown instead of Chown for symlinks to avoid following the link
	// Apply effective UID/GID, with sattr3 override only allowed for root
	{
		lchownUID := int(authCtx.EffectiveUID)
		lchownGID := int(authCtx.EffectiveGID)
		if sattr.SetUID && authCtx.EffectiveUID == 0 {
			lchownUID = int(sattr.UID)
		}
		if sattr.SetGID && authCtx.EffectiveUID == 0 {
			lchownGID = int(sattr.GID)
		}
		symlinkPath := path.Join(node.path, name)
		if err := h.server.handler.fs.Lchown(symlinkPath, lchownUID, lchownGID); err != nil {
			if h.server.options.Debug {
				h.server.logger.Printf("SYMLINK: Lchown failed for '%s': %v", symlinkPath, err)
			}
		} else {
			// The owner changed after Symlink cached the link's attributes.
			h.server.handler.invalidate(symlinkPath)
			if owned, err := h.server.handler.Lookup(symlinkPath); err == nil {
				newNode = owned
			}
		}
	}

	dirPostAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	handle := h.server.handler.fileMap.Allocate(newNode)

	// R4: Copy newNode attrs under RLock
	newNode.mu.RLock()
	newNodeAttrsCopy := *newNode.attrs
	newNode.mu.RUnlock()

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	xdrEncodeFileHandle(&buf, handle)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, &newNodeAttrsCopy); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}
	if err := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleMknod handles MKNOD (create special device) requests.
// Returns NFSERR_NOTSUPP since special device files are not supported.
func (h *NFSProcedureHandler) handleMknod(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	// Consume the MKNOD arguments to prevent stream desync
	// MKNOD3args: diropargs3(nfs_fh3 dir + filename3 name) + mknoddata3(ftype3 + union)
	xdrDecodeFileHandle(body) // dir handle
	xdrDecodeString(body)     // name
	xdrDecodeUint32(body)     // ftype3 discriminant

	return nfsErrorWithWcc(reply, NFSERR_NOTSUPP), nil
}

// symlinkTargetContained reports whether a symlink target can only lead
// down from the link's directory: it is relative and has no ".."
// component. Such links keep a backend that follows them on the host
// inside the export, whatever other links they pass through, because every
// link a client can make leads down.
func symlinkTargetContained(target string) bool {
	if strings.HasPrefix(target, "/") {
		return false
	}
	for _, component := range strings.Split(target, "/") {
		if component == ".." {
			return false
		}
	}
	return true
}
