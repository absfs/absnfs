package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync"
	"testing"
)

func TestXDREncoding(t *testing.T) {
	t.Run("uint32 encoding", func(t *testing.T) {
		var buf bytes.Buffer
		testValue := uint32(12345)

		// Test encoding
		err := xdrEncodeUint32(&buf, testValue)
		if err != nil {
			t.Errorf("xdrEncodeUint32 failed: %v", err)
		}

		// Test decoding
		decoded, err := xdrDecodeUint32(&buf)
		if err != nil {
			t.Errorf("xdrDecodeUint32 failed: %v", err)
		}
		if decoded != testValue {
			t.Errorf("Expected %d, got %d", testValue, decoded)
		}

		// Test decode error on empty buffer
		_, err = xdrDecodeUint32(&bytes.Buffer{})
		if err == nil {
			t.Error("Expected error decoding from empty buffer")
		}
	})

	t.Run("string encoding", func(t *testing.T) {
		var buf bytes.Buffer
		testString := "Hello, NFS!"

		// Test encoding
		err := xdrEncodeString(&buf, testString)
		if err != nil {
			t.Errorf("xdrEncodeString failed: %v", err)
		}

		// Test decoding
		decoded, err := xdrDecodeString(&buf)
		if err != nil {
			t.Errorf("xdrDecodeString failed: %v", err)
		}
		if decoded != testString {
			t.Errorf("Expected %q, got %q", testString, decoded)
		}

		// Test decode error on empty buffer
		_, err = xdrDecodeString(&bytes.Buffer{})
		if err == nil {
			t.Error("Expected error decoding from empty buffer")
		}

		// Test decode error on truncated length
		buf.Reset()
		binary.Write(&buf, binary.BigEndian, uint32(100)) // Length longer than actual data
		buf.WriteString("short")
		_, err = xdrDecodeString(&buf)
		if err == nil {
			t.Error("Expected error decoding truncated string")
		}

		// Test encoding error path
		failWriter := &failingWriter{failOn: "length"}
		err = xdrEncodeString(failWriter, "test")
		if err == nil {
			t.Error("Expected error on failing writer")
		}
	})
}

func TestRPCSuccessPaths(t *testing.T) {
	t.Run("successful call decode", func(t *testing.T) {
		buf := &bytes.Buffer{}
		// Write valid RPC call
		binary.Write(buf, binary.BigEndian, uint32(1))      // XID
		binary.Write(buf, binary.BigEndian, uint32(0))      // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))      // RPC Version
		binary.Write(buf, binary.BigEndian, uint32(100003)) // NFS Program
		binary.Write(buf, binary.BigEndian, uint32(3))      // Version
		binary.Write(buf, binary.BigEndian, uint32(0))      // Procedure
		binary.Write(buf, binary.BigEndian, uint32(0))      // Auth flavor
		binary.Write(buf, binary.BigEndian, uint32(0))      // Auth length
		binary.Write(buf, binary.BigEndian, uint32(0))      // Verifier flavor
		binary.Write(buf, binary.BigEndian, uint32(0))      // Verifier length

		call, err := DecodeRPCCall(buf)
		if err != nil {
			t.Errorf("DecodeRPCCall failed: %v", err)
		}
		if call.Header.Xid != 1 {
			t.Errorf("Expected XID 1, got %d", call.Header.Xid)
		}
		if call.Header.Program != NFS_PROGRAM {
			t.Errorf("Expected program %d, got %d", NFS_PROGRAM, call.Header.Program)
		}
	})

	t.Run("successful reply encode", func(t *testing.T) {
		reply := &RPCReply{
			Header: RPCMsgHeader{
				Xid:     1,
				MsgType: RPC_REPLY,
			},
			Status:       MSG_ACCEPTED,
			AcceptStatus: SUCCESS, // Must be SUCCESS for data to be encoded
			Verifier: RPCVerifier{
				Flavor: 0,
				Body:   []byte{},
			},
			Data: []byte("test data"),
		}

		buf := &bytes.Buffer{}
		err := EncodeRPCReply(buf, reply)
		if err != nil {
			t.Errorf("EncodeRPCReply failed: %v", err)
		}

		// Verify encoded data
		var xid, msgType, status, verFlavor, verLen, acceptStatus uint32
		binary.Read(buf, binary.BigEndian, &xid)
		if xid != 1 {
			t.Errorf("Expected XID 1, got %d", xid)
		}
		binary.Read(buf, binary.BigEndian, &msgType)
		if msgType != RPC_REPLY {
			t.Errorf("Expected message type %d, got %d", RPC_REPLY, msgType)
		}
		binary.Read(buf, binary.BigEndian, &status)
		if status != MSG_ACCEPTED {
			t.Errorf("Expected status %d, got %d", MSG_ACCEPTED, status)
		}
		binary.Read(buf, binary.BigEndian, &verFlavor)
		if verFlavor != 0 {
			t.Errorf("Expected verifier flavor 0, got %d", verFlavor)
		}
		binary.Read(buf, binary.BigEndian, &verLen)
		if verLen != 0 {
			t.Errorf("Expected verifier length 0, got %d", verLen)
		}
		binary.Read(buf, binary.BigEndian, &acceptStatus)
		if acceptStatus != SUCCESS {
			t.Errorf("Expected accept status %d, got %d", SUCCESS, acceptStatus)
		}

		// Verify we wrote the data bytes
		dataBytes := make([]byte, 9) // "test data" is 9 bytes
		n, err := buf.Read(dataBytes)
		if err != nil {
			t.Errorf("Failed to read data bytes: %v", err)
		}
		if n != 9 {
			t.Errorf("Expected to read 9 bytes, got %d", n)
		}
		if string(dataBytes) != "test data" {
			t.Errorf("Expected data 'test data', got '%s'", string(dataBytes))
		}
	})

	t.Run("encode reply with NFSAttrs data", func(t *testing.T) {
		// Create test attributes
		attrs := &NFSAttrs{
			Mode:   0644,
			Uid:    1000,
			Gid:    1000,
			Size:   4096,
			FileId: 12345,
		}

		reply := &RPCReply{
			Header: RPCMsgHeader{
				Xid:     2,
				MsgType: RPC_REPLY,
			},
			Status:       MSG_ACCEPTED,
			AcceptStatus: SUCCESS, // Must be SUCCESS for data to be encoded
			Verifier: RPCVerifier{
				Flavor: 0,
				Body:   []byte{},
			},
			Data: attrs,
		}

		buf := &bytes.Buffer{}
		err := EncodeRPCReply(buf, reply)
		if err != nil {
			t.Errorf("EncodeRPCReply failed with NFSAttrs: %v", err)
		}

		// Skip past the header data
		var headerData [24]byte // 6 uint32s (XID, msgType, status, verFlavor, verLen, acceptStatus)
		n, err := buf.Read(headerData[:])
		if err != nil || n != 24 {
			t.Errorf("Failed to read header data: %v", err)
		}

		// Should have encoded attributes data (84 bytes for fattr3)
		if buf.Len() < 84 {
			t.Errorf("Expected at least 84 bytes of attribute data, got %d", buf.Len())
		}

		// RFC 1813 fattr3: ftype comes first, then mode
		var ftype uint32
		binary.Read(buf, binary.BigEndian, &ftype)
		if ftype != NF3REG { // Regular file
			t.Errorf("Expected ftype NF3REG (1), got %d", ftype)
		}

		var mode uint32
		binary.Read(buf, binary.BigEndian, &mode)
		// Mode should be permission bits only (0644 = 420)
		if mode != uint32(attrs.Mode.Perm()) {
			t.Errorf("Expected mode %d, got %d", attrs.Mode.Perm(), mode)
		}
	})

	t.Run("encode reply with uint32 data", func(t *testing.T) {
		statusCode := uint32(NFSERR_NOENT)

		reply := &RPCReply{
			Header: RPCMsgHeader{
				Xid:     3,
				MsgType: RPC_REPLY,
			},
			Status:       MSG_ACCEPTED,
			AcceptStatus: SUCCESS, // Must be SUCCESS for data to be encoded
			Verifier: RPCVerifier{
				Flavor: 0,
				Body:   []byte{},
			},
			Data: statusCode,
		}

		buf := &bytes.Buffer{}
		err := EncodeRPCReply(buf, reply)
		if err != nil {
			t.Errorf("EncodeRPCReply failed with uint32: %v", err)
		}

		// Skip past the header data
		var headerData [24]byte // 6 uint32s (XID, msgType, status, verFlavor, verLen, acceptStatus)
		n, err := buf.Read(headerData[:])
		if err != nil || n != 24 {
			t.Errorf("Failed to read header data: %v", err)
		}

		// Read status code
		var encodedStatus uint32
		binary.Read(buf, binary.BigEndian, &encodedStatus)
		if encodedStatus != statusCode {
			t.Errorf("Expected status code %d, got %d", statusCode, encodedStatus)
		}
	})

	t.Run("encode reply with string data", func(t *testing.T) {
		testString := "error message"

		reply := &RPCReply{
			Header: RPCMsgHeader{
				Xid:     4,
				MsgType: RPC_REPLY,
			},
			Status:       MSG_ACCEPTED,
			AcceptStatus: SUCCESS, // Must be SUCCESS for data to be encoded
			Verifier: RPCVerifier{
				Flavor: 0,
				Body:   []byte{},
			},
			Data: testString,
		}

		buf := &bytes.Buffer{}
		err := EncodeRPCReply(buf, reply)
		if err != nil {
			t.Errorf("EncodeRPCReply failed with string: %v", err)
		}

		// Skip past the header data
		var headerData [24]byte // 6 uint32s (XID, msgType, status, verFlavor, verLen, acceptStatus)
		n, err := buf.Read(headerData[:])
		if err != nil || n != 24 {
			t.Errorf("Failed to read header data: %v", err)
		}

		// Read string length
		var strLen uint32
		binary.Read(buf, binary.BigEndian, &strLen)
		if strLen != uint32(len(testString)) {
			t.Errorf("Expected string length %d, got %d", len(testString), strLen)
		}

		// Read string data
		strData := make([]byte, strLen)
		n, err = buf.Read(strData)
		if err != nil || n != int(strLen) {
			t.Errorf("Failed to read string data: %v", err)
		}

		if string(strData) != testString {
			t.Errorf("Expected string '%s', got '%s'", testString, string(strData))
		}
	})
}

func TestRPCReplyEncodeWithTypes(t *testing.T) {
	t.Run("encode with different data types", func(t *testing.T) {
		// Test with different data types
		dataTypes := []interface{}{
			[]byte("test data"),
			&NFSAttrs{
				Mode: 0644,
				Uid:  1000,
				Gid:  1000,
				Size: 1024,
				// Mtime: time.Now()
				// Atime: time.Now()
			},
			"test string",
			uint32(12345),
			nil,
		}

		for _, data := range dataTypes {
			reply := &RPCReply{
				Header: RPCMsgHeader{
					Xid:     1,
					MsgType: RPC_REPLY,
				},
				Status:       MSG_ACCEPTED,
				AcceptStatus: SUCCESS, // Must be SUCCESS for data to be encoded
				Verifier: RPCVerifier{
					Flavor: 0,
					Body:   []byte{},
				},
				Data: data,
			}

			buf := &bytes.Buffer{}
			err := EncodeRPCReply(buf, reply)
			if err != nil {
				t.Errorf("EncodeRPCReply failed with data type %T: %v", data, err)
			}

			// Verify at least the header was encoded
			// Header: XID + MsgType + Status + VerFlavor + VerLen + AcceptStatus = 6 uint32s = 24 bytes
			if buf.Len() < 24 {
				t.Errorf("Encoded buffer too small for data type %T: %d bytes", data, buf.Len())
			}
		}
	})
}

func TestXDRStringLengthValidation(t *testing.T) {
	t.Run("excessive string length", func(t *testing.T) {
		buf := &bytes.Buffer{}
		// Write a length that exceeds MAX_XDR_STRING_LENGTH
		excessiveLength := uint32(MAX_XDR_STRING_LENGTH + 1)
		binary.Write(buf, binary.BigEndian, excessiveLength)

		_, err := xdrDecodeString(buf)
		if err == nil {
			t.Error("xdrDecodeString should fail with excessive length")
		}
		if err != nil && !bytes.Contains([]byte(err.Error()), []byte("exceeds maximum allowed length")) {
			t.Errorf("Expected length validation error, got: %v", err)
		}
	})

	t.Run("valid string length at boundary", func(t *testing.T) {
		buf := &bytes.Buffer{}
		// Write a length exactly at MAX_XDR_STRING_LENGTH
		maxLength := uint32(MAX_XDR_STRING_LENGTH)
		binary.Write(buf, binary.BigEndian, maxLength)
		// Write the actual string data
		testData := make([]byte, MAX_XDR_STRING_LENGTH)
		for i := range testData {
			testData[i] = 'A'
		}
		buf.Write(testData)

		result, err := xdrDecodeString(buf)
		if err != nil {
			t.Errorf("xdrDecodeString should succeed with max length: %v", err)
		}
		if len(result) != MAX_XDR_STRING_LENGTH {
			t.Errorf("Expected length %d, got %d", MAX_XDR_STRING_LENGTH, len(result))
		}
	})
}

func TestRPCAuthLengthValidation(t *testing.T) {
	t.Run("excessive credential length", func(t *testing.T) {
		buf := &bytes.Buffer{}
		// Write valid RPC call header
		binary.Write(buf, binary.BigEndian, uint32(1))      // XID
		binary.Write(buf, binary.BigEndian, uint32(0))      // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))      // RPC Version
		binary.Write(buf, binary.BigEndian, uint32(100003)) // NFS Program
		binary.Write(buf, binary.BigEndian, uint32(3))      // Version
		binary.Write(buf, binary.BigEndian, uint32(0))      // Procedure
		binary.Write(buf, binary.BigEndian, uint32(0))      // Auth flavor
		// Write excessive credential length
		excessiveLength := uint32(MAX_RPC_AUTH_LENGTH + 1)
		binary.Write(buf, binary.BigEndian, excessiveLength)

		_, err := DecodeRPCCall(buf)
		if err == nil {
			t.Error("DecodeRPCCall should fail with excessive credential length")
		}
		if err != nil && !bytes.Contains([]byte(err.Error()), []byte("credential length")) {
			t.Errorf("Expected credential length validation error, got: %v", err)
		}
	})

	t.Run("excessive verifier length", func(t *testing.T) {
		buf := &bytes.Buffer{}
		// Write valid RPC call header with valid credential
		binary.Write(buf, binary.BigEndian, uint32(1))      // XID
		binary.Write(buf, binary.BigEndian, uint32(0))      // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))      // RPC Version
		binary.Write(buf, binary.BigEndian, uint32(100003)) // NFS Program
		binary.Write(buf, binary.BigEndian, uint32(3))      // Version
		binary.Write(buf, binary.BigEndian, uint32(0))      // Procedure
		binary.Write(buf, binary.BigEndian, uint32(0))      // Auth flavor
		binary.Write(buf, binary.BigEndian, uint32(0))      // Auth length (valid)
		binary.Write(buf, binary.BigEndian, uint32(0))      // Verifier flavor
		// Write excessive verifier length
		excessiveLength := uint32(MAX_RPC_AUTH_LENGTH + 1)
		binary.Write(buf, binary.BigEndian, excessiveLength)

		_, err := DecodeRPCCall(buf)
		if err == nil {
			t.Error("DecodeRPCCall should fail with excessive verifier length")
		}
		if err != nil && !bytes.Contains([]byte(err.Error()), []byte("verifier length")) {
			t.Errorf("Expected verifier length validation error, got: %v", err)
		}
	})

	t.Run("valid credential length at boundary", func(t *testing.T) {
		buf := &bytes.Buffer{}
		// Write valid RPC call with max credential length
		binary.Write(buf, binary.BigEndian, uint32(1))                   // XID
		binary.Write(buf, binary.BigEndian, uint32(0))                   // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))                   // RPC Version
		binary.Write(buf, binary.BigEndian, uint32(100003))              // NFS Program
		binary.Write(buf, binary.BigEndian, uint32(3))                   // Version
		binary.Write(buf, binary.BigEndian, uint32(0))                   // Procedure
		binary.Write(buf, binary.BigEndian, uint32(0))                   // Auth flavor
		binary.Write(buf, binary.BigEndian, uint32(MAX_RPC_AUTH_LENGTH)) // Max auth length
		// Write credential body
		credBody := make([]byte, MAX_RPC_AUTH_LENGTH)
		buf.Write(credBody)
		binary.Write(buf, binary.BigEndian, uint32(0)) // Verifier flavor
		binary.Write(buf, binary.BigEndian, uint32(0)) // Verifier length

		call, err := DecodeRPCCall(buf)
		if err != nil {
			t.Errorf("DecodeRPCCall should succeed with max credential length: %v", err)
		}
		if len(call.Credential.Body) != MAX_RPC_AUTH_LENGTH {
			t.Errorf("Expected credential length %d, got %d", MAX_RPC_AUTH_LENGTH, len(call.Credential.Body))
		}
	})

	t.Run("valid verifier length at boundary", func(t *testing.T) {
		buf := &bytes.Buffer{}
		// Write valid RPC call with max verifier length
		binary.Write(buf, binary.BigEndian, uint32(1))                   // XID
		binary.Write(buf, binary.BigEndian, uint32(0))                   // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))                   // RPC Version
		binary.Write(buf, binary.BigEndian, uint32(100003))              // NFS Program
		binary.Write(buf, binary.BigEndian, uint32(3))                   // Version
		binary.Write(buf, binary.BigEndian, uint32(0))                   // Procedure
		binary.Write(buf, binary.BigEndian, uint32(0))                   // Auth flavor
		binary.Write(buf, binary.BigEndian, uint32(0))                   // Auth length
		binary.Write(buf, binary.BigEndian, uint32(0))                   // Verifier flavor
		binary.Write(buf, binary.BigEndian, uint32(MAX_RPC_AUTH_LENGTH)) // Max verifier length
		// Write verifier body
		verBody := make([]byte, MAX_RPC_AUTH_LENGTH)
		buf.Write(verBody)

		call, err := DecodeRPCCall(buf)
		if err != nil {
			t.Errorf("DecodeRPCCall should succeed with max verifier length: %v", err)
		}
		if len(call.Verifier.Body) != MAX_RPC_AUTH_LENGTH {
			t.Errorf("Expected verifier length %d, got %d", MAX_RPC_AUTH_LENGTH, len(call.Verifier.Body))
		}
	})
}

func TestRPCErrorPaths(t *testing.T) {
	t.Run("decode errors", func(t *testing.T) {
		// Test empty buffer
		if _, err := DecodeRPCCall(&bytes.Buffer{}); err == nil {
			t.Error("DecodeRPCCall should fail on empty buffer")
		}

		// Test truncated XID
		buf := bytes.NewBuffer([]byte{0x00})
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on truncated XID")
		}

		// Test truncated message type
		buf = bytes.NewBuffer([]byte{0x00, 0x00, 0x00, 0x01}) // XID only
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on truncated message type")
		}

		// Test invalid message type
		buf = &bytes.Buffer{}
		binary.Write(buf, binary.BigEndian, uint32(1)) // XID
		binary.Write(buf, binary.BigEndian, uint32(3)) // Invalid message type
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on invalid message type")
		}

		// Test truncated RPC version
		buf = &bytes.Buffer{}
		binary.Write(buf, binary.BigEndian, uint32(1)) // XID
		binary.Write(buf, binary.BigEndian, uint32(0)) // RPC_CALL
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on truncated RPC version")
		}

		// Test invalid RPC version
		buf = &bytes.Buffer{}
		binary.Write(buf, binary.BigEndian, uint32(1)) // XID
		binary.Write(buf, binary.BigEndian, uint32(0)) // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(3)) // Invalid version
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on invalid RPC version")
		}

		// Test truncated program number
		buf = &bytes.Buffer{}
		binary.Write(buf, binary.BigEndian, uint32(1)) // XID
		binary.Write(buf, binary.BigEndian, uint32(0)) // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2)) // Version
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on truncated program number")
		}

		// Test truncated program version
		buf = &bytes.Buffer{}
		binary.Write(buf, binary.BigEndian, uint32(1))      // XID
		binary.Write(buf, binary.BigEndian, uint32(0))      // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))      // Version
		binary.Write(buf, binary.BigEndian, uint32(100017)) // Program
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on truncated program version")
		}

		// Test truncated procedure number
		buf = &bytes.Buffer{}
		binary.Write(buf, binary.BigEndian, uint32(1))      // XID
		binary.Write(buf, binary.BigEndian, uint32(0))      // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))      // Version
		binary.Write(buf, binary.BigEndian, uint32(100017)) // Program
		binary.Write(buf, binary.BigEndian, uint32(3))      // Program version
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on truncated procedure number")
		}

		// Test truncated credential flavor
		buf = &bytes.Buffer{}
		binary.Write(buf, binary.BigEndian, uint32(1))      // XID
		binary.Write(buf, binary.BigEndian, uint32(0))      // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))      // Version
		binary.Write(buf, binary.BigEndian, uint32(100017)) // Program
		binary.Write(buf, binary.BigEndian, uint32(3))      // Program version
		binary.Write(buf, binary.BigEndian, uint32(0))      // Procedure
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on truncated credential flavor")
		}

		// Test truncated credential length
		buf = &bytes.Buffer{}
		binary.Write(buf, binary.BigEndian, uint32(1))      // XID
		binary.Write(buf, binary.BigEndian, uint32(0))      // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))      // Version
		binary.Write(buf, binary.BigEndian, uint32(100017)) // Program
		binary.Write(buf, binary.BigEndian, uint32(3))      // Program version
		binary.Write(buf, binary.BigEndian, uint32(0))      // Procedure
		binary.Write(buf, binary.BigEndian, uint32(0))      // Credential flavor
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on truncated credential length")
		}

		// Test truncated credential body
		buf = &bytes.Buffer{}
		binary.Write(buf, binary.BigEndian, uint32(1))      // XID
		binary.Write(buf, binary.BigEndian, uint32(0))      // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))      // Version
		binary.Write(buf, binary.BigEndian, uint32(100017)) // Program
		binary.Write(buf, binary.BigEndian, uint32(3))      // Program version
		binary.Write(buf, binary.BigEndian, uint32(0))      // Procedure
		binary.Write(buf, binary.BigEndian, uint32(0))      // Credential flavor
		binary.Write(buf, binary.BigEndian, uint32(4))      // Credential length
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on truncated credential body")
		}

		// Test truncated verifier flavor
		buf = &bytes.Buffer{}
		binary.Write(buf, binary.BigEndian, uint32(1))      // XID
		binary.Write(buf, binary.BigEndian, uint32(0))      // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))      // Version
		binary.Write(buf, binary.BigEndian, uint32(100017)) // Program
		binary.Write(buf, binary.BigEndian, uint32(3))      // Program version
		binary.Write(buf, binary.BigEndian, uint32(0))      // Procedure
		binary.Write(buf, binary.BigEndian, uint32(0))      // Credential flavor
		binary.Write(buf, binary.BigEndian, uint32(0))      // Credential length
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on truncated verifier flavor")
		}

		// Test truncated verifier length
		buf = &bytes.Buffer{}
		binary.Write(buf, binary.BigEndian, uint32(1))      // XID
		binary.Write(buf, binary.BigEndian, uint32(0))      // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))      // Version
		binary.Write(buf, binary.BigEndian, uint32(100017)) // Program
		binary.Write(buf, binary.BigEndian, uint32(3))      // Program version
		binary.Write(buf, binary.BigEndian, uint32(0))      // Procedure
		binary.Write(buf, binary.BigEndian, uint32(0))      // Credential flavor
		binary.Write(buf, binary.BigEndian, uint32(0))      // Credential length
		binary.Write(buf, binary.BigEndian, uint32(0))      // Verifier flavor
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on truncated verifier length")
		}

		// Test truncated verifier body
		buf = &bytes.Buffer{}
		binary.Write(buf, binary.BigEndian, uint32(1))      // XID
		binary.Write(buf, binary.BigEndian, uint32(0))      // RPC_CALL
		binary.Write(buf, binary.BigEndian, uint32(2))      // Version
		binary.Write(buf, binary.BigEndian, uint32(100017)) // Program
		binary.Write(buf, binary.BigEndian, uint32(3))      // Program version
		binary.Write(buf, binary.BigEndian, uint32(0))      // Procedure
		binary.Write(buf, binary.BigEndian, uint32(0))      // Credential flavor
		binary.Write(buf, binary.BigEndian, uint32(0))      // Credential length
		binary.Write(buf, binary.BigEndian, uint32(0))      // Verifier flavor
		binary.Write(buf, binary.BigEndian, uint32(4))      // Verifier length
		if _, err := DecodeRPCCall(buf); err == nil {
			t.Error("DecodeRPCCall should fail on truncated verifier body")
		}
	})

	t.Run("encode errors", func(t *testing.T) {
		// Test write error on XID
		failWriter := &failingWriter{failOn: "xid"}
		if err := EncodeRPCReply(failWriter, &RPCReply{
			Header: RPCMsgHeader{
				Xid:     1,
				MsgType: RPC_REPLY,
			},
		}); err == nil {
			t.Error("EncodeRPCReply should fail on XID write error")
		}

		// Test write error on message type
		failWriter = &failingWriter{failOn: "msgtype"}
		if err := EncodeRPCReply(failWriter, &RPCReply{
			Header: RPCMsgHeader{
				Xid:     1,
				MsgType: RPC_REPLY,
			},
		}); err == nil {
			t.Error("EncodeRPCReply should fail on message type write error")
		}

		// Test write error on reply status
		failWriter = &failingWriter{failOn: "status"}
		if err := EncodeRPCReply(failWriter, &RPCReply{
			Header: RPCMsgHeader{
				Xid:     1,
				MsgType: RPC_REPLY,
			},
		}); err == nil {
			t.Error("EncodeRPCReply should fail on reply status write error")
		}

		// Test write error on verifier flavor
		failWriter = &failingWriter{failOn: "verifier_flavor"}
		if err := EncodeRPCReply(failWriter, &RPCReply{
			Header: RPCMsgHeader{
				Xid:     1,
				MsgType: RPC_REPLY,
			},
		}); err == nil {
			t.Error("EncodeRPCReply should fail on verifier flavor write error")
		}

		// Test write error on verifier length
		failWriter = &failingWriter{failOn: "verifier_length"}
		if err := EncodeRPCReply(failWriter, &RPCReply{
			Header: RPCMsgHeader{
				Xid:     1,
				MsgType: RPC_REPLY,
			},
		}); err == nil {
			t.Error("EncodeRPCReply should fail on verifier length write error")
		}

		// Test write error on data
		failWriter = &failingWriter{failOn: "data"}
		if err := EncodeRPCReply(failWriter, &RPCReply{
			Header: RPCMsgHeader{
				Xid:     1,
				MsgType: RPC_REPLY,
			},
			Status:       MSG_ACCEPTED,
			AcceptStatus: SUCCESS, // Must be SUCCESS for data to be written
			Data:         []byte{1, 2, 3},
		}); err == nil {
			t.Error("EncodeRPCReply should fail on data write error")
		}
	})
}

// failingWriter is a helper type that fails writes based on a specified field
type failingWriter struct {
	failOn string
	count  int
}

func (w *failingWriter) Write(p []byte) (n int, err error) {
	w.count++
	switch w.failOn {
	case "xid":
		if w.count == 1 {
			return 0, io.ErrShortWrite
		}
	case "msgtype":
		if w.count == 2 {
			return 0, io.ErrShortWrite
		}
	case "status":
		if w.count == 3 {
			return 0, io.ErrShortWrite
		}
	case "verifier_flavor":
		if w.count == 4 {
			return 0, io.ErrShortWrite
		}
	case "verifier_length":
		if w.count == 5 {
			return 0, io.ErrShortWrite
		}
	case "accept_status":
		if w.count == 6 {
			return 0, io.ErrShortWrite
		}
	case "data":
		// Data is written after 6 header fields: xid, msgtype, status, verflavor, verlen, acceptstatus
		if w.count == 7 {
			return 0, io.ErrShortWrite
		}
	case "length":
		return 0, io.ErrShortWrite // Fail immediately
	}
	return len(p), nil
}

// TestC6_SkipAuthUnboundedAllocation verifies that skipAuth rejects auth bodies
// exceeding MAX_RPC_AUTH_LENGTH (400 bytes per RFC 5531).
func TestC6_SkipAuthUnboundedAllocation(t *testing.T) {
	pm := NewPortmapper()

	// Test: oversized auth body should be rejected
	t.Run("reject_oversized", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(0))                     // flavor
		binary.Write(&buf, binary.BigEndian, uint32(MAX_RPC_AUTH_LENGTH+1)) // length exceeds max
		err := pm.skipAuth(&buf)
		if err == nil {
			t.Fatal("expected error for oversized auth body, got nil")
		}
	})

	// Test: auth body at exactly MAX_RPC_AUTH_LENGTH should be accepted
	t.Run("accept_at_max", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(0))                   // flavor
		binary.Write(&buf, binary.BigEndian, uint32(MAX_RPC_AUTH_LENGTH)) // length at max
		buf.Write(make([]byte, MAX_RPC_AUTH_LENGTH))                      // body data
		err := pm.skipAuth(&buf)
		if err != nil {
			t.Fatalf("expected no error for auth body at max, got: %v", err)
		}
	})

	// Test: empty auth body should be accepted
	t.Run("accept_empty", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(0)) // flavor
		binary.Write(&buf, binary.BigEndian, uint32(0)) // length = 0
		err := pm.skipAuth(&buf)
		if err != nil {
			t.Fatalf("expected no error for empty auth body, got: %v", err)
		}
	})
}

// TestH9_ReadRecordReturnsCopy verifies that ReadRecord returns an independent
// copy of the data, not a slice of the internal buffer.
func TestH9_ReadRecordReturnsCopy(t *testing.T) {
	// Build two records back-to-back
	var wire bytes.Buffer

	// Record 1: "AAAA"
	record1Data := []byte("AAAA")
	header1 := uint32(len(record1Data)) | LastFragmentFlag
	binary.Write(&wire, binary.BigEndian, header1)
	wire.Write(record1Data)

	// Record 2: "BBBB"
	record2Data := []byte("BBBB")
	header2 := uint32(len(record2Data)) | LastFragmentFlag
	binary.Write(&wire, binary.BigEndian, header2)
	wire.Write(record2Data)

	reader := NewRecordMarkingReader(&wire)

	// Read first record
	data1, err := reader.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord 1 failed: %v", err)
	}
	if !bytes.Equal(data1, record1Data) {
		t.Fatalf("record 1: got %q, want %q", data1, record1Data)
	}

	// Save a copy of data1 for comparison
	data1Copy := make([]byte, len(data1))
	copy(data1Copy, data1)

	// Read second record - this should NOT corrupt data1
	data2, err := reader.ReadRecord()
	if err != nil {
		t.Fatalf("ReadRecord 2 failed: %v", err)
	}
	if !bytes.Equal(data2, record2Data) {
		t.Fatalf("record 2: got %q, want %q", data2, record2Data)
	}

	// Verify data1 is still intact (not corrupted by reading record 2)
	if !bytes.Equal(data1, data1Copy) {
		t.Fatalf("data1 was corrupted after reading record 2: got %q, want %q", data1, data1Copy)
	}
}

// TestM5_PortmapperBinaryReadErrorChecking verifies that portmapper handler
// functions properly check errors from binary.Read and return safe defaults
// on truncated input.
func TestM5_PortmapperBinaryReadErrorChecking(t *testing.T) {
	pm := NewPortmapper()

	// Test handleGetPort with truncated input
	t.Run("getport_truncated", func(t *testing.T) {
		// Only provide 2 of the 4 required uint32 values (8 bytes instead of 16)
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003)) // prog
		binary.Write(&buf, binary.BigEndian, uint32(3))      // vers (truncated - missing prot and port)

		result := pm.handleGetPort(&buf)
		// Should return port 0 (not found) rather than panic
		var port uint32
		binary.Read(bytes.NewReader(result), binary.BigEndian, &port)
		if port != 0 {
			t.Fatalf("expected port 0 for truncated input, got %d", port)
		}
	})

	// Test handleSet with truncated input
	t.Run("set_truncated", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003)) // prog only

		result := pm.handleSet(&buf, nil)
		var success uint32
		binary.Read(bytes.NewReader(result), binary.BigEndian, &success)
		if success != 0 {
			t.Fatalf("expected false (0) for truncated set, got %d", success)
		}
	})

	// Test handleUnset with truncated input
	t.Run("unset_truncated", func(t *testing.T) {
		var buf bytes.Buffer
		// empty input
		result := pm.handleUnset(&buf, nil)
		var success uint32
		binary.Read(bytes.NewReader(result), binary.BigEndian, &success)
		if success != 0 {
			t.Fatalf("expected false (0) for truncated unset, got %d", success)
		}
	})

	// Test handleGetAddr with truncated input
	t.Run("getaddr_truncated", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003)) // prog only, missing vers + strings

		result := pm.handleGetAddr(&buf)
		// Should return empty string (encoded as XDR string length 0)
		if len(result) < 4 {
			t.Fatalf("expected at least 4 bytes for empty XDR string, got %d", len(result))
		}
		var strLen uint32
		binary.Read(bytes.NewReader(result), binary.BigEndian, &strLen)
		if strLen != 0 {
			t.Fatalf("expected empty string (length 0) for truncated getaddr, got length %d", strLen)
		}
	})

	// Test handleRpcbSet with truncated input
	t.Run("rpcbset_truncated", func(t *testing.T) {
		var buf bytes.Buffer
		// empty input
		result := pm.handleRpcbSet(&buf)
		var success uint32
		binary.Read(bytes.NewReader(result), binary.BigEndian, &success)
		if success != 0 {
			t.Fatalf("expected false (0) for truncated rpcbset, got %d", success)
		}
	})

	// Test handleRpcbUnset with truncated input
	t.Run("rpcbunset_truncated", func(t *testing.T) {
		var buf bytes.Buffer
		// empty input
		result := pm.handleRpcbUnset(&buf)
		var success uint32
		binary.Read(bytes.NewReader(result), binary.BigEndian, &success)
		if success != 0 {
			t.Fatalf("expected false (0) for truncated rpcbunset, got %d", success)
		}
	})
}

// TestM6_XdrDecodeFileHandleOversizedConsumption verifies that xdrDecodeFileHandle
// consumes all bytes (padded to 4-byte boundary) for oversized handles, keeping
// the stream in sync.
func TestM6_XdrDecodeFileHandleOversizedConsumption(t *testing.T) {
	// Test: 128-byte handle (oversized, > 64 max per NFS3) is rejected immediately
	// R12: lengths > 64 return error before any allocation or read
	t.Run("oversized_handle_rejected", func(t *testing.T) {
		var buf bytes.Buffer
		handleLen := uint32(128)
		binary.Write(&buf, binary.BigEndian, handleLen)
		buf.Write(make([]byte, 128))

		r := bytes.NewReader(buf.Bytes())
		_, err := xdrDecodeFileHandle(r)
		if err == nil {
			t.Fatal("expected error for oversized handle")
		}
		if !bytes.Contains([]byte(err.Error()), []byte("exceeds NFS3 maximum")) {
			t.Fatalf("expected NFS3 maximum error, got: %v", err)
		}
	})

	// Test: wrong-size handle (e.g., 16 bytes, valid range but not 8) followed by sentinel
	t.Run("wrong_size_handle_consumed", func(t *testing.T) {
		var buf bytes.Buffer
		handleLen := uint32(16) // valid per NFS3 but not 8 bytes
		binary.Write(&buf, binary.BigEndian, handleLen)
		buf.Write(make([]byte, 16))

		sentinel := uint32(0xCAFEBABE)
		binary.Write(&buf, binary.BigEndian, sentinel)

		r := bytes.NewReader(buf.Bytes())
		_, err := xdrDecodeFileHandle(r)
		if err == nil {
			t.Fatal("expected error for wrong-size handle")
		}

		var readSentinel uint32
		if err := binary.Read(r, binary.BigEndian, &readSentinel); err != nil {
			t.Fatalf("failed to read sentinel after wrong-size handle: %v", err)
		}
		if readSentinel != sentinel {
			t.Fatalf("sentinel mismatch: got 0x%X, want 0x%X", readSentinel, sentinel)
		}
	})

	// Test: zero-length handle followed by sentinel
	t.Run("zero_length_handle", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(0)) // length = 0

		sentinel := uint32(0xFEEDFACE)
		binary.Write(&buf, binary.BigEndian, sentinel)

		r := bytes.NewReader(buf.Bytes())
		_, err := xdrDecodeFileHandle(r)
		if err == nil {
			t.Fatal("expected error for zero-length handle")
		}

		var readSentinel uint32
		if err := binary.Read(r, binary.BigEndian, &readSentinel); err != nil {
			t.Fatalf("failed to read sentinel after zero-length handle: %v", err)
		}
		if readSentinel != sentinel {
			t.Fatalf("sentinel mismatch: got 0x%X, want 0x%X", readSentinel, sentinel)
		}
	})
}

// TestM9_WriteRecordEmptyData verifies that WriteRecord handles empty data
// by emitting a single last-fragment header with length 0.
func TestM9_WriteRecordEmptyData(t *testing.T) {
	// Test: empty write produces a valid last-fragment header
	t.Run("empty_write", func(t *testing.T) {
		var buf bytes.Buffer
		writer := NewRecordMarkingWriter(&buf)

		err := writer.WriteRecord([]byte{})
		if err != nil {
			t.Fatalf("WriteRecord(empty) failed: %v", err)
		}

		// Should produce exactly 4 bytes: last-fragment header with length 0
		if buf.Len() != 4 {
			t.Fatalf("expected 4 bytes, got %d", buf.Len())
		}

		var header uint32
		binary.Read(&buf, binary.BigEndian, &header)
		if header != LastFragmentFlag {
			t.Fatalf("expected header 0x%08X, got 0x%08X", LastFragmentFlag, header)
		}
	})

	// Test: empty write round-trips through reader
	t.Run("empty_roundtrip", func(t *testing.T) {
		var buf bytes.Buffer
		writer := NewRecordMarkingWriter(&buf)
		writer.WriteRecord([]byte{})

		reader := NewRecordMarkingReader(&buf)
		data, err := reader.ReadRecord()
		if err != nil {
			t.Fatalf("ReadRecord failed: %v", err)
		}
		if len(data) != 0 {
			t.Fatalf("expected empty data, got %d bytes", len(data))
		}
	})
}

// TestM11_PortmapperUsesActualListenAddress verifies that the portmapper uses
// the configured listen address instead of hardcoded addresses.
func TestM11_PortmapperUsesActualListenAddress(t *testing.T) {
	pm := NewPortmapper()
	pm.SetListenAddr("192.168.1.100")
	pm.RegisterService(100003, 3, IPPROTO_TCP, 2049)

	// Test GETADDR returns configured address
	t.Run("getaddr_configured", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003)) // prog
		binary.Write(&buf, binary.BigEndian, uint32(3))      // vers
		xdrEncodeString(&buf, "tcp")                         // netid
		xdrEncodeString(&buf, "")                            // r_addr
		xdrEncodeString(&buf, "")                            // r_owner

		result := pm.handleGetAddr(&buf)
		// Decode the XDR string result
		r := bytes.NewReader(result)
		uaddr, err := xdrDecodeString(r)
		if err != nil {
			t.Fatalf("failed to decode uaddr: %v", err)
		}
		// 2049 = 8*256 + 1, so expect "192.168.1.100.8.1"
		expected := "192.168.1.100.8.1"
		if uaddr != expected {
			t.Fatalf("GETADDR: got %q, want %q", uaddr, expected)
		}
	})

	// Test DUMP returns configured address
	t.Run("dump_configured", func(t *testing.T) {
		result := pm.handleRpcbDump()
		// The dump contains multiple entries. Look for our registered service.
		r := bytes.NewReader(result)
		found := false
		for {
			var more uint32
			if err := binary.Read(r, binary.BigEndian, &more); err != nil {
				break
			}
			if more == 0 {
				break
			}
			var prog, vers uint32
			binary.Read(r, binary.BigEndian, &prog)
			binary.Read(r, binary.BigEndian, &vers)
			netid, _ := xdrDecodeString(r)
			uaddr, _ := xdrDecodeString(r)
			xdrDecodeString(r) // owner

			if prog == 100003 && vers == 3 && netid == "tcp" {
				expected := "192.168.1.100.8.1"
				if uaddr != expected {
					t.Fatalf("DUMP uaddr: got %q, want %q", uaddr, expected)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("NFS service not found in dump output")
		}
	})

	// Test default address when listenAddr is empty
	t.Run("default_address", func(t *testing.T) {
		pm2 := NewPortmapper()
		pm2.RegisterService(100003, 3, IPPROTO_TCP, 2049)

		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(100003))
		binary.Write(&buf, binary.BigEndian, uint32(3))
		xdrEncodeString(&buf, "tcp")
		xdrEncodeString(&buf, "")
		xdrEncodeString(&buf, "")

		result := pm2.handleGetAddr(&buf)
		r := bytes.NewReader(result)
		uaddr, _ := xdrDecodeString(r)
		expected := "0.0.0.0.8.1"
		if uaddr != expected {
			t.Fatalf("default GETADDR: got %q, want %q", uaddr, expected)
		}
	})
}

// TestM12_ReadRecordTotalSizeLimit verifies that ReadRecord rejects records
// that exceed the maximum total size across all fragments.
func TestM12_ReadRecordTotalSizeLimit(t *testing.T) {
	// Test: single fragment exceeding max size
	t.Run("exceeds_max", func(t *testing.T) {
		var wire bytes.Buffer
		dataSize := uint32(1024)
		header := dataSize | LastFragmentFlag
		binary.Write(&wire, binary.BigEndian, header)
		wire.Write(make([]byte, dataSize))

		reader := NewRecordMarkingReader(&wire)
		reader.MaxRecordSize = 512 // set limit below data size

		_, err := reader.ReadRecord()
		if err == nil {
			t.Fatal("expected error for record exceeding max size")
		}
	})

	// Test: record at exactly max size should succeed
	t.Run("at_max", func(t *testing.T) {
		var wire bytes.Buffer
		dataSize := uint32(512)
		header := dataSize | LastFragmentFlag
		binary.Write(&wire, binary.BigEndian, header)
		wire.Write(make([]byte, dataSize))

		reader := NewRecordMarkingReader(&wire)
		reader.MaxRecordSize = 512

		data, err := reader.ReadRecord()
		if err != nil {
			t.Fatalf("expected success for record at max size, got: %v", err)
		}
		if len(data) != int(dataSize) {
			t.Fatalf("expected %d bytes, got %d", dataSize, len(data))
		}
	})

	// Test: default MaxRecordSize is set
	t.Run("default_set", func(t *testing.T) {
		reader := NewRecordMarkingReader(bytes.NewReader(nil))
		if reader.MaxRecordSize != DefaultMaxRecordSize {
			t.Fatalf("expected default MaxRecordSize %d, got %d", DefaultMaxRecordSize, reader.MaxRecordSize)
		}
	})
}

// TestR12_FileHandleUnboundedAllocationDoS verifies that xdrDecodeFileHandle
// rejects handle lengths exceeding the NFS3 maximum of 64 bytes, preventing
// a DoS via huge memory allocation.
func TestR12_FileHandleUnboundedAllocationDoS(t *testing.T) {
	// Test: handle length exceeding NFS3 max (64 bytes) is rejected immediately
	t.Run("reject_huge_length", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(0xFFFFFFF0)) // ~4GB length
		_, err := xdrDecodeFileHandle(&buf)
		if err == nil {
			t.Fatal("expected error for huge handle length, got nil")
		}
		if !bytes.Contains([]byte(err.Error()), []byte("exceeds NFS3 maximum")) {
			t.Fatalf("expected NFS3 maximum error, got: %v", err)
		}
	})

	// Test: handle length of 65 is rejected (just above max)
	t.Run("reject_65_bytes", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(65))
		_, err := xdrDecodeFileHandle(&buf)
		if err == nil {
			t.Fatal("expected error for 65-byte handle length")
		}
		if !bytes.Contains([]byte(err.Error()), []byte("exceeds NFS3 maximum")) {
			t.Fatalf("expected NFS3 maximum error, got: %v", err)
		}
	})

	// Test: handle length of 64 is accepted (at max) but returns error for non-8
	t.Run("accept_64_bytes", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(64))
		buf.Write(make([]byte, 64)) // provide enough data
		_, err := xdrDecodeFileHandle(&buf)
		if err == nil {
			t.Fatal("expected error for 64-byte handle (not 8)")
		}
		// Should be "invalid handle length" not "exceeds NFS3 maximum"
		if bytes.Contains([]byte(err.Error()), []byte("exceeds NFS3 maximum")) {
			t.Fatalf("64 bytes should not trigger NFS3 maximum error, got: %v", err)
		}
	})

	// Test: io.ReadFull error is propagated when discarding
	t.Run("discard_read_error", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(32))
		buf.Write(make([]byte, 10)) // only 10 bytes, but need 32
		_, err := xdrDecodeFileHandle(&buf)
		if err == nil {
			t.Fatal("expected error when discarding truncated handle data")
		}
	})
}

// TestR13_PortmapperAtomicDebugListenAddr verifies that debug and listenAddr
// fields use atomic operations and are safe for concurrent access.
func TestR13_PortmapperAtomicDebugListenAddr(t *testing.T) {
	pm := NewPortmapper()

	// Test concurrent SetDebug and reads
	t.Run("concurrent_debug", func(t *testing.T) {
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(val bool) {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					pm.SetDebug(val)
				}
			}(i%2 == 0)
		}
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					_ = pm.debug.Load()
				}
			}()
		}
		wg.Wait()
	})

	// Test concurrent SetListenAddr and reads
	t.Run("concurrent_listenaddr", func(t *testing.T) {
		var wg sync.WaitGroup
		addrs := []string{"192.168.1.1", "10.0.0.1", "127.0.0.1"}
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func(idx int) {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					pm.SetListenAddr(addrs[idx%len(addrs)])
				}
			}(i)
		}
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for j := 0; j < 100; j++ {
					v, _ := pm.listenAddr.Load().(string)
					_ = v
				}
			}()
		}
		wg.Wait()
	})

	// Verify SetDebug/SetListenAddr work correctly
	t.Run("set_and_get", func(t *testing.T) {
		pm.SetDebug(true)
		if !pm.debug.Load() {
			t.Fatal("expected debug to be true")
		}
		pm.SetDebug(false)
		if pm.debug.Load() {
			t.Fatal("expected debug to be false")
		}
		pm.SetListenAddr("10.0.0.5")
		got, _ := pm.listenAddr.Load().(string)
		if got != "10.0.0.5" {
			t.Fatalf("expected listenAddr 10.0.0.5, got %q", got)
		}
	})
}

// TestR27_RPCCredentialVerifierXDRPadding verifies that DecodeRPCCall correctly
// consumes XDR padding after credential and verifier bodies.
func TestR27_RPCCredentialVerifierXDRPadding(t *testing.T) {
	// Build a valid RPC call with oddly-sized credential and verifier
	t.Run("odd_credential_padded", func(t *testing.T) {
		var buf bytes.Buffer
		// Header
		binary.Write(&buf, binary.BigEndian, uint32(0x12345678)) // XID
		binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))   // msg type
		binary.Write(&buf, binary.BigEndian, uint32(2))          // RPC version
		binary.Write(&buf, binary.BigEndian, uint32(100003))     // program
		binary.Write(&buf, binary.BigEndian, uint32(3))          // version
		binary.Write(&buf, binary.BigEndian, uint32(0))          // procedure

		// Credential: 5 bytes body (needs 3 bytes padding)
		binary.Write(&buf, binary.BigEndian, uint32(AUTH_NONE)) // flavor
		binary.Write(&buf, binary.BigEndian, uint32(5))         // length
		buf.Write([]byte{1, 2, 3, 4, 5})                        // body
		buf.Write([]byte{0, 0, 0})                              // XDR padding

		// Verifier: 3 bytes body (needs 1 byte padding)
		binary.Write(&buf, binary.BigEndian, uint32(AUTH_NONE)) // flavor
		binary.Write(&buf, binary.BigEndian, uint32(3))         // length
		buf.Write([]byte{6, 7, 8})                              // body
		buf.Write([]byte{0})                                    // XDR padding

		call, err := DecodeRPCCall(&buf)
		if err != nil {
			t.Fatalf("DecodeRPCCall failed: %v", err)
		}
		if len(call.Credential.Body) != 5 {
			t.Fatalf("expected credential body len 5, got %d", len(call.Credential.Body))
		}
		if len(call.Verifier.Body) != 3 {
			t.Fatalf("expected verifier body len 3, got %d", len(call.Verifier.Body))
		}
	})

	// Test: 4-byte aligned bodies need no padding
	t.Run("aligned_no_padding", func(t *testing.T) {
		var buf bytes.Buffer
		binary.Write(&buf, binary.BigEndian, uint32(0xAABBCCDD))
		binary.Write(&buf, binary.BigEndian, uint32(RPC_CALL))
		binary.Write(&buf, binary.BigEndian, uint32(2))
		binary.Write(&buf, binary.BigEndian, uint32(100003))
		binary.Write(&buf, binary.BigEndian, uint32(3))
		binary.Write(&buf, binary.BigEndian, uint32(0))

		// Credential: 8 bytes (aligned)
		binary.Write(&buf, binary.BigEndian, uint32(AUTH_NONE))
		binary.Write(&buf, binary.BigEndian, uint32(8))
		buf.Write(make([]byte, 8))

		// Verifier: 0 bytes (aligned)
		binary.Write(&buf, binary.BigEndian, uint32(AUTH_NONE))
		binary.Write(&buf, binary.BigEndian, uint32(0))

		call, err := DecodeRPCCall(&buf)
		if err != nil {
			t.Fatalf("DecodeRPCCall failed: %v", err)
		}
		if len(call.Credential.Body) != 8 {
			t.Fatalf("expected credential body len 8, got %d", len(call.Credential.Body))
		}
	})
}

// TestR27_EncodeRPCReplyVerifierPadding verifies that EncodeRPCReply pads the
// verifier body to a 4-byte boundary.
func TestR27_EncodeRPCReplyVerifierPadding(t *testing.T) {
	t.Run("odd_verifier_padded", func(t *testing.T) {
		var buf bytes.Buffer
		reply := &RPCReply{
			Header:       RPCMsgHeader{Xid: 1},
			Status:       MSG_ACCEPTED,
			AcceptStatus: SUCCESS,
			Verifier:     RPCVerifier{Flavor: AUTH_NONE, Body: []byte{0xAA, 0xBB, 0xCC}}, // 3 bytes
		}
		if err := EncodeRPCReply(&buf, reply); err != nil {
			t.Fatalf("EncodeRPCReply failed: %v", err)
		}
		// Total should be: XID(4) + type(4) + reply_stat(4) + verf_flavor(4) + verf_len(4)
		// + body(3) + pad(1) + accept_stat(4) = 28
		if buf.Len() != 28 {
			t.Fatalf("expected 28 bytes, got %d", buf.Len())
		}
	})

	t.Run("aligned_verifier_no_extra_padding", func(t *testing.T) {
		var buf bytes.Buffer
		reply := &RPCReply{
			Header:       RPCMsgHeader{Xid: 1},
			Status:       MSG_ACCEPTED,
			AcceptStatus: SUCCESS,
			Verifier:     RPCVerifier{Flavor: AUTH_NONE, Body: []byte{0xAA, 0xBB, 0xCC, 0xDD}}, // 4 bytes
		}
		if err := EncodeRPCReply(&buf, reply); err != nil {
			t.Fatalf("EncodeRPCReply failed: %v", err)
		}
		// XID(4) + type(4) + reply_stat(4) + verf_flavor(4) + verf_len(4) + body(4) + accept_stat(4) = 28
		if buf.Len() != 28 {
			t.Fatalf("expected 28 bytes, got %d", buf.Len())
		}
	})
}

// TestR28_PortmapperConditionalLogging verifies that the portmapper only logs
// call details when debug mode is enabled.
func TestR28_PortmapperConditionalLogging(t *testing.T) {
	pm := NewPortmapper()
	pm.RegisterService(100003, 3, IPPROTO_TCP, 2049)

	// Capture log output
	var logBuf bytes.Buffer
	pm.logger.SetOutput(&logBuf)

	// With debug disabled, handleCall should not log the "Call:" line
	t.Run("no_log_when_debug_off", func(t *testing.T) {
		logBuf.Reset()
		pm.SetDebug(false)

		// Build a minimal valid RPC call for portmapper GETPORT
		var callBuf bytes.Buffer
		binary.Write(&callBuf, binary.BigEndian, uint32(1))                 // XID
		binary.Write(&callBuf, binary.BigEndian, uint32(RPC_CALL))          // msg type
		binary.Write(&callBuf, binary.BigEndian, uint32(2))                 // RPC version
		binary.Write(&callBuf, binary.BigEndian, uint32(PortmapperProgram)) // program
		binary.Write(&callBuf, binary.BigEndian, uint32(2))                 // version
		binary.Write(&callBuf, binary.BigEndian, uint32(PMAPPROC_GETPORT))  // procedure
		// Credential: AUTH_NONE, length 0
		binary.Write(&callBuf, binary.BigEndian, uint32(AUTH_NONE))
		binary.Write(&callBuf, binary.BigEndian, uint32(0))
		// Verifier: AUTH_NONE, length 0
		binary.Write(&callBuf, binary.BigEndian, uint32(AUTH_NONE))
		binary.Write(&callBuf, binary.BigEndian, uint32(0))
		// GETPORT args
		binary.Write(&callBuf, binary.BigEndian, uint32(100003))      // prog
		binary.Write(&callBuf, binary.BigEndian, uint32(3))           // vers
		binary.Write(&callBuf, binary.BigEndian, uint32(IPPROTO_TCP)) // prot
		binary.Write(&callBuf, binary.BigEndian, uint32(0))           // port

		_, err := pm.handleCall(callBuf.Bytes(), nil)
		if err != nil {
			t.Fatalf("handleCall failed: %v", err)
		}

		if bytes.Contains(logBuf.Bytes(), []byte("Call:")) {
			t.Fatal("expected no 'Call:' log when debug is off")
		}
	})

	// With debug enabled, handleCall should log
	t.Run("log_when_debug_on", func(t *testing.T) {
		logBuf.Reset()
		pm.SetDebug(true)

		var callBuf bytes.Buffer
		binary.Write(&callBuf, binary.BigEndian, uint32(2))
		binary.Write(&callBuf, binary.BigEndian, uint32(RPC_CALL))
		binary.Write(&callBuf, binary.BigEndian, uint32(2))
		binary.Write(&callBuf, binary.BigEndian, uint32(PortmapperProgram))
		binary.Write(&callBuf, binary.BigEndian, uint32(2))
		binary.Write(&callBuf, binary.BigEndian, uint32(PMAPPROC_NULL))
		binary.Write(&callBuf, binary.BigEndian, uint32(AUTH_NONE))
		binary.Write(&callBuf, binary.BigEndian, uint32(0))
		binary.Write(&callBuf, binary.BigEndian, uint32(AUTH_NONE))
		binary.Write(&callBuf, binary.BigEndian, uint32(0))

		_, err := pm.handleCall(callBuf.Bytes(), nil)
		if err != nil {
			t.Fatalf("handleCall failed: %v", err)
		}

		if !bytes.Contains(logBuf.Bytes(), []byte("Call:")) {
			t.Fatal("expected 'Call:' log when debug is on")
		}
	})
}
