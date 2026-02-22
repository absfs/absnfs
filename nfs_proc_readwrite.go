package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
)

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
	if count > 65536 && h.server.handler.rateLimiter != nil && h.server.handler.policy.Load().EnableRateLimiting {
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
	if h.server.handler.policy.Load().ReadOnly {
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
	if count > 65536 && h.server.handler.rateLimiter != nil && h.server.handler.policy.Load().EnableRateLimiting {
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

	// Bound count to the server's advertised write size to prevent DoS
	maxWriteSize := uint32(h.server.handler.tuning.Load().TransferSize)
	if maxWriteSize == 0 {
		maxWriteSize = 1048576 // 1MB default
	}
	if count > maxWriteSize {
		return nfsErrorWithWcc(reply, NFSERR_INVAL), nil
	}

	data := make([]byte, count)
	if _, err := io.ReadFull(body, data); err != nil {
		return nfsErrorWithWcc(reply, GARBAGE_ARGS), nil
	}

	// Consume XDR opaque padding (0-3 bytes to reach 4-byte boundary)
	if pad := (4 - int(count)%4) % 4; pad > 0 {
		padBuf := make([]byte, pad)
		io.ReadFull(body, padBuf) // Best effort - padding may not be present in all implementations
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
	xdrEncodeUint32(&buf, 2)         // FILE_SYNC: server does synchronous writes
	buf.Write(h.server.writeVerf[:]) // writeverf unique per server boot

	reply.Data = buf.Bytes()
	return reply, nil
}

// handleCommit handles NFSPROC3_COMMIT - commit cached data
func (h *NFSProcedureHandler) handleCommit(body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error) {
	if h.server.handler.policy.Load().ReadOnly {
		return nfsErrorWithWcc(reply, NFSERR_ROFS), nil
	}

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
	buf.Write(h.server.writeVerf[:]) // writeverf unique per server boot

	reply.Data = buf.Bytes()
	return reply, nil
}
