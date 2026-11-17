package server

import (
	"context"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/sirupsen/logrus"
)

// SandboxMode reports the trading mode for MCP order tools.
//
// CORE-02: this used to inject `_sandbox_mode`/`_testnet` flags into tool
// params and result maps, but NOTHING downstream (internal/mcp-trading/tools,
// internal/mcp-trading/gctclient) ever read those flags to actually switch
// exchange sessions/credentials — they were purely cosmetic. The real switch
// that decides whether an order touches a testnet or a live exchange is
// `exchanges[].sandbox` in config, which `Server.Mode`/`enabled` here did NOT
// control (see tradingModeInfo in server.go, which already reports the real
// signal for the health endpoint). Rather than pretend a per-call flag makes
// execution safe, this type now only reports the ACTUAL exchange-config
// reality: `enabled` (and any "SANDBOX"/testnet wording returned to callers)
// reflects whether every enabled exchange is configured with sandbox=true,
// not the cosmetic server.mode setting.
type SandboxMode struct {
	cfg *config.Config
	log *logrus.Logger
}

// NewSandboxMode crea un nuevo sandbox mode reporter, driven by the real
// exchanges[].sandbox configuration rather than the cosmetic server.mode flag.
func NewSandboxMode(cfg *config.Config, log *logrus.Logger) *SandboxMode {
	return &SandboxMode{
		cfg: cfg,
		log: log,
	}
}

// engineRealMoney returns true if ANY enabled exchange is configured with
// sandbox=false — i.e. orders through it touch a real exchange with real
// money, regardless of MCP server.mode. Mirrors tradingModeInfo's logic so
// both surfaces (health endpoint and per-tool warnings) agree.
func (s *SandboxMode) engineRealMoney() bool {
	for _, ex := range s.cfg.GetEnabledExchanges() {
		if !ex.Sandbox {
			return true
		}
	}
	return false
}

// IsEnabled retorna true solo si TODOS los exchanges habilitados están en
// modo sandbox real (exchanges[].sandbox=true) — no el server.mode cosmético.
func (s *SandboxMode) IsEnabled() bool {
	return !s.engineRealMoney()
}

// ValidateOrderTool logs order-tool invocations for visibility. It no longer
// injects `_sandbox_mode`/`_testnet` into params — nothing consumed them, and
// leaving them there falsely implied the call path was safe.
func (s *SandboxMode) ValidateOrderTool(toolName string, params map[string]interface{}) error {
	if !isOrderTool(toolName) {
		return nil
	}
	s.log.WithFields(logrus.Fields{
		"tool":              toolName,
		"engine_real_money": s.engineRealMoney(),
	}).Info("Order tool called")
	return nil
}

// WrapResult envuelve el resultado de un order tool con el estado REAL de
// riesgo (exchanges[].sandbox), no el flag cosmético de server.mode.
func (s *SandboxMode) WrapResult(toolName string, result interface{}) interface{} {
	if !isOrderTool(toolName) {
		return result
	}

	resultMap, ok := result.(map[string]interface{})
	if !ok {
		return result
	}

	realMoney := s.engineRealMoney()
	resultMap["_sandbox_mode"] = !realMoney
	if realMoney {
		resultMap["_warning"] = "This order was executed against a REAL exchange (exchanges[].sandbox=false). Real money at risk."
	} else {
		resultMap["_warning"] = "This order was executed in SANDBOX mode (all enabled exchanges have sandbox=true)."
	}
	return resultMap
}

// LogSandboxWarning registra una advertencia cuando el motor opera con dinero real.
func (s *SandboxMode) LogSandboxWarning(message string) {
	if s.engineRealMoney() {
		s.log.WithField("mode", "production").Warn(message)
	}
}

// GetSandboxInfo retorna información REAL del modo de trading, basada en
// exchanges[].sandbox (no en server.mode).
func (s *SandboxMode) GetSandboxInfo() map[string]interface{} {
	realMoney := s.engineRealMoney()
	return map[string]interface{}{
		"enabled": !realMoney,
		"message": func() string {
			if realMoney {
				return "PRODUCTION MODE: Orders will be executed with real money. Use with caution."
			}
			return "SANDBOX MODE: All enabled exchanges are testnet. No real money at risk."
		}(),
		"testnet_only": !realMoney,
	}
}

// InterceptOrder intercepta una orden para validación en sandbox
func (s *SandboxMode) InterceptOrder(ctx context.Context, req *models.OrderRequest) (*models.OrderRequest, error) {
	if s.engineRealMoney() {
		return req, nil
	}

	// En sandbox, agregar marcador
	s.log.WithFields(logrus.Fields{
		"exchange": req.Exchange,
		"pair":     req.Pair,
		"side":     req.Side,
		"amount":   req.Amount,
		"type":     req.Type,
	}).Info("Intercepting order in sandbox mode")

	// Validar que no sea una cantidad muy grande (protección adicional)
	maxAmount := 1000.0 // Máximo $1000 USD en sandbox
	if req.Amount*req.Price > maxAmount {
		return nil, fmt.Errorf("order amount exceeds sandbox limit of $%.2f USD", maxAmount)
	}

	return req, nil
}

// ShouldBlockTool determina si un tool debe ser bloqueado en sandbox
func (s *SandboxMode) ShouldBlockTool(toolName string) (bool, string) {
	if !s.engineRealMoney() {
		return false, ""
	}

	// En sandbox, no bloqueamos ningún tool, pero advertimos
	// Podrías agregar lógica aquí para bloquear ciertos tools peligrosos

	return false, ""
}
