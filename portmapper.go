package absnfs

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"
)

// Portmapper constants (RFC 1833)
const (
	PortmapperPort    = 111
	PortmapperProgram = 100000
	PortmapperVersion = 2

	// Portmapper procedures
	PMAPPROC_NULL    = 0
	PMAPPROC_SET     = 1
	PMAPPROC_UNSET   = 2
	PMAPPROC_GETPORT = 3
	PMAPPROC_DUMP    = 4
	PMAPPROC_CALLIT  = 5

	// Transport protocols
	IPPROTO_TCP = 6
	IPPROTO_UDP = 17
)

// PortMapping represents a registered RPC service
type PortMapping struct {
	Program  uint32
	Version  uint32
	Protocol uint32 // IPPROTO_TCP or IPPROTO_UDP
	Port     uint32
}

// Portmapper implements the RFC 1833 portmapper service
type Portmapper struct {
	mu       sync.RWMutex
	mappings []PortMapping
	listener net.Listener
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	logger   *log.Logger
	debug    bool
}

// NewPortmapper creates a new portmapper instance
func NewPortmapper() *Portmapper {
	ctx, cancel := context.WithCancel(context.Background())
	return &Portmapper{
		mappings: make([]PortMapping, 0),
		ctx:      ctx,
		cancel:   cancel,
		logger:   log.New(os.Stderr, "[portmapper] ", log.LstdFlags),
	}
}

// SetDebug enables or disables debug logging
func (pm *Portmapper) SetDebug(debug bool) {
	pm.debug = debug
}

// RegisterService registers an RPC service with the portmapper
func (pm *Portmapper) RegisterService(prog, vers, prot, port uint32) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// Check if already registered
	for i, m := range pm.mappings {
		if m.Program == prog && m.Version == vers && m.Protocol == prot {
			// Update existing mapping
			pm.mappings[i].Port = port
			if pm.debug {
				pm.logger.Printf("Updated mapping: prog=%d vers=%d proto=%d port=%d",
					prog, vers, prot, port)
			}
			return
		}
	}

	// Add new mapping
	pm.mappings = append(pm.mappings, PortMapping{
		Program:  prog,
		Version:  vers,
		Protocol: prot,
		Port:     port,
	})

	if pm.debug {
		pm.logger.Printf("Registered mapping: prog=%d vers=%d proto=%d port=%d",
			prog, vers, prot, port)
	}
}

// UnregisterService unregisters an RPC service
func (pm *Portmapper) UnregisterService(prog, vers, prot uint32) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	for i, m := range pm.mappings {
		if m.Program == prog && m.Version == vers && m.Protocol == prot {
			// Remove mapping
			pm.mappings = append(pm.mappings[:i], pm.mappings[i+1:]...)
			if pm.debug {
				pm.logger.Printf("Unregistered mapping: prog=%d vers=%d proto=%d",
					prog, vers, prot)
			}
			return
		}
	}
}

// GetPort returns the port for a registered service (0 if not found)
func (pm *Portmapper) GetPort(prog, vers, prot uint32) uint32 {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	for _, m := range pm.mappings {
		if m.Program == prog && m.Version == vers && m.Protocol == prot {
			return m.Port
		}
	}
	return 0
}

// GetMappings returns a copy of all registered mappings
func (pm *Portmapper) GetMappings() []PortMapping {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	result := make([]PortMapping, len(pm.mappings))
	copy(result, pm.mappings)
	return result
}

// Start starts the portmapper service on port 111
func (pm *Portmapper) Start() error {
	return pm.StartOnPort(PortmapperPort)
}

// StartOnPort starts the portmapper service on a custom port.
// This is useful for testing without root privileges.
func (pm *Portmapper) StartOnPort(port int) error {
	addr := fmt.Sprintf(":%d", port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on port %d: %w", port, err)
	}
	pm.listener = listener

	// Register portmapper itself (v2 = portmap, v3/v4 = rpcbind)
	pm.RegisterService(PortmapperProgram, 2, IPPROTO_TCP, uint32(port))
	pm.RegisterService(PortmapperProgram, 2, IPPROTO_UDP, uint32(port))
	pm.RegisterService(PortmapperProgram, 3, IPPROTO_TCP, uint32(port))
	pm.RegisterService(PortmapperProgram, 3, IPPROTO_UDP, uint32(port))
	pm.RegisterService(PortmapperProgram, 4, IPPROTO_TCP, uint32(port))
	pm.RegisterService(PortmapperProgram, 4, IPPROTO_UDP, uint32(port))

	pm.logger.Printf("Portmapper started on port %d", port)

	pm.wg.Add(1)
	go pm.acceptLoop()

	return nil
}

// Stop stops the portmapper service
func (pm *Portmapper) Stop() error {
	pm.cancel()

	if pm.listener != nil {
		pm.listener.Close()
	}

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		pm.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("timeout waiting for portmapper shutdown")
	}
}

func (pm *Portmapper) acceptLoop() {
	defer pm.wg.Done()

	for {
		select {
		case <-pm.ctx.Done():
			return
		default:
			// Set accept deadline
			if tcpListener, ok := pm.listener.(*net.TCPListener); ok {
				tcpListener.SetDeadline(time.Now().Add(1 * time.Second))
			}

			conn, err := pm.listener.Accept()
			if err != nil {
				select {
				case <-pm.ctx.Done():
					return
				default:
					if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
						continue
					}
					pm.logger.Printf("Accept error: %v", err)
					continue
				}
			}

			pm.wg.Add(1)
			go func() {
				defer pm.wg.Done()
				pm.handleConnection(conn)
			}()
		}
	}
}

func (pm *Portmapper) handleConnection(conn net.Conn) {
	defer conn.Close()

	rmConn := NewRecordMarkingConn(conn, conn)

	for {
		select {
		case <-pm.ctx.Done():
			return
		default:
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))

			// Read complete record
			data, err := rmConn.ReadRecord()
			if err != nil {
				if err != io.EOF {
					if pm.debug {
						pm.logger.Printf("Read error: %v", err)
					}
				}
				return
			}

			// Process the RPC call
			reply, err := pm.handleCall(data)
			if err != nil {
				if pm.debug {
					pm.logger.Printf("Handle error: %v", err)
				}
				continue
			}

			// Write reply
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if err := rmConn.WriteRecord(reply); err != nil {
				if pm.debug {
					pm.logger.Printf("Write error: %v", err)
				}
				return
			}
		}
	}
}

func (pm *Portmapper) handleCall(data []byte) ([]byte, error) {
	r := bytes.NewReader(data)

	// Read RPC header
	var xid uint32
	if err := binary.Read(r, binary.BigEndian, &xid); err != nil {
		return nil, fmt.Errorf("failed to read XID: %w", err)
	}

	var msgType uint32
	if err := binary.Read(r, binary.BigEndian, &msgType); err != nil {
		return nil, fmt.Errorf("failed to read message type: %w", err)
	}
	if msgType != RPC_CALL {
		return nil, fmt.Errorf("expected RPC call, got %d", msgType)
	}

	var rpcVersion uint32
	if err := binary.Read(r, binary.BigEndian, &rpcVersion); err != nil {
		return nil, fmt.Errorf("failed to read RPC version: %w", err)
	}

	var program uint32
	if err := binary.Read(r, binary.BigEndian, &program); err != nil {
		return nil, fmt.Errorf("failed to read program: %w", err)
	}

	var version uint32
	if err := binary.Read(r, binary.BigEndian, &version); err != nil {
		return nil, fmt.Errorf("failed to read version: %w", err)
	}

	var procedure uint32
	if err := binary.Read(r, binary.BigEndian, &procedure); err != nil {
		return nil, fmt.Errorf("failed to read procedure: %w", err)
	}

	// Skip credentials and verifier
	if err := pm.skipAuth(r); err != nil {
		return nil, fmt.Errorf("failed to skip credentials: %w", err)
	}
	if err := pm.skipAuth(r); err != nil {
		return nil, fmt.Errorf("failed to skip verifier: %w", err)
	}

	// Always log calls for debugging
	pm.logger.Printf("Call: prog=%d vers=%d proc=%d", program, version, procedure)

	// Verify this is a portmapper call
	if program != PortmapperProgram {
		return pm.makeReply(xid, PROG_UNAVAIL, nil), nil
	}

	// Accept portmap v2 and rpcbind v3/v4
	// v2 = classic portmap, v3/v4 = rpcbind (RFC 1833)
	if version != 2 && version != 3 && version != 4 {
		return pm.makeReply(xid, PROG_MISMATCH, nil), nil
	}

	// Handle procedure
	// rpcbind v3/v4 procedures are different from portmap v2
	// v2: 0=NULL, 1=SET, 2=UNSET, 3=GETPORT, 4=DUMP
	// v3/v4: 0=NULL, 1=SET, 2=UNSET, 3=GETADDR, 4=DUMP, 5=CALLIT
	var result []byte
	if version == 2 {
		// Portmap v2 procedures
		switch procedure {
		case PMAPPROC_NULL:
			result = nil
		case PMAPPROC_SET:
			result = pm.handleSet(r)
		case PMAPPROC_UNSET:
			result = pm.handleUnset(r)
		case PMAPPROC_GETPORT:
			result = pm.handleGetPort(r)
		case PMAPPROC_DUMP:
			result = pm.handleDump()
		default:
			return pm.makeReply(xid, PROC_UNAVAIL, nil), nil
		}
	} else {
		// rpcbind v3/v4 procedures
		switch procedure {
		case 0: // RPCBPROC_NULL
			result = nil
		case 1: // RPCBPROC_SET - not implemented
			result = pm.handleRpcbSet(r)
		case 2: // RPCBPROC_UNSET - not implemented
			result = pm.handleRpcbUnset(r)
		case 3: // RPCBPROC_GETADDR
			result = pm.handleGetAddr(r)
		case 4: // RPCBPROC_DUMP
			result = pm.handleRpcbDump()
		default:
			return pm.makeReply(xid, PROC_UNAVAIL, nil), nil
		}
	}

	return pm.makeReply(xid, MSG_ACCEPTED, result), nil
}

func (pm *Portmapper) skipAuth(r io.Reader) error {
	var flavor uint32
	if err := binary.Read(r, binary.BigEndian, &flavor); err != nil {
		return err
	}

	var length uint32
	if err := binary.Read(r, binary.BigEndian, &length); err != nil {
		return err
	}

	if length > 0 {
		buf := make([]byte, length)
		if _, err := io.ReadFull(r, buf); err != nil {
			return err
		}
	}

	return nil
}

func (pm *Portmapper) handleGetPort(r io.Reader) []byte {
	var prog, vers, prot, port uint32
	binary.Read(r, binary.BigEndian, &prog)
	binary.Read(r, binary.BigEndian, &vers)
	binary.Read(r, binary.BigEndian, &prot)
	binary.Read(r, binary.BigEndian, &port) // ignored

	resultPort := pm.GetPort(prog, vers, prot)

	if pm.debug {
		pm.logger.Printf("GETPORT: prog=%d vers=%d proto=%d -> port=%d",
			prog, vers, prot, resultPort)
	}

	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, resultPort)
	return buf.Bytes()
}

func (pm *Portmapper) handleDump() []byte {
	mappings := pm.GetMappings()

	var buf bytes.Buffer
	for _, m := range mappings {
		// Write "more entries" flag (1 = true)
		binary.Write(&buf, binary.BigEndian, uint32(1))
		binary.Write(&buf, binary.BigEndian, m.Program)
		binary.Write(&buf, binary.BigEndian, m.Version)
		binary.Write(&buf, binary.BigEndian, m.Protocol)
		binary.Write(&buf, binary.BigEndian, m.Port)
	}
	// Write "no more entries" flag (0 = false)
	binary.Write(&buf, binary.BigEndian, uint32(0))

	return buf.Bytes()
}

func (pm *Portmapper) handleSet(r io.Reader) []byte {
	var prog, vers, prot, port uint32
	binary.Read(r, binary.BigEndian, &prog)
	binary.Read(r, binary.BigEndian, &vers)
	binary.Read(r, binary.BigEndian, &prot)
	binary.Read(r, binary.BigEndian, &port)

	pm.RegisterService(prog, vers, prot, port)

	// Return success (1 = true)
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	return buf.Bytes()
}

func (pm *Portmapper) handleUnset(r io.Reader) []byte {
	var prog, vers, prot, port uint32
	binary.Read(r, binary.BigEndian, &prog)
	binary.Read(r, binary.BigEndian, &vers)
	binary.Read(r, binary.BigEndian, &prot)
	binary.Read(r, binary.BigEndian, &port) // ignored

	pm.UnregisterService(prog, vers, prot)

	// Return success (1 = true)
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	return buf.Bytes()
}

// handleGetAddr handles rpcbind v3/v4 GETADDR procedure
// Returns universal address string like "0.0.0.0.8.1" for port 2049
func (pm *Portmapper) handleGetAddr(r io.Reader) []byte {
	// Read rpcb structure per RFC 1833:
	// r_prog: uint32
	// r_vers: uint32
	// r_netid: string (e.g., "tcp", "udp")
	// r_addr: string (universal address - ignored for lookup)
	// r_owner: string

	var prog, vers uint32
	binary.Read(r, binary.BigEndian, &prog)
	binary.Read(r, binary.BigEndian, &vers)

	// Read netid string
	netid, _ := xdrDecodeString(r)

	// Skip r_addr and r_owner
	xdrDecodeString(r) // r_addr
	xdrDecodeString(r) // r_owner

	// Determine protocol from netid
	var prot uint32
	if netid == "tcp" || netid == "tcp6" {
		prot = IPPROTO_TCP
	} else {
		prot = IPPROTO_UDP
	}

	port := pm.GetPort(prog, vers, prot)

	// Always log GETADDR for debugging
	pm.logger.Printf("GETADDR: prog=%d vers=%d netid=%s -> port=%d",
		prog, vers, netid, port)

	// Return universal address as XDR string
	// Format depends on netid:
	// - tcp/udp: "h1.h2.h3.h4.port_hi.port_lo" (IPv4)
	// - tcp6/udp6: "h1:h2:...:h8.port_hi.port_lo" (IPv6)
	var uaddr string
	if port > 0 {
		portHi := port / 256
		portLo := port % 256
		if netid == "tcp6" || netid == "udp6" {
			// IPv6 format: ::1.port_hi.port_lo (localhost)
			uaddr = fmt.Sprintf("::1.%d.%d", portHi, portLo)
		} else {
			// IPv4 format: 127.0.0.1.port_hi.port_lo (localhost)
			uaddr = fmt.Sprintf("127.0.0.1.%d.%d", portHi, portLo)
		}
	} else {
		uaddr = "" // Empty string means not found
	}

	var buf bytes.Buffer
	xdrEncodeString(&buf, uaddr)
	return buf.Bytes()
}

// handleRpcbSet handles rpcbind v3/v4 SET procedure
func (pm *Portmapper) handleRpcbSet(r io.Reader) []byte {
	// Read rpcb structure
	var prog, vers uint32
	binary.Read(r, binary.BigEndian, &prog)
	binary.Read(r, binary.BigEndian, &vers)

	netid, _ := xdrDecodeString(r)
	uaddr, _ := xdrDecodeString(r)
	xdrDecodeString(r) // r_owner - ignored

	// Parse universal address to get port
	// Format: "host.port_hi.port_lo"
	var port uint32
	var prot uint32 = IPPROTO_TCP
	if netid == "udp" || netid == "udp6" {
		prot = IPPROTO_UDP
	}

	// Parse port from uaddr
	if uaddr != "" {
		var a, b, c, d, hi, lo int
		if _, err := fmt.Sscanf(uaddr, "%d.%d.%d.%d.%d.%d", &a, &b, &c, &d, &hi, &lo); err == nil {
			port = uint32(hi*256 + lo)
		}
	}

	if port > 0 {
		pm.RegisterService(prog, vers, prot, port)
	}

	// Return success (1 = true)
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	return buf.Bytes()
}

// handleRpcbUnset handles rpcbind v3/v4 UNSET procedure
func (pm *Portmapper) handleRpcbUnset(r io.Reader) []byte {
	// Read rpcb structure
	var prog, vers uint32
	binary.Read(r, binary.BigEndian, &prog)
	binary.Read(r, binary.BigEndian, &vers)

	netid, _ := xdrDecodeString(r)
	xdrDecodeString(r) // r_addr - ignored
	xdrDecodeString(r) // r_owner - ignored

	var prot uint32 = IPPROTO_TCP
	if netid == "udp" || netid == "udp6" {
		prot = IPPROTO_UDP
	}

	pm.UnregisterService(prog, vers, prot)

	// Return success (1 = true)
	var buf bytes.Buffer
	binary.Write(&buf, binary.BigEndian, uint32(1))
	return buf.Bytes()
}

// handleRpcbDump handles rpcbind v3/v4 DUMP procedure
// Returns list of all registered services in rpcb format
func (pm *Portmapper) handleRpcbDump() []byte {
	mappings := pm.GetMappings()

	var buf bytes.Buffer
	for _, m := range mappings {
		// Write "more entries" flag (1 = true)
		binary.Write(&buf, binary.BigEndian, uint32(1))

		// Write rpcb structure
		binary.Write(&buf, binary.BigEndian, m.Program)
		binary.Write(&buf, binary.BigEndian, m.Version)

		// netid
		var netid string
		if m.Protocol == IPPROTO_TCP {
			netid = "tcp"
		} else {
			netid = "udp"
		}
		xdrEncodeString(&buf, netid)

		// uaddr - universal address format
		portHi := m.Port / 256
		portLo := m.Port % 256
		uaddr := fmt.Sprintf("0.0.0.0.%d.%d", portHi, portLo)
		xdrEncodeString(&buf, uaddr)

		// owner
		xdrEncodeString(&buf, "superuser")
	}
	// Write "no more entries" flag (0 = false)
	binary.Write(&buf, binary.BigEndian, uint32(0))

	return buf.Bytes()
}

func (pm *Portmapper) makeReply(xid uint32, status uint32, data []byte) []byte {
	var buf bytes.Buffer

	// XID
	binary.Write(&buf, binary.BigEndian, xid)
	// Message type (reply)
	binary.Write(&buf, binary.BigEndian, uint32(RPC_REPLY))
	// Reply status
	binary.Write(&buf, binary.BigEndian, uint32(MSG_ACCEPTED))
	// Verifier (null)
	binary.Write(&buf, binary.BigEndian, uint32(0)) // flavor
	binary.Write(&buf, binary.BigEndian, uint32(0)) // length

	// Accept status
	if status == MSG_ACCEPTED {
		binary.Write(&buf, binary.BigEndian, uint32(0)) // SUCCESS
		if data != nil {
			buf.Write(data)
		}
	} else {
		binary.Write(&buf, binary.BigEndian, status)
	}

	return buf.Bytes()
}
