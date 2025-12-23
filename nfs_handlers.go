package absnfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"time"
)

// NFSProcedureHandler handles NFS procedure calls
type NFSProcedureHandler struct {
	server *Server
}

// RPCError represents an RPC-specific error with a status code
type RPCError struct {
	Status uint32
	Msg    string
}

func (e *RPCError) Error() string {
	return e.Msg
}

// nfsHandler is a function type for handling individual NFS procedures
type nfsHandler func(h *NFSProcedureHandler, body io.Reader, reply *RPCReply, authCtx *AuthContext) (*RPCReply, error)

// nfsHandlers maps NFS procedure numbers to their handler functions
var nfsHandlers = map[uint32]nfsHandler{
	NFSPROC3_NULL:        (*NFSProcedureHandler).handleNull,
	NFSPROC3_GETATTR:     (*NFSProcedureHandler).handleGetattr,
	NFSPROC3_SETATTR:     (*NFSProcedureHandler).handleSetattr,
	NFSPROC3_LOOKUP:      (*NFSProcedureHandler).handleLookup,
	NFSPROC3_READLINK:    (*NFSProcedureHandler).handleReadlink,
	NFSPROC3_READ:        (*NFSProcedureHandler).handleRead,
	NFSPROC3_WRITE:       (*NFSProcedureHandler).handleWrite,
	NFSPROC3_CREATE:      (*NFSProcedureHandler).handleCreate,
	NFSPROC3_MKDIR:       (*NFSProcedureHandler).handleMkdir,
	NFSPROC3_SYMLINK:     (*NFSProcedureHandler).handleSymlink,
	NFSPROC3_READDIR:     (*NFSProcedureHandler).handleReaddir,
	NFSPROC3_READDIRPLUS: (*NFSProcedureHandler).handleReaddirplus,
	NFSPROC3_FSSTAT:      (*NFSProcedureHandler).handleFsstat,
	NFSPROC3_FSINFO:      (*NFSProcedureHandler).handleFsinfo,
	NFSPROC3_PATHCONF:    (*NFSProcedureHandler).handlePathconf,
	NFSPROC3_ACCESS:      (*NFSProcedureHandler).handleAccess,
	NFSPROC3_COMMIT:      (*NFSProcedureHandler).handleCommit,
	NFSPROC3_REMOVE:      (*NFSProcedureHandler).handleRemove,
	NFSPROC3_RMDIR:       (*NFSProcedureHandler).handleRmdir,
	NFSPROC3_RENAME:      (*NFSProcedureHandler).handleRename,
	NFSPROC3_LINK:        (*NFSProcedureHandler).handleLink,
}

// HandleCall processes an NFS RPC call and returns a reply
func (h *NFSProcedureHandler) HandleCall(call *RPCCall, body io.Reader, authCtx *AuthContext) (*RPCReply, error) {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reply := &RPCReply{
		Header:       call.Header,
		Status:       MSG_ACCEPTED,
		AcceptStatus: SUCCESS,
		Verifier: RPCVerifier{
			Flavor: 0,
			Body:   []byte{},
		},
	}

	// Validate authentication
	authResult := ValidateAuthentication(authCtx, h.server.handler.options)
	if !authResult.Allowed {
		reply.Status = MSG_DENIED
		if h.server.options.Debug {
			h.server.logger.Printf("Authentication denied: %s (client: %s:%d, flavor: %d)",
				authResult.Reason, authCtx.ClientIP, authCtx.ClientPort, authCtx.Credential.Flavor)
		}
		// Increment auth failure metric if available
		if h.server.handler.metrics != nil {
			h.server.handler.metrics.RecordError("AUTH")
		}
		return reply, nil
	}

	// Handle the call with timeout
	errChan := make(chan error, 1)
	replyChan := make(chan *RPCReply, 1)

	go func() {
		// Check if context is already cancelled before starting work
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Debug logging for all incoming calls
		if h.server.options.Debug {
			h.server.logger.Printf("HandleCall: prog=%d vers=%d proc=%d",
				call.Header.Program, call.Header.Version, call.Header.Procedure)
		}

		var result *RPCReply
		var err error

		switch call.Header.Program {
		case MOUNT_PROGRAM:
			result, err = h.handleMountCall(call, body, reply, authCtx)
		case NFS_PROGRAM:
			result, err = h.handleNFSCall(call, body, reply, authCtx)
		default:
			reply.AcceptStatus = PROG_UNAVAIL
			// Check context before sending
			select {
			case <-ctx.Done():
				return
			case replyChan <- reply:
			}
			return
		}

		if err != nil {
			// Convert error to appropriate RPC accept status
			switch err := err.(type) {
			case *RPCError:
				reply.AcceptStatus = err.Status
			default:
				if os.IsNotExist(err) {
					// NFS errors go in the procedure-specific data, not AcceptStatus
					reply.AcceptStatus = SUCCESS
				} else if os.IsPermission(err) {
					reply.AcceptStatus = SUCCESS
				} else {
					reply.AcceptStatus = GARBAGE_ARGS
				}
			}
			// Check context before sending
			select {
			case <-ctx.Done():
				return
			case replyChan <- reply:
			}
			return
		}
		// Check context before sending result
		select {
		case <-ctx.Done():
			return
		case replyChan <- result:
		}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("operation timed out")
	case err := <-errChan:
		return nil, err
	case result := <-replyChan:
		return result, nil
	}
}

// Helper functions for common operations

// nfsErrorReply creates an error response with the given NFS status code
func nfsErrorReply(reply *RPCReply, status uint32) *RPCReply {
	var buf bytes.Buffer
	xdrEncodeUint32(&buf, status)
	reply.Data = buf.Bytes()
	return reply
}

// lookupNode retrieves a node from the file handle map
// Returns the node and true if found, nil and false otherwise
func (h *NFSProcedureHandler) lookupNode(handle uint64) (*NFSNode, bool) {
	file, ok := h.server.handler.fileMap.Get(handle)
	if !ok {
		return nil, false
	}
	node, ok := file.(*NFSNode)
	return node, ok
}

// decodeAndLookupHandle decodes a file handle from the body and looks up the node
// Returns the node and handle value, or nil if not found (reply.Data will be set with error)
func (h *NFSProcedureHandler) decodeAndLookupHandle(body io.Reader, reply *RPCReply) (*NFSNode, uint64) {
	handleVal, err := xdrDecodeFileHandle(body)
	if err != nil {
		nfsErrorReply(reply, GARBAGE_ARGS)
		return nil, 0
	}

	node, ok := h.lookupNode(handleVal)
	if !ok {
		nfsErrorReply(reply, NFSERR_NOENT)
		return nil, 0
	}

	return node, handleVal
}

// encodeWccData encodes wcc_data (pre_op_attr + post_op_attr) to the buffer
func encodeWccData(buf *bytes.Buffer, preAttrs, postAttrs *NFSAttrs) error {
	// pre_op_attr: attributes_follow + wcc_attr
	xdrEncodeUint32(buf, 1) // attributes_follow = TRUE
	if err := encodeWccAttr(buf, preAttrs); err != nil {
		return err
	}

	// post_op_attr: attributes_follow + fattr3
	xdrEncodeUint32(buf, 1) // attributes_follow = TRUE
	return encodeFileAttributes(buf, postAttrs)
}

// encodePostOpAttr encodes post_op_attr to the buffer
func encodePostOpAttr(buf *bytes.Buffer, attrs *NFSAttrs) error {
	xdrEncodeUint32(buf, 1) // attributes_follow = TRUE
	return encodeFileAttributes(buf, attrs)
}

// encodeNoPostOpAttr encodes an empty post_op_attr (attributes_follow = FALSE)
func encodeNoPostOpAttr(buf *bytes.Buffer) {
	xdrEncodeUint32(buf, 0) // attributes_follow = FALSE
}
