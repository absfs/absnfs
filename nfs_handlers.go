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
