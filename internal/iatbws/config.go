package websocket

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Config contains WebSocket configuration.
type Config struct {
	// HeartbeatInterval is the interval between heartbeat messages.
	HeartbeatInterval time.Duration

	// ReadBufferSize is the size of the read buffer for WebSocket connections.
	ReadBufferSize int

	// WriteBufferSize is the size of the write buffer for WebSocket connections.
	WriteBufferSize int

	// MaxMessageSize is the maximum size of a message in bytes.
	MaxMessageSize int64

	// SendBufferSize is the size of the client send buffer.
	SendBufferSize int

	// RateLimitWindow is the time window for rate limiting.
	RateLimitWindow time.Duration

	// RateLimitMax is the maximum messages allowed per window.
	RateLimitMax int

	// AllowedOrigins is the list of allowed origins for CORS.
	AllowedOrigins []string

	// EnableCompression enables WebSocket compression.
	EnableCompression bool

	// WriteWait is the time allowed to write a message to the peer.
	WriteWait time.Duration

	// PongWait is the time allowed to read the next pong message from the peer.
	PongWait time.Duration

	// PingPeriod is the period to send pings to peer.
	PingPeriod time.Duration
}

// DefaultConfig returns the default WebSocket configuration.
func DefaultConfig() *Config {
	return &Config{
		HeartbeatInterval: 30 * time.Second,
		ReadBufferSize:    1024,
		WriteBufferSize:   1024,
		MaxMessageSize:    16384, // 16KB
		SendBufferSize:    256,
		RateLimitWindow:   time.Minute,
		RateLimitMax:      100,
		AllowedOrigins:    []string{"*"}, // Allow all in development
		EnableCompression: true,
		WriteWait:         10 * time.Second,
		PongWait:          60 * time.Second,
		PingPeriod:        54 * time.Second, // (PongWait * 9) / 10
	}
}

// LoadFromEnv loads configuration from environment variables.
// Environment variables override default values.
func LoadFromEnv() *Config {
	cfg := DefaultConfig()

	// Heartbeat interval
	if val := os.Getenv("WS_HEARTBEAT_INTERVAL"); val != "" {
		if duration, err := time.ParseDuration(val); err == nil {
			cfg.HeartbeatInterval = duration
		}
	}

	// Buffer sizes
	if val := os.Getenv("WS_READ_BUFFER_SIZE"); val != "" {
		if size, err := strconv.Atoi(val); err == nil && size > 0 {
			cfg.ReadBufferSize = size
		}
	}

	if val := os.Getenv("WS_WRITE_BUFFER_SIZE"); val != "" {
		if size, err := strconv.Atoi(val); err == nil && size > 0 {
			cfg.WriteBufferSize = size
		}
	}

	// Max message size
	if val := os.Getenv("WS_MAX_MESSAGE_SIZE"); val != "" {
		if size, err := strconv.ParseInt(val, 10, 64); err == nil && size > 0 {
			cfg.MaxMessageSize = size
		}
	}

	// Send buffer size
	if val := os.Getenv("WS_SEND_BUFFER_SIZE"); val != "" {
		if size, err := strconv.Atoi(val); err == nil && size > 0 {
			cfg.SendBufferSize = size
		}
	}

	// Rate limiting
	if val := os.Getenv("WS_RATE_LIMIT_WINDOW"); val != "" {
		if duration, err := time.ParseDuration(val); err == nil {
			cfg.RateLimitWindow = duration
		}
	}

	if val := os.Getenv("WS_RATE_LIMIT_MAX"); val != "" {
		if max, err := strconv.Atoi(val); err == nil && max > 0 {
			cfg.RateLimitMax = max
		}
	}

	// Allowed origins
	if val := os.Getenv("WS_ALLOWED_ORIGINS"); val != "" {
		origins := strings.Split(val, ",")
		for i, origin := range origins {
			origins[i] = strings.TrimSpace(origin)
		}
		cfg.AllowedOrigins = origins
	}

	// Compression
	if val := os.Getenv("WS_ENABLE_COMPRESSION"); val != "" {
		cfg.EnableCompression = val == "true" || val == "1"
	}

	// Timeouts
	if val := os.Getenv("WS_WRITE_WAIT"); val != "" {
		if duration, err := time.ParseDuration(val); err == nil {
			cfg.WriteWait = duration
		}
	}

	if val := os.Getenv("WS_PONG_WAIT"); val != "" {
		if duration, err := time.ParseDuration(val); err == nil {
			cfg.PongWait = duration
			// Recalculate ping period
			cfg.PingPeriod = (duration * 9) / 10
		}
	}

	return cfg
}

// Apply applies the configuration to the WebSocket handler.
func (c *Config) Apply() {
	// Set buffer sizes
	SetBufferSizes(c.ReadBufferSize, c.WriteBufferSize)

	// Set compression
	SetCompression(c.EnableCompression)

	// Set allowed origins
	if len(c.AllowedOrigins) > 0 && c.AllowedOrigins[0] != "*" {
		SetAllowedOrigins(c.AllowedOrigins)
	}
}

// Validate validates the configuration.
func (c *Config) Validate() error {
	if c.HeartbeatInterval <= 0 {
		c.HeartbeatInterval = 30 * time.Second
	}

	if c.ReadBufferSize <= 0 {
		c.ReadBufferSize = 1024
	}

	if c.WriteBufferSize <= 0 {
		c.WriteBufferSize = 1024
	}

	if c.MaxMessageSize <= 0 {
		c.MaxMessageSize = 16384
	}

	if c.SendBufferSize <= 0 {
		c.SendBufferSize = 256
	}

	if c.RateLimitWindow <= 0 {
		c.RateLimitWindow = time.Minute
	}

	if c.RateLimitMax <= 0 {
		c.RateLimitMax = 100
	}

	if c.WriteWait <= 0 {
		c.WriteWait = 10 * time.Second
	}

	if c.PongWait <= 0 {
		c.PongWait = 60 * time.Second
	}

	if c.PingPeriod <= 0 {
		c.PingPeriod = (c.PongWait * 9) / 10
	}

	return nil
}
