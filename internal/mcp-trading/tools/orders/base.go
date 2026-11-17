package orders

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/tools/common"
	"github.com/sirupsen/logrus"
)

// Package-level logger for orders tools
var log *logrus.Logger

// InitLogger initializes the package-level logger for orders tools.
// If a nil logger is passed, a default logger with JSON formatting is created.
func InitLogger(logger *logrus.Logger) {
	if logger != nil {
		log = logger
	} else {
		log = logrus.New()
		log.SetFormatter(&logrus.JSONFormatter{
			TimestampFormat: time.RFC3339Nano,
		})
		log.SetLevel(logrus.InfoLevel)
	}
}

// getLogger returns the package logger, initializing with defaults if needed
func getLogger() *logrus.Logger {
	if log == nil {
		InitLogger(nil)
	}
	return log
}

// BaseTool contiene funcionalidad común para todos los tools de orders
type BaseTool struct {
	common.BaseTool
	client *gctclient.Client
}

// validateOrderParams valida parámetros básicos de una orden
func (b *BaseTool) validateOrderParams(params map[string]interface{}) (exchange, pair, side string, err error) {
	exchange, ok := params["exchange"].(string)
	if !ok || exchange == "" {
		return "", "", "", &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "exchange is required",
		}
	}

	pair, ok = params["pair"].(string)
	if !ok || pair == "" {
		return "", "", "", &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "pair is required",
		}
	}

	side, ok = params["side"].(string)
	if !ok || (side != "buy" && side != "sell") {
		return "", "", "", &models.MCPError{
			Code:    -32602,
			Message: "Invalid params",
			Data:    "side must be 'buy' or 'sell'",
		}
	}

	return exchange, pair, side, nil
}

// getFloatParam obtiene un parámetro float (wrapper for common function with required flag)
func getFloatParam(params map[string]interface{}, key string, required bool) (float64, error) {
	val := common.GetFloatParam(params, key, 0)
	if val == 0 && required {
		_, ok := params[key]
		if !ok {
			return 0, &models.MCPError{
				Code:    -32602,
				Message: "Invalid params",
				Data:    fmt.Sprintf("%s is required", key),
			}
		}
	}
	return val, nil
}

// getStringParam wrapper for common function
func getStringParam(params map[string]interface{}, key string, defaultValue string) string {
	return common.GetStringParam(params, key, defaultValue)
}

// getBoolParam obtiene un parámetro booleano de un map
func getBoolParam(params map[string]interface{}, key string, defaultValue bool) bool {
	if val, ok := params[key]; ok {
		if boolVal, ok := val.(bool); ok {
			return boolVal
		}
	}
	return defaultValue
}

// deterministicClientOrderID derives a stable clientOrderId from the order's
// intent (audit P0a — idempotent retries). On a network timeout the same order
// retried within the dedup window produces the SAME id, so the exchange dedups
// the duplicate instead of opening a second position; genuinely distinct orders
// differ. Mirrors the live AI bot's deterministicOrderID pattern.
//
// The id is hashed over exchange|pair|side|type|sizeTrunc|priceTrunc|minuteBucket.
// The minute bucket bounds the dedup window to ~1 minute, matching the order's
// intended placement window. Output: "mcp-<16-hex>" (20 chars, well within the
// KuCoin 40-char clientOid limit).
func deterministicClientOrderID(exchange, pair, side, orderType string, amount, price float64) string {
	minute := time.Now().UTC().Truncate(time.Minute).Unix()
	raw := fmt.Sprintf("%s|%s|%s|%s|%.8f|%.8f|%d", exchange, pair, side, orderType, amount, price, minute)
	sum := sha256.Sum256([]byte(raw))
	return "mcp-" + hex.EncodeToString(sum[:])[:16]
}

// resolveClientOrderID respects a caller-supplied non-empty ClientID and only
// generates a deterministic one when none was provided (audit P0a).
func resolveClientOrderID(supplied, exchange, pair, side, orderType string, amount, price float64) string {
	if supplied != "" {
		return supplied
	}
	return deterministicClientOrderID(exchange, pair, side, orderType, amount, price)
}

// validateMarketType wrapper for common function
func (b *BaseTool) validateMarketType(marketType string) error {
	return common.ValidateMarketType(marketType)
}

// convertSymbol wrapper for common function
func (b *BaseTool) convertSymbol(symbol, marketType, exchange string) (string, error) {
	b.BaseTool.Client = b.client
	return b.BaseTool.ConvertSymbol(symbol, marketType, exchange)
}

func executeWithContext(ctx context.Context, fn func(context.Context) (interface{}, error)) (interface{}, error) {
	return common.ExecuteWithContext(ctx, fn)
}

// validateLeverageForMarketType validates leverage based on market type
func (b *BaseTool) validateLeverageForMarketType(leverage float64, marketType string) error {
	if marketType == "spot" && leverage != 0 && leverage != 1 {
		return &models.MCPError{
			Code:    -32602,
			Message: "Invalid leverage",
			Data:    "Leverage not allowed for spot trading",
		}
	}

	if marketType == "futures" && leverage > 0 && (leverage < 1 || leverage > 100) {
		return &models.MCPError{
			Code:    -32602,
			Message: "Invalid leverage",
			Data:    "Leverage must be between 1 and 100 for futures",
		}
	}

	return nil
}
