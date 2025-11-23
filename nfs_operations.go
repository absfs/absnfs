package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"os"
)

// handleNFSCall handles NFS protocol operations
func (h *NFSProcedureHandler) handleNFSCall(call *RPCCall, body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	// Check version first
	if call.Header.Version != NFS_V3 {
		reply.Status = PROG_MISMATCH
		return reply, nil
	}

	// Set default status to MSG_ACCEPTED
	reply.Status = MSG_ACCEPTED

	switch call.Header.Procedure {
	case NFSPROC3_NULL:
		return reply, nil

	case NFSPROC3_GETATTR:
		handle := FileHandle{}
		if err := binary.Read(body, binary.BigEndian, &handle.Handle); err != nil {
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
		if err := binary.Read(body, binary.BigEndian, &handle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

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

	case NFSPROC3_LOOKUP:
		// Decode directory handle
		dirHandle := FileHandle{}
		if err := binary.Read(body, binary.BigEndian, &dirHandle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

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

		// Verify it's a directory
		node, ok := dirNode.(*NFSNode)
		if !ok {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
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
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Lookup the file with timeout
		lookupNode, err := h.server.handler.Lookup(node.path + "/" + name)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Allocate new handle
		handle := h.server.handler.fileMap.Allocate(lookupNode)

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		binary.Write(&buf, binary.BigEndian, handle)
		if err := encodeFileAttributes(&buf, lookupNode.attrs); err != nil {
			return nil, err
		}
		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_READ:
		handle := FileHandle{}
		if err := binary.Read(body, binary.BigEndian, &handle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

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

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// Handle empty data (EOF) case
		if len(data) == 0 {
			binary.Write(&buf, binary.BigEndian, uint32(0)) // Count read = 0
			binary.Write(&buf, binary.BigEndian, true)      // EOF flag = true
		} else {
			binary.Write(&buf, binary.BigEndian, uint32(len(data))) // Count read
			binary.Write(&buf, binary.BigEndian, false)             // EOF flag = false
			buf.Write(data)                                         // Write data
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
		if err := binary.Read(body, binary.BigEndian, &handle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

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

		// Write data
		n, err := h.server.handler.Write(node, int64(offset), data)
		if err != nil {
			return nil, err
		}

		// Get updated attributes
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
		binary.Write(&buf, binary.BigEndian, uint32(n)) // Count written
		binary.Write(&buf, binary.BigEndian, stable)    // Write stability level
		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_CREATE:
		// Decode directory handle
		dirHandle := FileHandle{}
		if err := binary.Read(body, binary.BigEndian, &dirHandle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Decode filename
		name, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Read create mode and attributes
		var createMode uint32
		if err := binary.Read(body, binary.BigEndian, &createMode); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		var mode uint32
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

		// Create new attributes
		attrs := &NFSAttrs{
			Mode: os.FileMode(mode),
			Uid:  0,
			Gid:  0,
		}

		// Create the file
		newNode, err := h.server.handler.Create(node, name, attrs)
		if err != nil {
			return nil, err
		}

		// Allocate new handle
		handle := h.server.handler.fileMap.Allocate(newNode)

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		binary.Write(&buf, binary.BigEndian, handle)
		if err := encodeFileAttributes(&buf, newNode.attrs); err != nil {
			return nil, err
		}
		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_MKDIR:
		// Decode directory handle
		dirHandle := FileHandle{}
		if err := binary.Read(body, binary.BigEndian, &dirHandle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Decode dirname
		name, err := xdrDecodeString(body)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
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
		if mode&0x8000 != 0 {
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

		// Create the directory
		if err := h.server.handler.fs.Mkdir(node.path+"/"+name, os.FileMode(mode)); err != nil {
			return nil, err
		}

		// Lookup the new directory
		newNode, err := h.server.handler.Lookup(node.path + "/" + name)
		if err != nil {
			return nil, err
		}

		// Allocate new handle
		handle := h.server.handler.fileMap.Allocate(newNode)

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		binary.Write(&buf, binary.BigEndian, handle)
		if err := encodeFileAttributes(&buf, newNode.attrs); err != nil {
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
		if err := binary.Read(body, binary.BigEndian, &handle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

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
		
		// Encode post-op attributes (directory attributes)
		attrs, err := h.server.handler.GetAttr(dir)
		if err != nil {
			return nil, err
		}
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// Encode cookie verifier (same as input for now)
		buf.Write(cookieVerf[:])

		// Encode directory entries
		entryCount := 0
		maxReplySize := int(count) - 100 // Leave room for headers
		
		// Start encoding entries
		// Value 1 means at least one entry follows
		xdrEncodeUint32(&buf, 1)
		
		for i, entry := range entries {
			if uint64(i) < cookie {
				continue
			}
			
			// Calculate this entry's cookie
			entryCookie := uint64(i + 1)
			
			// Encode fileid (we'll use node handle as fileid)
			entryHandle := h.server.handler.fileMap.Allocate(entry)
			xdrEncodeUint32(&buf, uint32(entryHandle))
			
			// Encode name
			name := ""
			if entry.path == "/" {
				name = "/"
			} else {
				// Extract just the filename from the path
				lastSlash := 0
				for i := len(entry.path) - 1; i >= 0; i-- {
					if entry.path[i] == '/' {
						lastSlash = i
						break
					}
				}
				if lastSlash < len(entry.path)-1 {
					name = entry.path[lastSlash+1:]
				}
			}
			xdrEncodeString(&buf, name)
			
			// Encode cookie
			binary.Write(&buf, binary.BigEndian, entryCookie)
			
			entryCount++
			
			// Check if we need to stop encoding to respect maxReplySize
			if buf.Len() >= maxReplySize && i < len(entries)-1 {
				// More entries remain, but we've hit our limit
				// Value 1 means more entries follow
				xdrEncodeUint32(&buf, 1)
				break
			}
			
			// If this is the last entry, signal that no more entries follow
			if i == len(entries)-1 {
				xdrEncodeUint32(&buf, 0)
			} else {
				// More entries follow
				xdrEncodeUint32(&buf, 1)
			}
		}
		
		// If no entries were encoded at all
		if entryCount == 0 {
			// No entries follow
			xdrEncodeUint32(&buf, 0)
		}
		
		// Encode EOF (true if we've reached the end)
		eof := entryCount == 0 || entryCount == len(entries)
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
		if err := binary.Read(body, binary.BigEndian, &handle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

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
		
		// Encode post-op attributes (directory attributes)
		attrs, err := h.server.handler.GetAttr(dir)
		if err != nil {
			return nil, err
		}
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// Encode cookie verifier (same as input for now)
		buf.Write(cookieVerf[:])

		// Encode directory entries
		entryCount := 0
		maxReplySize := int(maxCount) - 200 // Leave room for headers and attributes
		
		// Start encoding entries
		// Value 1 means at least one entry follows
		xdrEncodeUint32(&buf, 1)
		
		for i, entry := range entries {
			if uint64(i) < cookie {
				continue
			}
			
			// Calculate this entry's cookie
			entryCookie := uint64(i + 1)
			
			// Encode fileid (we'll use node handle as fileid)
			entryHandle := h.server.handler.fileMap.Allocate(entry)
			xdrEncodeUint32(&buf, uint32(entryHandle))
			
			// Encode name
			name := ""
			if entry.path == "/" {
				name = "/"
			} else {
				// Extract just the filename from the path
				lastSlash := 0
				for i := len(entry.path) - 1; i >= 0; i-- {
					if entry.path[i] == '/' {
						lastSlash = i
						break
					}
				}
				if lastSlash < len(entry.path)-1 {
					name = entry.path[lastSlash+1:]
				}
			}
			xdrEncodeString(&buf, name)
			
			// Encode cookie
			binary.Write(&buf, binary.BigEndian, entryCookie)
			
			// Encode file attributes
			xdrEncodeUint32(&buf, 1) // Has attributes
			if err := encodeFileAttributes(&buf, entry.attrs); err != nil {
				return nil, err
			}
			
			// Encode file handle
			xdrEncodeUint32(&buf, 1) // Has handle
			binary.Write(&buf, binary.BigEndian, entryHandle)
			
			entryCount++
			
			// Check if we need to stop encoding to respect maxReplySize
			if buf.Len() >= maxReplySize && i < len(entries)-1 {
				// More entries remain, but we've hit our limit
				// Value 1 means more entries follow
				xdrEncodeUint32(&buf, 1)
				break
			}
			
			// If this is the last entry, signal that no more entries follow
			if i == len(entries)-1 {
				xdrEncodeUint32(&buf, 0)
			} else {
				// More entries follow
				xdrEncodeUint32(&buf, 1)
			}
		}
		
		// If no entries were encoded at all
		if entryCount == 0 {
			// No entries follow
			xdrEncodeUint32(&buf, 0)
		}
		
		// Encode EOF (true if we've reached the end)
		eof := entryCount == 0 || entryCount == len(entries)
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
		if err := binary.Read(body, binary.BigEndian, &handle.Handle); err != nil {
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

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		
		// Encode post-op attributes
		if err := encodeFileAttributes(&buf, attrs); err != nil {
			return nil, err
		}

		// We don't have actual filesystem stats, so provide dummy values
		// Total bytes
		binary.Write(&buf, binary.BigEndian, uint64(1024*1024*1024*10)) // 10GB
		// Free bytes
		binary.Write(&buf, binary.BigEndian, uint64(1024*1024*1024*5)) // 5GB
		// Available bytes
		binary.Write(&buf, binary.BigEndian, uint64(1024*1024*1024*5)) // 5GB
		// Total files
		binary.Write(&buf, binary.BigEndian, uint64(1000000))
		// Free files
		binary.Write(&buf, binary.BigEndian, uint64(900000))
		// Invariant time
		binary.Write(&buf, binary.BigEndian, uint32(0))

		reply.Data = buf.Bytes()
		return reply, nil

	case NFSPROC3_FSINFO:
		// Decode arguments
		handle := FileHandle{}
		if err := binary.Read(body, binary.BigEndian, &handle.Handle); err != nil {
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

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		
		// Encode post-op attributes
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
		
		// dtpref - Preferred READDIR request size
		binary.Write(&buf, binary.BigEndian, uint32(8192)) // 8KB
		
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
		if err := binary.Read(body, binary.BigEndian, &handle.Handle); err != nil {
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

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		
		// Encode post-op attributes
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
		if err := binary.Read(body, binary.BigEndian, &handle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

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
		
		// Encode post-op attributes
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
		if err := binary.Read(body, binary.BigEndian, &handle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

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

		// We don't need to do any special commit operation
		// For future: could implement fsync here

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		
		// Encode post-op attributes
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
		if err := binary.Read(body, binary.BigEndian, &dirHandle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

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
		if err := h.server.handler.Remove(node, name); err != nil {
			// Map the error
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, mapError(err))
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get updated directory attributes
		dirPostAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		
		// Encode pre-operation directory attributes (has attributes)
		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, dirPreAttrs); err != nil {
			return nil, err
		}
		
		// Encode post-operation directory attributes (has attributes)
		xdrEncodeUint32(&buf, 1)
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
		if err := binary.Read(body, binary.BigEndian, &dirHandle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

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
		targetPath := node.path + "/" + name
		targetInfo, err := h.server.handler.fs.Stat(targetPath)
		if err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOENT)
			reply.Data = buf.Bytes()
			return reply, nil
		}
		
		if !targetInfo.IsDir() {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, NFSERR_NOTDIR)
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Try to remove the directory
		if err := h.server.handler.fs.Remove(targetPath); err != nil {
			var buf bytes.Buffer
			if os.IsPermission(err) {
				xdrEncodeUint32(&buf, NFSERR_ACCES)
			} else if os.IsNotExist(err) {
				xdrEncodeUint32(&buf, NFSERR_NOENT)
			} else {
				// Directory might not be empty
				xdrEncodeUint32(&buf, NFSERR_NOTEMPTY)
			}
			reply.Data = buf.Bytes()
			return reply, nil
		}

		// Get updated directory attributes
		dirPostAttrs, err := h.server.handler.GetAttr(node)
		if err != nil {
			return nil, err
		}

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		
		// Encode pre-operation directory attributes (has attributes)
		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, dirPreAttrs); err != nil {
			return nil, err
		}
		
		// Encode post-operation directory attributes (has attributes)
		xdrEncodeUint32(&buf, 1)
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
		if err := binary.Read(body, binary.BigEndian, &srcDirHandle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

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
		if err := binary.Read(body, binary.BigEndian, &dstDirHandle.Handle); err != nil {
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, GARBAGE_ARGS)
			reply.Data = buf.Bytes()
			return reply, nil
		}

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
			var buf bytes.Buffer
			xdrEncodeUint32(&buf, mapError(err))
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

		// Encode response
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFS_OK)
		
		// Encode source pre-operation directory attributes (has attributes)
		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, srcDirPreAttrs); err != nil {
			return nil, err
		}
		
		// Encode source post-operation directory attributes (has attributes)
		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, srcDirPostAttrs); err != nil {
			return nil, err
		}
		
		// Encode destination pre-operation directory attributes (has attributes)
		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, dstDirPreAttrs); err != nil {
			return nil, err
		}
		
		// Encode destination post-operation directory attributes (has attributes)
		xdrEncodeUint32(&buf, 1)
		if err := encodeFileAttributes(&buf, dstDirPostAttrs); err != nil {
			return nil, err
		}

		reply.Data = buf.Bytes()
		return reply, nil

	default:
		reply.Status = PROC_UNAVAIL
		return reply, nil
	}
}
