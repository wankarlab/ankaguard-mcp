package risk

import (
	"testing"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/portfolio"
	"github.com/stretchr/testify/assert"
)

// Both tools are registered on the same MCP server at startup; a shared name
// makes RegisterTool fail and the server exits with "Tool already registered".
func TestGetPositionsTool_NameDoesNotCollideWithPortfolio(t *testing.T) {
	client := &gctclient.Client{}
	assert.NotEqual(t, portfolio.NewGetPositionsTool(client).Name(), NewGetPositionsTool(client, nil).Name())
}
