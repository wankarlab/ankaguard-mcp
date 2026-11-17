package hft

import (
	"context"
	"fmt"
	"net"
	"syscall"
	"time"
)

// NetworkConfig holds network optimization configuration
type NetworkConfig struct {
	// TCP options
	NoDelay         bool          // Disable Nagle's algorithm
	KeepAlive       bool          // Enable TCP keep-alive
	KeepAlivePeriod time.Duration // Keep-alive period

	// Buffer sizes
	ReadBufferSize  int // Socket read buffer size
	WriteBufferSize int // Socket write buffer size

	// Timeouts
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration

	// Advanced options
	ReuseAddr bool // SO_REUSEADDR
	ReusePort bool // SO_REUSEPORT (Linux)
	Linger    int  // SO_LINGER (-1 to disable)
}

// DefaultNetworkConfig returns default network configuration
func DefaultNetworkConfig() NetworkConfig {
	return NetworkConfig{
		NoDelay:         true,
		KeepAlive:       true,
		KeepAlivePeriod: time.Second * 30,
		ReadBufferSize:  1024 * 1024, // 1MB
		WriteBufferSize: 1024 * 1024, // 1MB
		ConnectTimeout:  time.Second * 10,
		ReadTimeout:     time.Second * 30,
		WriteTimeout:    time.Second * 30,
		ReuseAddr:       true,
		ReusePort:       false,
		Linger:          -1,
	}
}

// LowLatencyNetworkConfig returns configuration optimized for low latency
func LowLatencyNetworkConfig() NetworkConfig {
	return NetworkConfig{
		NoDelay:         true, // Critical for low latency
		KeepAlive:       true,
		KeepAlivePeriod: time.Second * 15,
		ReadBufferSize:  512 * 1024, // 512KB (smaller for lower latency)
		WriteBufferSize: 512 * 1024, // 512KB
		ConnectTimeout:  time.Second * 5,
		ReadTimeout:     time.Second * 10,
		WriteTimeout:    time.Second * 10,
		ReuseAddr:       true,
		ReusePort:       true, // Enable for better concurrency
		Linger:          0,    // Immediate close
	}
}

// HighThroughputNetworkConfig returns configuration optimized for throughput
func HighThroughputNetworkConfig() NetworkConfig {
	return NetworkConfig{
		NoDelay:         false, // Allow Nagle for better throughput
		KeepAlive:       true,
		KeepAlivePeriod: time.Second * 60,
		ReadBufferSize:  4 * 1024 * 1024, // 4MB
		WriteBufferSize: 4 * 1024 * 1024, // 4MB
		ConnectTimeout:  time.Second * 30,
		ReadTimeout:     time.Second * 60,
		WriteTimeout:    time.Second * 60,
		ReuseAddr:       true,
		ReusePort:       true,
		Linger:          -1,
	}
}

// OptimizedDialer creates a dialer with optimized settings
type OptimizedDialer struct {
	config NetworkConfig
	dialer *net.Dialer
}

// NewOptimizedDialer creates a new optimized dialer
func NewOptimizedDialer(config NetworkConfig) *OptimizedDialer {
	return &OptimizedDialer{
		config: config,
		dialer: &net.Dialer{
			Timeout:   config.ConnectTimeout,
			KeepAlive: config.KeepAlivePeriod,
		},
	}
}

// Dial creates an optimized connection
func (d *OptimizedDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

// DialContext creates an optimized connection with context
func (d *OptimizedDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	// Dial connection
	conn, err := d.dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("failed to dial: %w", err)
	}

	// Apply optimizations
	if err := d.optimizeConnection(conn); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to optimize connection: %w", err)
	}

	return conn, nil
}

// optimizeConnection applies network optimizations to connection
func (d *OptimizedDialer) optimizeConnection(conn net.Conn) error {
	tcpConn, ok := conn.(*net.TCPConn)
	if !ok {
		return nil // Not a TCP connection, skip optimizations
	}

	// Set TCP_NODELAY
	if err := tcpConn.SetNoDelay(d.config.NoDelay); err != nil {
		return fmt.Errorf("failed to set TCP_NODELAY: %w", err)
	}

	// Set keep-alive
	if err := tcpConn.SetKeepAlive(d.config.KeepAlive); err != nil {
		return fmt.Errorf("failed to set keep-alive: %w", err)
	}

	if d.config.KeepAlive {
		if err := tcpConn.SetKeepAlivePeriod(d.config.KeepAlivePeriod); err != nil {
			return fmt.Errorf("failed to set keep-alive period: %w", err)
		}
	}

	// Set buffer sizes
	if err := tcpConn.SetReadBuffer(d.config.ReadBufferSize); err != nil {
		return fmt.Errorf("failed to set read buffer: %w", err)
	}

	if err := tcpConn.SetWriteBuffer(d.config.WriteBufferSize); err != nil {
		return fmt.Errorf("failed to set write buffer: %w", err)
	}

	// Set timeouts
	if d.config.ReadTimeout > 0 {
		if err := tcpConn.SetReadDeadline(time.Now().Add(d.config.ReadTimeout)); err != nil {
			return fmt.Errorf("failed to set read deadline: %w", err)
		}
	}

	if d.config.WriteTimeout > 0 {
		if err := tcpConn.SetWriteDeadline(time.Now().Add(d.config.WriteTimeout)); err != nil {
			return fmt.Errorf("failed to set write deadline: %w", err)
		}
	}

	// Apply advanced socket options
	if err := d.applyAdvancedOptions(tcpConn); err != nil {
		// Log warning but don't fail - these are optional optimizations
		// In production, you might want to log this
	}

	return nil
}

// applyAdvancedOptions applies advanced socket options
func (d *OptimizedDialer) applyAdvancedOptions(tcpConn *net.TCPConn) error {
	// Get raw file descriptor
	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		return fmt.Errorf("failed to get syscall conn: %w", err)
	}

	var sockOptErr error
	err = rawConn.Control(func(fd uintptr) {
		// Set SO_REUSEADDR
		if d.config.ReuseAddr {
			if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
				sockOptErr = fmt.Errorf("failed to set SO_REUSEADDR: %w", err)
				return
			}
		}

		// Set SO_REUSEPORT (Linux only)
		if d.config.ReusePort {
			// SO_REUSEPORT = 15 on Linux
			if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, 0x0F, 1); err != nil {
				// Ignore error - might not be supported on this platform
			}
		}

		// Set SO_LINGER
		if d.config.Linger >= 0 {
			linger := syscall.Linger{
				Onoff:  1,
				Linger: int32(d.config.Linger),
			}
			if err := syscall.SetsockoptLinger(int(fd), syscall.SOL_SOCKET, syscall.SO_LINGER, &linger); err != nil {
				sockOptErr = fmt.Errorf("failed to set SO_LINGER: %w", err)
				return
			}
		}
	})

	if err != nil {
		return err
	}

	return sockOptErr
}

// ConnectionPool manages a pool of reusable connections
type ConnectionPool struct {
	address string
	dialer  *OptimizedDialer
	pool    chan net.Conn
	maxSize int

	// Metrics
	created uint64
	reused  uint64
	closed  uint64
}

// NewConnectionPool creates a new connection pool
func NewConnectionPool(address string, dialer *OptimizedDialer, maxSize int) *ConnectionPool {
	return &ConnectionPool{
		address: address,
		dialer:  dialer,
		pool:    make(chan net.Conn, maxSize),
		maxSize: maxSize,
	}
}

// Get gets a connection from the pool or creates a new one
func (p *ConnectionPool) Get(ctx context.Context) (net.Conn, error) {
	select {
	case conn := <-p.pool:
		// Test connection is still alive
		if p.isAlive(conn) {
			p.reused++
			return conn, nil
		}
		// Connection dead, close and create new one
		conn.Close()
		p.closed++

	default:
		// Pool empty, create new connection
	}

	// Create new connection
	conn, err := p.dialer.DialContext(ctx, "tcp", p.address)
	if err != nil {
		return nil, err
	}

	p.created++
	return conn, nil
}

// Put returns a connection to the pool
func (p *ConnectionPool) Put(conn net.Conn) {
	if conn == nil {
		return
	}

	select {
	case p.pool <- conn:
		// Connection returned to pool
	default:
		// Pool full, close connection
		conn.Close()
		p.closed++
	}
}

// Close closes all connections in the pool
func (p *ConnectionPool) Close() {
	close(p.pool)
	for conn := range p.pool {
		conn.Close()
	}
}

// isAlive checks if connection is still alive
func (p *ConnectionPool) isAlive(conn net.Conn) bool {
	// Try to set a deadline
	if err := conn.SetReadDeadline(time.Now().Add(time.Millisecond)); err != nil {
		return false
	}

	// Try to read one byte
	one := make([]byte, 1)
	_, err := conn.Read(one)

	// Reset deadline
	conn.SetReadDeadline(time.Time{})

	// If we got EOF or timeout, connection might be closed
	if err != nil {
		return false
	}

	return true
}

// Stats returns connection pool statistics
func (p *ConnectionPool) Stats() ConnectionPoolStats {
	return ConnectionPoolStats{
		Address:  p.address,
		PoolSize: len(p.pool),
		MaxSize:  p.maxSize,
		Created:  p.created,
		Reused:   p.reused,
		Closed:   p.closed,
	}
}

// ConnectionPoolStats holds connection pool statistics
type ConnectionPoolStats struct {
	Address  string
	PoolSize int
	MaxSize  int
	Created  uint64
	Reused   uint64
	Closed   uint64
}
