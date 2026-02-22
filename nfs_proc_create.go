// nfs_proc_create.go: NFSv3 object creation operations.
//
// Implements the CREATE, MKDIR, SYMLINK, and MKNOD procedures as defined
// in RFC 1813 sections 3.3.8, 3.3.9, 3.3.10, and 3.3.11. These handlers
// create regular files, directories, symbolic links, and device nodes in
// the exported filesystem. All respect the read-only policy check.
package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"path"
	"strings"
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
	var isExclusive bool
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
		var verf [8]byte
		if _, err := io.ReadFull(body, verf[:]); err != nil {
			return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
		}
		isExclusive = true
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

	newNode, err := h.server.handler.Create(node, name, attrs)
	if err != nil {
		// For EXCLUSIVE creates, if file already exists, return success
		// (simplified idempotent behavior per RFC 1813 - full verifier comparison not implemented)
		if isExclusive && os.IsExist(err) {
			lookupPath := path.Join(node.path, name)
			existingNode, lookupErr := h.server.handler.Lookup(lookupPath)
			if lookupErr == nil {
				dirPostAttrs, _ := h.server.handler.GetAttr(node)
				if dirPostAttrs == nil {
					dirPostAttrs = dirPreAttrs
				}
				handle := h.server.handler.fileMap.Allocate(existingNode)
				existingNode.mu.RLock()
				existingAttrsCopy := *existingNode.attrs
				existingNode.mu.RUnlock()
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, NFS_OK)
				xdrEncodeUint32(&buf, 1)
				xdrEncodeFileHandle(&buf, handle)
				xdrEncodeUint32(&buf, 1)
				if err := encodeFileAttributes(&buf, &existingAttrsCopy); err != nil {
					return nfsErrorWithWcc(reply, NFSERR_IO), nil
				}
				if err := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); err != nil {
					return nfsErrorWithWcc(reply, NFSERR_IO), nil
				}
				reply.Data = buf.Bytes()
				return reply, nil
			}
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

	// Validate symlink target to prevent escape from export root
	if strings.HasPrefix(target, "/") {
		return nfsErrorWithWcc(reply, NFSERR_ACCES), nil
	}
	// Check for .. components that could escape the export
	for _, component := range strings.Split(target, "/") {
		if component == ".." {
			return nfsErrorWithWcc(reply, NFSERR_ACCES), nil
		}
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
