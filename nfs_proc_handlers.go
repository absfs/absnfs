package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path"
)

// handleNull handles NFSPROC3_NULL - a no-op procedure for testing connectivity
func (h *NFSProcedureHandler) handleNull(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	return reply, nil
}

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
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nil, err
	}
	reply.Data = buf.Bytes()
	return reply, nil
}

// handleSetattr handles NFSPROC3_SETATTR - set file attributes
func (h *NFSProcedureHandler) handleSetattr(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	// Read attribute flags and values
	var setMode, setUid, setGid uint32
	var mode, uid, gid uint32
	if err := binary.Read(body, binary.BigEndian, &setMode); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}
	if setMode != 0 {
		if err := binary.Read(body, binary.BigEndian, &mode); err != nil {
			return nfsErrorReply(reply, GARBAGE_ARGS), nil
		}
		if mode&0x8000 != 0 {
			return nfsErrorReply(reply, NFSERR_INVAL), nil
		}
	}

	if err := binary.Read(body, binary.BigEndian, &setUid); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}
	if setUid != 0 {
		if err := binary.Read(body, binary.BigEndian, &uid); err != nil {
			return nfsErrorReply(reply, GARBAGE_ARGS), nil
		}
	}

	if err := binary.Read(body, binary.BigEndian, &setGid); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}
	if setGid != 0 {
		if err := binary.Read(body, binary.BigEndian, &gid); err != nil {
			return nfsErrorReply(reply, GARBAGE_ARGS), nil
		}
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	preAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	node.mu.RLock()
	attrs := &NFSAttrs{
		Mode: node.attrs.Mode,
		Uid:  node.attrs.Uid,
		Gid:  node.attrs.Gid,
	}
	node.mu.RUnlock()

	if setMode != 0 {
		attrs.Mode = os.FileMode(mode)
	}
	if setUid != 0 {
		attrs.Uid = uid
	}
	if setGid != 0 {
		attrs.Gid = gid
	}

	if err := h.server.handler.SetAttr(node, attrs); err != nil {
		return nil, err
	}

	postAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeWccData(&buf, preAttrs, postAttrs); err != nil {
		return nil, err
	}
	reply.Data = buf.Bytes()
	return reply, nil
}

// handleLookup handles NFSPROC3_LOOKUP - look up filename
func (h *NFSProcedureHandler) handleLookup(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	name, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFSERR_NOENT)
		xdrEncodeUint32(&buf, 0) // dir_attributes: attributes_follow = FALSE
		reply.Data = buf.Bytes()
		return reply, nil
	}

	node.mu.RLock()
	isDir := node.attrs.Mode&os.ModeDir != 0
	node.mu.RUnlock()

	if !isDir {
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFSERR_NOTDIR)
		xdrEncodeUint32(&buf, 1)
		encodeFileAttributes(&buf, node.attrs)
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
		xdrEncodeUint32(&buf, NFSERR_NOENT)
		xdrEncodeUint32(&buf, 1)
		encodeFileAttributes(&buf, node.attrs)
		reply.Data = buf.Bytes()
		return reply, nil
	}

	handle := h.server.handler.fileMap.Allocate(lookupNode)
	if h.server.options.Debug {
		h.server.logger.Printf("LOOKUP: Found '%s', allocated handle %d", lookupPath, handle)
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeFileHandle(&buf, handle)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, lookupNode.attrs); err != nil {
		return nil, err
	}
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, node.attrs); err != nil {
		return nil, err
	}
	reply.Data = buf.Bytes()
	return reply, nil
}

// handleReadlink handles NFSPROC3_READLINK - read symbolic link
func (h *NFSProcedureHandler) handleReadlink(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	node.mu.RLock()
	isSymlink := node.attrs.Mode&os.ModeSymlink != 0
	node.mu.RUnlock()

	if !isSymlink {
		return nfsErrorReply(reply, NFSERR_INVAL), nil
	}

	target, err := h.server.handler.Readlink(node)
	if err != nil {
		return nil, err
	}

	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nil, err
	}
	if err := xdrEncodeString(&buf, target); err != nil {
		return nil, err
	}
	reply.Data = buf.Bytes()
	return reply, nil
}

// handleRead handles NFSPROC3_READ - read from file
func (h *NFSProcedureHandler) handleRead(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	var offset uint64
	var count uint32
	if err := binary.Read(body, binary.BigEndian, &offset); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}
	if err := binary.Read(body, binary.BigEndian, &count); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	if offset > math.MaxUint64-uint64(count) {
		return nfsErrorReply(reply, NFSERR_INVAL), nil
	}

	// Rate limiting for large reads
	if count > 65536 && h.server.handler.rateLimiter != nil && h.server.handler.options.EnableRateLimiting {
		if !h.server.handler.rateLimiter.AllowOperation(authCtx.ClientIP, OpTypeReadLarge) {
			if h.server.handler.metrics != nil {
				h.server.handler.metrics.RecordRateLimitExceeded()
			}
			return nfsErrorReply(reply, NFSERR_DELAY), nil
		}
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	data, err := h.server.handler.Read(node, int64(offset), int64(count))
	if err != nil {
		return nil, err
	}

	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nil, err
	}
	xdrEncodeUint32(&buf, uint32(len(data)))

	if int64(offset)+int64(len(data)) >= attrs.Size {
		xdrEncodeUint32(&buf, 1) // EOF = TRUE
	} else {
		xdrEncodeUint32(&buf, 0) // EOF = FALSE
	}

	xdrEncodeUint32(&buf, uint32(len(data)))
	buf.Write(data)
	padding := (4 - (len(data) % 4)) % 4
	if padding > 0 {
		buf.Write(make([]byte, padding))
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleWrite handles NFSPROC3_WRITE - write to file
func (h *NFSProcedureHandler) handleWrite(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	if h.server.handler.options.ReadOnly {
		return nfsErrorReply(reply, ACCESS_DENIED), nil
	}

	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	var offset uint64
	var count, stable uint32
	if err := binary.Read(body, binary.BigEndian, &offset); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}
	if err := binary.Read(body, binary.BigEndian, &count); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}
	if err := binary.Read(body, binary.BigEndian, &stable); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	if offset > math.MaxUint64-uint64(count) {
		return nfsErrorReply(reply, NFSERR_INVAL), nil
	}

	// Rate limiting for large writes
	if count > 65536 && h.server.handler.rateLimiter != nil && h.server.handler.options.EnableRateLimiting {
		if !h.server.handler.rateLimiter.AllowOperation(authCtx.ClientIP, OpTypeWriteLarge) {
			if h.server.handler.metrics != nil {
				h.server.handler.metrics.RecordRateLimitExceeded()
			}
			return nfsErrorReply(reply, NFSERR_DELAY), nil
		}
	}

	var dataLen uint32
	if err := binary.Read(body, binary.BigEndian, &dataLen); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}
	if dataLen != count {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	data := make([]byte, count)
	if _, err := io.ReadFull(body, data); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	if h.server.options.Debug {
		h.server.logger.Printf("WRITE: handle=%d path='%s' offset=%d count=%d stable=%d", handleVal, node.path, offset, count, stable)
	}

	preAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	n, err := h.server.handler.Write(node, int64(offset), data)
	if err != nil {
		if h.server.options.Debug {
			h.server.logger.Printf("WRITE: Failed to write to '%s': %v", node.path, err)
		}
		postAttrs, _ := h.server.handler.GetAttr(node)
		if postAttrs == nil {
			postAttrs = preAttrs
		}

		var buf bytes.Buffer
		xdrEncodeUint32(&buf, mapError(err))
		if err := encodeWccData(&buf, preAttrs, postAttrs); err != nil {
			return nil, err
		}
		reply.Data = buf.Bytes()
		return reply, nil
	}

	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	if h.server.options.Debug {
		h.server.logger.Printf("WRITE: Success, wrote %d bytes to '%s'", n, node.path)
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeWccData(&buf, preAttrs, attrs); err != nil {
		return nil, err
	}
	xdrEncodeUint32(&buf, uint32(n))
	xdrEncodeUint32(&buf, stable)
	buf.Write(make([]byte, 8)) // writeverf

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleCreate handles NFSPROC3_CREATE - create a file
func (h *NFSProcedureHandler) handleCreate(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	name, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	if status := validateFilename(name); status != NFS_OK {
		return nfsErrorReply(reply, status), nil
	}

	var createHow uint32
	if err := binary.Read(body, binary.BigEndian, &createHow); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	var mode uint32 = 0644
	if createHow == 0 || createHow == 1 {
		// Parse sattr3
		var setMode uint32
		if err := binary.Read(body, binary.BigEndian, &setMode); err != nil {
			return nfsErrorReply(reply, GARBAGE_ARGS), nil
		}
		if setMode != 0 {
			if err := binary.Read(body, binary.BigEndian, &mode); err != nil {
				return nfsErrorReply(reply, GARBAGE_ARGS), nil
			}
		}

		// Skip remaining sattr3 fields
		var setUid uint32
		if err := binary.Read(body, binary.BigEndian, &setUid); err != nil {
			return nfsErrorReply(reply, GARBAGE_ARGS), nil
		}
		if setUid != 0 {
			var uid uint32
			binary.Read(body, binary.BigEndian, &uid)
		}

		var setGid uint32
		if err := binary.Read(body, binary.BigEndian, &setGid); err != nil {
			return nfsErrorReply(reply, GARBAGE_ARGS), nil
		}
		if setGid != 0 {
			var gid uint32
			binary.Read(body, binary.BigEndian, &gid)
		}

		var setSize uint32
		if err := binary.Read(body, binary.BigEndian, &setSize); err != nil {
			return nfsErrorReply(reply, GARBAGE_ARGS), nil
		}
		if setSize != 0 {
			var size uint64
			binary.Read(body, binary.BigEndian, &size)
		}

		var setAtime uint32
		if err := binary.Read(body, binary.BigEndian, &setAtime); err != nil {
			return nfsErrorReply(reply, GARBAGE_ARGS), nil
		}
		if setAtime == 2 {
			var atimeSec, atimeNsec uint32
			binary.Read(body, binary.BigEndian, &atimeSec)
			binary.Read(body, binary.BigEndian, &atimeNsec)
		}

		var setMtime uint32
		if err := binary.Read(body, binary.BigEndian, &setMtime); err != nil {
			return nfsErrorReply(reply, GARBAGE_ARGS), nil
		}
		if setMtime == 2 {
			var mtimeSec, mtimeNsec uint32
			binary.Read(body, binary.BigEndian, &mtimeSec)
			binary.Read(body, binary.BigEndian, &mtimeNsec)
		}
	} else if createHow == 2 {
		var verf [8]byte
		body.Read(verf[:])
	}

	if status := validateMode(mode, false); status != NFS_OK {
		return nfsErrorReply(reply, status), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	dirPreAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	attrs := &NFSAttrs{
		Mode: os.FileMode(mode),
		Uid:  0,
		Gid:  0,
	}

	newNode, err := h.server.handler.Create(node, name, attrs)
	if err != nil {
		dirPostAttrs, _ := h.server.handler.GetAttr(node)
		if dirPostAttrs == nil {
			dirPostAttrs = dirPreAttrs
		}

		var buf bytes.Buffer
		xdrEncodeUint32(&buf, mapError(err))
		encodeWccData(&buf, dirPreAttrs, dirPostAttrs)
		reply.Data = buf.Bytes()
		return reply, nil
	}

	dirPostAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	handle := h.server.handler.fileMap.Allocate(newNode)

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	xdrEncodeFileHandle(&buf, handle)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, newNode.attrs); err != nil {
		return nil, err
	}
	if err := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); err != nil {
		return nil, err
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleMkdir handles NFSPROC3_MKDIR - create a directory
func (h *NFSProcedureHandler) handleMkdir(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	name, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	if status := validateFilename(name); status != NFS_OK {
		return nfsErrorReply(reply, status), nil
	}

	var mode uint32
	if err := binary.Read(body, binary.BigEndian, &mode); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	if status := validateMode(mode, true); status != NFS_OK {
		return nfsErrorReply(reply, status), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	dirPreAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	if err := h.server.handler.fs.Mkdir(path.Join(node.path, name), os.FileMode(mode)); err != nil {
		dirPostAttrs, _ := h.server.handler.GetAttr(node)
		if dirPostAttrs == nil {
			dirPostAttrs = dirPreAttrs
		}

		var buf bytes.Buffer
		xdrEncodeUint32(&buf, mapError(err))
		encodeWccData(&buf, dirPreAttrs, dirPostAttrs)
		reply.Data = buf.Bytes()
		return reply, nil
	}

	newNode, err := h.server.handler.Lookup(path.Join(node.path, name))
	if err != nil {
		return nil, err
	}

	dirPostAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	handle := h.server.handler.fileMap.Allocate(newNode)

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	xdrEncodeFileHandle(&buf, handle)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, newNode.attrs); err != nil {
		return nil, err
	}
	if err := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); err != nil {
		return nil, err
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleSymlink handles NFSPROC3_SYMLINK - create a symbolic link
func (h *NFSProcedureHandler) handleSymlink(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	if h.server.handler.options.ReadOnly {
		return nfsErrorReply(reply, ACCESS_DENIED), nil
	}

	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	name, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	if status := validateFilename(name); status != NFS_OK {
		return nfsErrorReply(reply, status), nil
	}

	var mode uint32
	if err := binary.Read(body, binary.BigEndian, &mode); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	target, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	if target == "" {
		return nfsErrorReply(reply, NFSERR_INVAL), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	dirPreAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	attrs := &NFSAttrs{
		Mode: os.FileMode(mode) | os.ModeSymlink,
		Uid:  0,
		Gid:  0,
	}

	newNode, err := h.server.handler.Symlink(node, name, target, attrs)
	if err != nil {
		return nil, err
	}

	dirPostAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	handle := h.server.handler.fileMap.Allocate(newNode)

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	xdrEncodeFileHandle(&buf, handle)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, newNode.attrs); err != nil {
		return nil, err
	}
	if err := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); err != nil {
		return nil, err
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleReaddir handles NFSPROC3_READDIR - read directory entries
func (h *NFSProcedureHandler) handleReaddir(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	// Rate limiting
	if h.server.handler.rateLimiter != nil && h.server.handler.options.EnableRateLimiting {
		if !h.server.handler.rateLimiter.AllowOperation(authCtx.ClientIP, OpTypeReaddir) {
			if h.server.handler.metrics != nil {
				h.server.handler.metrics.RecordRateLimitExceeded()
			}
			return nfsErrorReply(reply, NFSERR_DELAY), nil
		}
	}

	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	var cookie uint64
	var cookieVerf [8]byte
	if err := binary.Read(body, binary.BigEndian, &cookie); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}
	if _, err := io.ReadFull(body, cookieVerf[:]); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	var count uint32
	if err := binary.Read(body, binary.BigEndian, &count); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	dir, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	if dir.attrs.Mode&os.ModeDir == 0 {
		return nfsErrorReply(reply, NFSERR_NOTDIR), nil
	}

	entries, err := h.server.handler.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)

	attrs, err := h.server.handler.GetAttr(dir)
	if err != nil {
		return nil, err
	}
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nil, err
	}

	buf.Write(cookieVerf[:])

	entryCount := 0
	maxReplySize := int(count) - 100
	reachedLimit := false

	for i, entry := range entries {
		if uint64(i) < cookie {
			continue
		}

		if buf.Len() >= maxReplySize {
			reachedLimit = true
			break
		}

		xdrEncodeUint32(&buf, 1)

		entryHandle := h.server.handler.fileMap.Allocate(entry)
		binary.Write(&buf, binary.BigEndian, entryHandle)

		name := ""
		if entry.path == "/" {
			name = "/"
		} else {
			lastSlash := 0
			for j := len(entry.path) - 1; j >= 0; j-- {
				if entry.path[j] == '/' {
					lastSlash = j
					break
				}
			}
			if lastSlash < len(entry.path)-1 {
				name = entry.path[lastSlash+1:]
			}
		}
		xdrEncodeString(&buf, name)

		entryCookie := uint64(i + 1)
		binary.Write(&buf, binary.BigEndian, entryCookie)

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
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	var cookie uint64
	var cookieVerf [8]byte
	if err := binary.Read(body, binary.BigEndian, &cookie); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}
	if _, err := io.ReadFull(body, cookieVerf[:]); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	var dirCount, maxCount uint32
	if err := binary.Read(body, binary.BigEndian, &dirCount); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}
	if err := binary.Read(body, binary.BigEndian, &maxCount); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	dir, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	if dir.attrs.Mode&os.ModeDir == 0 {
		return nfsErrorReply(reply, NFSERR_NOTDIR), nil
	}

	entries, err := h.server.handler.ReadDirPlus(dir)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)

	attrs, err := h.server.handler.GetAttr(dir)
	if err != nil {
		return nil, err
	}
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nil, err
	}

	buf.Write(cookieVerf[:])

	entryCount := 0
	reachedLimit := false
	maxReplySize := int(maxCount) - 200

	for i, entry := range entries {
		if uint64(i) < cookie {
			continue
		}

		if buf.Len() >= maxReplySize && entryCount > 0 {
			reachedLimit = true
			break
		}

		xdrEncodeUint32(&buf, 1)

		entryCookie := uint64(i + 1)
		entryHandle := h.server.handler.fileMap.Allocate(entry)
		binary.Write(&buf, binary.BigEndian, entryHandle)

		name := ""
		if entry.path == "/" {
			name = "/"
		} else {
			lastSlash := 0
			for j := len(entry.path) - 1; j >= 0; j-- {
				if entry.path[j] == '/' {
					lastSlash = j
					break
				}
			}
			if lastSlash < len(entry.path)-1 {
				name = entry.path[lastSlash+1:]
			}
		}
		xdrEncodeString(&buf, name)

		binary.Write(&buf, binary.BigEndian, entryCookie)

		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, entry.attrs); err != nil {
			return nil, err
		}

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

// handleFsstat handles NFSPROC3_FSSTAT - get filesystem statistics
func (h *NFSProcedureHandler) handleFsstat(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nil, err
	}

	binary.Write(&buf, binary.BigEndian, uint64(1024*1024*1024*10)) // tbytes
	binary.Write(&buf, binary.BigEndian, uint64(1024*1024*1024*5))  // fbytes
	binary.Write(&buf, binary.BigEndian, uint64(1024*1024*1024*5))  // abytes
	binary.Write(&buf, binary.BigEndian, uint64(1000000))           // tfiles
	binary.Write(&buf, binary.BigEndian, uint64(900000))            // ffiles
	binary.Write(&buf, binary.BigEndian, uint64(900000))            // afiles
	binary.Write(&buf, binary.BigEndian, uint32(0))                 // invarsec

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleFsinfo handles NFSPROC3_FSINFO - get filesystem info
func (h *NFSProcedureHandler) handleFsinfo(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nil, err
	}

	binary.Write(&buf, binary.BigEndian, uint32(1048576))       // rtmax
	binary.Write(&buf, binary.BigEndian, uint32(65536))         // rtpref
	binary.Write(&buf, binary.BigEndian, uint32(4096))          // rtmult
	binary.Write(&buf, binary.BigEndian, uint32(1048576))       // wtmax
	binary.Write(&buf, binary.BigEndian, uint32(65536))         // wtpref
	binary.Write(&buf, binary.BigEndian, uint32(4096))          // wtmult
	binary.Write(&buf, binary.BigEndian, uint64(8192))          // dtpref
	binary.Write(&buf, binary.BigEndian, uint64(1099511627776)) // maxfilesize
	binary.Write(&buf, binary.BigEndian, uint32(0))             // time_delta.seconds
	binary.Write(&buf, binary.BigEndian, uint32(1000000))       // time_delta.nseconds

	var properties uint32 = 0
	if h.server.handler.options.ReadOnly {
		properties |= 1
	}
	properties |= 2  // FSF_HOMOGENEOUS
	properties |= 4  // FSF_CANSETTIME
	properties |= 8  // FSF_SYMLINK
	properties |= 16 // FSF_HARDLINK
	binary.Write(&buf, binary.BigEndian, properties)

	reply.Data = buf.Bytes()
	return reply, nil
}

// handlePathconf handles NFSPROC3_PATHCONF - get path configuration
func (h *NFSProcedureHandler) handlePathconf(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nil, err
	}

	binary.Write(&buf, binary.BigEndian, uint32(1024)) // linkmax
	binary.Write(&buf, binary.BigEndian, uint32(255))  // name_max
	binary.Write(&buf, binary.BigEndian, uint32(1))    // no_trunc
	binary.Write(&buf, binary.BigEndian, uint32(1))    // chown_restricted
	binary.Write(&buf, binary.BigEndian, uint32(0))    // case_insensitive
	binary.Write(&buf, binary.BigEndian, uint32(1))    // case_preserving

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleAccess handles NFSPROC3_ACCESS - check access permission
func (h *NFSProcedureHandler) handleAccess(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	var access uint32
	if err := binary.Read(body, binary.BigEndian, &access); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	var accessAllowed uint32 = 0

	// ACCESS3_READ
	if access&1 != 0 {
		accessAllowed |= 1
	}

	// ACCESS3_LOOKUP (only for directories)
	if access&2 != 0 && attrs.Mode&os.ModeDir != 0 {
		accessAllowed |= 2
	}

	if !h.server.handler.options.ReadOnly {
		// ACCESS3_MODIFY
		if access&4 != 0 {
			accessAllowed |= 4
		}
		// ACCESS3_EXTEND
		if access&8 != 0 {
			accessAllowed |= 8
		}
		// ACCESS3_DELETE
		if access&16 != 0 {
			accessAllowed |= 16
		}
		// ACCESS3_EXECUTE
		if access&32 != 0 && (attrs.Mode&0111 != 0) {
			accessAllowed |= 32
		}
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nil, err
	}
	binary.Write(&buf, binary.BigEndian, accessAllowed)

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleCommit handles NFSPROC3_COMMIT - commit cached data
func (h *NFSProcedureHandler) handleCommit(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	var offset uint64
	var count uint32
	if err := binary.Read(body, binary.BigEndian, &offset); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}
	if err := binary.Read(body, binary.BigEndian, &count); err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFSERR_NOENT)
		xdrEncodeUint32(&buf, 0)
		xdrEncodeUint32(&buf, 0)
		reply.Data = buf.Bytes()
		return reply, nil
	}

	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 0) // pre_op_attr: no
	xdrEncodeUint32(&buf, 1) // post_op_attr: yes
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nil, err
	}
	buf.Write(make([]byte, 8)) // writeverf

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleRemove handles NFSPROC3_REMOVE - remove a file
func (h *NFSProcedureHandler) handleRemove(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	if h.server.handler.options.ReadOnly {
		return nfsErrorReply(reply, ACCESS_DENIED), nil
	}

	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	name, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	node.mu.RLock()
	isDir := node.attrs.Mode&os.ModeDir != 0
	node.mu.RUnlock()

	if !isDir {
		return nfsErrorReply(reply, NFSERR_NOTDIR), nil
	}

	dirPreAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
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
		encodeWccData(&buf, dirPreAttrs, dirPostAttrs)
		reply.Data = buf.Bytes()
		return reply, nil
	}

	if h.server.options.Debug {
		h.server.logger.Printf("REMOVE: Successfully removed '%s' from '%s'", name, node.path)
	}

	dirPostAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); err != nil {
		return nil, err
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleRmdir handles NFSPROC3_RMDIR - remove a directory
func (h *NFSProcedureHandler) handleRmdir(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	if h.server.handler.options.ReadOnly {
		return nfsErrorReply(reply, ACCESS_DENIED), nil
	}

	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	name, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	node.mu.RLock()
	isDir := node.attrs.Mode&os.ModeDir != 0
	node.mu.RUnlock()

	if !isDir {
		return nfsErrorReply(reply, NFSERR_NOTDIR), nil
	}

	dirPreAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	targetPath := path.Join(node.path, name)
	targetInfo, err := h.server.handler.fs.Stat(targetPath)
	if err != nil {
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFSERR_NOENT)
		encodeWccData(&buf, dirPreAttrs, dirPreAttrs)
		reply.Data = buf.Bytes()
		return reply, nil
	}

	if !targetInfo.IsDir() {
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFSERR_NOTDIR)
		encodeWccData(&buf, dirPreAttrs, dirPreAttrs)
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
			errCode = NFSERR_NOTEMPTY
		}

		var buf bytes.Buffer
		xdrEncodeUint32(&buf, errCode)
		encodeWccData(&buf, dirPreAttrs, dirPostAttrs)
		reply.Data = buf.Bytes()
		return reply, nil
	}

	dirPostAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeWccData(&buf, dirPreAttrs, dirPostAttrs); err != nil {
		return nil, err
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleRename handles NFSPROC3_RENAME - rename a file or directory
func (h *NFSProcedureHandler) handleRename(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	if h.server.handler.options.ReadOnly {
		return nfsErrorReply(reply, ACCESS_DENIED), nil
	}

	srcHandleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	srcName, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	dstHandleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	dstName, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorReply(reply, GARBAGE_ARGS), nil
	}

	srcDir, ok := h.lookupNode(srcHandleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	dstDir, ok := h.lookupNode(dstHandleVal)
	if !ok {
		return nfsErrorReply(reply, NFSERR_NOENT), nil
	}

	srcDirPreAttrs, err := h.server.handler.GetAttr(srcDir)
	if err != nil {
		return nil, err
	}

	dstDirPreAttrs, err := h.server.handler.GetAttr(dstDir)
	if err != nil {
		return nil, err
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

		var buf bytes.Buffer
		xdrEncodeUint32(&buf, mapError(err))
		encodeWccData(&buf, srcDirPreAttrs, srcDirPostAttrs)
		encodeWccData(&buf, dstDirPreAttrs, dstDirPostAttrs)
		reply.Data = buf.Bytes()
		return reply, nil
	}

	srcDirPostAttrs, err := h.server.handler.GetAttr(srcDir)
	if err != nil {
		return nil, err
	}

	dstDirPostAttrs, err := h.server.handler.GetAttr(dstDir)
	if err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeWccData(&buf, srcDirPreAttrs, srcDirPostAttrs); err != nil {
		return nil, err
	}
	if err := encodeWccData(&buf, dstDirPreAttrs, dstDirPostAttrs); err != nil {
		return nil, err
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleLink handles NFSPROC3_LINK - create hard link (not supported)
func (h *NFSProcedureHandler) handleLink(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	notSupported := &NotSupportedError{
		Operation: "LINK",
		Reason:    "hard links are not supported by this NFS implementation",
	}
	return nfsErrorReply(reply, mapError(notSupported)), nil
}
