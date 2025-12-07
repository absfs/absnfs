package absnfs

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ServerOptions defines the configuration for the NFS server
type ServerOptions struct {
	Name     string // Server name
	UID      uint32 // Server UID
	GID      uint32 // Server GID
	ReadOnly bool   // Read-only mode
	Port     int    // Port to listen on (0 = random port, default NFS port is 2049)
	Hostname string // Hostname to bind to
	Debug    bool   // Enable debug logging
}

// connectionState tracks the state of an active connection
type connectionState struct {
	lastActivity   time.Time
	unregisterOnce sync.Once // Ensures connection is only unregistered once
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
	acceptErrs     atomic.Int32 // Counter for accept errors to prevent excessive logging

	// Connection management
	connMutex      sync.Mutex
	activeConns    map[net.Conn]*connectionState  // Map of active connections and their state
	connCount      int                            // Current connection count
}

// NewServer creates a new NFS server
func NewServer(options ServerOptions) (*Server, error) {
	if options.Port < 0 {
		return nil, fmt.Errorf("invalid port")
	}
	// Note: Port 0 means let the OS assign a random port (useful for testing)
	// The default NFS port 2049 should be explicitly specified by the caller
	if options.Hostname == "" {
		options.Hostname = "localhost"
	}

	ctx, cancel := context.WithCancel(context.Background())
	return &Server{
		options:     options,
		logger:      log.New(os.Stderr, "[absnfs] ", log.LstdFlags),
		ctx:         ctx,
		cancel:      cancel,
		activeConns: make(map[net.Conn]*connectionState),
	}, nil
}

// SetHandler sets the filesystem handler for the server
func (s *Server) SetHandler(handler *AbsfsNFS) {
	s.handler = handler
}

// isIPAllowed checks if the client IP is in the AllowedIPs list
// It supports both individual IPs (e.g., "192.168.1.100") and CIDR notation (e.g., "192.168.1.0/24")
func (s *Server) isIPAllowed(clientIP string) bool {
	// If no handler or AllowedIPs is empty/nil, allow all IPs
	if s.handler == nil || len(s.handler.options.AllowedIPs) == 0 {
		return true
	}

	// Parse the client IP
	ip := net.ParseIP(clientIP)
	if ip == nil {
		// Invalid IP, reject
		return false
	}

	// Check against each allowed IP/subnet
	for _, allowedIP := range s.handler.options.AllowedIPs {
		// Check if it's a CIDR notation
		if strings.Contains(allowedIP, "/") {
			_, ipNet, err := net.ParseCIDR(allowedIP)
			if err != nil {
				// Invalid CIDR, skip this entry
				if s.options.Debug {
					s.logger.Printf("Invalid CIDR notation in AllowedIPs: %s", allowedIP)
				}
				continue
			}
			if ipNet.Contains(ip) {
				return true
			}
		} else {
			// Direct IP comparison
			allowedIPParsed := net.ParseIP(allowedIP)
			if allowedIPParsed != nil && ip.Equal(allowedIPParsed) {
				return true
			}
		}
	}

	// IP not found in allowed list
	return false
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

		// Log structured message
		if s.handler.structuredLogger != nil {
			if s.handler.options.Log != nil && s.handler.options.Log.LogClientIPs {
				clientAddr := ""
				if conn != nil && conn.RemoteAddr() != nil {
					clientAddr = conn.RemoteAddr().String()
				}
				s.handler.structuredLogger.Warn("connection limit reached",
					LogField{Key: "limit", Value: s.handler.options.MaxConnections},
					LogField{Key: "client_addr", Value: clientAddr})
			} else {
				s.handler.structuredLogger.Warn("connection limit reached",
					LogField{Key: "limit", Value: s.handler.options.MaxConnections})
			}
		}

		return false
	}

	// Add to tracking map with current time and unregister-once mechanism
	s.activeConns[conn] = &connectionState{
		lastActivity: time.Now(),
	}
	s.connCount++

	if s.options.Debug {
		s.logger.Printf("New connection accepted (total: %d)", s.connCount)
	}

	// Log structured message
	if s.handler.structuredLogger != nil {
		if s.handler.options.Log != nil && s.handler.options.Log.LogClientIPs {
			clientAddr := ""
			if conn != nil && conn.RemoteAddr() != nil {
				clientAddr = conn.RemoteAddr().String()
			}
			s.handler.structuredLogger.Info("connection accepted",
				LogField{Key: "total_connections", Value: s.connCount},
				LogField{Key: "client_addr", Value: clientAddr})
		} else {
			s.handler.structuredLogger.Info("connection accepted",
				LogField{Key: "total_connections", Value: s.connCount})
		}
	}

	return true
}

// unregisterConnection removes a connection from the tracking map and decrements the counter
// Uses sync.Once to ensure this only happens once per connection, preventing race conditions
func (s *Server) unregisterConnection(conn net.Conn) {
	s.connMutex.Lock()
	state, exists := s.activeConns[conn]
	s.connMutex.Unlock()

	if !exists {
		return // Connection already unregistered
	}

	// Use sync.Once to ensure the unregistration happens exactly once
	state.unregisterOnce.Do(func() {
		s.connMutex.Lock()
		defer s.connMutex.Unlock()

		// Double-check the connection still exists (in case another goroutine removed it)
		if _, stillExists := s.activeConns[conn]; stillExists {
			delete(s.activeConns, conn)
			s.connCount--

			if s.options.Debug {
				s.logger.Printf("Connection closed (total: %d)", s.connCount)
			}

			// Log structured message
			if s.handler != nil && s.handler.structuredLogger != nil {
				if s.handler.options.Log != nil && s.handler.options.Log.LogClientIPs {
					clientAddr := ""
					if conn != nil && conn.RemoteAddr() != nil {
						clientAddr = conn.RemoteAddr().String()
					}
					s.handler.structuredLogger.Info("connection closed",
						LogField{Key: "total_connections", Value: s.connCount},
						LogField{Key: "client_addr", Value: clientAddr})
				} else {
					s.handler.structuredLogger.Info("connection closed",
						LogField{Key: "total_connections", Value: s.connCount})
				}
			}
		}
	})
}

// updateConnectionActivity updates the last activity time for a connection
func (s *Server) updateConnectionActivity(conn net.Conn) {
	s.connMutex.Lock()
	defer s.connMutex.Unlock()

	// Update last activity time if connection still exists
	if state, exists := s.activeConns[conn]; exists {
		state.lastActivity = time.Now()
	}
}

// cleanupIdleConnections closes connections that have been idle for too long
func (s *Server) cleanupIdleConnections() {
	if s.handler == nil || s.handler.options.IdleTimeout <= 0 {
		return // No handler or idle timeout not set
	}

	s.connMutex.Lock()
	now := time.Now()
	idleTimeout := s.handler.options.IdleTimeout

	// Collect idle connections while holding the lock
	var idleConns []net.Conn
	for conn, state := range s.activeConns {
		if now.Sub(state.lastActivity) > idleTimeout {
			idleConns = append(idleConns, conn)
		}
	}
	s.connMutex.Unlock()

	// Close idle connections and unregister them
	// The sync.Once in unregisterConnection ensures this happens exactly once
	for _, conn := range idleConns {
		conn.Close()
		s.unregisterConnection(conn)
	}

	if len(idleConns) > 0 && s.options.Debug {
		s.logger.Printf("Closed %d idle connections", len(idleConns))
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

	// Check if TLS is enabled
	var listener net.Listener
	var err error

	if s.handler.options.TLS != nil && s.handler.options.TLS.Enabled {
		// Build TLS configuration
		tlsConfig, err := s.handler.options.TLS.BuildConfig()
		if err != nil {
			return fmt.Errorf("failed to build TLS config: %w", err)
		}

		// Create TLS listener
		listener, err = tls.Listen("tcp", addr, tlsConfig)
		if err != nil {
			return fmt.Errorf("failed to listen on %s with TLS: %w", addr, err)
		}

		// If port was 0 (random port), update to the actual port assigned
		if s.options.Port == 0 {
			if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
				s.options.Port = tcpAddr.Port
			}
		}

		s.logger.Printf("TLS enabled on %s (MinVersion: %s, MaxVersion: %s, ClientAuth: %s)",
			addr,
			TLSVersionString(s.handler.options.TLS.MinVersion),
			TLSVersionString(s.handler.options.TLS.MaxVersion),
			s.handler.options.TLS.GetClientAuthString())

		if s.handler.options.TLS.InsecureSkipVerify {
			s.logger.Printf("WARNING: TLS verification disabled (InsecureSkipVerify=true)")
		}
	} else {
		// Create regular TCP listener (no TLS)
		listener, err = net.Listen("tcp", addr)
		if err != nil {
			return fmt.Errorf("failed to listen on %s: %w", addr, err)
		}

		// If port was 0 (random port), update to the actual port assigned
		if s.options.Port == 0 {
			if tcpAddr, ok := listener.Addr().(*net.TCPAddr); ok {
				s.options.Port = tcpAddr.Port
			}
		}

		if s.options.Debug {
			s.logger.Printf("TLS disabled - connections are unencrypted")
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
				if s.acceptErrs.Load() < maxAcceptErrors {
					// Only log if we're in debug mode AND it's not a test error
					if s.options.Debug && !strings.Contains(err.Error(), "test error") {
						s.logger.Printf("accept error: %v", err)
					}
					s.acceptErrs.Add(1)
					time.Sleep(acceptErrorDelay) // Prevent tight error loop
				}
				continue
			}
			s.acceptErrs.Store(0) // Reset error counter on successful accept
			
			// Extract client IP from connection
			clientAddr := conn.RemoteAddr().String()
			clientIP, _, err := net.SplitHostPort(clientAddr)
			if err != nil {
				// If we can't parse the address, assume it's already just an IP
				clientIP = clientAddr
			}

			// Check if the client IP is allowed
			if !s.isIPAllowed(clientIP) {
				if s.options.Debug {
					s.logger.Printf("Connection rejected: IP %s not in AllowedIPs list", clientIP)
				}

				// Log structured message
				if s.handler != nil && s.handler.structuredLogger != nil {
					if s.handler.options.Log != nil && s.handler.options.Log.LogClientIPs {
						s.handler.structuredLogger.Warn("connection rejected: IP not allowed",
							LogField{Key: "client_ip", Value: clientIP})
					} else {
						s.handler.structuredLogger.Warn("connection rejected: IP not allowed")
					}
				}

				conn.Close()
				continue
			}

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
					if err := tcpConn.SetKeepAlive(true); err != nil {
						s.logger.Printf("Warning: failed to set TCP keepalive: %v", err)
					}
					if err := tcpConn.SetKeepAlivePeriod(60 * time.Second); err != nil {
						s.logger.Printf("Warning: failed to set TCP keepalive period: %v", err)
					}
				}

				// Apply TCP no delay setting (disable Nagle's algorithm)
				if s.handler.options.TCPNoDelay {
					if err := tcpConn.SetNoDelay(true); err != nil {
						s.logger.Printf("Warning: failed to set TCP no delay: %v", err)
					}
				}

				// Apply buffer sizes
				if s.handler.options.SendBufferSize > 0 {
					if err := tcpConn.SetWriteBuffer(s.handler.options.SendBufferSize); err != nil {
						s.logger.Printf("Warning: failed to set send buffer size: %v", err)
					}
				}

				if s.handler.options.ReceiveBufferSize > 0 {
					if err := tcpConn.SetReadBuffer(s.handler.options.ReceiveBufferSize); err != nil {
						s.logger.Printf("Warning: failed to set receive buffer size: %v", err)
					}
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

	// Generate a unique connection ID for per-connection rate limiting
	connID := fmt.Sprintf("%p", conn)
	defer func() {
		// Clean up connection-specific rate limiter on exit
		if s.handler != nil && s.handler.rateLimiter != nil {
			s.handler.rateLimiter.CleanupConnection(connID)
		}
	}()

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

			// Extract client IP and port for authentication
			authCtx := &AuthContext{
				Credential: &call.Credential,
			}
			if remoteAddr := conn.RemoteAddr(); remoteAddr != nil {
				if tcpAddr, ok := remoteAddr.(*net.TCPAddr); ok {
					authCtx.ClientIP = tcpAddr.IP.String()
					authCtx.ClientPort = tcpAddr.Port
				} else {
					// Fallback for non-TCP connections
					addrStr := remoteAddr.String()
					host, port, err := net.SplitHostPort(addrStr)
					if err == nil {
						authCtx.ClientIP = host
						// Parse port as int
						if p, err := net.LookupPort("tcp", port); err == nil {
							authCtx.ClientPort = p
						}
					}
				}
			}

			// Check rate limit before processing request
			if s.handler != nil && s.handler.rateLimiter != nil && s.handler.options.EnableRateLimiting {
				if !s.handler.rateLimiter.AllowRequest(authCtx.ClientIP, connID) {
					// Rate limit exceeded - send error reply
					reply := &RPCReply{
						Header: call.Header,
						Status: MSG_DENIED,
						Verifier: RPCVerifier{
							Flavor: 0,
							Body:   []byte{},
						},
					}

					if s.options.Debug {
						s.logger.Printf("Rate limit exceeded for client %s", authCtx.ClientIP)
					}

					// Log structured message
					if s.handler.structuredLogger != nil {
						if s.handler.options.Log != nil && s.handler.options.Log.LogClientIPs {
							s.handler.structuredLogger.Warn("rate limit exceeded",
								LogField{Key: "client_ip", Value: authCtx.ClientIP})
						} else {
							s.handler.structuredLogger.Warn("rate limit exceeded")
						}
					}

					// Record rate limit rejection in metrics
					if s.handler.metrics != nil {
						s.handler.metrics.RecordRateLimitExceeded()
					}

					// Send rejection and continue to next request
					if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err == nil {
						s.writeRPCReply(conn, reply)
					}
					continue
				}
			}

			// Use worker pool to handle the call if available
			var reply *RPCReply
			var handleErr error

			if s.handler != nil && s.handler.workerPool != nil {
				// Process with worker pool
				result := s.handler.ExecuteWithWorker(func() interface{} {
					r, e := procHandler.HandleCall(call, body, authCtx)
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
				reply, handleErr = procHandler.HandleCall(call, body, authCtx)
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
	// Collect all connections while holding the lock
	var conns []net.Conn
	for conn := range s.activeConns {
		conns = append(conns, conn)
	}
	connCount := len(conns)
	s.connMutex.Unlock()

	// Close all connections and unregister them
	// The sync.Once in unregisterConnection ensures cleanup happens exactly once
	for _, conn := range conns {
		conn.Close()
		s.unregisterConnection(conn)
	}

	if connCount > 0 && s.options.Debug {
		s.logger.Printf("Closed %d connections during shutdown", connCount)
	}
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