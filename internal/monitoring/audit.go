package monitoring

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/sirupsen/logrus"
)

// AuditEventType represents the type of audit event
type AuditEventType string

const (
	// Authentication events
	AuditEventLogin       AuditEventType = "login"
	AuditEventLogout      AuditEventType = "logout"
	AuditEventAuthFailure AuditEventType = "auth_failure"

	// Trading events
	AuditEventOrderPlace    AuditEventType = "order_place"
	AuditEventOrderCancel   AuditEventType = "order_cancel"
	AuditEventOrderFill     AuditEventType = "order_fill"
	AuditEventPositionOpen  AuditEventType = "position_open"
	AuditEventPositionClose AuditEventType = "position_close"

	// Configuration events
	AuditEventConfigChange  AuditEventType = "config_change"
	AuditEventCredentialSet AuditEventType = "credential_set"
	AuditEventCredentialGet AuditEventType = "credential_get"

	// MCP events
	AuditEventMCPRequest    AuditEventType = "mcp_request"
	AuditEventToolExecution AuditEventType = "tool_execution"

	// System events
	AuditEventSystemStart AuditEventType = "system_start"
	AuditEventSystemStop  AuditEventType = "system_stop"
	AuditEventHealthCheck AuditEventType = "health_check"

	// Security events
	AuditEventSecurityAlert      AuditEventType = "security_alert"
	AuditEventRateLimitHit       AuditEventType = "rate_limit_hit"
	AuditEventSuspiciousActivity AuditEventType = "suspicious_activity"

	// Notification events
	AuditEventNotificationSent AuditEventType = "notification_sent"
	AuditEventAlertTriggered   AuditEventType = "alert_triggered"
)

// AuditSeverity represents the severity level of an audit event
type AuditSeverity string

const (
	AuditSeverityInfo     AuditSeverity = "info"
	AuditSeverityWarning  AuditSeverity = "warning"
	AuditSeverityError    AuditSeverity = "error"
	AuditSeverityCritical AuditSeverity = "critical"
)

// AuditEvent represents an audit log entry
type AuditEvent struct {
	ID        string                 `json:"id"`
	Timestamp time.Time              `json:"timestamp"`
	EventType AuditEventType         `json:"event_type"`
	Severity  AuditSeverity          `json:"severity"`
	UserID    string                 `json:"user_id,omitempty"`
	TenantID  string                 `json:"tenant_id,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	IPAddress string                 `json:"ip_address,omitempty"`
	UserAgent string                 `json:"user_agent,omitempty"`
	Resource  string                 `json:"resource,omitempty"`
	Action    string                 `json:"action,omitempty"`
	Result    string                 `json:"result"`
	Message   string                 `json:"message"`
	Details   map[string]interface{} `json:"details,omitempty"`
	Duration  time.Duration          `json:"duration,omitempty"`
	Error     string                 `json:"error,omitempty"`
	RequestID string                 `json:"request_id,omitempty"`
	TraceID   string                 `json:"trace_id,omitempty"`
	SpanID    string                 `json:"span_id,omitempty"`
}

// AuditLogger handles structured audit logging
type AuditLogger struct {
	logger  *logrus.Logger
	enabled bool
	fields  logrus.Fields
}

// NewAuditLogger creates a new audit logger
func NewAuditLogger(logger *logrus.Logger) *AuditLogger {
	return &AuditLogger{
		logger:  logger,
		enabled: true,
		fields:  make(logrus.Fields),
	}
}

// SetEnabled enables or disables audit logging
func (al *AuditLogger) SetEnabled(enabled bool) {
	al.enabled = enabled
}

// LogEvent logs an audit event
func (al *AuditLogger) LogEvent(event *AuditEvent) {
	if !al.enabled {
		return
	}

	// Generate ID if not provided
	if event.ID == "" {
		event.ID = generateEventID()
	}

	// Set timestamp if not provided
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Create log entry with structured fields
	entry := al.logger.WithFields(logrus.Fields{
		"audit_event_id":   event.ID,
		"audit_timestamp":  event.Timestamp,
		"audit_event_type": event.EventType,
		"audit_severity":   event.Severity,
		"audit_user_id":    event.UserID,
		"audit_tenant_id":  event.TenantID,
		"audit_session_id": event.SessionID,
		"audit_ip_address": event.IPAddress,
		"audit_user_agent": event.UserAgent,
		"audit_resource":   event.Resource,
		"audit_action":     event.Action,
		"audit_result":     event.Result,
		"audit_duration":   event.Duration,
		"audit_request_id": event.RequestID,
		"audit_trace_id":   event.TraceID,
		"audit_span_id":    event.SpanID,
	})

	// Add details as separate fields
	if event.Details != nil {
		for key, value := range event.Details {
			entry = entry.WithField("audit_detail_"+key, value)
		}
	}

	// Add error if present
	if event.Error != "" {
		entry = entry.WithField("audit_error", event.Error)
	}

	// Log at appropriate level based on severity
	switch event.Severity {
	case AuditSeverityCritical:
		entry.Error(event.Message)
	case AuditSeverityError:
		entry.Error(event.Message)
	case AuditSeverityWarning:
		entry.Warn(event.Message)
	default:
		entry.Info(event.Message)
	}
}

// LogAuthentication logs authentication events
func (al *AuditLogger) LogAuthentication(eventType AuditEventType, userID, sessionID, ipAddress, userAgent string, success bool, details map[string]interface{}) {
	result := "success"
	severity := AuditSeverityInfo

	if !success {
		result = "failure"
		severity = AuditSeverityWarning
	}

	event := &AuditEvent{
		EventType: eventType,
		Severity:  severity,
		UserID:    userID,
		SessionID: sessionID,
		IPAddress: ipAddress,
		UserAgent: userAgent,
		Action:    string(eventType),
		Result:    result,
		Message:   fmt.Sprintf("Authentication %s: %s", eventType, result),
		Details:   details,
	}

	al.LogEvent(event)
}

// LogTradingActivity logs trading-related events
func (al *AuditLogger) LogTradingActivity(eventType AuditEventType, userID, exchange, symbol string, details map[string]interface{}) {
	event := &AuditEvent{
		EventType: eventType,
		Severity:  AuditSeverityInfo,
		UserID:    userID,
		Resource:  fmt.Sprintf("%s:%s", exchange, symbol),
		Action:    string(eventType),
		Result:    "success",
		Message:   fmt.Sprintf("Trading activity: %s on %s %s", eventType, exchange, symbol),
		Details:   details,
	}

	al.LogEvent(event)
}

// LogConfigurationChange logs configuration changes
func (al *AuditLogger) LogConfigurationChange(userID, section string, changes map[string]interface{}) {
	event := &AuditEvent{
		EventType: AuditEventConfigChange,
		Severity:  AuditSeverityInfo,
		UserID:    userID,
		Resource:  fmt.Sprintf("config:%s", section),
		Action:    "update",
		Result:    "success",
		Message:   fmt.Sprintf("Configuration changed: %s", section),
		Details:   changes,
	}

	al.LogEvent(event)
}

// LogMCPRequest logs MCP requests
func (al *AuditLogger) LogMCPRequest(userID, method, tool string, duration time.Duration, success bool, details map[string]interface{}) {
	result := "success"
	severity := AuditSeverityInfo

	if !success {
		result = "failure"
		severity = AuditSeverityWarning
	}

	event := &AuditEvent{
		EventType: AuditEventMCPRequest,
		Severity:  severity,
		UserID:    userID,
		Resource:  fmt.Sprintf("mcp:%s", tool),
		Action:    method,
		Result:    result,
		Message:   fmt.Sprintf("MCP request: %s %s", method, tool),
		Duration:  duration,
		Details:   details,
	}

	al.LogEvent(event)
}

// LogToolExecution logs MCP tool executions
func (al *AuditLogger) LogToolExecution(userID, tool string, duration time.Duration, success bool, details map[string]interface{}) {
	result := "success"
	severity := AuditSeverityInfo

	if !success {
		result = "failure"
		severity = AuditSeverityError
	}

	event := &AuditEvent{
		EventType: AuditEventToolExecution,
		Severity:  severity,
		UserID:    userID,
		Resource:  fmt.Sprintf("tool:%s", tool),
		Action:    "execute",
		Result:    result,
		Message:   fmt.Sprintf("Tool execution: %s", tool),
		Duration:  duration,
		Details:   details,
	}

	al.LogEvent(event)
}

// LogSecurityEvent logs security-related events
func (al *AuditLogger) LogSecurityEvent(eventType AuditEventType, userID, ipAddress, description string, details map[string]interface{}) {
	event := &AuditEvent{
		EventType: eventType,
		Severity:  AuditSeverityWarning,
		UserID:    userID,
		IPAddress: ipAddress,
		Action:    string(eventType),
		Result:    "detected",
		Message:   fmt.Sprintf("Security event: %s - %s", eventType, description),
		Details:   details,
	}

	al.LogEvent(event)
}

// LogSystemEvent logs system-level events
func (al *AuditLogger) LogSystemEvent(eventType AuditEventType, message string, details map[string]interface{}) {
	event := &AuditEvent{
		EventType: eventType,
		Severity:  AuditSeverityInfo,
		Action:    string(eventType),
		Result:    "success",
		Message:   message,
		Details:   details,
	}

	al.LogEvent(event)
}

// LogNotificationEvent logs notification events
func (al *AuditLogger) LogNotificationEvent(eventType AuditEventType, userID, channel, notificationType string, success bool, details map[string]interface{}) {
	result := "success"
	severity := AuditSeverityInfo

	if !success {
		result = "failure"
		severity = AuditSeverityWarning
	}

	event := &AuditEvent{
		EventType: eventType,
		Severity:  severity,
		UserID:    userID,
		Resource:  fmt.Sprintf("notification:%s", channel),
		Action:    "send",
		Result:    result,
		Message:   fmt.Sprintf("Notification %s via %s: %s", notificationType, channel, result),
		Details:   details,
	}

	al.LogEvent(event)
}

// LogHealthCheck logs health check events
func (al *AuditLogger) LogHealthCheck(component string, status HealthStatus, duration time.Duration, details map[string]interface{}) {
	severity := AuditSeverityInfo
	if status == HealthStatusUnhealthy {
		severity = AuditSeverityError
	} else if status == HealthStatusDegraded {
		severity = AuditSeverityWarning
	}

	event := &AuditEvent{
		EventType: AuditEventHealthCheck,
		Severity:  severity,
		Resource:  fmt.Sprintf("health:%s", component),
		Action:    "check",
		Result:    string(status),
		Message:   fmt.Sprintf("Health check: %s is %s", component, status),
		Duration:  duration,
		Details:   details,
	}

	al.LogEvent(event)
}

// WithContext adds context information to the audit logger
func (al *AuditLogger) WithContext(ctx context.Context) *AuditLogger {
	newLogger := &AuditLogger{
		logger:  al.logger,
		enabled: al.enabled,
		fields:  make(logrus.Fields),
	}

	// Copy existing fields
	for k, v := range al.fields {
		newLogger.fields[k] = v
	}

	// Extract context information
	if requestID := getRequestIDFromContext(ctx); requestID != "" {
		newLogger.fields["request_id"] = requestID
	}

	if traceID := getTraceIDFromContext(ctx); traceID != "" {
		newLogger.fields["trace_id"] = traceID
	}

	if spanID := getSpanIDFromContext(ctx); spanID != "" {
		newLogger.fields["span_id"] = spanID
	}

	return newLogger
}

// WithFields adds additional fields to the audit logger
func (al *AuditLogger) WithFields(fields map[string]interface{}) *AuditLogger {
	newLogger := &AuditLogger{
		logger:  al.logger,
		enabled: al.enabled,
		fields:  make(logrus.Fields),
	}

	// Copy existing fields
	for k, v := range al.fields {
		newLogger.fields[k] = v
	}

	// Add new fields
	for k, v := range fields {
		newLogger.fields[k] = v
	}

	return newLogger
}

// Helper functions

func generateEventID() string {
	return fmt.Sprintf("audit_%d_%d", time.Now().UnixNano(), time.Now().Unix())
}

func getRequestIDFromContext(ctx context.Context) string {
	if requestID := ctx.Value("request_id"); requestID != nil {
		if id, ok := requestID.(string); ok {
			return id
		}
	}
	return ""
}

func getTraceIDFromContext(ctx context.Context) string {
	if traceID := ctx.Value("trace_id"); traceID != nil {
		if id, ok := traceID.(string); ok {
			return id
		}
	}
	return ""
}

func getSpanIDFromContext(ctx context.Context) string {
	if spanID := ctx.Value("span_id"); spanID != nil {
		if id, ok := spanID.(string); ok {
			return id
		}
	}
	return ""
}

// AuditMiddleware creates middleware for HTTP request auditing
func (al *AuditLogger) AuditMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			// Create a response writer wrapper to capture status code
			wrapper := &responseWriter{ResponseWriter: w, statusCode: 200}

			// Process request
			next.ServeHTTP(wrapper, r)

			// Log the request
			duration := time.Since(start)

			event := &AuditEvent{
				EventType: AuditEventType("http_request"),
				Severity:  AuditSeverityInfo,
				IPAddress: r.RemoteAddr,
				UserAgent: r.UserAgent(),
				Resource:  r.URL.Path,
				Action:    r.Method,
				Result:    fmt.Sprintf("%d", wrapper.statusCode),
				Message:   fmt.Sprintf("HTTP %s %s - %d", r.Method, r.URL.Path, wrapper.statusCode),
				Duration:  duration,
				Details: map[string]interface{}{
					"method":      r.Method,
					"path":        r.URL.Path,
					"query":       r.URL.RawQuery,
					"status_code": wrapper.statusCode,
					"user_agent":  r.UserAgent(),
					"remote_addr": r.RemoteAddr,
				},
			}

			al.LogEvent(event)
		})
	}
}

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}

// AuditEventFilter represents a filter for audit events
type AuditEventFilter struct {
	EventTypes []AuditEventType `json:"event_types,omitempty"`
	Severities []AuditSeverity  `json:"severities,omitempty"`
	UserIDs    []string         `json:"user_ids,omitempty"`
	StartTime  *time.Time       `json:"start_time,omitempty"`
	EndTime    *time.Time       `json:"end_time,omitempty"`
	Resources  []string         `json:"resources,omitempty"`
	Actions    []string         `json:"actions,omitempty"`
	Results    []string         `json:"results,omitempty"`
}

// MatchesFilter checks if an audit event matches the given filter
func (event *AuditEvent) MatchesFilter(filter *AuditEventFilter) bool {
	if filter == nil {
		return true
	}

	// Check event types
	if len(filter.EventTypes) > 0 {
		found := false
		for _, eventType := range filter.EventTypes {
			if event.EventType == eventType {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check severities
	if len(filter.Severities) > 0 {
		found := false
		for _, severity := range filter.Severities {
			if event.Severity == severity {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check user IDs
	if len(filter.UserIDs) > 0 {
		found := false
		for _, userID := range filter.UserIDs {
			if event.UserID == userID {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check time range
	if filter.StartTime != nil && event.Timestamp.Before(*filter.StartTime) {
		return false
	}

	if filter.EndTime != nil && event.Timestamp.After(*filter.EndTime) {
		return false
	}

	// Check resources
	if len(filter.Resources) > 0 {
		found := false
		for _, resource := range filter.Resources {
			if event.Resource == resource {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check actions
	if len(filter.Actions) > 0 {
		found := false
		for _, action := range filter.Actions {
			if event.Action == action {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Check results
	if len(filter.Results) > 0 {
		found := false
		for _, result := range filter.Results {
			if event.Result == result {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}
