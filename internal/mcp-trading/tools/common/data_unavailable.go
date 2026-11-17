package common

import (
	"errors"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
)

const (
	// ErrCodeDataUnavailable is the JSON-RPC code for DATA_UNAVAILABLE
	// (next to PERMISSION_DENIED -32003 and PAPER_UNAVAILABLE -32004).
	ErrCodeDataUnavailable = -32005
	// DataUnavailableCode is the data.code clients see when a tool could not
	// obtain the real data it needs. A tool returning it never returns filler values.
	DataUnavailableCode = "DATA_UNAVAILABLE"
)

// DataUnavailable builds the DATA_UNAVAILABLE error. reason says why the real
// data could not be obtained; details (optional) adds machine-readable fields
// such as the amount of data that is missing. The tool server returns a
// *models.MCPError as-is, so the client sees error.data.code == "DATA_UNAVAILABLE".
func DataUnavailable(reason string, details map[string]interface{}) *models.MCPError {
	data := map[string]interface{}{
		"code":   DataUnavailableCode,
		"reason": reason,
	}
	for k, v := range details {
		if k != "code" && k != "reason" {
			data[k] = v
		}
	}
	return &models.MCPError{
		Code:    ErrCodeDataUnavailable,
		Message: DataUnavailableCode,
		Data:    data,
	}
}

// IsDataUnavailable reports whether err is (or wraps) a DATA_UNAVAILABLE error.
func IsDataUnavailable(err error) bool {
	var mcpErr *models.MCPError
	return errors.As(err, &mcpErr) && mcpErr.Code == ErrCodeDataUnavailable
}
