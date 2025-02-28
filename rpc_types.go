package absnfs

import (
	"encoding/binary"
	"fmt"
	"io"
)

// RPC message types
const (
	RPC_CALL  = 0
	RPC_REPLY = 1
)

// RPC reply status
const (
	MSG_ACCEPTED  = 0
	MSG_DENIED    = 1
	PROG_UNAVAIL  = 1
	PROG_MISMATCH = 2
	PROC_UNAVAIL  = 3
	GARBAGE_ARGS  = 4
	ACCESS_DENIED = 5
)

// RPC program numbers
const (
	MOUNT_PROGRAM = 100005
	NFS_PROGRAM   = 100003
)

// RPC versions
const (
	MOUNT_V3 = 3
	NFS_V3   = 3
)

// RPC procedures for NFS v3
const (
	NFSPROC3_NULL        = 0
	NFSPROC3_GETATTR     = 1
	NFSPROC3_SETATTR     = 2
	NFSPROC3_LOOKUP      = 3
	NFSPROC3_ACCESS      = 4
	NFSPROC3_READLINK    = 5
	NFSPROC3_READ        = 6
	NFSPROC3_WRITE       = 7
	NFSPROC3_CREATE      = 8
	NFSPROC3_MKDIR       = 9
	NFSPROC3_SYMLINK     = 10
	NFSPROC3_MKNOD       = 11
	NFSPROC3_REMOVE      = 12
	NFSPROC3_RMDIR       = 13
	NFSPROC3_RENAME      = 14
	NFSPROC3_LINK        = 15
	NFSPROC3_READDIR     = 16
	NFSPROC3_READDIRPLUS = 17
	NFSPROC3_FSSTAT      = 18
	NFSPROC3_FSINFO      = 19
	NFSPROC3_PATHCONF    = 20
	NFSPROC3_COMMIT      = 21
)

// RPC message header
type RPCMsgHeader struct {
	Xid        uint32
	MsgType    uint32
	RPCVersion uint32
	Program    uint32
	Version    uint32
	Procedure  uint32
}

// XDR encoding/decoding helpers
func xdrEncodeUint32(w io.Writer, v uint32) error {
	return binary.Write(w, binary.BigEndian, v)
}

func xdrDecodeUint32(r io.Reader) (uint32, error) {
	var v uint32
	err := binary.Read(r, binary.BigEndian, &v)
	return v, err
}

func xdrEncodeString(w io.Writer, s string) error {
	if err := xdrEncodeUint32(w, uint32(len(s))); err != nil {
		return err
	}
	_, err := w.Write([]byte(s))
	return err
}

func xdrDecodeString(r io.Reader) (string, error) {
	length, err := xdrDecodeUint32(r)
	if err != nil {
		return "", err
	}

	buf := make([]byte, length)
	_, err = io.ReadFull(r, buf)
	if err != nil {
		return "", err
	}

	return string(buf), nil
}

// RPCCall represents an incoming RPC call
type RPCCall struct {
	Header     RPCMsgHeader
	Credential RPCCredential
	Verifier   RPCVerifier
}

// RPCCredential represents RPC authentication credentials
type RPCCredential struct {
	Flavor uint32
	Body   []byte
}

// RPCVerifier represents RPC authentication verifier
type RPCVerifier struct {
	Flavor uint32
	Body   []byte
}

// RPCReply represents an RPC reply message
type RPCReply struct {
	Header   RPCMsgHeader
	Status   uint32
	Verifier RPCVerifier
	Data     interface{}
}

// DecodeRPCCall decodes an RPC call from a reader
func DecodeRPCCall(r io.Reader) (*RPCCall, error) {
	call := &RPCCall{}

	// Decode header
	xid, err := xdrDecodeUint32(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decode XID: %v", err)
	}
	call.Header.Xid = xid

	msgType, err := xdrDecodeUint32(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decode message type: %v", err)
	}
	if msgType != RPC_CALL {
		return nil, fmt.Errorf("expected RPC call, got message type %d", msgType)
	}
	call.Header.MsgType = msgType

	// Decode RPC version, program, version, and procedure
	if call.Header.RPCVersion, err = xdrDecodeUint32(r); err != nil {
		return nil, fmt.Errorf("failed to decode RPC version: %v", err)
	}
	if call.Header.Program, err = xdrDecodeUint32(r); err != nil {
		return nil, fmt.Errorf("failed to decode program: %v", err)
	}
	if call.Header.Version, err = xdrDecodeUint32(r); err != nil {
		return nil, fmt.Errorf("failed to decode version: %v", err)
	}
	if call.Header.Procedure, err = xdrDecodeUint32(r); err != nil {
		return nil, fmt.Errorf("failed to decode procedure: %v", err)
	}

	// Decode credential
	if call.Credential.Flavor, err = xdrDecodeUint32(r); err != nil {
		return nil, fmt.Errorf("failed to decode credential flavor: %v", err)
	}
	credLen, err := xdrDecodeUint32(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decode credential length: %v", err)
	}
	call.Credential.Body = make([]byte, credLen)
	if _, err = io.ReadFull(r, call.Credential.Body); err != nil {
		return nil, fmt.Errorf("failed to read credential body: %v", err)
	}

	// Decode verifier
	if call.Verifier.Flavor, err = xdrDecodeUint32(r); err != nil {
		return nil, fmt.Errorf("failed to decode verifier flavor: %v", err)
	}
	verLen, err := xdrDecodeUint32(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decode verifier length: %v", err)
	}
	call.Verifier.Body = make([]byte, verLen)
	if _, err = io.ReadFull(r, call.Verifier.Body); err != nil {
		return nil, fmt.Errorf("failed to read verifier body: %v", err)
	}

	return call, nil
}

// EncodeRPCReply encodes an RPC reply to a writer
func EncodeRPCReply(w io.Writer, reply *RPCReply) error {
	// Encode header
	if err := xdrEncodeUint32(w, reply.Header.Xid); err != nil {
		return fmt.Errorf("failed to encode XID: %v", err)
	}
	if err := xdrEncodeUint32(w, RPC_REPLY); err != nil {
		return fmt.Errorf("failed to encode message type: %v", err)
	}

	// Encode reply status
	if err := xdrEncodeUint32(w, reply.Status); err != nil {
		return fmt.Errorf("failed to encode reply status: %v", err)
	}

	// Encode verifier
	if err := xdrEncodeUint32(w, reply.Verifier.Flavor); err != nil {
		return fmt.Errorf("failed to encode verifier flavor: %v", err)
	}
	if err := xdrEncodeUint32(w, uint32(len(reply.Verifier.Body))); err != nil {
		return fmt.Errorf("failed to encode verifier length: %v", err)
	}
	if _, err := w.Write(reply.Verifier.Body); err != nil {
		return fmt.Errorf("failed to write verifier body: %v", err)
	}

	// Encode reply data based on procedure type
	if reply.Data != nil {
		switch data := reply.Data.(type) {
		case []byte:
			// Raw byte data (pre-encoded)
			_, err := w.Write(data)
			return err
		case *NFSAttrs:
			// File attributes
			return encodeFileAttributes(w, data)
		case string:
			// String data (mainly for error messages)
			return xdrEncodeString(w, data)
		case uint32:
			// Status or error code
			return xdrEncodeUint32(w, data)
		default:
			// If no specific encoding is provided, assume data is already encoded
			if dataBytes, ok := data.([]byte); ok {
				_, err := w.Write(dataBytes)
				return err
			}
		}
	}

	return nil
}
