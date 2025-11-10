package absnfs

import (
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

// HandleCall processes an NFS RPC call and returns a reply
func (h *NFSProcedureHandler) HandleCall(call *RPCCall, body io.Reader) (*RPCReply, error) {
	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	reply := &RPCReply{
		Header: call.Header,
		Status: MSG_ACCEPTED,
		Verifier: RPCVerifier{
			Flavor: 0,
			Body:   []byte{},
		},
	}

	// Check authentication
	if call.Credential.Flavor != 0 {
		reply.Status = MSG_DENIED
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

		var result *RPCReply
		var err error

		switch call.Header.Program {
		case MOUNT_PROGRAM:
			result, err = h.handleMountCall(call, body, reply)
		case NFS_PROGRAM:
			result, err = h.handleNFSCall(call, body, reply)
		default:
			reply.Status = PROG_UNAVAIL
			// Use select to avoid blocking if context is cancelled
			select {
			case replyChan <- reply:
			case <-ctx.Done():
			}
			return
		}

		if err != nil {
			// Convert error to appropriate RPC status
			switch err := err.(type) {
			case *RPCError:
				reply.Status = err.Status
			default:
				if os.IsNotExist(err) {
					reply.Status = NFSERR_NOENT
				} else if os.IsPermission(err) {
					reply.Status = ACCESS_DENIED
				} else {
					reply.Status = GARBAGE_ARGS
				}
			}
			// Use select to avoid blocking if context is cancelled
			select {
			case replyChan <- reply:
			case <-ctx.Done():
			}
			return
		}
		// Use select to avoid blocking if context is cancelled
		select {
		case replyChan <- result:
		case <-ctx.Done():
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
