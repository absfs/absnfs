package absnfs

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/binary"
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
	Name             string // Server name
	UID              uint32 // Server UID
	GID              uint32 // Server GID
	ReadOnly         bool   // Read-only mode
	Port             int    // Port to listen on (0 = random port, default NFS port is 2049)
	MountPort        int    // Port for mount daemon (0 = same as NFS port, 635 = standard mountd port)
	Hostname         string // Hostname to bind to
	Debug            bool   // Enable debug logging
	UsePortmapper    bool   // Whether to start portmapper service (requires root for port 111)
	UseRecordMarking bool   // Use RPC record marking (required for standard NFS clients)
}

// connectionState tracks the state of an active connection
type connectionState struct {
	lastActivity   time.Time
	unregisterOnce sync.Once // Ensures connection is only unregistered once
}

// Server represents an NFS server instance
type Server struct {
	options       ServerOptions
	handler       *AbsfsNFS
	listener      net.Listener
	mountListener net.Listener // Separate listener for mount daemon
	portmapper    *Portmapper  // Portmapper service
	logger        *log.Logger
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	acceptErrs    atomic.Int32 // Counter for accept errors to prevent excessive logging
	writeVerf     [8]byte      // Write verifier unique per server boot (RFC 1813)

	// Connection management
	connMutex   sync.Mutex
	activeConns map[net.Conn]*connectionState // Map of active connections and their state
	connCount   int                           // Current connection count
	nextConnID  atomic.Uint64                 // Monotonic counter for connection IDs
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
	s := &Server{
		options:     options,
		logger:      log.New(os.Stderr, "[absnfs] ", log.LstdFlags),
		ctx:         ctx,
		cancel:      cancel,
		activeConns: make(map[net.Conn]*connectionState),
	}
	// Initialize write verifier unique to this server boot (RFC 1813)
	binary.BigEndian.PutUint64(s.writeVerf[:], uint64(time.Now().UnixNano()))
	return s, nil
}

// SetHandler sets the AbsfsNFS handler for this server.
// Must be called before Listen() and is not safe for concurrent use.
func (s *Server) SetHandler(handler *AbsfsNFS) {
	s.handler = handler
}

// isIPAllowed checks if the client IP is in the AllowedIPs list
// It supports both individual IPs (e.g., "192.168.1.100") and CIDR notation (e.g., "192.168.1.0/24")
func (s *Server) isIPAllowed(clientIP string) bool {
	// If no handler or AllowedIPs is empty/nil, allow all IPs
	if s.handler == nil {
		return true
	}
	policy := s.handler.policy.Load()
	if len(policy.AllowedIPs) == 0 {
		return true
	}

	// Parse the client IP
	ip := net.ParseIP(clientIP)
	if ip == nil {
		// Invalid IP, reject
		return false
	}
	ip = normalizeIP(ip)

	// Check against each allowed IP/subnet
	for _, allowedIP := range policy.AllowedIPs {
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
			if allowedIPParsed != nil && normalizeIP(allowedIPParsed).Equal(ip) {
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

	tuning := s.handler.tuning.Load()

	s.connMutex.Lock()
	defer s.connMutex.Unlock()

	// Check if we're at the connection limit
	if tuning.MaxConnections > 0 && s.connCount >= tuning.MaxConnections {
		// We're at the limit, reject this connection
		if s.options.Debug {
			s.logger.Printf("Connection limit reached (%d), rejecting new connection", tuning.MaxConnections)
		}

		// Log structured message
		if slog := s.handler.getStructuredLogger(); slog != nil {
			if tuning.Log != nil && tuning.Log.LogClientIPs {
				clientAddr := ""
				if conn != nil && conn.RemoteAddr() != nil {
					clientAddr = conn.RemoteAddr().String()
				}
				slog.Warn("connection limit reached",
					LogField{Key: "limit", Value: tuning.MaxConnections},
					LogField{Key: "client_addr", Value: clientAddr})
			} else {
				slog.Warn("connection limit reached",
					LogField{Key: "limit", Value: tuning.MaxConnections})
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
	if slog := s.handler.getStructuredLogger(); slog != nil {
		if tuning.Log != nil && tuning.Log.LogClientIPs {
			clientAddr := ""
			if conn != nil && conn.RemoteAddr() != nil {
				clientAddr = conn.RemoteAddr().String()
			}
			slog.Info("connection accepted",
				LogField{Key: "total_connections", Value: s.connCount},
				LogField{Key: "client_addr", Value: clientAddr})
		} else {
			slog.Info("connection accepted",
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
			if s.handler != nil {
				if slog := s.handler.getStructuredLogger(); slog != nil {
					tuning := s.handler.tuning.Load()
					if tuning.Log != nil && tuning.Log.LogClientIPs {
						clientAddr := ""
						if conn != nil && conn.RemoteAddr() != nil {
							clientAddr = conn.RemoteAddr().String()
						}
						slog.Info("connection closed",
							LogField{Key: "total_connections", Value: s.connCount},
							LogField{Key: "client_addr", Value: clientAddr})
					} else {
						slog.Info("connection closed",
							LogField{Key: "total_connections", Value: s.connCount})
					}
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
	if s.handler == nil {
		return // No handler
	}
	tuning := s.handler.tuning.Load()
	if tuning.IdleTimeout <= 0 {
		return // Idle timeout not set
	}

	s.connMutex.Lock()
	now := time.Now()
	idleTimeout := tuning.IdleTimeout

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

	if s.handler != nil {
		tuning := s.handler.tuning.Load()
		if tuning.IdleTimeout > 0 {
			// Use half the idle timeout as a reasonable check interval
			halfTimeout := tuning.IdleTimeout / 2
			if halfTimeout < checkInterval {
				checkInterval = halfTimeout
			}
		}
	}

	ticker := time.NewTicker(checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return // Server is shutting down
		case <-ticker.C:
			// Re-read tuning options on each tick so the loop picks up
			// dynamic configuration changes instead of using a stale snapshot.
			if s.handler != nil {
				tuning := s.handler.tuning.Load()
				if tuning.IdleTimeout > 0 {
					newInterval := tuning.IdleTimeout / 2
					if newInterval < 1*time.Minute {
						// Update ticker if interval changed
						if newInterval != checkInterval {
							checkInterval = newInterval
							ticker.Reset(checkInterval)
						}
					}
				}
			}
			s.cleanupIdleConnections()
		}
	}
}

func (s *Server) Listen() error {
	if s.handler == nil {
		return fmt.Errorf("no handler set")
	}

	tuning := s.handler.tuning.Load()
	policy := s.handler.policy.Load()

	// Start periodic idle connection cleanup if needed
	if tuning.IdleTimeout > 0 {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.idleConnectionCleanupLoop()
		}()

		if s.options.Debug {
			s.logger.Printf("Connection management enabled (max connections: %d, idle timeout: %v)",
				tuning.MaxConnections, tuning.IdleTimeout)
		}
	}

	// Try to bind to the specified port
	addr := fmt.Sprintf("%s:%d", s.options.Hostname, s.options.Port)

	// Check if TLS is enabled
	var listener net.Listener
	var err error

	if policy.TLS != nil && policy.TLS.Enabled {
		// Build TLS configuration
		tlsConfig, err := policy.TLS.BuildConfig()
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
			TLSVersionString(policy.TLS.MinVersion),
			TLSVersionString(policy.TLS.MaxVersion),
			policy.TLS.GetClientAuthString())

		if policy.TLS.InsecureSkipVerify {
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

// StartWithPortmapper starts the NFS server with portmapper service.
// This is required for standard NFS clients that expect to query portmapper.
// Note: Portmapper requires root/administrator privileges to bind to port 111.
func (s *Server) StartWithPortmapper() error {
	if s.handler == nil {
		return fmt.Errorf("no handler set")
	}

	// Enable record marking for standard NFS clients
	s.options.UseRecordMarking = true

	// Start portmapper
	s.portmapper = NewPortmapper()
	s.portmapper.SetDebug(s.options.Debug)
	s.portmapper.SetListenAddr(s.options.Hostname)
	if err := s.portmapper.Start(); err != nil {
		return fmt.Errorf("failed to start portmapper: %w", err)
	}

	// Start the NFS server
	if err := s.Listen(); err != nil {
		s.portmapper.Stop()
		return err
	}

	// Register services with portmapper
	nfsPort := uint32(s.options.Port)
	if nfsPort == 0 {
		nfsPort = 2049
	}

	// Register NFS service
	s.portmapper.RegisterService(NFS_PROGRAM, NFS_V3, IPPROTO_TCP, nfsPort)

	// Register MOUNT service (same port in this implementation)
	// Register for both v1 and v3 since some clients (like showmount) use v1
	mountPort := uint32(s.options.MountPort)
	if mountPort == 0 {
		mountPort = nfsPort // Use NFS port for mount if not specified
	}
	s.portmapper.RegisterService(MOUNT_PROGRAM, 1, IPPROTO_TCP, mountPort)        // v1
	s.portmapper.RegisterService(MOUNT_PROGRAM, MOUNT_V3, IPPROTO_TCP, mountPort) // v3

	s.logger.Printf("NFS server started with portmapper (NFS port: %d, Mount port: %d)", nfsPort, mountPort)

	return nil
}

// GetPort returns the port the server is listening on
func (s *Server) GetPort() int {
	return s.options.Port
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
				if s.handler != nil {
					if slog := s.handler.getStructuredLogger(); slog != nil {
						tuning := s.handler.tuning.Load()
						if tuning.Log != nil && tuning.Log.LogClientIPs {
							slog.Warn("connection rejected: IP not allowed",
								LogField{Key: "client_ip", Value: clientIP})
						} else {
							slog.Warn("connection rejected: IP not allowed")
						}
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
				tuning := s.handler.tuning.Load()

				// Apply TCP keepalive setting
				if tuning.TCPKeepAlive {
					if err := tcpConn.SetKeepAlive(true); err != nil {
						s.logger.Printf("Warning: failed to set TCP keepalive: %v", err)
					}
					if err := tcpConn.SetKeepAlivePeriod(60 * time.Second); err != nil {
						s.logger.Printf("Warning: failed to set TCP keepalive period: %v", err)
					}
				}

				// Apply TCP no delay setting (disable Nagle's algorithm)
				if tuning.TCPNoDelay {
					if err := tcpConn.SetNoDelay(true); err != nil {
						s.logger.Printf("Warning: failed to set TCP no delay: %v", err)
					}
				}

				// Apply buffer sizes
				if tuning.SendBufferSize > 0 {
					if err := tcpConn.SetWriteBuffer(tuning.SendBufferSize); err != nil {
						s.logger.Printf("Warning: failed to set send buffer size: %v", err)
					}
				}

				if tuning.ReceiveBufferSize > 0 {
					if err := tcpConn.SetReadBuffer(tuning.ReceiveBufferSize); err != nil {
						s.logger.Printf("Warning: failed to set receive buffer size: %v", err)
					}
				}

				if s.options.Debug {
					s.logger.Printf("Configured TCP connection (keepalive: %v, nodelay: %v, sendbuf: %d, recvbuf: %d)",
						tuning.TCPKeepAlive,
						tuning.TCPNoDelay,
						tuning.SendBufferSize,
						tuning.ReceiveBufferSize)
				}
			}

			s.wg.Add(1)
			go func() {
				defer s.wg.Done()
				defer s.unregisterConnection(conn) // Ensure connection is unregistered when done
				defer func() {
					if r := recover(); r != nil {
						s.logger.Printf("recovered panic in connection handler: %v", r)
					}
				}()
				if s.options.UseRecordMarking {
					s.handleConnectionWithRecordMarking(conn, procHandler)
				} else {
					s.handleConnection(conn, procHandler)
				}
			}()
		}
	}
}

// connIO abstracts the read/write framing for a connection, allowing the
// shared connection loop to work with both raw TCP and record-marking modes.
type connIO interface {
	ReadCall() (*RPCCall, io.Reader, error)
	WriteReply(*RPCReply) error
}

// rawConnIO implements connIO for direct TCP connections (no record marking).
type rawConnIO struct {
	server *Server
	conn   net.Conn
}

func (r *rawConnIO) ReadCall() (*RPCCall, io.Reader, error) {
	call, err := DecodeRPCCall(r.conn)
	if err != nil {
		return nil, nil, err
	}
	return call, r.conn, nil
}

func (r *rawConnIO) WriteReply(reply *RPCReply) error {
	return EncodeRPCReply(r.conn, reply)
}

// recordMarkingConnIO implements connIO for RFC 1831 record-marking connections.
type recordMarkingConnIO struct {
	server *Server
	rmConn *RecordMarkingConn
}

func (rm *recordMarkingConnIO) ReadCall() (*RPCCall, io.Reader, error) {
	data, err := rm.rmConn.ReadRecord()
	if err != nil {
		return nil, nil, err
	}
	reader := bytes.NewReader(data)
	call, err := DecodeRPCCall(reader)
	if err != nil {
		return nil, nil, err
	}
	remaining := data[len(data)-reader.Len():]
	return call, bytes.NewReader(remaining), nil
}

func (rm *recordMarkingConnIO) WriteReply(reply *RPCReply) error {
	var buf bytes.Buffer
	if err := EncodeRPCReply(&buf, reply); err != nil {
		return err
	}
	return rm.rmConn.WriteRecord(buf.Bytes())
}

func (s *Server) handleConnection(conn net.Conn, procHandler *NFSProcedureHandler) {
	cio := &rawConnIO{server: s, conn: conn}
	s.handleConnectionLoop(conn, procHandler, cio, 5*time.Second, 5*time.Second)
}

// handleConnectionWithRecordMarking handles a connection with RFC 1831 record marking
func (s *Server) handleConnectionWithRecordMarking(conn net.Conn, procHandler *NFSProcedureHandler) {
	rmConn := NewRecordMarkingConn(conn, conn)
	cio := &recordMarkingConnIO{server: s, rmConn: rmConn}
	s.handleConnectionLoop(conn, procHandler, cio, 30*time.Second, 30*time.Second)
}

// handleConnectionLoop is the shared connection handling loop used by both
// raw TCP and record-marking modes. The connIO interface abstracts the
// read/write framing so the auth, rate limiting, worker dispatch, and
// connection lifecycle logic lives in one place.
func (s *Server) handleConnectionLoop(conn net.Conn, procHandler *NFSProcedureHandler, cio connIO, readTimeout, writeTimeout time.Duration) {
	defer conn.Close()

	connID := fmt.Sprintf("conn-%d", s.nextConnID.Add(1))

	var connRateLimiter *RateLimiter
	if s.handler != nil {
		connRateLimiter = s.handler.rateLimiter
	}
	defer func() {
		if connRateLimiter != nil {
			connRateLimiter.CleanupConnection(connID)
		}
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		default:
			if err := conn.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
				return
			}

			call, body, readErr := cio.ReadCall()
			if readErr != nil {
				if readErr != io.EOF && !isTimeoutError(readErr) && !isConnectionResetError(readErr) {
					if s.options.Debug {
						s.logger.Printf("read error: %v", readErr)
					}
				}
				return
			}

			if s.options.Debug {
				s.logger.Printf("Received RPC call: prog=%d vers=%d proc=%d",
					call.Header.Program, call.Header.Version, call.Header.Procedure)
			}

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
					addrStr := remoteAddr.String()
					host, port, err := net.SplitHostPort(addrStr)
					if err == nil {
						authCtx.ClientIP = host
						if p, err := net.LookupPort("tcp", port); err == nil {
							authCtx.ClientPort = p
						}
					}
				}
			}

			// Check rate limit
			if connRateLimiter != nil && s.handler != nil && s.handler.policy.Load().EnableRateLimiting {
				if !connRateLimiter.AllowRequest(authCtx.ClientIP, connID) {
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
					if slog := s.handler.getStructuredLogger(); slog != nil {
						tuning := s.handler.tuning.Load()
						if tuning.Log != nil && tuning.Log.LogClientIPs {
							slog.Warn("rate limit exceeded",
								LogField{Key: "client_ip", Value: authCtx.ClientIP})
						} else {
							slog.Warn("rate limit exceeded")
						}
					}
					if s.handler.metrics != nil {
						s.handler.metrics.RecordRateLimitExceeded()
					}
					if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err == nil {
						if writeErr := cio.WriteReply(reply); writeErr != nil {
							return
						}
					}
					continue
				}
			}

			// Dispatch to handler via worker pool or directly
			var reply *RPCReply
			var handleErr error

			if s.handler != nil && s.handler.workerPool != nil {
				result := s.handler.ExecuteWithWorker(func() interface{} {
					r, e := procHandler.HandleCall(call, body, authCtx)
					return struct {
						Reply *RPCReply
						Err   error
					}{r, e}
				})
				typedResult, ok := result.(struct {
					Reply *RPCReply
					Err   error
				})
				if !ok {
					s.logger.Printf("worker pool returned unexpected result type")
					return
				}
				reply, handleErr = typedResult.Reply, typedResult.Err
			} else {
				reply, handleErr = procHandler.HandleCall(call, body, authCtx)
			}

			if handleErr != nil {
				if s.options.Debug {
					s.logger.Printf("handle error: %v", handleErr)
				}
				return
			}

			if err := conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
				return
			}

			if writeErr := cio.WriteReply(reply); writeErr != nil {
				if !isTimeoutError(writeErr) && !isConnectionResetError(writeErr) {
					if s.options.Debug {
						s.logger.Printf("write error: %v", writeErr)
					}
				}
				return
			}

			s.updateConnectionActivity(conn)
		}
	}
}

// Stop stops the NFS server
func (s *Server) Stop() error {
	s.cancel() // Signal all goroutines to stop

	// Stop portmapper if running
	if s.portmapper != nil {
		s.portmapper.Stop()
	}

	// Close listener to stop accepting new connections
	if s.listener != nil {
		s.listener.Close()
	}

	// Close mount listener if separate
	if s.mountListener != nil {
		s.mountListener.Close()
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
