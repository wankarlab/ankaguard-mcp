package notifications

import (
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
)

// BaseTool provides common functionality for notification tools
type BaseTool struct {
	client *gctclient.Client
}