package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path"
	"time"
)

// sattr3 represents the parsed NFS3 sattr3 structure (RFC 1813 section 2.3.4).
// Each field has an associated "set" flag indicating whether the client wants
// to change that attribute.
type sattr3 struct {
	SetMode   bool
	Mode      uint32
	SetUID    bool
	UID       uint32
	SetGID    bool
	GID       uint32
	SetSize   bool
	Size      uint64
	SetAtime  uint32 // 0=don't set, 1=SET_TO_SERVER_TIME, 2=SET_TO_CLIENT_TIME
	AtimeSec  uint32
	AtimeNsec uint32
	SetMtime  uint32 // 0=don't set, 1=SET_TO_SERVER_TIME, 2=SET_TO_CLIENT_TIME
	MtimeSec  uint32
	MtimeNsec uint32
}

// decodeSattr3 reads the NFS3 sattr3 structure from the wire.
// Per RFC 1813, sattr3 is a series of discriminated unions — each attribute
// has a boolean flag followed by the value (if set).
func decodeSattr3(body io.Reader) (sattr3, error) {
	var s sattr3

	var flag uint32
	if err := binary.Read(body, binary.BigEndian, &flag); err != nil {
		return s, err
	}
	s.SetMode = flag != 0
	if s.SetMode {
		if err := binary.Read(body, binary.BigEndian, &s.Mode); err != nil {
			return s, err
		}
	}

	if err := binary.Read(body, binary.BigEndian, &flag); err != nil {
		return s, err
	}
	s.SetUID = flag != 0
	if s.SetUID {
		if err := binary.Read(body, binary.BigEndian, &s.UID); err != nil {
			return s, err
		}
	}

	if err := binary.Read(body, binary.BigEndian, &flag); err != nil {
		return s, err
	}
	s.SetGID = flag != 0
	if s.SetGID {
		if err := binary.Read(body, binary.BigEndian, &s.GID); err != nil {
			return s, err
		}
	}

	if err := binary.Read(body, binary.BigEndian, &flag); err != nil {
		return s, err
	}
	s.SetSize = flag != 0
	if s.SetSize {
		if err := binary.Read(body, binary.BigEndian, &s.Size); err != nil {
			return s, err
		}
	}

	if err := binary.Read(body, binary.BigEndian, &s.SetAtime); err != nil {
		return s, err
	}
	if s.SetAtime == 2 { // SET_TO_CLIENT_TIME
		if err := binary.Read(body, binary.BigEndian, &s.AtimeSec); err != nil {
			return s, err
		}
		if err := binary.Read(body, binary.BigEndian, &s.AtimeNsec); err != nil {
			return s, err
		}
	}

	if err := binary.Read(body, binary.BigEndian, &s.SetMtime); err != nil {
		return s, err
	}
	if s.SetMtime == 2 { // SET_TO_CLIENT_TIME
		if err := binary.Read(body, binary.BigEndian, &s.MtimeSec); err != nil {
			return s, err
		}
		if err := binary.Read(body, binary.BigEndian, &s.MtimeNsec); err != nil {
			return s, err
		}
	}

	return s, nil
}

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
	if h.server.handler.options.ReadOnly {
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
		attrs.Uid = sattr.UID
	}
	if sattr.SetGID {
		attrs.Gid = sattr.GID
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
		nodeAttrsCopy := *node.attrs
		node.mu.RUnlock()
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFSERR_NOTDIR)
		xdrEncodeUint32(&buf, 1)
		encodeFileAttributes(&buf, &nodeAttrsCopy)
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
		nodeAttrsCopy := *node.attrs
		node.mu.RUnlock()
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFSERR_NOENT)
		xdrEncodeUint32(&buf, 1)
		encodeFileAttributes(&buf, &nodeAttrsCopy)
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

// handleRead handles NFSPROC3_READ - read from file
func (h *NFSProcedureHandler) handleRead(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	var offset uint64
	var count uint32
	if err := binary.Read(body, binary.BigEndian, &offset); err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}
	if err := binary.Read(body, binary.BigEndian, &count); err != nil {
		return nfsErrorWithPostOp(reply, GARBAGE_ARGS), nil
	}

	if offset > math.MaxUint64-uint64(count) {
		return nfsErrorWithPostOp(reply, NFSERR_INVAL), nil
	}

	// Rate limiting for large reads
	if count > 65536 && h.server.handler.rateLimiter != nil && h.server.handler.options.EnableRateLimiting {
		if !h.server.handler.rateLimiter.AllowOperation(authCtx.ClientIP, OpTypeReadLarge) {
			if h.server.handler.metrics != nil {
				h.server.handler.metrics.RecordRateLimitExceeded()
			}
			return nfsErrorWithPostOp(reply, NFSERR_DELAY), nil
		}
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithPostOp(reply, NFSERR_STALE), nil
	}

	// R22: Return NFS error instead of nil,err
	data, err := h.server.handler.Read(node, int64(offset), int64(count))
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
		return nfsErrorWithWcc(reply, NFSERR_ROFS), nil
	}

	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	var offset uint64
	var count, stable uint32
	if err := binary.Read(body, binary.BigEndian, &offset); err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}
	if err := binary.Read(body, binary.BigEndian, &count); err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}
	if err := binary.Read(body, binary.BigEndian, &stable); err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	if offset > math.MaxUint64-uint64(count) {
		return nfsErrorWithWcc(reply, NFSERR_INVAL), nil
	}

	// Rate limiting for large writes
	if count > 65536 && h.server.handler.rateLimiter != nil && h.server.handler.options.EnableRateLimiting {
		if !h.server.handler.rateLimiter.AllowOperation(authCtx.ClientIP, OpTypeWriteLarge) {
			if h.server.handler.metrics != nil {
				h.server.handler.metrics.RecordRateLimitExceeded()
			}
			return nfsErrorWithWcc(reply, NFSERR_DELAY), nil
		}
	}

	var dataLen uint32
	if err := binary.Read(body, binary.BigEndian, &dataLen); err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}
	if dataLen != count {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	data := make([]byte, count)
	if _, err := io.ReadFull(body, data); err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithWcc(reply, NFSERR_STALE), nil
	}

	if h.server.options.Debug {
		h.server.logger.Printf("WRITE: handle=%d path='%s' offset=%d count=%d stable=%d", handleVal, node.path, offset, count, stable)
	}

	// R23: Return NFS error instead of nil,err
	preAttrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
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
			return nfsErrorWithWcc(reply, NFSERR_IO), nil
		}
		reply.Data = buf.Bytes()
		return reply, nil
	}

	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	if h.server.options.Debug {
		h.server.logger.Printf("WRITE: Success, wrote %d bytes to '%s'", n, node.path)
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeWccData(&buf, preAttrs, attrs); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}
	xdrEncodeUint32(&buf, uint32(n))
	xdrEncodeUint32(&buf, 2) // FILE_SYNC: server does synchronous writes
	buf.Write(make([]byte, 8)) // writeverf

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleCreate handles NFSPROC3_CREATE - create a file
func (h *NFSProcedureHandler) handleCreate(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	// R5: Check read-only before processing
	if h.server.handler.options.ReadOnly {
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
	var uid, gid uint32
	var setUID, setGID bool
	if createHow == 0 || createHow == 1 {
		sattr, err := decodeSattr3(body)
		if err != nil {
			return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
		}
		if sattr.SetMode {
			mode = sattr.Mode
		}
		if sattr.SetUID {
			uid = sattr.UID
			setUID = true
		}
		if sattr.SetGID {
			gid = sattr.GID
			setGID = true
		}
	} else if createHow == 2 {
		// M14: Use io.ReadFull for the 8-byte EXCLUSIVE verifier
		var verf [8]byte
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
	}
	if setUID {
		attrs.Uid = uid
	}
	if setGID {
		attrs.Gid = gid
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
	if h.server.handler.options.ReadOnly {
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
		encodeWccData(&buf, dirPreAttrs, dirPostAttrs)
		reply.Data = buf.Bytes()
		return reply, nil
	}

	// Apply uid/gid from sattr3 if requested.
	// Use -1 for unchanged fields (standard POSIX convention).
	if sattr.SetUID || sattr.SetGID {
		newUID := -1
		newGID := -1
		if sattr.SetUID {
			newUID = int(sattr.UID)
		}
		if sattr.SetGID {
			newGID = int(sattr.GID)
		}
		if err := h.server.handler.fs.Chown(dirPath, newUID, newGID); err != nil {
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
	if h.server.handler.options.ReadOnly {
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
		mode = sattr.Mode
	}

	attrs := &NFSAttrs{
		Mode: os.FileMode(mode) | os.ModeSymlink,
	}
	if sattr.SetUID {
		attrs.Uid = sattr.UID
	}
	if sattr.SetGID {
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
		encodeWccData(&buf, dirPreAttrs, dirPostAttrs)
		reply.Data = buf.Bytes()
		return reply, nil
	}

	// R9: Use Lchown instead of Chown for symlinks to avoid following the link
	if sattr.SetUID || sattr.SetGID {
		newUID := -1
		newGID := -1
		if sattr.SetUID {
			newUID = int(sattr.UID)
		}
		if sattr.SetGID {
			newGID = int(sattr.GID)
		}
		symlinkPath := path.Join(node.path, name)
		if err := h.server.handler.fs.Lchown(symlinkPath, newUID, newGID); err != nil {
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

// handleReaddir handles NFSPROC3_READDIR - read directory entries
func (h *NFSProcedureHandler) handleReaddir(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	// Rate limiting
	if h.server.handler.rateLimiter != nil && h.server.handler.options.EnableRateLimiting {
		if !h.server.handler.rateLimiter.AllowOperation(authCtx.ClientIP, OpTypeReaddir) {
			if h.server.handler.metrics != nil {
				h.server.handler.metrics.RecordRateLimitExceeded()
			}
			return nfsErrorWithPostOp(reply, NFSERR_DELAY), nil
		}
	}

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

		// R4: Copy fileId under RLock
		entry.mu.RLock()
		fileId := entry.attrs.FileId
		entry.mu.RUnlock()
		binary.Write(&buf, binary.BigEndian, fileId)

		// M1: Use path.Base() for name extraction
		name := path.Base(entry.path)
		if entry.path == "/" {
			name = "/"
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

		// R4: Copy attrs under RLock
		entry.mu.RLock()
		entryAttrsCopy := *entry.attrs
		entry.mu.RUnlock()

		binary.Write(&buf, binary.BigEndian, entryAttrsCopy.FileId)

		// M1: Use path.Base() for name extraction
		name := path.Base(entry.path)
		if entry.path == "/" {
			name = "/"
		}
		xdrEncodeString(&buf, name)

		binary.Write(&buf, binary.BigEndian, entryCookie)

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

// handleFsstat handles NFSPROC3_FSSTAT - get filesystem statistics
func (h *NFSProcedureHandler) handleFsstat(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
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

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nfsErrorWithPostOp(reply, NFSERR_IO), nil
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

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nfsErrorWithPostOp(reply, NFSERR_IO), nil
	}

	binary.Write(&buf, binary.BigEndian, uint32(1048576))       // rtmax
	binary.Write(&buf, binary.BigEndian, uint32(65536))         // rtpref
	binary.Write(&buf, binary.BigEndian, uint32(4096))          // rtmult
	binary.Write(&buf, binary.BigEndian, uint32(1048576))       // wtmax
	binary.Write(&buf, binary.BigEndian, uint32(65536))         // wtpref
	binary.Write(&buf, binary.BigEndian, uint32(4096))          // wtmult
	binary.Write(&buf, binary.BigEndian, uint32(8192))          // dtpref (C1: uint32 not uint64)
	binary.Write(&buf, binary.BigEndian, uint64(1099511627776)) // maxfilesize
	binary.Write(&buf, binary.BigEndian, uint32(0))             // time_delta.seconds
	binary.Write(&buf, binary.BigEndian, uint32(1000000))       // time_delta.nseconds

	// R1: Correct FSINFO properties bitmask per RFC 1813
	// FSF3_LINK=0x0001, FSF3_SYMLINK=0x0002, FSF3_HOMOGENEOUS=0x0008, FSF3_CANSETTIME=0x0010
	var properties uint32 = 0x0002 | 0x0008 | 0x0010 // symlink + homogeneous + cansettime
	if h.server.handler.options.ReadOnly {
		properties |= 0x0001 // FSF3_LINK (read-only flag maps to bit 0)
	}
	binary.Write(&buf, binary.BigEndian, properties)

	reply.Data = buf.Bytes()
	return reply, nil
}

// handlePathconf handles NFSPROC3_PATHCONF - get path configuration
func (h *NFSProcedureHandler) handlePathconf(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
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

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	xdrEncodeUint32(&buf, 1)
	if err := encodeFileAttributes(&buf, attrs); err != nil {
		return nfsErrorWithPostOp(reply, NFSERR_IO), nil
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

	var accessAllowed uint32 = 0

	// ACCESS3_READ
	if access&1 != 0 {
		accessAllowed |= 1
	}

	// ACCESS3_LOOKUP (only for directories)
	if access&2 != 0 && attrs.Mode&os.ModeDir != 0 {
		accessAllowed |= 2
	}

	// M2: ACCESS3_EXECUTE should be granted on read-only exports when file has execute bits
	if access&32 != 0 && (attrs.Mode&0111 != 0) {
		accessAllowed |= 32
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

// handleCommit handles NFSPROC3_COMMIT - commit cached data
func (h *NFSProcedureHandler) handleCommit(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	var offset uint64
	var count uint32
	if err := binary.Read(body, binary.BigEndian, &offset); err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}
	if err := binary.Read(body, binary.BigEndian, &count); err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		return nfsErrorWithWcc(reply, NFSERR_STALE), nil
	}

	// R23: Return NFS error instead of nil,err
	attrs, err := h.server.handler.GetAttr(node)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	// wcc_data (RFC 1813 section 3.3.21)
	if err := encodeWccData(&buf, attrs, attrs); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}
	buf.Write(make([]byte, 8)) // writeverf

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleRemove handles NFSPROC3_REMOVE - remove a file
func (h *NFSProcedureHandler) handleRemove(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	if h.server.handler.options.ReadOnly {
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
		encodeWccData(&buf, dirPreAttrs, dirPostAttrs)
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
	if h.server.handler.options.ReadOnly {
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
	if h.server.handler.options.ReadOnly {
		return nfsErrorWithWcc(reply, NFSERR_ROFS), nil
	}

	srcHandleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	srcName, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	// R7: Validate source filename
	if status := validateFilename(srcName); status != NFS_OK {
		return nfsErrorWithWcc(reply, status), nil
	}

	dstHandleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	dstName, err := xdrDecodeString(body)
	if err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	// R7: Validate destination filename
	if status := validateFilename(dstName); status != NFS_OK {
		return nfsErrorWithWcc(reply, status), nil
	}

	srcDir, ok := h.lookupNode(srcHandleVal)
	if !ok {
		return nfsErrorWithWcc(reply, NFSERR_STALE), nil
	}

	dstDir, ok := h.lookupNode(dstHandleVal)
	if !ok {
		return nfsErrorWithWcc(reply, NFSERR_STALE), nil
	}

	// R23: Return NFS error instead of nil,err
	srcDirPreAttrs, err := h.server.handler.GetAttr(srcDir)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	dstDirPreAttrs, err := h.server.handler.GetAttr(dstDir)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
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
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	dstDirPostAttrs, err := h.server.handler.GetAttr(dstDir)
	if err != nil {
		return nfsErrorWithWcc(reply, mapError(err)), nil
	}

	var buf bytes.Buffer
	xdrEncodeUint32(&buf, NFS_OK)
	if err := encodeWccData(&buf, srcDirPreAttrs, srcDirPostAttrs); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}
	if err := encodeWccData(&buf, dstDirPreAttrs, dstDirPostAttrs); err != nil {
		return nfsErrorWithWcc(reply, NFSERR_IO), nil
	}

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleLink handles NFSPROC3_LINK - create hard link (not supported)
// R2: LINK3resfail format is status + post_op_attr + wcc_data (not just wcc_data)
func (h *NFSProcedureHandler) handleLink(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	notSupported := &NotSupportedError{
		Operation: "LINK",
		Reason:    "hard links are not supported by this NFS implementation",
	}
	return nfsErrorWithPostOpAndWcc(reply, mapError(notSupported)), nil
}
