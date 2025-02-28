package absnfs

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
	"time"
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
			Status: MSG_ACCEPTED,
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
		var xid, msgType, status, verFlavor, verLen uint32
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
			Mode:  0644,
			Uid:   1000,
			Gid:   1000,
			Size:  4096,
			Mtime: time.Now(),
			Atime: time.Now(),
		}
		
		reply := &RPCReply{
			Header: RPCMsgHeader{
				Xid:     2,
				MsgType: RPC_REPLY,
			},
			Status: MSG_ACCEPTED,
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
		var headerData [20]byte // 5 uint32s (XID, msgType, status, verFlavor, verLen)
		n, err := buf.Read(headerData[:])
		if err != nil || n != 20 {
			t.Errorf("Failed to read header data: %v", err)
		}
		
		// Should have encoded attributes data
		if buf.Len() == 0 {
			t.Errorf("No attribute data was encoded")
		}
		
		// Read mode as a basic check
		var mode uint32
		binary.Read(buf, binary.BigEndian, &mode)
		if mode != uint32(attrs.Mode) {
			t.Errorf("Expected mode %d, got %d", attrs.Mode, mode)
		}
	})
	
	t.Run("encode reply with uint32 data", func(t *testing.T) {
		statusCode := uint32(NFSERR_NOENT)
		
		reply := &RPCReply{
			Header: RPCMsgHeader{
				Xid:     3,
				MsgType: RPC_REPLY,
			},
			Status: MSG_ACCEPTED,
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
		var headerData [20]byte // 5 uint32s (XID, msgType, status, verFlavor, verLen)
		n, err := buf.Read(headerData[:])
		if err != nil || n != 20 {
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
			Status: MSG_ACCEPTED,
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
		var headerData [20]byte // 5 uint32s (XID, msgType, status, verFlavor, verLen)
		n, err := buf.Read(headerData[:])
		if err != nil || n != 20 {
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
					Mode:  0644,
					Uid:   1000,
					Gid:   1000,
					Size:  1024,
					Mtime: time.Now(),
					Atime: time.Now(),
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
				Status: MSG_ACCEPTED,
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
			if buf.Len() < 20 { // min header size
				t.Errorf("Encoded buffer too small for data type %T: %d bytes", data, buf.Len())
			}
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
			Data: []byte{1, 2, 3},
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
	case "data":
		if w.count == 6 {
			return 0, io.ErrShortWrite
		}
	case "length":
		return 0, io.ErrShortWrite // Fail immediately
	}
	return len(p), nil
}
