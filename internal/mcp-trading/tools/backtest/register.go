package backtest

import (
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
)

// RegisterAll registers the backtesting tools. There is exactly one:
// backtest_strategy, which replays a strategy over real exchange candles and
// labels its output as an estimate with the assumptions used. Tools that had no
// real implementation (optimize_parameters, compare_strategies,
// get_backtest_metrics, walk_forward_analysis) are intentionally not registered.
func RegisterAll(srv *server.MCPServer, client *gctclient.Client) error {
	return srv.RegisterTool(NewBacktestStrategyTool(client))
}
