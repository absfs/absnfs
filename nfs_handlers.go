// nfs_handlers.go: NFS RPC dispatch layer.
//
// Contains NFSProcedureHandler which maps NFS3 procedure numbers to
// handler functions and dispatches incoming RPC calls via HandleCall.
// Includes error reply helpers, drain-and-swap logic for live policy
// updates, and routing for NFS, MOUNT, and portmapper programs.
package absnfs

import (
	"bytes"
	"context"
	"fmt"
	"io"
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
	NFSPROC3_MKNOD:       (*NFSProcedureHandler).handleMknod,
}

// HandleCall processes an NFS RPC call and returns a reply.
// It snapshots options at entry, tracks in-flight requests for drain-and-swap,
// and rejects new requests during a policy drain.
func (h *NFSProcedureHandler) HandleCall(call *RPCCall, body io.Reader, authCtx *AuthContext) (*RPCReply, error) {
	handler := h.server.handler

	reply := &RPCReply{
		Header:       call.Header,
		Status:       MSG_ACCEPTED,
		AcceptStatus: SUCCESS,
		Verifier: RPCVerifier{
			Flavor: 0,
			Body:   []byte{},
		},
	}

	// Acquire policy read lock. TryRLock fails if a policy update (Lock)
	// is in progress, causing us to return JUKEBOX so clients retry.
	if !handler.policyRWMu.TryRLock() {
		// Policy drain in progress -- return NFS3ERR_JUKEBOX
		var buf bytes.Buffer
		xdrEncodeUint32(&buf, NFSERR_JUKEBOX)
		reply.Data = buf.Bytes()
		return reply, nil
	}
	defer handler.policyRWMu.RUnlock()

	// Snapshot options for this request
	opts := handler.snapshotOptions()

	// Create context with timeout using snapshotted timeout
	timeout := opts.Tuning.Timeouts.DefaultTimeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Validate authentication using policy snapshot
	authResult := ValidateAuthentication(authCtx, opts.Policy)
	if !authResult.Allowed {
		reply.Status = MSG_DENIED
		if h.server.options.Debug {
			h.server.logger.Printf("Authentication denied: %s (client: %s:%d, flavor: %d)",
				authResult.Reason, authCtx.ClientIP, authCtx.ClientPort, authCtx.Credential.Flavor)
		}
		if handler.metrics != nil {
			handler.metrics.RecordError("AUTH")
		}
		return reply, nil
	}
	// Apply squashed credentials to the auth context
	authCtx.EffectiveUID = authResult.UID
	authCtx.EffectiveGID = authResult.GID

	// Handle the call with timeout
	replyChan := make(chan *RPCReply, 1)

	go func() {
		select {
		case <-ctx.Done():
			return
		default:
		}

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
			select {
			case <-ctx.Done():
				return
			case replyChan <- reply:
			}
			return
		}

		if err != nil {
			switch err := err.(type) {
			case *RPCError:
				reply.AcceptStatus = err.Status
			default:
				reply.AcceptStatus = SYSTEM_ERR
			}
			select {
			case <-ctx.Done():
				return
			case replyChan <- reply:
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case replyChan <- result:
		}
	}()

	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("operation timed out")
	case result := <-replyChan:
		return result, nil
	}
}

// Helper functions for common operations

// nfsErrorReply creates an error response with the given NFS status code.
// Used for procedures that need only the status (e.g. GETATTR).
func nfsErrorReply(reply *RPCReply, status uint32) *RPCReply {
	var buf bytes.Buffer
	xdrEncodeUint32(&buf, status)
	reply.Data = buf.Bytes()
	return reply
}

// nfsErrorWithPostOp creates an error response with status + post_op_attr (attributes_follow=FALSE).
// Used for read-type procedures: LOOKUP, ACCESS, READ, READLINK, READDIR, READDIRPLUS, FSSTAT, FSINFO, PATHCONF.
func nfsErrorWithPostOp(reply *RPCReply, status uint32) *RPCReply {
	var buf bytes.Buffer
	xdrEncodeUint32(&buf, status)
	xdrEncodeUint32(&buf, 0) // post_op_attr: attributes_follow = FALSE
	reply.Data = buf.Bytes()
	return reply
}

// nfsErrorWithWcc creates an error response with status + empty wcc_data.
// Used for write/mutating procedures: SETATTR, WRITE, CREATE, MKDIR, SYMLINK, REMOVE, RMDIR, RENAME, LINK, COMMIT.
func nfsErrorWithWcc(reply *RPCReply, status uint32) *RPCReply {
	var buf bytes.Buffer
	xdrEncodeUint32(&buf, status)
	xdrEncodeUint32(&buf, 0) // pre_op_attr: attributes_follow = FALSE
	xdrEncodeUint32(&buf, 0) // post_op_attr: attributes_follow = FALSE
	reply.Data = buf.Bytes()
	return reply
}

// nfsErrorWithPostOpAndWcc creates an error response with status + post_op_attr + wcc_data.
// Used for LINK3resfail: status + post_op_attr (source) + wcc_data (target dir).
func nfsErrorWithPostOpAndWcc(reply *RPCReply, status uint32) *RPCReply {
	var buf bytes.Buffer
	xdrEncodeUint32(&buf, status)
	xdrEncodeUint32(&buf, 0) // post_op_attr: attributes_follow = FALSE
	xdrEncodeUint32(&buf, 0) // wcc_data pre_op_attr: attributes_follow = FALSE
	xdrEncodeUint32(&buf, 0) // wcc_data post_op_attr: attributes_follow = FALSE
	reply.Data = buf.Bytes()
	return reply
}

// nfsErrorWithDoubleWcc creates an error response with status + two empty wcc_data.
// Used for RENAME3resfail: status + wcc_data(fromdir_wcc) + wcc_data(todir_wcc).
func nfsErrorWithDoubleWcc(reply *RPCReply, status uint32) *RPCReply {
	var buf bytes.Buffer
	xdrEncodeUint32(&buf, status)
	xdrEncodeUint32(&buf, 0) // fromdir wcc pre_op_attr: FALSE
	xdrEncodeUint32(&buf, 0) // fromdir wcc post_op_attr: FALSE
	xdrEncodeUint32(&buf, 0) // todir wcc pre_op_attr: FALSE
	xdrEncodeUint32(&buf, 0) // todir wcc post_op_attr: FALSE
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
		nfsErrorReply(reply, NFSERR_STALE)
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
