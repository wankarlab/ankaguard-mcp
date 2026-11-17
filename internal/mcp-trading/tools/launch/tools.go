// Package launch exposes read-only AnkaLaunch phase-1 MCP tools. They share
// their queries with the REST console (internal/launch/launchapi) and never
// mutate anything: no decisions, no kill switch, no orders.
package launch

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/rantipay/trading-mcp/internal/launch/launchapi"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
)

// base holds what every tool needs.
type base struct {
	pool         *pgxpool.Pool
	requirements launchapi.RequirementsFunc
}

func (b base) check() error {
	if b.pool == nil {
		return fmt.Errorf("launch database is not configured")
	}
	return nil
}

func (b base) req() launchapi.GateRequirements {
	if b.requirements == nil {
		return launchapi.DefaultRequirements()
	}
	return b.requirements()
}

func argInt(args map[string]interface{}, key string) int64 {
	switch v := args[key].(type) {
	case float64:
		return int64(v)
	case int:
		return int64(v)
	case int64:
		return v
	}
	return 0
}

func argString(args map[string]interface{}, key string) string {
	s, _ := args[key].(string)
	return s
}

// StatusTool implements launch_status.
type StatusTool struct{ base }

func (t *StatusTool) Name() string { return "launch_status" }
func (t *StatusTool) Description() string {
	return "AnkaLaunch status (read-only): service heartbeat, sources, circuit breaker, current edge-gate verdict, shadow exposure/PnL and 24h counters. Shadow mode only, no real money."
}
func (t *StatusTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *StatusTool) Execute(ctx context.Context, _ map[string]interface{}) (interface{}, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	return launchapi.GetStatus(ctx, t.pool, t.req(), time.Time{})
}

// ListOpportunitiesTool implements launch_list_opportunities.
type ListOpportunitiesTool struct{ base }

func (t *ListOpportunitiesTool) Name() string { return "launch_list_opportunities" }
func (t *ListOpportunitiesTool) Description() string {
	return "List AnkaLaunch opportunities (read-only), newest first, with score breakdown and decision. Filter by band and status."
}
func (t *ListOpportunitiesTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"band":   map[string]interface{}{"type": "string", "description": "Score band, e.g. MANUAL_APPROVAL"},
			"status": map[string]interface{}{"type": "string", "description": "Opportunity status, e.g. PENDING_APPROVAL"},
			"limit":  map[string]interface{}{"type": "integer", "description": "Max items (default 50, max 200)"},
		},
	}
}
func (t *ListOpportunitiesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	return launchapi.ListOpportunities(ctx, t.pool, launchapi.OpportunityFilter{
		Band: argString(args, "band"), Status: argString(args, "status"), Limit: int(argInt(args, "limit")),
	})
}

// GetPositionTool implements launch_get_position.
type GetPositionTool struct{ base }

func (t *GetPositionTool) Name() string { return "launch_get_position" }
func (t *GetPositionTool) Description() string {
	return "Get one AnkaLaunch shadow position with costs, PnL and orders (read-only)."
}
func (t *GetPositionTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":     "object",
		"required": []string{"id"},
		"properties": map[string]interface{}{
			"id": map[string]interface{}{"type": "integer", "description": "Position id"},
		},
	}
}
func (t *GetPositionTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	id := argInt(args, "id")
	if id <= 0 {
		return nil, invalidParams("id is required")
	}
	pos, err := launchapi.GetPosition(ctx, t.pool, id)
	if errors.Is(err, launchapi.ErrNotFound) {
		// A missing position is a caller error, not an internal failure.
		return nil, invalidParams(fmt.Sprintf("position %d not found", id))
	}
	return pos, err
}

// invalidParams is the JSON-RPC -32602 error the dispatcher passes through as is.
func invalidParams(msg string) error {
	return &models.MCPError{Code: -32602, Message: "Invalid params", Data: msg}
}

// GateVerdictTool implements launch_gate_verdict.
type GateVerdictTool struct{ base }

func (t *GateVerdictTool) Name() string { return "launch_gate_verdict" }
func (t *GateVerdictTool) Description() string {
	return "Current AnkaLaunch edge-gate verdict (INSUFFICIENT_DATA / NO_EDGE / EDGE) with its metrics, requirements and recent history (read-only)."
}
func (t *GateVerdictTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"limit": map[string]interface{}{"type": "integer", "description": "History length (default 20)"},
		},
	}
}
func (t *GateVerdictTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	if err := t.check(); err != nil {
		return nil, err
	}
	return launchapi.Gate(ctx, t.pool, t.req(), int(argInt(args, "limit")))
}

// RegisterAll registers the four read-only tools. The MCP endpoint is already
// behind the server's JWT auth. pool may be nil: the tools then fail with an
// explicit error instead of returning data.
func RegisterAll(srv *server.MCPServer, pool *pgxpool.Pool, requirements launchapi.RequirementsFunc) error {
	b := base{pool: pool, requirements: requirements}
	tools := []server.Tool{
		&StatusTool{b}, &ListOpportunitiesTool{b}, &GetPositionTool{b}, &GateVerdictTool{b},
	}
	for _, tool := range tools {
		if err := srv.RegisterTool(tool); err != nil {
			return err
		}
	}
	return nil
}
