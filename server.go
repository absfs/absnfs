package absnfs

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// ServerOptions defines the configuration for the NFS server
type ServerOptions struct {
	Name     string // Server name
	UID      uint32 // Server UID
	GID      uint32 // Server GID
	ReadOnly bool   // Read-only mode
	Port     int    // Port to listen on (default: 2049)
	Hostname string // Hostname to bind to
	Debug    bool   // Enable debug logging
}

// Server represents an NFS server instance
type Server struct {
	options    ServerOptions
	handler    *AbsfsNFS
	listener   net.Listener
	logger     *log.Logger
	ctx        context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	acceptErrs int // Counter for accept errors to prevent excessive logging
}

// NewServer creates a new NFS server
func NewServer(options ServerOptions) (*Server, error) {
	if options.Port < 0 {
		return nil, fmt.Errorf("invalid port")
	}
	if options.Port == 0 {
		options.Port = 2049
	}
	if options.Hostname == "" {
		options.Hostname = "localhost"
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		options: options,
		logger:  log.New(os.Stderr, "[absnfs] ", log.LstdFlags),
		ctx:     ctx,
		cancel:  cancel,
	}, nil
}

// SetHandler sets the filesystem handler for the server
func (s *Server) SetHandler(handler *AbsfsNFS) {
	s.handler = handler
}

// Listen starts the NFS server
func (s *Server) Listen() error {
	if s.handler == nil {
		return fmt.Errorf("no handler set")
	}

	// Try to bind to the specified port
	addr := fmt.Sprintf("%s:%d", s.options.Hostname, s.options.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		// If port is in use and we're using the default port, try a random port
		if s.options.Port == 2049 && isAddrInUse(err) {
			addr = fmt.Sprintf("%s:0", s.options.Hostname)
			listener, err = net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("failed to listen on %s: %v", addr, err)
			}
			// Update the port to the actual port assigned
			if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
				s.options.Port = tcpAddr.Port
			}
		} else {
			return fmt.Errorf("failed to listen on %s: %v", addr, err)
		}
	}
	s.listener = listener

	procHandler := &NFSProcedureHandler{server: s}

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.acceptLoop(procHandler)
	}()

	return nil
}

// isAddrInUse checks if the error is "address already in use"
func isAddrInUse(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "address already in use")
}

func (s *Server) acceptLoop(procHandler *NFSProcedureHandler) {
	const maxAcceptErrors = 3 // Limit consecutive accept errors logged
	const acceptErrorDelay = 100 * time.Millisecond

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			// Set accept deadline to prevent blocking forever
			if tcpListener, ok := s.listener.(*net.TCPListener); ok {
				tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
			}

			conn, err := s.listener.Accept()
			if err != nil {
				// Check if server is shutting down
				select {
				case <-s.ctx.Done():
					return
				default:
				}

				if netErr, ok := err.(net.Error); ok {
					if netErr.Timeout() {
						continue // Skip logging timeout errors
					}
					if opErr, ok := netErr.(*net.OpError); ok {
						if opErr.Op == "accept" && strings.Contains(opErr.Error(), "use of closed network connection") {
							return // Server is shutting down
						}
					}
				}

				// Only log if we haven't seen too many consecutive errors
				if s.acceptErrs < maxAcceptErrors {
					// Only log if we're in debug mode AND it's not a test error
					if s.options.Debug && !strings.Contains(err.Error(), "test error") {
						s.logger.Printf("accept error: %v", err)
					}
					s.acceptErrs++
					time.Sleep(acceptErrorDelay) // Prevent tight error loop
				}
				continue
			}
			s.acceptErrs = 0 // Reset error counter on successful accept

			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				s.handleConnection(conn, procHandler)
			}()
		}
	}
}

func (s *Server) handleConnection(conn net.Conn, procHandler *NFSProcedureHandler) {
	defer conn.Close()

	// Set timeouts for network operations
	const (
		readTimeout  = 5 * time.Second
		writeTimeout = 5 * time.Second
	)

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			// Set read deadline
			if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
				return
			}

			// Read RPC call
			call, body, err := s.readRPCCall(conn)
			if err != nil {
				// Don't log common expected errors in tests
				if err != io.EOF && !isTimeoutError(err) && !isConnectionResetError(err) {
					s.logger.Printf("read error: %v", err)
				}
				return
			}

			// Handle the call
			reply, err := procHandler.HandleCall(call, body)
			if err != nil {
				s.logger.Printf("handle error: %v", err)
				continue
			}

			// Set write deadline
			if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				return
			}

			// Send reply
			if err := s.writeRPCReply(conn, reply); err != nil {
				// Don't log common expected errors in tests
				if !isTimeoutError(err) && !isConnectionResetError(err) {
					s.logger.Printf("write error: %v", err)
				}
				return
			}
		}
	}
}

func (s *Server) readRPCCall(conn net.Conn) (*RPCCall, io.Reader, error) {
	// Read the RPC call
	call, err := DecodeRPCCall(conn)
	if err != nil {
		return nil, nil, err
	}

	// Return the connection as the body reader
	return call, conn, nil
}

func (s *Server) writeRPCReply(conn net.Conn, reply *RPCReply) error {
	return EncodeRPCReply(conn, reply)
}

// Stop stops the NFS server
func (s *Server) Stop() error {
	s.cancel() // Signal all goroutines to stop

	// Close listener to stop accepting new connections
	if s.listener != nil {
		s.listener.Close()
	}

	// Wait for all goroutines to finish with timeout
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout waiting for server shutdown")
	}
}

// isTimeoutError checks if an error is a timeout
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	netErr, ok := err.(net.Error)
	return ok && netErr.Timeout()
}

// isConnectionResetError checks if an error is due to connection reset
func isConnectionResetError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "connection reset by peer") || 
		   strings.Contains(errStr, "broken pipe") ||
		   strings.Contains(errStr, "EOF") ||
		   strings.Contains(errStr, "i/o timeout") ||
		   strings.Contains(errStr, "use of closed network connection")
}
