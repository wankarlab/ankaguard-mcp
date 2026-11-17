package server

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/models"
	"github.com/rantipay/trading-mcp/internal/redact"
	"github.com/sirupsen/logrus"
)

// AuditLogger maneja el audit trail inmutable
type AuditLogger struct {
	file   *os.File
	mu     sync.Mutex
	log    *logrus.Logger
	config AuditConfig
}

// AuditConfig configuración del audit logger
type AuditConfig struct {
	Enabled        bool
	LogFile        string
	LogAllRequests bool
	LogAllOrders   bool
}

// AuditEntry representa una entrada en el audit log
type AuditEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Type      string                 `json:"type"` // "request", "order", "error"
	Method    string                 `json:"method,omitempty"`
	ToolName  string                 `json:"tool_name,omitempty"`
	Params    map[string]interface{} `json:"params,omitempty"`
	Result    interface{}            `json:"result,omitempty"`
	Error     string                 `json:"error,omitempty"`
	RequestID interface{}            `json:"request_id,omitempty"`
	Duration  string                 `json:"duration,omitempty"`
	Success   bool                   `json:"success"`
	IPAddress string                 `json:"ip_address,omitempty"`
	UserAgent string                 `json:"user_agent,omitempty"`
	// TokenID identifies the token of the caller ("jti:<id>" or "sub:<subject>").
	// It is never the token itself.
	TokenID string `json:"token_id,omitempty"`
	Class   string `json:"class,omitempty"`
	Profile string `json:"profile,omitempty"`
}

// NewAuditLogger crea un nuevo audit logger
func NewAuditLogger(config AuditConfig, log *logrus.Logger) (*AuditLogger, error) {
	if !config.Enabled {
		return &AuditLogger{
			config: config,
			log:    log,
		}, nil
	}

	// Crear directorio si no existe
	if err := os.MkdirAll("logs", 0755); err != nil {
		return nil, fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Abrir archivo de audit log en modo append
	file, err := os.OpenFile(config.LogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open audit log file: %w", err)
	}

	return &AuditLogger{
		file:   file,
		config: config,
		log:    log,
	}, nil
}

// LogRequest registra un request MCP
func (a *AuditLogger) LogRequest(req *models.MCPRequest, result interface{}, err error, duration time.Duration) {
	if !a.config.Enabled || !a.config.LogAllRequests {
		return
	}

	entry := AuditEntry{
		Timestamp: time.Now(),
		Type:      "request",
		Method:    req.Method,
		RequestID: req.ID,
		Duration:  duration.String(),
		Success:   err == nil,
	}

	if req.Params != nil {
		entry.ToolName = req.Params.Name
		entry.Params = redact.Map(req.Params.Arguments)
	}

	if err != nil {
		entry.Error = err.Error()
	} else {
		entry.Result = result
	}

	a.writeEntry(entry)
}

// LogOrder registra una orden
func (a *AuditLogger) LogOrder(toolName string, params map[string]interface{}, result interface{}, err error) {
	if !a.config.Enabled || !a.config.LogAllOrders {
		return
	}

	entry := AuditEntry{
		Timestamp: time.Now(),
		Type:      "order",
		ToolName:  toolName,
		Params:    redact.Map(params),
		Success:   err == nil,
	}

	if err != nil {
		entry.Error = err.Error()
	} else {
		entry.Result = result
	}

	a.writeEntry(entry)
}

// LogPermissionDenied records a tool call rejected by the permission policy.
// It is written whenever the audit log is enabled, independently of
// LogAllRequests, because a denial is a security event.
func (a *AuditLogger) LogPermissionDenied(toolName, class, profile, tokenID, reason string) {
	if !a.config.Enabled {
		return
	}
	a.writeEntry(AuditEntry{
		Timestamp: time.Now(),
		Type:      "permission_denied",
		ToolName:  toolName,
		Class:     class,
		Profile:   profile,
		TokenID:   tokenID,
		Error:     reason,
		Success:   false,
	})
}

// LogError registra un error crítico
func (a *AuditLogger) LogError(context string, err error, details map[string]interface{}) {
	if !a.config.Enabled {
		return
	}

	entry := AuditEntry{
		Timestamp: time.Now(),
		Type:      "error",
		Method:    context,
		Error:     err.Error(),
		Params:    details,
		Success:   false,
	}

	a.writeEntry(entry)
}

// writeEntry escribe una entrada al archivo de audit log
func (a *AuditLogger) writeEntry(entry AuditEntry) {
	if a.file == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	// Serializar a JSON
	data, err := json.Marshal(entry)
	if err != nil {
		a.log.WithError(err).Error("Failed to marshal audit entry")
		return
	}

	// Escribir al archivo (una línea por entrada)
	if _, err := a.file.Write(append(data, '\n')); err != nil {
		a.log.WithError(err).Error("Failed to write audit entry")
		return
	}

	// Flush inmediatamente para garantizar persistencia
	if err := a.file.Sync(); err != nil {
		a.log.WithError(err).Error("Failed to sync audit log")
	}
}

// Close cierra el audit logger
func (a *AuditLogger) Close() error {
	if a.file != nil {
		return a.file.Close()
	}
	return nil
}

// GetRecentEntries obtiene las últimas N entradas del audit log
func (a *AuditLogger) GetRecentEntries(limit int) ([]AuditEntry, error) {
	if !a.config.Enabled || a.file == nil {
		return []AuditEntry{}, nil
	}

	// TODO: Implementar lectura eficiente del archivo
	// Por ahora retornamos vacío
	return []AuditEntry{}, nil
}

// GetEntriesByType obtiene entradas por tipo
func (a *AuditLogger) GetEntriesByType(entryType string, limit int) ([]AuditEntry, error) {
	if !a.config.Enabled || a.file == nil {
		return []AuditEntry{}, nil
	}

	// TODO: Implementar filtrado por tipo
	return []AuditEntry{}, nil
}

// LogSpecEvent records an append-only Spec lifecycle event (promotion, demotion,
// validation, appeal) for the RBAC-5 audit trail. All roles (level>=1) can read
// this log via GetEntriesByType("spec_lifecycle"). Records are never mutated.
func (a *AuditLogger) LogSpecEvent(event, specID, userID, details string) {
	entry := AuditEntry{
		Timestamp: time.Now(),
		Type:      "spec_lifecycle",
		ToolName:  event,
		RequestID: fmt.Sprintf("spec:%s:%d", specID, time.Now().UnixNano()),
		Success:   true,
		Result: map[string]interface{}{
			"spec_id": specID,
			"event":   event,
			"user_id": userID,
			"details": details,
		},
	}
	a.writeEntry(entry)
}

// LogSpecAppeal records a Spec gate-rejection appeal filed by the author.
// Only the author's sub may file an appeal for their own Spec; only an arbiter
// (FeatureArbitrate, level>=4) may resolve it. RBAC-5 enforcement is at the
// API layer; this method only records the event.
func (a *AuditLogger) LogSpecAppeal(specID, authorID, reason string) {
	a.LogSpecEvent("appeal_filed", specID, authorID, reason)
}

// LogSpecAppealResolution records the arbiter's resolution of a Spec appeal.
func (a *AuditLogger) LogSpecAppealResolution(specID, arbiterID, decision, reason string) {
	a.LogSpecEvent("appeal_resolved:"+decision, specID, arbiterID, reason)
}

// GetOrderHistory obtiene historial de órdenes del audit log
func (a *AuditLogger) GetOrderHistory(limit int) ([]AuditEntry, error) {
	return a.GetEntriesByType("order", limit)
}
