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

// RPC authentication flavors (RFC 1831)
const (
	AUTH_NONE  = 0 // No authentication
	AUTH_SYS   = 1 // UNIX-style authentication (formerly AUTH_UNIX)
	AUTH_SHORT = 2 // Short hand UNIX-style
	AUTH_DH    = 3 // Diffie-Hellman authentication
)

// Maximum sizes for XDR data structures to prevent DoS attacks
const (
	// MAX_XDR_STRING_LENGTH is the maximum allowed length for XDR strings (8KB)
	// This prevents memory exhaustion attacks where malicious clients send
	// extremely large length values
	MAX_XDR_STRING_LENGTH = 8192

	// MAX_RPC_AUTH_LENGTH is the maximum allowed length for RPC authentication
	// credentials and verifiers (400 bytes as per RFC 1831)
	MAX_RPC_AUTH_LENGTH = 400
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

	// Validate length to prevent DoS attacks via memory exhaustion
	if length > MAX_XDR_STRING_LENGTH {
		return "", fmt.Errorf("XDR string length %d exceeds maximum allowed length %d", length, MAX_XDR_STRING_LENGTH)
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

// AuthSysCredential represents AUTH_SYS credentials (RFC 1831)
type AuthSysCredential struct {
	Stamp      uint32   // Arbitrary ID which the client may generate
	MachineName string  // Name of the client machine (or empty string)
	UID        uint32   // Caller's effective user ID
	GID        uint32   // Caller's effective group ID
	AuxGIDs    []uint32 // Auxiliary group IDs
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
		return nil, fmt.Errorf("failed to decode XID: %w", err)
	}
	call.Header.Xid = xid

	msgType, err := xdrDecodeUint32(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decode message type: %w", err)
	}
	if msgType != RPC_CALL {
		return nil, fmt.Errorf("expected RPC call, got message type %d", msgType)
	}
	call.Header.MsgType = msgType

	// Decode RPC version, program, version, and procedure
	if call.Header.RPCVersion, err = xdrDecodeUint32(r); err != nil {
		return nil, fmt.Errorf("failed to decode RPC version: %w", err)
	}
	if call.Header.Program, err = xdrDecodeUint32(r); err != nil {
		return nil, fmt.Errorf("failed to decode program: %w", err)
	}
	if call.Header.Version, err = xdrDecodeUint32(r); err != nil {
		return nil, fmt.Errorf("failed to decode version: %w", err)
	}
	if call.Header.Procedure, err = xdrDecodeUint32(r); err != nil {
		return nil, fmt.Errorf("failed to decode procedure: %w", err)
	}

	// Decode credential
	if call.Credential.Flavor, err = xdrDecodeUint32(r); err != nil {
		return nil, fmt.Errorf("failed to decode credential flavor: %w", err)
	}
	credLen, err := xdrDecodeUint32(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decode credential length: %w", err)
	}
	// Validate credential length to prevent DoS attacks via memory exhaustion
	if credLen > MAX_RPC_AUTH_LENGTH {
		return nil, fmt.Errorf("credential length %d exceeds maximum allowed length %d", credLen, MAX_RPC_AUTH_LENGTH)
	}
	call.Credential.Body = make([]byte, credLen)
	if _, err = io.ReadFull(r, call.Credential.Body); err != nil {
		return nil, fmt.Errorf("failed to read credential body: %w", err)
	}

	// Decode verifier
	if call.Verifier.Flavor, err = xdrDecodeUint32(r); err != nil {
		return nil, fmt.Errorf("failed to decode verifier flavor: %w", err)
	}
	verLen, err := xdrDecodeUint32(r)
	if err != nil {
		return nil, fmt.Errorf("failed to decode verifier length: %w", err)
	}
	// Validate verifier length to prevent DoS attacks via memory exhaustion
	if verLen > MAX_RPC_AUTH_LENGTH {
		return nil, fmt.Errorf("verifier length %d exceeds maximum allowed length %d", verLen, MAX_RPC_AUTH_LENGTH)
	}
	call.Verifier.Body = make([]byte, verLen)
	if _, err = io.ReadFull(r, call.Verifier.Body); err != nil {
		return nil, fmt.Errorf("failed to read verifier body: %w", err)
	}

	return call, nil
}

// EncodeRPCReply encodes an RPC reply to a writer
func EncodeRPCReply(w io.Writer, reply *RPCReply) error {
	// Encode header
	if err := xdrEncodeUint32(w, reply.Header.Xid); err != nil {
		return fmt.Errorf("failed to encode XID: %w", err)
	}
	if err := xdrEncodeUint32(w, RPC_REPLY); err != nil {
		return fmt.Errorf("failed to encode message type: %w", err)
	}

	// Encode reply status
	if err := xdrEncodeUint32(w, reply.Status); err != nil {
		return fmt.Errorf("failed to encode reply status: %w", err)
	}

	// Encode verifier
	if err := xdrEncodeUint32(w, reply.Verifier.Flavor); err != nil {
		return fmt.Errorf("failed to encode verifier flavor: %w", err)
	}
	if err := xdrEncodeUint32(w, uint32(len(reply.Verifier.Body))); err != nil {
		return fmt.Errorf("failed to encode verifier length: %w", err)
	}
	if _, err := w.Write(reply.Verifier.Body); err != nil {
		return fmt.Errorf("failed to write verifier body: %w", err)
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

// ParseAuthSysCredential parses AUTH_SYS credential data from raw bytes
func ParseAuthSysCredential(body []byte) (*AuthSysCredential, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty AUTH_SYS credential body")
	}

	r := &byteReader{data: body, pos: 0}
	cred := &AuthSysCredential{}

	// Read stamp (4 bytes)
	var err error
	cred.Stamp, err = r.readUint32()
	if err != nil {
		return nil, fmt.Errorf("failed to read stamp: %w", err)
	}

	// Read machine name
	cred.MachineName, err = r.readString()
	if err != nil {
		return nil, fmt.Errorf("failed to read machine name: %w", err)
	}

	// Read UID
	cred.UID, err = r.readUint32()
	if err != nil {
		return nil, fmt.Errorf("failed to read UID: %w", err)
	}

	// Read GID
	cred.GID, err = r.readUint32()
	if err != nil {
		return nil, fmt.Errorf("failed to read GID: %w", err)
	}

	// Read auxiliary GIDs count
	gidCount, err := r.readUint32()
	if err != nil {
		return nil, fmt.Errorf("failed to read GID count: %w", err)
	}

	// Validate GID count to prevent DoS
	if gidCount > 16 {
		return nil, fmt.Errorf("too many auxiliary GIDs: %d (max 16)", gidCount)
	}

	// Read auxiliary GIDs
	cred.AuxGIDs = make([]uint32, gidCount)
	for i := uint32(0); i < gidCount; i++ {
		cred.AuxGIDs[i], err = r.readUint32()
		if err != nil {
			return nil, fmt.Errorf("failed to read auxiliary GID %d: %w", i, err)
		}
	}

	return cred, nil
}

// byteReader is a simple helper for reading XDR-encoded data from a byte slice
type byteReader struct {
	data []byte
	pos  int
}

func (r *byteReader) readUint32() (uint32, error) {
	if r.pos+4 > len(r.data) {
		return 0, fmt.Errorf("not enough data for uint32")
	}
	val := binary.BigEndian.Uint32(r.data[r.pos : r.pos+4])
	r.pos += 4
	return val, nil
}

func (r *byteReader) readString() (string, error) {
	length, err := r.readUint32()
	if err != nil {
		return "", err
	}

	// Validate length
	if length > MAX_XDR_STRING_LENGTH {
		return "", fmt.Errorf("string length %d exceeds maximum %d", length, MAX_XDR_STRING_LENGTH)
	}

	// Calculate padded length (XDR strings are padded to 4-byte boundaries)
	paddedLength := (length + 3) &^ 3

	if r.pos+int(paddedLength) > len(r.data) {
		return "", fmt.Errorf("not enough data for string")
	}

	str := string(r.data[r.pos : r.pos+int(length)])
	r.pos += int(paddedLength)
	return str, nil
}
