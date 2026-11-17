package server

import (
	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/sirupsen/logrus"
)

// GCTClientInterface defines the interface for GCT client used by the server
type GCTClientInterface interface {
	IsConnected() bool
}

// TestGCTClient is a mock GCT client for testing purposes
type TestGCTClient struct {
	Connected bool
}

// IsConnected returns the connection status
func (t *TestGCTClient) IsConnected() bool {
	return t.Connected
}

// testGCTClientWrapper wraps a GCTClientInterface to satisfy gctclient.Client interface
type testGCTClientWrapper struct {
	mockClient GCTClientInterface
}

// mockGCTClient is used internally to create a minimal gctclient.Client for testing
type mockGCTClient struct {
	connected bool
}

func (m *mockGCTClient) IsConnected() bool {
	return m.connected
}

// NewMCPServerForTesting creates an MCP server suitable for testing
// It uses a mock GCT client to avoid Prometheus metrics registration issues
func NewMCPServerForTesting(cfg *config.Config, log *logrus.Logger, mockClient GCTClientInterface) *MCPServer {
	// Inicializar sandbox mode (driven by exchanges[].sandbox, see CORE-02)
	sandboxMode := NewSandboxMode(cfg, log)

	if sandboxMode.IsEnabled() {
		log.Warn("Test Server: SANDBOX MODE ENABLED")
	}

	// Create a minimal gctclient.Client wrapper for testing
	// We use a nil gctClient and handle nil checks in the server methods
	return &MCPServer{
		config:         cfg,
		log:            log,
		gctClient:      createMinimalGCTClient(cfg, log),
		registry:       NewToolRegistry(),
		sandboxMode:    sandboxMode,
		testMockClient: mockClient, // Store mock client for test use
	}
}

// createMinimalGCTClient creates a minimal GCT client that won't register Prometheus metrics
// This is used for testing only
func createMinimalGCTClient(cfg *config.Config, log *logrus.Logger) *gctclient.Client {
	// Return nil - the server will need to handle nil checks
	// We'll add a field to track mock connection status instead
	return nil
}

// TestMockClient is exposed for testing access
func (s *MCPServer) TestMockClient() GCTClientInterface {
	return s.testMockClient
}
