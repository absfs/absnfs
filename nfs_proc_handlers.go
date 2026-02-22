// nfs_proc_handlers.go: NFSv3 shared types and miscellaneous procedures.
//
// Defines the sattr3 type and its decoder, used across all handlers that
// accept client-supplied attributes. Also implements NULL, FSSTAT, FSINFO,
// and PATHCONF as defined in RFC 1813 sections 3.3.1, 3.3.18, 3.3.19,
// and 3.3.20.
package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
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
	binary.Write(&buf, binary.BigEndian, uint32(1))                 // invarsec (1 second stability)

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
	// FSF3_SYMLINK=0x0002, FSF3_HOMOGENEOUS=0x0008, FSF3_CANSETTIME=0x0010
	// FSF3_LINK (0x0001) is NOT set because handleLink always returns NFSERR_NOTSUPP
	var properties uint32 = 0x0002 | 0x0008 | 0x0010 // symlink + homogeneous + cansettime
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
