// Package credentials provides secure storage and management of user exchange API credentials.
package credentials

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// AuditAction represents the type of action performed on a credential.
type AuditAction string

const (
	// AuditActionCreate indicates a credential was created.
	AuditActionCreate AuditAction = "create"
	// AuditActionRead indicates a credential was read/accessed.
	AuditActionRead AuditAction = "read"
	// AuditActionUpdate indicates a credential was updated.
	AuditActionUpdate AuditAction = "update"
	// AuditActionDelete indicates a credential was deleted.
	AuditActionDelete AuditAction = "delete"
	// AuditActionTest indicates a credential was tested against the exchange.
	AuditActionTest AuditAction = "test"
	// AuditActionUse indicates a credential was used for an API call.
	AuditActionUse AuditAction = "use"
	// AuditActionVerify indicates a credential was verified.
	AuditActionVerify AuditAction = "verify"
	// AuditActionRotate indicates a credential's encryption key was rotated.
	AuditActionRotate AuditAction = "rotate"
	// AuditActionList indicates credentials were listed.
	AuditActionList AuditAction = "list"
	// AuditActionValidateAll indicates all credentials were validated.
	AuditActionValidateAll AuditAction = "validate_all"
)

// CredentialAuditLog represents an audit log entry for credential operations.
type CredentialAuditLog struct {
	// ID is the unique identifier for this audit log entry.
	ID string `json:"id" gorm:"primaryKey;type:uuid;default:gen_random_uuid()"`

	// CredentialID is the ID of the credential that was accessed.
	CredentialID string `json:"credential_id" gorm:"type:uuid;not null;index:idx_audit_credential"`

	// UserID is the ID of the user who performed the action.
	UserID string `json:"user_id" gorm:"type:uuid;not null;index:idx_audit_user"`

	// TenantID is the tenant ID for multi-tenant isolation.
	TenantID string `json:"tenant_id" gorm:"type:uuid;not null;index:idx_audit_tenant"`

	// Action is the type of operation performed.
	Action AuditAction `json:"action" gorm:"type:varchar(50);not null;index:idx_audit_action"`

	// ActionDetails contains additional JSON details about the action.
	ActionDetails string `json:"action_details,omitempty" gorm:"type:jsonb"`

	// IPAddress is the IP address from which the request originated.
	IPAddress string `json:"ip_address,omitempty" gorm:"type:inet"`

	// UserAgent is the user agent string of the client making the request.
	UserAgent string `json:"user_agent,omitempty" gorm:"type:text"`

	// RequestID is the unique identifier for the request (for correlation).
	RequestID string `json:"request_id,omitempty" gorm:"type:varchar(100)"`

	// Success indicates whether the operation succeeded.
	Success bool `json:"success" gorm:"not null"`

	// ErrorMessage contains the error message if the operation failed.
	ErrorMessage string `json:"error_message,omitempty" gorm:"type:text"`

	// CreatedAt is the timestamp when the audit log entry was created.
	CreatedAt time.Time `json:"created_at" gorm:"autoCreateTime;index:idx_audit_created,sort:desc"`
}

// TableName returns the table name for GORM.
func (CredentialAuditLog) TableName() string {
	return "credential_audit_log"
}

// AuditContext contains contextual information for audit logging.
type AuditContext struct {
	// UserID is the ID of the user performing the action.
	UserID string
	// TenantID is the tenant ID for multi-tenant isolation.
	TenantID string
	// IPAddress is the IP address from which the request originated.
	IPAddress string
	// UserAgent is the user agent string of the client.
	UserAgent string
	// RequestID is a unique identifier for the request.
	RequestID string
}

// AuditDetails represents additional details for an audit log entry.
type AuditDetails struct {
	// Exchange is the name of the exchange involved in the operation.
	Exchange string `json:"exchange,omitempty"`
	// Label is the credential label if applicable.
	Label string `json:"label,omitempty"`
	// OldValue contains the previous value for update operations.
	OldValue interface{} `json:"old_value,omitempty"`
	// NewValue contains the new value for update operations.
	NewValue interface{} `json:"new_value,omitempty"`
	// FieldsUpdated lists the fields that were modified.
	FieldsUpdated []string `json:"fields_updated,omitempty"`
	// TestResult contains test operation results.
	TestResult *TestCredentialResult `json:"test_result,omitempty"`
	// CredentialsRotated is the count of credentials rotated during key rotation.
	CredentialsRotated int `json:"credentials_rotated,omitempty"`
	// Message provides additional context about the operation.
	Message string `json:"message,omitempty"`
}

// AuditLogger defines the interface for audit logging credential operations.
type AuditLogger interface {
	// Log records an audit log entry for a credential operation.
	Log(ctx context.Context, credentialID string, auditCtx *AuditContext, action AuditAction, success bool, err error, details *AuditDetails) error

	// LogCreate records an audit log entry for credential creation.
	LogCreate(ctx context.Context, credential *UserCredential, auditCtx *AuditContext, success bool, err error) error

	// LogRead records an audit log entry for credential read/access.
	LogRead(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, success bool, err error) error

	// LogUpdate records an audit log entry for credential update.
	LogUpdate(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, fieldsUpdated []string, success bool, err error) error

	// LogDelete records an audit log entry for credential deletion.
	LogDelete(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, success bool, err error) error

	// LogTest records an audit log entry for credential testing.
	LogTest(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, result *TestCredentialResult, err error) error

	// LogUse records an audit log entry for credential usage.
	LogUse(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, success bool, err error) error

	// LogList records an audit log entry for listing credentials.
	LogList(ctx context.Context, auditCtx *AuditContext, count int, success bool, err error) error

	// LogKeyRotation records an audit log entry for encryption key rotation.
	LogKeyRotation(ctx context.Context, auditCtx *AuditContext, oldKeyID string, rotatedCount int, success bool, err error) error

	// GetAuditLogs retrieves audit logs with optional filtering.
	GetAuditLogs(ctx context.Context, filter *AuditLogFilter) ([]CredentialAuditLog, error)

	// GetAuditLogsForCredential retrieves audit logs for a specific credential.
	GetAuditLogsForCredential(ctx context.Context, credentialID string, limit int) ([]CredentialAuditLog, error)

	// GetAuditLogsForUser retrieves audit logs for a specific user.
	GetAuditLogsForUser(ctx context.Context, userID string, limit int) ([]CredentialAuditLog, error)
}

// AuditLogFilter defines filtering options for audit log queries.
type AuditLogFilter struct {
	// CredentialID filters by credential ID.
	CredentialID string
	// UserID filters by user ID.
	UserID string
	// TenantID filters by tenant ID.
	TenantID string
	// Action filters by action type.
	Action AuditAction
	// StartTime filters logs created after this time.
	StartTime *time.Time
	// EndTime filters logs created before this time.
	EndTime *time.Time
	// SuccessOnly filters to only successful operations.
	SuccessOnly bool
	// FailedOnly filters to only failed operations.
	FailedOnly bool
	// IPAddress filters by IP address.
	IPAddress string
	// Limit limits the number of results.
	Limit int
	// Offset skips the first N results.
	Offset int
}

// PostgresAuditLogger implements AuditLogger using PostgreSQL with GORM.
type PostgresAuditLogger struct {
	db     *gorm.DB
	logger *logrus.Logger
	async  bool
}

// AuditLoggerConfig holds configuration for the audit logger.
type AuditLoggerConfig struct {
	// DB is the GORM database connection.
	DB *gorm.DB
	// Logger is the logrus logger for internal logging.
	Logger *logrus.Logger
	// Async enables asynchronous audit logging (non-blocking).
	Async bool
}

// NewPostgresAuditLogger creates a new PostgresAuditLogger.
func NewPostgresAuditLogger(cfg *AuditLoggerConfig) (*PostgresAuditLogger, error) {
	if cfg == nil {
		return nil, errors.New("config is required")
	}
	if cfg.DB == nil {
		return nil, errors.New("database connection is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = logrus.New()
	}

	return &PostgresAuditLogger{
		db:     cfg.DB,
		logger: logger,
		async:  cfg.Async,
	}, nil
}

// Migrate runs the database migrations for the audit log table.
func (a *PostgresAuditLogger) Migrate() error {
	return a.db.AutoMigrate(&CredentialAuditLog{})
}

// Log records an audit log entry for a credential operation.
func (a *PostgresAuditLogger) Log(ctx context.Context, credentialID string, auditCtx *AuditContext, action AuditAction, success bool, err error, details *AuditDetails) error {
	if auditCtx == nil {
		auditCtx = &AuditContext{}
	}

	entry := &CredentialAuditLog{
		CredentialID: credentialID,
		UserID:       auditCtx.UserID,
		TenantID:     auditCtx.TenantID,
		Action:       action,
		IPAddress:    sanitizeIPAddress(auditCtx.IPAddress),
		UserAgent:    truncateString(auditCtx.UserAgent, 500),
		RequestID:    auditCtx.RequestID,
		Success:      success,
	}

	// Serialize action details to JSON
	if details != nil {
		detailsJSON, jsonErr := json.Marshal(details)
		if jsonErr == nil {
			entry.ActionDetails = string(detailsJSON)
		} else {
			a.logger.WithError(jsonErr).Warn("Failed to serialize audit details")
		}
	}

	// Capture error message
	if err != nil {
		entry.ErrorMessage = truncateString(err.Error(), 1000)
	}

	// Log the audit entry
	if a.async {
		go a.saveAuditLog(context.Background(), entry)
		return nil
	}

	return a.saveAuditLog(ctx, entry)
}

// saveAuditLog persists the audit log entry to the database.
func (a *PostgresAuditLogger) saveAuditLog(ctx context.Context, entry *CredentialAuditLog) error {
	if err := a.db.WithContext(ctx).Create(entry).Error; err != nil {
		a.logger.WithFields(logrus.Fields{
			"credential_id": entry.CredentialID,
			"user_id":       entry.UserID,
			"action":        entry.Action,
			"error":         err.Error(),
		}).Error("Failed to save audit log")
		return fmt.Errorf("failed to save audit log: %w", err)
	}

	a.logger.WithFields(logrus.Fields{
		"audit_id":      entry.ID,
		"credential_id": entry.CredentialID,
		"user_id":       entry.UserID,
		"action":        entry.Action,
		"success":       entry.Success,
	}).Debug("Audit log recorded")

	return nil
}

// LogCreate records an audit log entry for credential creation.
func (a *PostgresAuditLogger) LogCreate(ctx context.Context, credential *UserCredential, auditCtx *AuditContext, success bool, err error) error {
	credentialID := ""
	details := &AuditDetails{}

	if credential != nil {
		credentialID = credential.ID
		details.Exchange = credential.ExchangeName
		details.Label = credential.Label
	}

	return a.Log(ctx, credentialID, auditCtx, AuditActionCreate, success, err, details)
}

// LogRead records an audit log entry for credential read/access.
func (a *PostgresAuditLogger) LogRead(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, success bool, err error) error {
	details := &AuditDetails{
		Exchange: exchange,
	}
	return a.Log(ctx, credentialID, auditCtx, AuditActionRead, success, err, details)
}

// LogUpdate records an audit log entry for credential update.
func (a *PostgresAuditLogger) LogUpdate(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, fieldsUpdated []string, success bool, err error) error {
	details := &AuditDetails{
		Exchange:      exchange,
		FieldsUpdated: fieldsUpdated,
	}
	return a.Log(ctx, credentialID, auditCtx, AuditActionUpdate, success, err, details)
}

// LogDelete records an audit log entry for credential deletion.
func (a *PostgresAuditLogger) LogDelete(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, success bool, err error) error {
	details := &AuditDetails{
		Exchange: exchange,
	}
	return a.Log(ctx, credentialID, auditCtx, AuditActionDelete, success, err, details)
}

// LogTest records an audit log entry for credential testing.
func (a *PostgresAuditLogger) LogTest(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, result *TestCredentialResult, err error) error {
	success := result != nil && result.Success
	details := &AuditDetails{
		Exchange:   exchange,
		TestResult: result,
	}
	return a.Log(ctx, credentialID, auditCtx, AuditActionTest, success, err, details)
}

// LogUse records an audit log entry for credential usage.
func (a *PostgresAuditLogger) LogUse(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, success bool, err error) error {
	details := &AuditDetails{
		Exchange: exchange,
	}
	return a.Log(ctx, credentialID, auditCtx, AuditActionUse, success, err, details)
}

// LogList records an audit log entry for listing credentials.
func (a *PostgresAuditLogger) LogList(ctx context.Context, auditCtx *AuditContext, count int, success bool, err error) error {
	details := &AuditDetails{
		Message: fmt.Sprintf("Listed %d credentials", count),
	}
	// Use a placeholder credential ID for list operations
	return a.Log(ctx, "00000000-0000-0000-0000-000000000000", auditCtx, AuditActionList, success, err, details)
}

// LogKeyRotation records an audit log entry for encryption key rotation.
func (a *PostgresAuditLogger) LogKeyRotation(ctx context.Context, auditCtx *AuditContext, oldKeyID string, rotatedCount int, success bool, err error) error {
	details := &AuditDetails{
		CredentialsRotated: rotatedCount,
		Message:            fmt.Sprintf("Rotated encryption key from %s, affected %d credentials", oldKeyID, rotatedCount),
	}
	// Use a placeholder credential ID for rotation operations
	return a.Log(ctx, "00000000-0000-0000-0000-000000000000", auditCtx, AuditActionRotate, success, err, details)
}

// GetAuditLogs retrieves audit logs with optional filtering.
func (a *PostgresAuditLogger) GetAuditLogs(ctx context.Context, filter *AuditLogFilter) ([]CredentialAuditLog, error) {
	if filter == nil {
		filter = &AuditLogFilter{Limit: 100}
	}

	query := a.db.WithContext(ctx).Model(&CredentialAuditLog{})

	// Apply filters
	if filter.CredentialID != "" {
		query = query.Where("credential_id = ?", filter.CredentialID)
	}
	if filter.UserID != "" {
		query = query.Where("user_id = ?", filter.UserID)
	}
	if filter.TenantID != "" {
		query = query.Where("tenant_id = ?", filter.TenantID)
	}
	if filter.Action != "" {
		query = query.Where("action = ?", filter.Action)
	}
	if filter.StartTime != nil {
		query = query.Where("created_at >= ?", filter.StartTime)
	}
	if filter.EndTime != nil {
		query = query.Where("created_at <= ?", filter.EndTime)
	}
	if filter.SuccessOnly {
		query = query.Where("success = ?", true)
	}
	if filter.FailedOnly {
		query = query.Where("success = ?", false)
	}
	if filter.IPAddress != "" {
		query = query.Where("ip_address = ?", filter.IPAddress)
	}

	// Apply pagination
	if filter.Limit > 0 {
		query = query.Limit(filter.Limit)
	} else {
		query = query.Limit(100) // Default limit
	}
	if filter.Offset > 0 {
		query = query.Offset(filter.Offset)
	}

	// Order by created_at descending (most recent first)
	query = query.Order("created_at DESC")

	var logs []CredentialAuditLog
	if err := query.Find(&logs).Error; err != nil {
		return nil, fmt.Errorf("failed to get audit logs: %w", err)
	}

	return logs, nil
}

// GetAuditLogsForCredential retrieves audit logs for a specific credential.
func (a *PostgresAuditLogger) GetAuditLogsForCredential(ctx context.Context, credentialID string, limit int) ([]CredentialAuditLog, error) {
	if credentialID == "" {
		return nil, errors.New("credential ID is required")
	}
	if limit <= 0 {
		limit = 50
	}

	return a.GetAuditLogs(ctx, &AuditLogFilter{
		CredentialID: credentialID,
		Limit:        limit,
	})
}

// GetAuditLogsForUser retrieves audit logs for a specific user.
func (a *PostgresAuditLogger) GetAuditLogsForUser(ctx context.Context, userID string, limit int) ([]CredentialAuditLog, error) {
	if userID == "" {
		return nil, errors.New("user ID is required")
	}
	if limit <= 0 {
		limit = 50
	}

	return a.GetAuditLogs(ctx, &AuditLogFilter{
		UserID: userID,
		Limit:  limit,
	})
}

// NoOpAuditLogger is a no-operation audit logger for testing or when auditing is disabled.
type NoOpAuditLogger struct{}

// NewNoOpAuditLogger creates a new NoOpAuditLogger.
func NewNoOpAuditLogger() *NoOpAuditLogger {
	return &NoOpAuditLogger{}
}

func (n *NoOpAuditLogger) Log(ctx context.Context, credentialID string, auditCtx *AuditContext, action AuditAction, success bool, err error, details *AuditDetails) error {
	return nil
}

func (n *NoOpAuditLogger) LogCreate(ctx context.Context, credential *UserCredential, auditCtx *AuditContext, success bool, err error) error {
	return nil
}

func (n *NoOpAuditLogger) LogRead(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, success bool, err error) error {
	return nil
}

func (n *NoOpAuditLogger) LogUpdate(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, fieldsUpdated []string, success bool, err error) error {
	return nil
}

func (n *NoOpAuditLogger) LogDelete(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, success bool, err error) error {
	return nil
}

func (n *NoOpAuditLogger) LogTest(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, result *TestCredentialResult, err error) error {
	return nil
}

func (n *NoOpAuditLogger) LogUse(ctx context.Context, credentialID, exchange string, auditCtx *AuditContext, success bool, err error) error {
	return nil
}

func (n *NoOpAuditLogger) LogList(ctx context.Context, auditCtx *AuditContext, count int, success bool, err error) error {
	return nil
}

func (n *NoOpAuditLogger) LogKeyRotation(ctx context.Context, auditCtx *AuditContext, oldKeyID string, rotatedCount int, success bool, err error) error {
	return nil
}

func (n *NoOpAuditLogger) GetAuditLogs(ctx context.Context, filter *AuditLogFilter) ([]CredentialAuditLog, error) {
	return nil, nil
}

func (n *NoOpAuditLogger) GetAuditLogsForCredential(ctx context.Context, credentialID string, limit int) ([]CredentialAuditLog, error) {
	return nil, nil
}

func (n *NoOpAuditLogger) GetAuditLogsForUser(ctx context.Context, userID string, limit int) ([]CredentialAuditLog, error) {
	return nil, nil
}

// Helper functions

// sanitizeIPAddress validates and sanitizes an IP address string.
func sanitizeIPAddress(ip string) string {
	if ip == "" {
		return ""
	}

	// Parse the IP address to validate it
	parsedIP := net.ParseIP(ip)
	if parsedIP == nil {
		// Try to extract IP from common formats like "ip:port"
		host, _, err := net.SplitHostPort(ip)
		if err == nil {
			parsedIP = net.ParseIP(host)
		}
	}

	if parsedIP != nil {
		return parsedIP.String()
	}

	return ""
}

// truncateString truncates a string to the specified maximum length.
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

// contextKey is a type for context keys.
type contextKey string

const (
	// AuditContextKey is the context key for AuditContext.
	AuditContextKey contextKey = "audit_context"
)

// WithAuditContext adds an AuditContext to the context.
func WithAuditContext(ctx context.Context, auditCtx *AuditContext) context.Context {
	return context.WithValue(ctx, AuditContextKey, auditCtx)
}

// AuditContextFromContext retrieves the AuditContext from the context.
func AuditContextFromContext(ctx context.Context) *AuditContext {
	auditCtx, ok := ctx.Value(AuditContextKey).(*AuditContext)
	if !ok {
		return nil
	}
	return auditCtx
}

// AuditContextFromRequest creates an AuditContext from common request parameters.
// This is a convenience function for creating audit contexts from HTTP requests.
func AuditContextFromRequest(userID, tenantID, ipAddress, userAgent, requestID string) *AuditContext {
	return &AuditContext{
		UserID:    userID,
		TenantID:  tenantID,
		IPAddress: ipAddress,
		UserAgent: userAgent,
		RequestID: requestID,
	}
}
