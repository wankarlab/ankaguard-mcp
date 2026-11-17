package server

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/toolpolicy"
	"github.com/rantipay/trading-mcp/internal/paper"
)

// JSON-RPC application codes for paper broker errors (error.data.code carries
// the symbolic name).
const (
	// ErrCodeDataUnavailable is the JSON-RPC code for DATA_UNAVAILABLE.
	ErrCodeDataUnavailable = -32005
	// ErrCodePaperUnsupported is the JSON-RPC code for PAPER_UNSUPPORTED.
	ErrCodePaperUnsupported = -32008
	// ErrCodePaperRejected is the JSON-RPC code for the remaining paper
	// rejections (INSUFFICIENT_FUNDS, PAPER_ORDER_REJECTED, PAPER_NOT_FOUND,
	// PAPER_NO_HOLDER).
	ErrCodePaperRejected = -32009
	// ErrCodeRiskRejected is the JSON-RPC code of an order blocked by the risk
	// sentinel (real or paper); error.message and error.data.code carry
	// ORDER_CAP_EXCEEDED, ORDER_NOTIONAL_UNKNOWN or RISK_REJECTED.
	ErrCodeRiskRejected = -32010
)

// PaperBroker is what the server needs from the paper broker: the execution
// assumptions to attach to the responses it serves.
type PaperBroker interface {
	Assumptions() map[string]interface{}
}

// SetPaperBroker wires the paper broker. Once set, paperBrokerReady() is true
// and calls with a paper profile are served by it (the selector in
// gctclient.Client routes the order and account methods).
func (s *MCPServer) SetPaperBroker(b PaperBroker) {
	s.paperMu.Lock()
	s.paperBroker = b
	s.paperMu.Unlock()
}

func (s *MCPServer) paperBrokerInstance() PaperBroker {
	s.paperMu.RLock()
	defer s.paperMu.RUnlock()
	return s.paperBroker
}

// paperEnvelope applies the paper-profile rules that run after the permission
// check: tools with no paper equivalent are rejected, and the context carries
// the paper account holder so the order and account methods are served by the
// broker. Returns the context to run the tool with, or an error to return.
func (s *MCPServer) paperEnvelope(ctx context.Context, tool string, class toolpolicy.Class) (context.Context, *models.MCPError) {
	profile, _ := s.callerFromContext(ctx)
	if profile != toolpolicy.ProfilePaper {
		return ctx, nil
	}
	if reason, bad := toolpolicy.PaperUnsupportedReason(tool); bad {
		return ctx, paperRejection(ErrCodePaperUnsupported, paper.CodeUnsupported, tool, class, reason)
	}
	if !s.paperBrokerReady() && toolpolicy.ServedByPaper(tool, class) {
		return ctx, paperUnavailableError(tool, class)
	}
	// Every paper-profile call carries the holder, even market data reads: any
	// order or account method a tool reaches is then served by the broker (or
	// fails closed) and can never fall through to the real client.
	holder, ok := UserIDFromContext(ctx)
	if !ok {
		return ctx, paperRejection(ErrCodePaperRejected, paper.CodeNoHolder, tool, class,
			"the token has no subject, so there is no paper account to use; a shared paper account is never used")
	}
	return paper.WithHolder(ctx, holder), nil
}

func paperRejection(code int, name, tool string, class toolpolicy.Class, reason string) *models.MCPError {
	return &models.MCPError{
		Code:    code,
		Message: name,
		Data: map[string]interface{}{
			"code":    name,
			"tool":    tool,
			"class":   string(class),
			"profile": string(toolpolicy.ProfilePaper),
			"reason":  reason,
		},
	}
}

// paperErrorFrom converts an error returned by a tool into the JSON-RPC error
// of the paper broker when it is one (also when the tool wrapped it with %w).
func paperErrorFrom(err error) (*models.MCPError, bool) {
	code := paper.CodeOf(err)
	if code == "" {
		var pe *paper.Error
		if !errors.As(err, &pe) {
			return nil, false
		}
		code = pe.Code
	}
	rpc := ErrCodePaperRejected
	switch code {
	case paper.CodeDataUnavailable:
		rpc = ErrCodeDataUnavailable
	case paper.CodeUnsupported:
		rpc = ErrCodePaperUnsupported
	case PaperUnavailableCode:
		rpc = ErrCodePaperUnavailable
	}
	data := map[string]interface{}{"code": code, "message": err.Error()}
	var pe *paper.Error
	if errors.As(err, &pe) && code == paper.CodeInsufficientFunds {
		data["available"] = pe.Available
		data["required"] = pe.Required
	}
	return &models.MCPError{Code: rpc, Message: code, Data: data}, true
}

// markPaperResult marks a response served by the paper broker: it adds
// "paper": true and the execution assumptions at the top level. A result that
// is not a JSON object is wrapped as {"paper": true, "paper_assumptions": ...,
// "data": <result>}.
func (s *MCPServer) markPaperResult(result interface{}) interface{} {
	var assumptions interface{}
	if b := s.paperBrokerInstance(); b != nil {
		assumptions = b.Assumptions()
	}
	obj := map[string]interface{}{}
	raw, err := json.Marshal(result)
	if err == nil {
		if json.Unmarshal(raw, &obj) != nil || obj == nil {
			obj = map[string]interface{}{"data": json.RawMessage(raw)}
		}
	} else {
		obj["data"] = result
	}
	obj["paper"] = true
	if assumptions != nil {
		obj["paper_assumptions"] = assumptions
	}
	return obj
}
