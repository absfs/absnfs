package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
	"path"
	"runtime"
	"strings"
)

// validateFilename validates a filename for CREATE/MKDIR operations
// Returns error status code if invalid, NFS_OK if valid
func validateFilename(name string) uint32 {
	// Check for empty name
	if name == "" {
		return NFSERR_INVAL
	}

	// Check for maximum length (255 bytes for most filesystems)
	if len(name) > 255 {
		return NFSERR_NAMETOOLONG
	}

	// Check for null bytes
	if strings.Contains(name, "\x00") {
		return NFSERR_INVAL
	}

	// Check for path separators (both forward and back slash)
	if strings.ContainsAny(name, "/\\") {
		return NFSERR_INVAL
	}

	// Check for parent directory references
	if name == "." || name == ".." {
		return NFSERR_INVAL
	}

	// Check for reserved names on Windows
	if runtime.GOOS == "windows" {
		upperName := strings.ToUpper(name)
		// Check base name without extension
		baseName := upperName
		if idx := strings.Index(upperName, "."); idx != -1 {
			baseName = upperName[:idx]
		}

		reservedNames := []string{
			"CON", "PRN", "AUX", "NUL",
			"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
			"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
		}
		for _, reserved := range reservedNames {
			if baseName == reserved {
				return NFSERR_INVAL
			}
		}
	}

	return NFS_OK
}

// validateMode validates file/directory mode for CREATE/MKDIR/SETATTR operations
// Returns error status code if invalid, NFS_OK if valid
func validateMode(mode uint32, isDir bool) uint32 {
	// Valid permission bits: 0777 (rwxrwxrwx)
	const validPermBits = 0777

	// Valid file type bits (octal 0170000)
	const fileTypeMask = 0170000

	// Extract file type bits
	fileTypeBits := mode & fileTypeMask

	// For CREATE/MKDIR, file type bits should not be set (0)
	// The file type is determined by the operation itself
	if fileTypeBits != 0 {
		return NFSERR_INVAL
	}

	// Check that only valid permission bits are set
	if mode&^validPermBits != 0 {
		return NFSERR_INVAL
	}

	// For directories, execute bits are often required for traversal
	// But we don't enforce this as it's a valid use case to create
	// directories without execute permissions (though impractical)

	return NFS_OK
}

// handleNFSCall handles NFS protocol operations
func (h *NFSProcedureHandler) handleNFSCall(call *RPCCall, body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	// Check version first
	if call.Header.Version != NFS_V3 {
		reply.AcceptStatus = PROG_MISMATCH
		return reply, nil
	}

	switch call.Header.Procedure {
	case NFSPROC3_NULL:
		return reply, nil

	case NFSPROC3_GETATTR:
		handle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			if h.server.options.Debug {
				h.server.logger.Printf("GETATTR: Failed to decode handle: %v", err)
			}
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		handle.Handle = handleVal
		if h.server.options.Debug {
			h.server.logger.Printf("GETATTR: Looking up handle %d, fileMap count: %d", handle.Handle, h.server.handler.fileMap.Count())
		}

		// Find the node
		file, ok := h.server.handler.fileMap.Get(handle.Handle)
		if !ok {
			if h.server.options.Debug {
				h.server.logger.Printf("GETATTR: Handle %d not found in fileMap", handle.Handle)
			}
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := file.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get attributes with timeout
		attrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}
		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_SETATTR:
		handle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		handle.Handle = handleVal

		// Read attribute flags and values
		var setMode, setUid, setGid uint32
		var mode, uid, gid uint32
		if err := binary.Read(body, binary.BigEndian, &setMode); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		if setMode != 0 {
			if err := binary.Read(body, binary.BigEndian, &mode); err != nil {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, GARBAGE_ARGS)
				reply.Data = buf.Bytes()
				return reply, nil
			}
			// Validate mode
			if mode&0x8000 != 0 {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, NFSERR_INVAL)
				reply.Data = buf.Bytes()
				return reply, nil
			}
		}

		if err := binary.Read(body, binary.BigEndian, &setUid); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		if setUid != 0 {
			if err := binary.Read(body, binary.BigEndian, &uid); err != nil {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, GARBAGE_ARGS)
				reply.Data = buf.Bytes()
				return reply, nil
			}
		}

		if err := binary.Read(body, binary.BigEndian, &setGid); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		if setGid != 0 {
			if err := binary.Read(body, binary.BigEndian, &gid); err != nil {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, GARBAGE_ARGS)
				reply.Data = buf.Bytes()
				return reply, nil
			}
		}

		// Find the node
		file, ok := h.server.handler.fileMap.Get(handle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := file.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get pre-operation attributes for wcc_data
		preAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Create new attributes - read with lock protection
		node.mu.RLock()
		attrs := &NFSAttrs{
			Mode: node.attrs.Mode,
			Uid:  node.attrs.Uid,
			Gid:  node.attrs.Gid,
		}
		node.mu.RUnlock()

		// Update attributes
		if setMode != 0 {
			attrs.Mode = os.FileMode(mode)
		}
		if setUid != 0 {
			attrs.Uid = uid
		}
		if setGid != 0 {
			attrs.Gid = gid
		}

		// Set attributes
		if err := h.server.handler.SetAttr(node, attrs); err != nil {
			return nil, err
		}

		// Get updated attributes
		postAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Encode response (RFC 1813 SETATTR3resok)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// wcc_data: pre_op_attr + post_op_attr
		// pre_op_attr: attributes_follow + wcc_attr
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeWccAttr(&buf, preAttrs); err != nil {
			return nil, err
		}

		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, postAttrs); err != nil {
			return nil, err
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_LOOKUP:
		// Decode directory handle
		dirHandle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			// No dir_attributes available for GARBAGE_ARGS
			reply.Data = buf.Bytes()
			return reply, nil
		}
		dirHandle.Handle = handleVal

		// Decode filename
		name, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the directory node
		dirNode, ok := h.server.handler.fileMap.Get(dirHandle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			// dir_attributes: attributes_follow = FALSE (we don't have the dir)
			xdrEncodeUint32(&buf, 0)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Verify it's a directory
		node, ok := dirNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			xdrEncodeUint32(&buf, 0) // dir_attributes: attributes_follow = FALSE
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Check if it's a file handle instead of directory handle
		node.mu.RLock()
		isDir := node.attrs.Mode&os.ModeDir != 0
		node.mu.RUnlock()

		if !isDir {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOTDIR)
			// dir_attributes: attributes_follow + fattr3
			xdrEncodeUint32(&buf, 1)
			encodeFileAttributes(&buf, node.attrs)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Lookup the file with timeout
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
			// dir_attributes: attributes_follow + fattr3 for parent directory
			xdrEncodeUint32(&buf, 1)
			encodeFileAttributes(&buf, node.attrs)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Allocate new handle
		handle := h.server.handler.fileMap.Allocate(lookupNode)
		if h.server.options.Debug {
			h.server.logger.Printf("LOOKUP: Found '%s', allocated handle %d", lookupPath, handle)
		}

		// Encode response (RFC 1813 LOOKUP3resok)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		xdrEncodeFileHandle(&buf, handle)

		// obj_attributes: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, lookupNode.attrs); err != nil {
			return nil, err
		}

		// dir_attributes: attributes_follow + fattr3 for the parent directory
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, node.attrs); err != nil {
			return nil, err
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_READLINK:
		// Decode symlink handle
		handle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		handle.Handle = handleVal

		// Find the symlink node
		fileNode, ok := h.server.handler.fileMap.Get(handle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := fileNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Check if it's a symlink
		node.mu.RLock()
		isSymlink := node.attrs.Mode&os.ModeSymlink != 0
		node.mu.RUnlock()

		if !isSymlink {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_INVAL)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Read the symlink target
		target, err := h.server.handler.Readlink(node)
		if err != nil {
			return nil, err
		}

		// Get attributes for post-op attributes
		attrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// Encode post-op attributes
		xdrEncodeUint32(&buf, 1) // Has attributes
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// Encode symlink target
		if err := xdrEncodeString(&buf, target); err != nil {
			return nil, err
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_READ:
		handle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		handle.Handle = handleVal

		// Read offset and count
		var offset uint64
		var count uint32
		if err := binary.Read(body, binary.BigEndian, &offset); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		if err := binary.Read(body, binary.BigEndian, &count); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Check for integer overflow: offset + count must not overflow uint64
		if offset > math.MaxUint64-uint64(count) {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_INVAL)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Apply rate limiting for large reads (> 64KB)
		if count > 65536 && h.server.handler.rateLimiter != nil && h.server.handler.options.EnableRateLimiting {
			if !h.server.handler.rateLimiter.AllowOperation(authCtx.ClientIP, OpTypeReadLarge) {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, NFSERR_DELAY) // Server is busy
				reply.Data = buf.Bytes()

				// Record rate limit exceeded
				if h.server.handler.metrics != nil {
					h.server.handler.metrics.RecordRateLimitExceeded()
				}

				return reply, nil
			}
		}

		// Find the node
		file, ok := h.server.handler.fileMap.Get(handle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := file.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Read data
		data, err := h.server.handler.Read(node, int64(offset), int64(count))
		if err != nil {
			return nil, err
		}

		// Get updated attributes
		attrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Encode response (RFC 1813 READ3resok)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// count (uint32)
		xdrEncodeUint32(&buf, uint32(len(data)))

		// eof (XDR bool - uint32)
		if int64(offset)+int64(len(data)) >= attrs.Size {
			xdrEncodeUint32(&buf, 1) // EOF = TRUE
		} else {
			xdrEncodeUint32(&buf, 0) // EOF = FALSE
		}

		// opaque data<> - XDR format: length + data + padding
		xdrEncodeUint32(&buf, uint32(len(data))) // data length
		buf.Write(data)
		// Pad to 4-byte boundary
		padding := (4 - (len(data) % 4)) % 4
		if padding > 0 {
			buf.Write(make([]byte, padding))
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_WRITE:
		if h.server.handler.options.ReadOnly {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, ACCESS_DENIED)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		handle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		handle.Handle = handleVal

		// Read offset and count
		var offset uint64
		var count, stable uint32
		if err := binary.Read(body, binary.BigEndian, &offset); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		if err := binary.Read(body, binary.BigEndian, &count); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		if err := binary.Read(body, binary.BigEndian, &stable); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Check for integer overflow: offset + count must not overflow uint64
		if offset > math.MaxUint64-uint64(count) {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_INVAL)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Apply rate limiting for large writes (> 64KB)
		if count > 65536 && h.server.handler.rateLimiter != nil && h.server.handler.options.EnableRateLimiting {
			if !h.server.handler.rateLimiter.AllowOperation(authCtx.ClientIP, OpTypeWriteLarge) {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, NFSERR_DELAY) // Server is busy
				reply.Data = buf.Bytes()

				// Record rate limit exceeded
				if h.server.handler.metrics != nil {
					h.server.handler.metrics.RecordRateLimitExceeded()
				}

				return reply, nil
			}
		}

		// Read data length and data
		var dataLen uint32
		if err := binary.Read(body, binary.BigEndian, &dataLen); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		if dataLen != count {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		data := make([]byte, count)
		if _, err := io.ReadFull(body, data); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the node
		file, ok := h.server.handler.fileMap.Get(handle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := file.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		if h.server.options.Debug {
			h.server.logger.Printf("WRITE: handle=%d path='%s' offset=%d count=%d stable=%d", handle.Handle, node.path, offset, count, stable)
		}

		// Get pre-operation file attributes for wcc_data
		preAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Write data
		n, err := h.server.handler.Write(node, int64(offset), data)
		if err != nil {
			if h.server.options.Debug {
				h.server.logger.Printf("WRITE: Failed to write to '%s': %v", node.path, err)
			}
			// Get post-operation file attributes
			postAttrs, _ := h.server.handler.GetAttr(node)
			if postAttrs == nil {
				postAttrs = preAttrs
			}

			// Return error with wcc_data (RFC 1813 WRITE3resfail)
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, mapError(err))
			// file_wcc: wcc_data
			// pre_op_attr: attributes_follow + wcc_attr
			xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
			encodeWccAttr(&buf, preAttrs)
			// post_op_attr: attributes_follow + fattr3
			xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
			encodeFileAttributes(&buf, postAttrs)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get updated attributes
		attrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		if h.server.options.Debug {
			h.server.logger.Printf("WRITE: Success, wrote %d bytes to '%s'", n, node.path)
		}

		// Encode response (RFC 1813 WRITE3resok)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// wcc_data: pre_op_attr + post_op_attr
		// pre_op_attr: attributes_follow + wcc_attr
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeWccAttr(&buf, preAttrs); err != nil {
			return nil, err
		}

		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// count (bytes written)
		xdrEncodeUint32(&buf, uint32(n))

		// committed (stable_how)
		xdrEncodeUint32(&buf, stable)

		// writeverf (8 bytes - server restart identifier)
		buf.Write(make([]byte, 8)) // Use zeros as verifier for now

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_CREATE:
		// Decode directory handle
		dirHandle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		dirHandle.Handle = handleVal

		// Decode filename
		name, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Validate filename
		if status := validateFilename(name); status != NFS_OK {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, status)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Read createhow3 (UNCHECKED=0, GUARDED=1, EXCLUSIVE=2)
		var createHow uint32
		if err := binary.Read(body, binary.BigEndian, &createHow); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Default mode for new files (readable/writable by owner, readable by group/others)
		var mode uint32 = 0644

		// For UNCHECKED or GUARDED, parse sattr3 structure
		// For EXCLUSIVE, skip 8-byte verifier (not implemented yet)
		if createHow == 0 || createHow == 1 {
			// Parse sattr3: set_mode, [mode], set_uid, [uid], set_gid, [gid], set_size, [size], set_atime, set_mtime
			var setMode uint32
			if err := binary.Read(body, binary.BigEndian, &setMode); err != nil {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, GARBAGE_ARGS)
				reply.Data = buf.Bytes()
				return reply, nil
			}
			if setMode != 0 {
				if err := binary.Read(body, binary.BigEndian, &mode); err != nil {
					var buf bytes.Buffer
					xdrEncodeUint32(&buf, GARBAGE_ARGS)
					reply.Data = buf.Bytes()
					return reply, nil
				}
			}

			// Skip remaining sattr3 fields (uid, gid, size, atime, mtime)
			// set_uid
			var setUid uint32
			if err := binary.Read(body, binary.BigEndian, &setUid); err != nil {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, GARBAGE_ARGS)
				reply.Data = buf.Bytes()
				return reply, nil
			}
			if setUid != 0 {
				var uid uint32
				binary.Read(body, binary.BigEndian, &uid) // Skip uid value
			}

			// set_gid
			var setGid uint32
			if err := binary.Read(body, binary.BigEndian, &setGid); err != nil {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, GARBAGE_ARGS)
				reply.Data = buf.Bytes()
				return reply, nil
			}
			if setGid != 0 {
				var gid uint32
				binary.Read(body, binary.BigEndian, &gid) // Skip gid value
			}

			// set_size
			var setSize uint32
			if err := binary.Read(body, binary.BigEndian, &setSize); err != nil {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, GARBAGE_ARGS)
				reply.Data = buf.Bytes()
				return reply, nil
			}
			if setSize != 0 {
				var size uint64
				binary.Read(body, binary.BigEndian, &size) // Skip size value
			}

			// set_atime (0=DONT_CHANGE, 1=SET_TO_SERVER_TIME, 2=SET_TO_CLIENT_TIME)
			var setAtime uint32
			if err := binary.Read(body, binary.BigEndian, &setAtime); err != nil {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, GARBAGE_ARGS)
				reply.Data = buf.Bytes()
				return reply, nil
			}
			if setAtime == 2 {
				var atimeSec, atimeNsec uint32
				binary.Read(body, binary.BigEndian, &atimeSec)
				binary.Read(body, binary.BigEndian, &atimeNsec)
			}

			// set_mtime (0=DONT_CHANGE, 1=SET_TO_SERVER_TIME, 2=SET_TO_CLIENT_TIME)
			var setMtime uint32
			if err := binary.Read(body, binary.BigEndian, &setMtime); err != nil {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, GARBAGE_ARGS)
				reply.Data = buf.Bytes()
				return reply, nil
			}
			if setMtime == 2 {
				var mtimeSec, mtimeNsec uint32
				binary.Read(body, binary.BigEndian, &mtimeSec)
				binary.Read(body, binary.BigEndian, &mtimeNsec)
			}
		} else if createHow == 2 {
			// EXCLUSIVE mode: skip 8-byte verifier
			var verf [8]byte
			body.Read(verf[:])
		}

		// Validate mode
		if status := validateMode(mode, false); status != NFS_OK {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, status)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the directory node
		dirNode, ok := h.server.handler.fileMap.Get(dirHandle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := dirNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get pre-operation directory attributes for wcc_data
		dirPreAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Create new attributes
		attrs := &NFSAttrs{
			Mode: os.FileMode(mode),
			Uid:  0,
			Gid:  0,
		}

		// Create the file
		newNode, err := h.server.handler.Create(node, name, attrs)
		if err != nil {
			// Get post-operation directory attributes
			dirPostAttrs, _ := h.server.handler.GetAttr(node)
			if dirPostAttrs == nil {
				dirPostAttrs = dirPreAttrs
			}

			// Return error with wcc_data (RFC 1813 CREATE3resfail)
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, mapError(err))
			// dir_wcc: wcc_data
			// pre_op_attr: attributes_follow + wcc_attr
			xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
			encodeWccAttr(&buf, dirPreAttrs)
			// post_op_attr: attributes_follow + fattr3
			xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
			encodeFileAttributes(&buf, dirPostAttrs)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get post-operation directory attributes
		dirPostAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Allocate new handle
		handle := h.server.handler.fileMap.Allocate(newNode)

		// Encode response (RFC 1813 CREATE3resok)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// post_op_fh3: handle_follows + nfs_fh3
		xdrEncodeUint32(&buf, 1) // handle_follows = TRUE
		xdrEncodeFileHandle(&buf, handle)

		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, newNode.attrs); err != nil {
			return nil, err
		}

		// dir_wcc: wcc_data for directory
		// pre_op_attr: attributes_follow + wcc_attr
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeWccAttr(&buf, dirPreAttrs); err != nil {
			return nil, err
		}
		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, dirPostAttrs); err != nil {
			return nil, err
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_MKDIR:
		// Decode directory handle
		dirHandle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		dirHandle.Handle = handleVal

		// Decode dirname
		name, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Validate dirname
		if status := validateFilename(name); status != NFS_OK {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, status)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Read directory mode
		var mode uint32
		if err := binary.Read(body, binary.BigEndian, &mode); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Validate mode
		if status := validateMode(mode, true); status != NFS_OK {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, status)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the parent directory node
		dirNode, ok := h.server.handler.fileMap.Get(dirHandle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := dirNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get pre-operation directory attributes for wcc_data
		dirPreAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Create the directory
		if err := h.server.handler.fs.Mkdir(path.Join(node.path, name), os.FileMode(mode)); err != nil {
			// Get post-operation directory attributes
			dirPostAttrs, _ := h.server.handler.GetAttr(node)
			if dirPostAttrs == nil {
				dirPostAttrs = dirPreAttrs
			}

			// Return error with wcc_data (RFC 1813 MKDIR3resfail)
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, mapError(err))
			// dir_wcc: wcc_data
			// pre_op_attr: attributes_follow + wcc_attr
			xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
			encodeWccAttr(&buf, dirPreAttrs)
			// post_op_attr: attributes_follow + fattr3
			xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
			encodeFileAttributes(&buf, dirPostAttrs)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Lookup the new directory
		newNode, err := h.server.handler.Lookup(path.Join(node.path, name))
		if err != nil {
			return nil, err
		}

		// Get post-operation directory attributes
		dirPostAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Allocate new handle
		handle := h.server.handler.fileMap.Allocate(newNode)

		// Encode response (RFC 1813 MKDIR3resok - same as CREATE3resok)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// post_op_fh3: handle_follows + nfs_fh3
		xdrEncodeUint32(&buf, 1) // handle_follows = TRUE
		xdrEncodeFileHandle(&buf, handle)

		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, newNode.attrs); err != nil {
			return nil, err
		}

		// dir_wcc: wcc_data for parent directory
		// pre_op_attr: attributes_follow + wcc_attr
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeWccAttr(&buf, dirPreAttrs); err != nil {
			return nil, err
		}
		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, dirPostAttrs); err != nil {
			return nil, err
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_SYMLINK:
		if h.server.handler.options.ReadOnly {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, ACCESS_DENIED)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Decode directory handle
		dirHandle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		dirHandle.Handle = handleVal

		// Decode symlink name
		name, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Validate symlink name
		if status := validateFilename(name); status != NFS_OK {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, status)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Read symlink attributes (mode)
		var mode uint32
		if err := binary.Read(body, binary.BigEndian, &mode); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Decode symlink target
		target, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Validate target is not empty
		if target == "" {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_INVAL)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the parent directory node
		dirNode, ok := h.server.handler.fileMap.Get(dirHandle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := dirNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get parent directory pre-operation attributes
		dirPreAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Create new attributes for the symlink
		attrs := &NFSAttrs{
			Mode: os.FileMode(mode) | os.ModeSymlink,
			Uid:  0,
			Gid:  0,
		}

		// Create the symlink
		newNode, err := h.server.handler.Symlink(node, name, target, attrs)
		if err != nil {
			return nil, err
		}

		// Get parent directory post-operation attributes
		dirPostAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Allocate new handle
		handle := h.server.handler.fileMap.Allocate(newNode)

		// Encode response (RFC 1813 SYMLINK3resok)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// post_op_fh3: handle_follows + nfs_fh3
		xdrEncodeUint32(&buf, 1) // handle_follows = TRUE
		xdrEncodeFileHandle(&buf, handle)

		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, newNode.attrs); err != nil {
			return nil, err
		}

		// dir_wcc: wcc_data for parent directory
		// pre_op_attr: attributes_follow + wcc_attr
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeWccAttr(&buf, dirPreAttrs); err != nil {
			return nil, err
		}
		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, dirPostAttrs); err != nil {
			return nil, err
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_READDIR:
		// Apply rate limiting for READDIR operations
		if h.server.handler.rateLimiter != nil && h.server.handler.options.EnableRateLimiting {
			if !h.server.handler.rateLimiter.AllowOperation(authCtx.ClientIP, OpTypeReaddir) {
				var buf bytes.Buffer
				xdrEncodeUint32(&buf, NFSERR_DELAY) // Server is busy
				reply.Data = buf.Bytes()

				// Record rate limit exceeded
				if h.server.handler.metrics != nil {
					h.server.handler.metrics.RecordRateLimitExceeded()
				}

				return reply, nil
			}
		}

		// Decode arguments
		handle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		handle.Handle = handleVal

		// Read cookie and cookie verifier
		var cookie uint64
		var cookieVerf [8]byte
		if err := binary.Read(body, binary.BigEndian, &cookie); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		if _, err := io.ReadFull(body, cookieVerf[:]); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Read count
		var count uint32
		if err := binary.Read(body, binary.BigEndian, &count); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the directory node
		dirNode, ok := h.server.handler.fileMap.Get(handle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		dir, ok := dirNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Check if it's a directory
		if dir.attrs.Mode&os.ModeDir == 0 {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOTDIR)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Read directory entries
		entries, err := h.server.handler.ReadDir(dir)
		if err != nil {
			return nil, err
		}

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// Encode post-op attributes (RFC 1813: attributes_follow boolean + fattr3)
		attrs, err := h.server.handler.GetAttr(dir)
		if err != nil {
			return nil, err
		}
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// Encode cookie verifier (same as input for now)
		buf.Write(cookieVerf[:])

		// Encode directory entries
		// NFSv3 READDIR response format (RFC 1813):
		//   entry3 *entries (linked list)
		//   bool eof
		//
		// entry3 format:
		//   fileid3 fileid (uint64)
		//   filename3 name
		//   cookie3 cookie (uint64)
		//   entry3 *nextentry (bool: 1=more entries, 0=end of list)

		entryCount := 0
		maxReplySize := int(count) - 100 // Leave room for headers
		reachedLimit := false

		for i, entry := range entries {
			if uint64(i) < cookie {
				continue
			}

			// Check if we need to stop encoding to respect maxReplySize
			if buf.Len() >= maxReplySize {
				reachedLimit = true
				break
			}

			// Write value_follows = 1 (an entry follows)
			xdrEncodeUint32(&buf, 1)

			// Encode fileid (uint64 - we use node handle as fileid)
			entryHandle := h.server.handler.fileMap.Allocate(entry)
			binary.Write(&buf, binary.BigEndian, entryHandle)

			// Encode name
			name := ""
			if entry.path == "/" {
				name = "/"
			} else {
				// Extract just the filename from the path
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

			// Encode cookie (uint64)
			entryCookie := uint64(i + 1)
			binary.Write(&buf, binary.BigEndian, entryCookie)

			entryCount++
		}

		// Write value_follows = 0 (no more entries)
		xdrEncodeUint32(&buf, 0)

		// Encode EOF (true if we've reached the end of directory)
		eof := !reachedLimit
		if eof {
			xdrEncodeUint32(&buf, 1)
		} else {
			xdrEncodeUint32(&buf, 0)
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_READDIRPLUS:
		// Decode arguments
		handle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		handle.Handle = handleVal

		// Read cookie and cookie verifier
		var cookie uint64
		var cookieVerf [8]byte
		if err := binary.Read(body, binary.BigEndian, &cookie); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		if _, err := io.ReadFull(body, cookieVerf[:]); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Read dirCount and maxCount
		var dirCount, maxCount uint32
		if err := binary.Read(body, binary.BigEndian, &dirCount); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		if err := binary.Read(body, binary.BigEndian, &maxCount); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the directory node
		dirNode, ok := h.server.handler.fileMap.Get(handle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		dir, ok := dirNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Check if it's a directory
		if dir.attrs.Mode&os.ModeDir == 0 {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOTDIR)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Read directory entries with attributes
		entries, err := h.server.handler.ReadDirPlus(dir)
		if err != nil {
			return nil, err
		}

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// Encode post-op attributes (RFC 1813: attributes_follow boolean + fattr3)
		attrs, err := h.server.handler.GetAttr(dir)
		if err != nil {
			return nil, err
		}
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// Encode cookie verifier (same as input for now)
		buf.Write(cookieVerf[:])

		// Encode directory entries
		// RFC 1813: entryplus3 is a linked list with value_follows before each entry
		entryCount := 0
		reachedLimit := false
		maxReplySize := int(maxCount) - 200 // Leave room for headers and attributes

		for i, entry := range entries {
			if uint64(i) < cookie {
				continue
			}

			// Check if we need to stop encoding to respect maxReplySize
			if buf.Len() >= maxReplySize && entryCount > 0 {
				reachedLimit = true
				break
			}

			// Write value_follows = TRUE (entry follows)
			xdrEncodeUint32(&buf, 1)

			// Calculate this entry's cookie
			entryCookie := uint64(i + 1)

			// Encode fileid (uint64 per RFC 1813)
			entryHandle := h.server.handler.fileMap.Allocate(entry)
			binary.Write(&buf, binary.BigEndian, entryHandle) // fileid is uint64

			// Encode name
			name := ""
			if entry.path == "/" {
				name = "/"
			} else {
				// Extract just the filename from the path
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

			// Encode cookie (uint64)
			binary.Write(&buf, binary.BigEndian, entryCookie)

			// Encode post_op_attr (name_attributes)
			xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
			if err := encodeFileAttributes(&buf, entry.attrs); err != nil {
				return nil, err
			}

			// Encode post_op_fh3 (name_handle) - uses xdrEncodeFileHandle for proper opaque encoding
			xdrEncodeUint32(&buf, 1) // handle_follows = TRUE
			xdrEncodeFileHandle(&buf, entryHandle)

			entryCount++
		}

		// Write value_follows = FALSE (no more entries)
		xdrEncodeUint32(&buf, 0)

		// Encode EOF (true if we've reached the end of directory)
		eof := !reachedLimit
		if eof {
			xdrEncodeUint32(&buf, 1)
		} else {
			xdrEncodeUint32(&buf, 0)
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_FSSTAT:
		// Decode arguments
		handle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		handle.Handle = handleVal

		// Find the node
		fileNode, ok := h.server.handler.fileMap.Get(handle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := fileNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get attributes
		attrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// Encode post-op attributes (RFC 1813: attributes_follow boolean + fattr3)
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// We don't have actual filesystem stats, so provide dummy values
		// tbytes - Total bytes
		binary.Write(&buf, binary.BigEndian, uint64(1024*1024*1024*10)) // 10GB
		// fbytes - Free bytes
		binary.Write(&buf, binary.BigEndian, uint64(1024*1024*1024*5)) // 5GB
		// abytes - Available bytes
		binary.Write(&buf, binary.BigEndian, uint64(1024*1024*1024*5)) // 5GB
		// tfiles - Total file slots
		binary.Write(&buf, binary.BigEndian, uint64(1000000))
		// ffiles - Free file slots
		binary.Write(&buf, binary.BigEndian, uint64(900000))
		// afiles - Available file slots
		binary.Write(&buf, binary.BigEndian, uint64(900000))
		// invarsec - Invariant time
		binary.Write(&buf, binary.BigEndian, uint32(0))

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_FSINFO:
		// Decode arguments
		handle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		handle.Handle = handleVal

		// Find the node
		fileNode, ok := h.server.handler.fileMap.Get(handle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := fileNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get attributes
		attrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// Encode post-op attributes (RFC 1813: attributes_follow boolean + fattr3)
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// FSInfo values
		// rtmax - Max read transfer size
		binary.Write(&buf, binary.BigEndian, uint32(1048576)) // 1MB
		// rtpref - Preferred read transfer size
		binary.Write(&buf, binary.BigEndian, uint32(65536)) // 64KB
		// rtmult - Suggested multiple for read transfer size
		binary.Write(&buf, binary.BigEndian, uint32(4096)) // 4KB

		// wtmax - Max write transfer size
		binary.Write(&buf, binary.BigEndian, uint32(1048576)) // 1MB
		// wtpref - Preferred write transfer size
		binary.Write(&buf, binary.BigEndian, uint32(65536)) // 64KB
		// wtmult - Suggested multiple for write transfer size
		binary.Write(&buf, binary.BigEndian, uint32(4096)) // 4KB

		// dtpref - Preferred READDIR request size (size3 = uint64)
		binary.Write(&buf, binary.BigEndian, uint64(8192)) // 8KB

		// maxfilesize - Maximum file size
		binary.Write(&buf, binary.BigEndian, uint64(1099511627776)) // 1TB

		// time_delta - Server time granularity
		// seconds
		binary.Write(&buf, binary.BigEndian, uint32(0))
		// nanoseconds
		binary.Write(&buf, binary.BigEndian, uint32(1000000)) // 1ms

		// properties - File system properties
		var properties uint32 = 0
		if h.server.handler.options.ReadOnly {
			// FSF_READONLY - Read-only file system
			properties |= 1
		}
		// FSF_HOMOGENEOUS - UNIX attributes the same for all files
		properties |= 2
		// FSF_CANSETTIME - Server can set file times via SETATTR
		properties |= 4
		// FSF_SYMLINK - Server supports symbolic links
		properties |= 8
		// FSF_HARDLINK - Server supports hard links
		properties |= 16
		binary.Write(&buf, binary.BigEndian, properties)

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_PATHCONF:
		// Decode arguments
		handle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		handle.Handle = handleVal

		// Find the node
		fileNode, ok := h.server.handler.fileMap.Get(handle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := fileNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get attributes
		attrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// Encode post-op attributes (RFC 1813: attributes_follow boolean + fattr3)
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// Pathconf values
		// linkmax - Maximum number of hard links
		binary.Write(&buf, binary.BigEndian, uint32(1024))
		// name_max - Maximum filename length
		binary.Write(&buf, binary.BigEndian, uint32(255))
		// no_trunc - If true, filename > name_max gets error
		binary.Write(&buf, binary.BigEndian, uint32(1)) // true
		// chown_restricted - If true, only root can change owner
		binary.Write(&buf, binary.BigEndian, uint32(1)) // true
		// case_insensitive - If true, case insensitive filenames
		binary.Write(&buf, binary.BigEndian, uint32(0)) // false
		// case_preserving - If true, case preserving filenames
		binary.Write(&buf, binary.BigEndian, uint32(1)) // true

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_ACCESS:
		// Decode arguments
		handle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		handle.Handle = handleVal

		// Read access mask
		var access uint32
		if err := binary.Read(body, binary.BigEndian, &access); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the node
		fileNode, ok := h.server.handler.fileMap.Get(handle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := fileNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get attributes
		attrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Check access permissions based on file mode and read-only flag
		var accessAllowed uint32 = 0

		// ACCESS3_READ (1) - Read data from file or read a directory
		if access&1 != 0 {
			accessAllowed |= 1
		}

		// ACCESS3_LOOKUP (2) - Look up a name in a directory (only for directories)
		if access&2 != 0 && attrs.Mode&os.ModeDir != 0 {
			accessAllowed |= 2
		}

		if !h.server.handler.options.ReadOnly {
			// ACCESS3_MODIFY (4) - Rewrite existing file data or modify directory
			if access&4 != 0 {
				accessAllowed |= 4
			}

			// ACCESS3_EXTEND (8) - Add to a file or create a subdirectory
			if access&8 != 0 {
				accessAllowed |= 8
			}

			// ACCESS3_DELETE (16) - Delete a file or directory entry
			if access&16 != 0 {
				accessAllowed |= 16
			}

			// ACCESS3_EXECUTE (32) - Execute file or search directory
			if access&32 != 0 && (attrs.Mode&0111 != 0) {
				accessAllowed |= 32
			}
		}

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// Encode post-op attributes (RFC 1813: attributes_follow boolean + fattr3)
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// Encode access permissions
		binary.Write(&buf, binary.BigEndian, accessAllowed)

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_COMMIT:
		// Decode arguments
		handle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		handle.Handle = handleVal

		// Read offset and count
		var offset uint64
		var count uint32
		if err := binary.Read(body, binary.BigEndian, &offset); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		if err := binary.Read(body, binary.BigEndian, &count); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the node
		fileNode, ok := h.server.handler.fileMap.Get(handle.Handle)
		if !ok {
			// Return error with wcc_data (RFC 1813 COMMIT3resfail)
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			// file_wcc: wcc_data - no attributes available
			xdrEncodeUint32(&buf, 0) // pre_op_attr: attributes_follow = FALSE
			xdrEncodeUint32(&buf, 0) // post_op_attr: attributes_follow = FALSE
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := fileNode.(*NFSNode)
		if !ok {
			// Return error with wcc_data (RFC 1813 COMMIT3resfail)
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			// file_wcc: wcc_data - no attributes available
			xdrEncodeUint32(&buf, 0) // pre_op_attr: attributes_follow = FALSE
			xdrEncodeUint32(&buf, 0) // post_op_attr: attributes_follow = FALSE
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get attributes
		attrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// We don't need to do any special commit operation
		// For future: could implement fsync here

		// Encode response (RFC 1813 COMMIT3resok)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// Encode wcc_data (pre_op_attr + post_op_attr)
		// pre_op_attr: attributes_follow = FALSE (COMMIT doesn't modify attributes)
		xdrEncodeUint32(&buf, 0) // pre_op_attr attributes_follow = FALSE

		// post_op_attr: attributes_follow = TRUE + fattr3
		xdrEncodeUint32(&buf, 1) // post_op_attr attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// Encode write verifier (8 bytes)
		// Just using zeros for now, but should be a unique value per server restart
		for i := 0; i < 8; i++ {
			buf.WriteByte(0)
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_REMOVE:
		if h.server.handler.options.ReadOnly {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, ACCESS_DENIED)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Decode directory handle
		dirHandle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		dirHandle.Handle = handleVal

		// Decode filename
		name, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the directory node
		dirNode, ok := h.server.handler.fileMap.Get(dirHandle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := dirNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Check if it's a directory
		node.mu.RLock()
		isDir := node.attrs.Mode&os.ModeDir != 0
		node.mu.RUnlock()

		if !isDir {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOTDIR)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get directory attributes before the operation
		dirPreAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Remove the file
		if h.server.options.Debug {
			h.server.logger.Printf("REMOVE: Removing '%s' from directory '%s'", name, node.path)
		}
		if err := h.server.handler.Remove(node, name); err != nil {
			if h.server.options.Debug {
				h.server.logger.Printf("REMOVE: Failed to remove '%s': %v", name, err)
			}
			// Get post-operation directory attributes
			dirPostAttrs, _ := h.server.handler.GetAttr(node)
			if dirPostAttrs == nil {
				dirPostAttrs = dirPreAttrs
			}

			// Return error with wcc_data (RFC 1813 REMOVE3resfail)
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, mapError(err))
			// dir_wcc: wcc_data
			// pre_op_attr: attributes_follow + wcc_attr
			xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
			encodeWccAttr(&buf, dirPreAttrs)
			// post_op_attr: attributes_follow + fattr3
			xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
			encodeFileAttributes(&buf, dirPostAttrs)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		if h.server.options.Debug {
			h.server.logger.Printf("REMOVE: Successfully removed '%s' from '%s'", name, node.path)
		}

		// Get updated directory attributes
		dirPostAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Encode response (RFC 1813 REMOVE3resok)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// wcc_data: pre_op_attr + post_op_attr
		// pre_op_attr: attributes_follow + wcc_attr (NOT fattr3!)
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeWccAttr(&buf, dirPreAttrs); err != nil {
			return nil, err
		}

		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, dirPostAttrs); err != nil {
			return nil, err
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_RMDIR:
		if h.server.handler.options.ReadOnly {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, ACCESS_DENIED)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Decode directory handle
		dirHandle := FileHandle{}
		handleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		dirHandle.Handle = handleVal

		// Decode directory name
		name, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the parent directory node
		dirNode, ok := h.server.handler.fileMap.Get(dirHandle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		node, ok := dirNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Check if it's a directory
		node.mu.RLock()
		isDir := node.attrs.Mode&os.ModeDir != 0
		node.mu.RUnlock()

		if !isDir {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOTDIR)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get directory attributes before the operation
		dirPreAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Check if the target is really a directory
		targetPath := path.Join(node.path, name)
		targetInfo, err := h.server.handler.fs.Stat(targetPath)
		if err != nil {
			// Return error with wcc_data (RFC 1813 RMDIR3resfail)
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			// dir_wcc: wcc_data
			xdrEncodeUint32(&buf, 1) // pre_op_attr: attributes_follow = TRUE
			encodeWccAttr(&buf, dirPreAttrs)
			xdrEncodeUint32(&buf, 1)                // post_op_attr: attributes_follow = TRUE
			encodeFileAttributes(&buf, dirPreAttrs) // same as pre since no change
			reply.Data = buf.Bytes()
			return reply, nil
		}

		if !targetInfo.IsDir() {
			// Return error with wcc_data (RFC 1813 RMDIR3resfail)
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOTDIR)
			// dir_wcc: wcc_data
			xdrEncodeUint32(&buf, 1) // pre_op_attr: attributes_follow = TRUE
			encodeWccAttr(&buf, dirPreAttrs)
			xdrEncodeUint32(&buf, 1)                // post_op_attr: attributes_follow = TRUE
			encodeFileAttributes(&buf, dirPreAttrs) // same as pre since no change
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Try to remove the directory
		if err := h.server.handler.fs.Remove(targetPath); err != nil {
			// Get post-operation directory attributes
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
				// Directory might not be empty
				errCode = NFSERR_NOTEMPTY
			}

			// Return error with wcc_data (RFC 1813 RMDIR3resfail)
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, errCode)
			// dir_wcc: wcc_data
			xdrEncodeUint32(&buf, 1) // pre_op_attr: attributes_follow = TRUE
			encodeWccAttr(&buf, dirPreAttrs)
			xdrEncodeUint32(&buf, 1) // post_op_attr: attributes_follow = TRUE
			encodeFileAttributes(&buf, dirPostAttrs)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get updated directory attributes
		dirPostAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Encode response (RFC 1813 RMDIR3resok)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// wcc_data: pre_op_attr + post_op_attr
		// pre_op_attr: attributes_follow + wcc_attr (NOT fattr3!)
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeWccAttr(&buf, dirPreAttrs); err != nil {
			return nil, err
		}

		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, dirPostAttrs); err != nil {
			return nil, err
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_RENAME:
		if h.server.handler.options.ReadOnly {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, ACCESS_DENIED)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Decode source directory handle
		srcDirHandle := FileHandle{}
		srcHandleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		srcDirHandle.Handle = srcHandleVal

		// Decode source filename
		srcName, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Decode destination directory handle
		dstDirHandle := FileHandle{}
		dstHandleVal, err := xdrDecodeFileHandle(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		dstDirHandle.Handle = dstHandleVal

		// Decode destination filename
		dstName, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the source directory node
		srcDirNode, ok := h.server.handler.fileMap.Get(srcDirHandle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		srcDir, ok := srcDirNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Find the destination directory node
		dstDirNode, ok := h.server.handler.fileMap.Get(dstDirHandle.Handle)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		dstDir, ok := dstDirNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get source directory attributes before the operation
		srcDirPreAttrs, err := h.server.handler.GetAttr(srcDir)
		if err != nil {
			return nil, err
		}

		// Get destination directory attributes before the operation
		dstDirPreAttrs, err := h.server.handler.GetAttr(dstDir)
		if err != nil {
			return nil, err
		}

		// Perform the rename
		if err := h.server.handler.Rename(srcDir, srcName, dstDir, dstName); err != nil {
			// Get post-operation directory attributes
			srcDirPostAttrs, _ := h.server.handler.GetAttr(srcDir)
			if srcDirPostAttrs == nil {
				srcDirPostAttrs = srcDirPreAttrs
			}
			dstDirPostAttrs, _ := h.server.handler.GetAttr(dstDir)
			if dstDirPostAttrs == nil {
				dstDirPostAttrs = dstDirPreAttrs
			}

			// Return error with wcc_data (RFC 1813 RENAME3resfail)
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, mapError(err))

			// fromdir_wcc: wcc_data for source directory
			xdrEncodeUint32(&buf, 1) // pre_op_attr: attributes_follow = TRUE
			encodeWccAttr(&buf, srcDirPreAttrs)
			xdrEncodeUint32(&buf, 1) // post_op_attr: attributes_follow = TRUE
			encodeFileAttributes(&buf, srcDirPostAttrs)

			// todir_wcc: wcc_data for destination directory
			xdrEncodeUint32(&buf, 1) // pre_op_attr: attributes_follow = TRUE
			encodeWccAttr(&buf, dstDirPreAttrs)
			xdrEncodeUint32(&buf, 1) // post_op_attr: attributes_follow = TRUE
			encodeFileAttributes(&buf, dstDirPostAttrs)

			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get updated source directory attributes
		srcDirPostAttrs, err := h.server.handler.GetAttr(srcDir)
		if err != nil {
			return nil, err
		}

		// Get updated destination directory attributes
		dstDirPostAttrs, err := h.server.handler.GetAttr(dstDir)
		if err != nil {
			return nil, err
		}

		// Encode response (RFC 1813 RENAME3resok)
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)

		// fromdir_wcc: wcc_data for source directory
		// pre_op_attr: attributes_follow + wcc_attr
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeWccAttr(&buf, srcDirPreAttrs); err != nil {
			return nil, err
		}
		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, srcDirPostAttrs); err != nil {
			return nil, err
		}

		// todir_wcc: wcc_data for destination directory
		// pre_op_attr: attributes_follow + wcc_attr
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeWccAttr(&buf, dstDirPreAttrs); err != nil {
			return nil, err
		}
		// post_op_attr: attributes_follow + fattr3
		xdrEncodeUint32(&buf, 1) // attributes_follow = TRUE
		if err := encodeFileAttributes(&buf, dstDirPostAttrs); err != nil {
			return nil, err
		}

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_LINK:
		// LINK operation is not supported - hard links are not implemented
		// Return NFSERR_NOTSUPP to indicate this operation is not available
		var buf bytes.Buffer
		notSupported := &NotSupportedError{
			Operation: "LINK",
			Reason:    "hard links are not supported by this NFS implementation",
		}
		xdrEncodeUint32(&buf, mapError(notSupported))
		reply.Data = buf.Bytes()
		return reply, nil

	default:
		reply.AcceptStatus = PROC_UNAVAIL
		return reply, nil
	}
}
