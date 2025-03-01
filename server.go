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
	options        ServerOptions
	handler        *AbsfsNFS
	listener       net.Listener
	logger         *log.Logger
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	acceptErrs     int // Counter for accept errors to prevent excessive logging
	
	// Connection management
	connMutex      sync.Mutex
	activeConns    map[net.Conn]time.Time  // Map of active connections and their last activity time
	connCount      int                     // Current connection count
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
		options:     options,
		logger:      log.New(os.Stderr, "[absnfs] ", log.LstdFlags),
		ctx:         ctx,
		cancel:      cancel,
		activeConns: make(map[net.Conn]time.Time),
	}, nil
}

// SetHandler sets the filesystem handler for the server
func (s *Server) SetHandler(handler *AbsfsNFS) {
	s.handler = handler
}

// Listen starts the NFS server
// registerConnection adds a connection to the tracking map and increments the counter
func (s *Server) registerConnection(conn net.Conn) bool {
	if s.handler == nil {
		return true // If no handler, always allow connections
	}

	s.connMutex.Lock()
	defer s.connMutex.Unlock()

	// Check if we're at the connection limit
	if s.handler.options.MaxConnections > 0 && s.connCount >= s.handler.options.MaxConnections {
		// We're at the limit, reject this connection
		if s.options.Debug {
			s.logger.Printf("Connection limit reached (%d), rejecting new connection", s.handler.options.MaxConnections)
		}
		return false
	}

	// Add to tracking map with current time
	s.activeConns[conn] = time.Now()
	s.connCount++

	if s.options.Debug {
		s.logger.Printf("New connection accepted (total: %d)", s.connCount)
	}
	return true
}

// unregisterConnection removes a connection from the tracking map and decrements the counter
func (s *Server) unregisterConnection(conn net.Conn) {
	s.connMutex.Lock()
	defer s.connMutex.Unlock()

	// Remove from tracking map
	if _, exists := s.activeConns[conn]; exists {
		delete(s.activeConns, conn)
		s.connCount--
		
		if s.options.Debug {
			s.logger.Printf("Connection closed (total: %d)", s.connCount)
		}
	}
}

// updateConnectionActivity updates the last activity time for a connection
func (s *Server) updateConnectionActivity(conn net.Conn) {
	s.connMutex.Lock()
	defer s.connMutex.Unlock()

	// Update last activity time
	s.activeConns[conn] = time.Now()
}

// cleanupIdleConnections closes connections that have been idle for too long
func (s *Server) cleanupIdleConnections() {
	if s.handler == nil || s.handler.options.IdleTimeout <= 0 {
		return // No handler or idle timeout not set
	}

	s.connMutex.Lock()
	defer s.connMutex.Unlock()

	now := time.Now()
	idleTimeout := s.handler.options.IdleTimeout

	// Check each connection
	var idleCount int
	for conn, lastActivity := range s.activeConns {
		if now.Sub(lastActivity) > idleTimeout {
			// This connection has been idle for too long
			conn.Close() // Close the connection
			delete(s.activeConns, conn)
			s.connCount--
			idleCount++
		}
	}

	if idleCount > 0 && s.options.Debug {
		s.logger.Printf("Closed %d idle connections (remaining: %d)", idleCount, s.connCount)
	}
}

// idleConnectionCleanupLoop periodically checks for and closes idle connections
func (s *Server) idleConnectionCleanupLoop() {
	// Default check interval is 1 minute or IdleTimeout/2, whichever is shorter
	checkInterval := 1 * time.Minute
	
	if s.handler != nil && s.handler.options.IdleTimeout > 0 {
		// Use half the idle timeout as a reasonable check interval
		halfTimeout := s.handler.options.IdleTimeout / 2
		if halfTimeout < checkInterval {
			checkInterval = halfTimeout
		}
	}
	
	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-s.ctx.Done():
			return // Server is shutting down
		case <-ticker.C:
			s.cleanupIdleConnections()
		}
	}
}

func (s *Server) Listen() error {
	if s.handler == nil {
		return fmt.Errorf("no handler set")
	}
	
	// Start periodic idle connection cleanup if needed
	if s.handler.options.IdleTimeout > 0 {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.idleConnectionCleanupLoop()
		}()
		
		if s.options.Debug {
			s.logger.Printf("Connection management enabled (max connections: %d, idle timeout: %v)",
				s.handler.options.MaxConnections, s.handler.options.IdleTimeout)
		}
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
			
			// Check if we can accept this connection based on connection limits
			if !s.registerConnection(conn) {
				// Connection limit reached, reject this connection
				conn.Close()
				continue
			}
			
			// Configure TCP connection options if this is a TCP connection
			if tcpConn, ok := conn.(*net.TCPConn); ok && s.handler != nil {
				// Apply TCP keepalive setting
				if s.handler.options.TCPKeepAlive {
					tcpConn.SetKeepAlive(true)
					tcpConn.SetKeepAlivePeriod(60 * time.Second) // Standard keepalive period
				}
				
				// Apply TCP no delay setting (disable Nagle's algorithm)
				if s.handler.options.TCPNoDelay {
					tcpConn.SetNoDelay(true)
				}
				
				// Apply buffer sizes
				if s.handler.options.SendBufferSize > 0 {
					tcpConn.SetWriteBuffer(s.handler.options.SendBufferSize)
				}
				
				if s.handler.options.ReceiveBufferSize > 0 {
					tcpConn.SetReadBuffer(s.handler.options.ReceiveBufferSize)
				}
				
				if s.options.Debug {
					s.logger.Printf("Configured TCP connection (keepalive: %v, nodelay: %v, sendbuf: %d, recvbuf: %d)",
						s.handler.options.TCPKeepAlive,
						s.handler.options.TCPNoDelay,
						s.handler.options.SendBufferSize,
						s.handler.options.ReceiveBufferSize)
				}
			}

			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer s.unregisterConnection(conn) // Ensure connection is unregistered when done
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
			call, body, readErr := s.readRPCCall(conn)
			if readErr != nil {
				// Don't log common expected errors in tests
				if readErr != io.EOF && !isTimeoutError(readErr) && !isConnectionResetError(readErr) {
					s.logger.Printf("read error: %v", readErr)
				}
				return
			}
			
			// Update last activity time for this connection
			s.updateConnectionActivity(conn)

			// Use worker pool to handle the call if available
			var reply *RPCReply
			var handleErr error
			
			if s.handler != nil && s.handler.workerPool != nil {
				// Process with worker pool
				result := s.handler.ExecuteWithWorker(func() interface{} {
					r, e := procHandler.HandleCall(call, body)
					return struct {
						Reply *RPCReply
						Err   error
					}{r, e}
				})
				
				// Extract result
				typedResult := result.(struct {
					Reply *RPCReply
					Err   error
				})
				reply, handleErr = typedResult.Reply, typedResult.Err
			} else {
				// Process directly
				reply, handleErr = procHandler.HandleCall(call, body)
			}
			if handleErr != nil {
				s.logger.Printf("handle error: %v", handleErr)
				continue
			}

			// Set write deadline
			if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				return
			}

			// Send reply
			if writeErr := s.writeRPCReply(conn, reply); writeErr != nil {
				// Don't log common expected errors in tests
				if !isTimeoutError(writeErr) && !isConnectionResetError(writeErr) {
					s.logger.Printf("write error: %v", writeErr)
				}
				return
			}
			
			// Update last activity time for this connection after successful write
			s.updateConnectionActivity(conn)
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
	
	// Close all active connections
	s.closeAllConnections()

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

// closeAllConnections closes all active connections
func (s *Server) closeAllConnections() {
	s.connMutex.Lock()
	defer s.connMutex.Unlock()

	// Close all active connections
	for conn := range s.activeConns {
		conn.Close()
	}
	
	// Clear the map
	if len(s.activeConns) > 0 && s.options.Debug {
		s.logger.Printf("Closed %d connections during shutdown", len(s.activeConns))
	}
	s.activeConns = make(map[net.Conn]time.Time)
	s.connCount = 0
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