package pairs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	"github.com/rantipay/trading-mcp/internal/pairs"
	"github.com/sirupsen/logrus"
)

// ConvertPairTool converts trading pairs between different exchange formats
type ConvertPairTool struct {
	converter *pairs.PairConverter
	logger    *logrus.Logger
}

// ConvertPairRequest represents the request for pair conversion
type ConvertPairRequest struct {
	Pair       string               `json:"pair"`        // Input pair (e.g., "DOGEUSDTM", "BTC-USDT", "BTCUSDT")
	FromFormat pairs.ExchangeFormat `json:"from_format"` // Source format (e.g., "kucoin_futures", "binance")
	ToFormat   pairs.ExchangeFormat `json:"to_format"`   // Target format (e.g., "standard", "kucoin_spot")
	MarketType pairs.MarketType     `json:"market_type"` // Market type (e.g., "spot", "futures")
}

// ConvertPairResponse represents the response for pair conversion
type ConvertPairResponse struct {
	OriginalPair  string               `json:"original_pair"`  // Input pair
	ConvertedPair string               `json:"converted_pair"` // Converted pair
	FromFormat    pairs.ExchangeFormat `json:"from_format"`    // Source format
	ToFormat      pairs.ExchangeFormat `json:"to_format"`      // Target format
	MarketType    pairs.MarketType     `json:"market_type"`    // Market type
	Standard      string               `json:"standard"`       // Standard format (BASE-QUOTE)
	Base          string               `json:"base"`           // Base currency
	Quote         string               `json:"quote"`          // Quote currency
}

// NewConvertPairTool creates a new pair conversion tool
func NewConvertPairTool(logger *logrus.Logger) *ConvertPairTool {
	return &ConvertPairTool{
		converter: pairs.NewPairConverter(),
		logger:    logger,
	}
}

// Name returns the tool name
func (t *ConvertPairTool) Name() string {
	return "convert_trading_pair"
}

// Description returns the tool description
func (t *ConvertPairTool) Description() string {
	return "Convert trading pairs between different exchange formats (e.g., DOGEUSDTM ↔ DOGE-USDT ↔ DOGEUSDT). Supports KuCoin (spot/futures), Binance, Bybit, OKX formats."
}

// InputSchema returns the JSON schema for the tool input
func (t *ConvertPairTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pair": map[string]interface{}{
				"type":        "string",
				"description": "Trading pair to convert (e.g., 'DOGEUSDTM', 'BTC-USDT', 'BTCUSDT')",
				"examples":    []string{"DOGEUSDTM", "BTC-USDT", "BTCUSDT", "DOGE-USDT", "ETHUSDTM"},
			},
			"from_format": map[string]interface{}{
				"type":        "string",
				"description": "Source exchange format",
				"enum":        []string{"standard", "kucoin_spot", "kucoin_futures", "binance", "bybit", "okx"},
				"examples":    []string{"kucoin_futures", "binance", "standard"},
			},
			"to_format": map[string]interface{}{
				"type":        "string",
				"description": "Target exchange format",
				"enum":        []string{"standard", "kucoin_spot", "kucoin_futures", "binance", "bybit", "okx"},
				"examples":    []string{"standard", "kucoin_spot", "binance"},
			},
			"market_type": map[string]interface{}{
				"type":        "string",
				"description": "Market type (affects conversion for some exchanges)",
				"enum":        []string{"spot", "futures", "margin", "options", "swap"},
				"default":     "spot",
				"examples":    []string{"spot", "futures"},
			},
		},
		"required": []string{"pair", "from_format", "to_format"},
	}
}

// Execute performs the pair conversion
func (t *ConvertPairTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	t.logger.WithField("args", args).Debug("Executing convert_trading_pair tool")

	// Parse arguments
	var req ConvertPairRequest
	if err := t.parseArgs(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	// Set default market type if not provided
	if req.MarketType == "" {
		req.MarketType = pairs.MarketSpot
	}

	// Validate input pair format
	if req.Pair == "" {
		return nil, fmt.Errorf("pair cannot be empty")
	}

	// Convert the pair
	convertedPair, err := t.converter.ConvertPair(req.Pair, req.FromFormat, req.ToFormat, req.MarketType)
	if err != nil {
		return nil, fmt.Errorf("failed to convert pair: %w", err)
	}

	// Get standard format for additional info
	standardPair, err := t.converter.NormalizeToStandard(req.Pair, req.FromFormat, req.MarketType)
	if err != nil {
		return nil, fmt.Errorf("failed to normalize pair: %w", err)
	}

	// Parse base and quote
	base, quote, err := t.converter.ParsePair(standardPair)
	if err != nil {
		return nil, fmt.Errorf("failed to parse pair: %w", err)
	}

	// Create response
	response := ConvertPairResponse{
		OriginalPair:  req.Pair,
		ConvertedPair: convertedPair,
		FromFormat:    req.FromFormat,
		ToFormat:      req.ToFormat,
		MarketType:    req.MarketType,
		Standard:      standardPair,
		Base:          base,
		Quote:         quote,
	}

	t.logger.WithFields(logrus.Fields{
		"original":  req.Pair,
		"converted": convertedPair,
		"standard":  standardPair,
		"base":      base,
		"quote":     quote,
	}).Info("Successfully converted trading pair")

	return response, nil
}

// parseArgs parses the tool arguments
func (t *ConvertPairTool) parseArgs(args map[string]interface{}, req *ConvertPairRequest) error {
	// Convert to JSON and back to ensure proper type conversion
	jsonData, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("failed to marshal arguments: %w", err)
	}

	if err := json.Unmarshal(jsonData, req); err != nil {
		return fmt.Errorf("failed to unmarshal arguments: %w", err)
	}

	return nil
}

// BatchConvertPairTool converts multiple trading pairs at once
type BatchConvertPairTool struct {
	converter *pairs.PairConverter
	logger    *logrus.Logger
}

// BatchConvertPairRequest represents the request for batch pair conversion
type BatchConvertPairRequest struct {
	Pairs      []string             `json:"pairs"`       // Input pairs
	FromFormat pairs.ExchangeFormat `json:"from_format"` // Source format
	ToFormat   pairs.ExchangeFormat `json:"to_format"`   // Target format
	MarketType pairs.MarketType     `json:"market_type"` // Market type
}

// BatchConvertPairResponse represents the response for batch pair conversion
type BatchConvertPairResponse struct {
	Conversions map[string]string    `json:"conversions"` // Original -> Converted mapping
	FromFormat  pairs.ExchangeFormat `json:"from_format"` // Source format
	ToFormat    pairs.ExchangeFormat `json:"to_format"`   // Target format
	MarketType  pairs.MarketType     `json:"market_type"` // Market type
	Total       int                  `json:"total"`       // Total pairs converted
	Successful  int                  `json:"successful"`  // Successfully converted
	Failed      []string             `json:"failed"`      // Failed conversions
}

// NewBatchConvertPairTool creates a new batch pair conversion tool
func NewBatchConvertPairTool(logger *logrus.Logger) *BatchConvertPairTool {
	return &BatchConvertPairTool{
		converter: pairs.NewPairConverter(),
		logger:    logger,
	}
}

// Name returns the tool name
func (t *BatchConvertPairTool) Name() string {
	return "batch_convert_trading_pairs"
}

// Description returns the tool description
func (t *BatchConvertPairTool) Description() string {
	return "Convert multiple trading pairs between different exchange formats in a single operation. Efficient for bulk conversions."
}

// InputSchema returns the JSON schema for the tool input
func (t *BatchConvertPairTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pairs": map[string]interface{}{
				"type":        "array",
				"description": "Array of trading pairs to convert",
				"items": map[string]interface{}{
					"type": "string",
				},
				"examples": [][]string{
					{"BTCUSDTM", "DOGEUSDTM", "ETHUSDTM"},
					{"BTC-USDT", "DOGE-USDT", "ETH-USDT"},
					{"BTCUSDT", "DOGEUSDT", "ETHUSDT"},
				},
			},
			"from_format": map[string]interface{}{
				"type":        "string",
				"description": "Source exchange format",
				"enum":        []string{"standard", "kucoin_spot", "kucoin_futures", "binance", "bybit", "okx"},
			},
			"to_format": map[string]interface{}{
				"type":        "string",
				"description": "Target exchange format",
				"enum":        []string{"standard", "kucoin_spot", "kucoin_futures", "binance", "bybit", "okx"},
			},
			"market_type": map[string]interface{}{
				"type":        "string",
				"description": "Market type",
				"enum":        []string{"spot", "futures", "margin", "options", "swap"},
				"default":     "spot",
			},
		},
		"required": []string{"pairs", "from_format", "to_format"},
	}
}

// Execute performs the batch pair conversion
func (t *BatchConvertPairTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	t.logger.WithField("args", args).Debug("Executing batch_convert_trading_pairs tool")

	// Parse arguments
	var req BatchConvertPairRequest
	if err := t.parseArgs(args, &req); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}

	// Set default market type if not provided
	if req.MarketType == "" {
		req.MarketType = pairs.MarketSpot
	}

	// Validate input
	if len(req.Pairs) == 0 {
		return nil, fmt.Errorf("pairs array cannot be empty")
	}

	// Convert pairs
	conversions := make(map[string]string)
	failed := []string{}
	successful := 0

	for _, pair := range req.Pairs {
		convertedPair, err := t.converter.ConvertPair(pair, req.FromFormat, req.ToFormat, req.MarketType)
		if err != nil {
			t.logger.WithError(err).Warnf("Failed to convert pair: %s", pair)
			failed = append(failed, pair)
			continue
		}

		conversions[pair] = convertedPair
		successful++
	}

	// Create response
	response := BatchConvertPairResponse{
		Conversions: conversions,
		FromFormat:  req.FromFormat,
		ToFormat:    req.ToFormat,
		MarketType:  req.MarketType,
		Total:       len(req.Pairs),
		Successful:  successful,
		Failed:      failed,
	}

	t.logger.WithFields(logrus.Fields{
		"total":      len(req.Pairs),
		"successful": successful,
		"failed":     len(failed),
	}).Info("Completed batch pair conversion")

	return response, nil
}

// parseArgs parses the tool arguments
func (t *BatchConvertPairTool) parseArgs(args map[string]interface{}, req *BatchConvertPairRequest) error {
	// Convert to JSON and back to ensure proper type conversion
	jsonData, err := json.Marshal(args)
	if err != nil {
		return fmt.Errorf("failed to marshal arguments: %w", err)
	}

	if err := json.Unmarshal(jsonData, req); err != nil {
		return fmt.Errorf("failed to unmarshal arguments: %w", err)
	}

	return nil
}

// GetSupportedFormatsTool returns supported exchange formats and market types
type GetSupportedFormatsTool struct {
	converter *pairs.PairConverter
	logger    *logrus.Logger
}

// SupportedFormatsResponse represents the response for supported formats
type SupportedFormatsResponse struct {
	ExchangeFormats []pairs.ExchangeFormat                     `json:"exchange_formats"` // Supported exchange formats
	MarketTypes     []pairs.MarketType                         `json:"market_types"`     // Supported market types
	Examples        map[string]map[pairs.ExchangeFormat]string `json:"examples"`         // Example conversions
	Description     map[string]string                          `json:"description"`      // Format descriptions
}

// NewGetSupportedFormatsTool creates a new supported formats tool
func NewGetSupportedFormatsTool(logger *logrus.Logger) *GetSupportedFormatsTool {
	return &GetSupportedFormatsTool{
		converter: pairs.NewPairConverter(),
		logger:    logger,
	}
}

// Name returns the tool name
func (t *GetSupportedFormatsTool) Name() string {
	return "get_supported_pair_formats"
}

// Description returns the tool description
func (t *GetSupportedFormatsTool) Description() string {
	return "Get all supported trading pair formats and market types with examples. Useful for understanding available conversion options."
}

// InputSchema returns the JSON schema for the tool input
func (t *GetSupportedFormatsTool) InputSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
		"required":   []string{},
	}
}

// Execute returns supported formats and examples
func (t *GetSupportedFormatsTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	t.logger.Debug("Executing get_supported_pair_formats tool")

	// Get supported formats
	exchangeFormats := t.converter.GetSupportedFormats()
	marketTypes := t.converter.GetSupportedMarketTypes()
	examples := t.converter.GetExamplePairs()

	// Create format descriptions
	descriptions := map[string]string{
		"standard":       "Standard MATP format: BASE-QUOTE (e.g., BTC-USDT)",
		"kucoin_spot":    "KuCoin spot format: BASE-QUOTE (e.g., BTC-USDT)",
		"kucoin_futures": "KuCoin futures format: BASEQUOTEM (e.g., BTCUSDTM)",
		"binance":        "Binance format: BASEQUOTE (e.g., BTCUSDT)",
		"bybit":          "Bybit format: BASEQUOTE (e.g., BTCUSDT)",
		"okx":            "OKX format: BASE-QUOTE or BASE-QUOTE-SWAP (e.g., BTC-USDT, BTC-USDT-SWAP)",
	}

	// Create response
	response := SupportedFormatsResponse{
		ExchangeFormats: exchangeFormats,
		MarketTypes:     marketTypes,
		Examples:        examples,
		Description:     descriptions,
	}

	t.logger.WithFields(logrus.Fields{
		"formats":      len(exchangeFormats),
		"market_types": len(marketTypes),
		"examples":     len(examples),
	}).Info("Retrieved supported pair formats")

	return response, nil
}

// RegisterAll registers all pair conversion tools
func RegisterAll(mcpServer *server.MCPServer, logger *logrus.Logger) error {
	tools := []server.Tool{
		NewConvertPairTool(logger),
		NewBatchConvertPairTool(logger),
		NewGetSupportedFormatsTool(logger),
	}

	for _, tool := range tools {
		if err := mcpServer.RegisterTool(tool); err != nil {
			return fmt.Errorf("failed to register tool %s: %w", tool.Name(), err)
		}
	}

	logger.WithField("count", len(tools)).Info("Registered pair conversion tools")
	return nil
}
