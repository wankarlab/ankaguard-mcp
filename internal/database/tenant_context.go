// Package database provides database utilities including Row-Level Security (RLS)
// tenant context management for multi-tenant SaaS applications.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// Context keys for tenant information
type contextKey string

const (
	// TenantIDKey is the context key for tenant ID
	TenantIDKey contextKey = "tenant_id"
	// UserIDKey is the context key for user ID
	UserIDKey contextKey = "user_id"
	// UserLevelKey is the context key for user authorization level
	UserLevelKey contextKey = "user_level"
	// IsAdminKey is the context key for admin session flag
	IsAdminKey contextKey = "is_admin"
	// CompanyIDKey is the context key for company ID
	CompanyIDKey contextKey = "company_id"
)

// TenantContext holds the current tenant context information
type TenantContext struct {
	TenantID  uuid.UUID
	UserID    uuid.UUID
	UserLevel int
	CompanyID uuid.UUID
	IsAdmin   bool
}

// Validate checks if the tenant context has all required fields
func (tc *TenantContext) Validate() error {
	if tc.TenantID == uuid.Nil {
		return errors.New("tenant_id is required")
	}
	if tc.UserID == uuid.Nil {
		return errors.New("user_id is required")
	}
	return nil
}

// String returns a string representation for logging
func (tc *TenantContext) String() string {
	return fmt.Sprintf("TenantContext{TenantID: %s, UserID: %s, Level: %d, IsAdmin: %t}",
		tc.TenantID.String(), tc.UserID.String(), tc.UserLevel, tc.IsAdmin)
}

// TenantContextManager manages tenant context for database operations
type TenantContextManager struct {
	logger *logrus.Logger
	mu     sync.RWMutex
}

// NewTenantContextManager creates a new tenant context manager
func NewTenantContextManager(logger *logrus.Logger) *TenantContextManager {
	if logger == nil {
		logger = logrus.New()
	}
	return &TenantContextManager{
		logger: logger,
	}
}

// SetTenantContext sets the PostgreSQL session variables for RLS
// This must be called at the beginning of each request/transaction
func SetTenantContext(ctx context.Context, db *sql.DB, tc *TenantContext) error {
	if tc == nil {
		return errors.New("tenant context cannot be nil")
	}

	if err := tc.Validate(); err != nil {
		return fmt.Errorf("invalid tenant context: %w", err)
	}

	// Set session variables for RLS policies
	queries := []struct {
		name  string
		query string
		value string
	}{
		{"tenant_id", "SET LOCAL app.tenant_id = $1", tc.TenantID.String()},
		{"user_id", "SET LOCAL app.user_id = $1", tc.UserID.String()},
		{"user_level", "SET LOCAL app.user_level = $1", fmt.Sprintf("%d", tc.UserLevel)},
		{"is_admin", "SET LOCAL app.is_admin = $1", fmt.Sprintf("%t", tc.IsAdmin)},
	}

	for _, q := range queries {
		if _, err := db.ExecContext(ctx, q.query, q.value); err != nil {
			return fmt.Errorf("failed to set %s: %w", q.name, err)
		}
	}

	return nil
}

// SetTenantContextGorm sets the PostgreSQL session variables for RLS using GORM
func SetTenantContextGorm(ctx context.Context, db *gorm.DB, tc *TenantContext) error {
	if tc == nil {
		return errors.New("tenant context cannot be nil")
	}

	if err := tc.Validate(); err != nil {
		return fmt.Errorf("invalid tenant context: %w", err)
	}

	// RLS session variables (SET LOCAL app.*) are a PostgreSQL feature. On SQLite
	// (local/dev) there is no row-level security, so this is a no-op — otherwise
	// the Postgres-only syntax errors and aborts every tenant-scoped request.
	if db.Dialector != nil && db.Dialector.Name() != "postgres" {
		return nil
	}

	// Use raw SQL to set session variables
	// SET LOCAL ensures variables are only valid for current transaction
	if err := db.WithContext(ctx).Exec("SET LOCAL app.tenant_id = ?", tc.TenantID.String()).Error; err != nil {
		return fmt.Errorf("failed to set tenant_id: %w", err)
	}

	if err := db.WithContext(ctx).Exec("SET LOCAL app.user_id = ?", tc.UserID.String()).Error; err != nil {
		return fmt.Errorf("failed to set user_id: %w", err)
	}

	if err := db.WithContext(ctx).Exec("SET LOCAL app.user_level = ?", tc.UserLevel).Error; err != nil {
		return fmt.Errorf("failed to set user_level: %w", err)
	}

	if err := db.WithContext(ctx).Exec("SET LOCAL app.is_admin = ?", tc.IsAdmin).Error; err != nil {
		return fmt.Errorf("failed to set is_admin: %w", err)
	}

	return nil
}

// ClearTenantContext clears the PostgreSQL session variables
func ClearTenantContext(ctx context.Context, db *sql.DB) error {
	queries := []string{
		"RESET app.tenant_id",
		"RESET app.user_id",
		"RESET app.user_level",
		"RESET app.is_admin",
	}

	for _, q := range queries {
		if _, err := db.ExecContext(ctx, q); err != nil {
			// Log but don't fail - RESET might fail if variable wasn't set
			continue
		}
	}

	return nil
}

// ClearTenantContextGorm clears the PostgreSQL session variables using GORM
func ClearTenantContextGorm(ctx context.Context, db *gorm.DB) error {
	queries := []string{
		"RESET app.tenant_id",
		"RESET app.user_id",
		"RESET app.user_level",
		"RESET app.is_admin",
	}

	for _, q := range queries {
		if err := db.WithContext(ctx).Exec(q).Error; err != nil {
			// Log but don't fail
			continue
		}
	}

	return nil
}

// WithTenantContext returns a new context with tenant information embedded
func WithTenantContext(ctx context.Context, tc *TenantContext) context.Context {
	if tc == nil {
		return ctx
	}

	ctx = context.WithValue(ctx, TenantIDKey, tc.TenantID)
	ctx = context.WithValue(ctx, UserIDKey, tc.UserID)
	ctx = context.WithValue(ctx, UserLevelKey, tc.UserLevel)
	ctx = context.WithValue(ctx, IsAdminKey, tc.IsAdmin)
	if tc.CompanyID != uuid.Nil {
		ctx = context.WithValue(ctx, CompanyIDKey, tc.CompanyID)
	}

	return ctx
}

// GetTenantContext extracts tenant context from the context
func GetTenantContext(ctx context.Context) (*TenantContext, error) {
	tc := &TenantContext{}

	if tenantID, ok := ctx.Value(TenantIDKey).(uuid.UUID); ok {
		tc.TenantID = tenantID
	} else if tenantIDStr, ok := ctx.Value(TenantIDKey).(string); ok {
		parsed, err := uuid.Parse(tenantIDStr)
		if err != nil {
			return nil, fmt.Errorf("invalid tenant_id format: %w", err)
		}
		tc.TenantID = parsed
	} else {
		return nil, errors.New("tenant_id not found in context")
	}

	if userID, ok := ctx.Value(UserIDKey).(uuid.UUID); ok {
		tc.UserID = userID
	} else if userIDStr, ok := ctx.Value(UserIDKey).(string); ok {
		parsed, err := uuid.Parse(userIDStr)
		if err != nil {
			return nil, fmt.Errorf("invalid user_id format: %w", err)
		}
		tc.UserID = parsed
	} else {
		return nil, errors.New("user_id not found in context")
	}

	if userLevel, ok := ctx.Value(UserLevelKey).(int); ok {
		tc.UserLevel = userLevel
	}

	if isAdmin, ok := ctx.Value(IsAdminKey).(bool); ok {
		tc.IsAdmin = isAdmin
	}

	if companyID, ok := ctx.Value(CompanyIDKey).(uuid.UUID); ok {
		tc.CompanyID = companyID
	} else if companyIDStr, ok := ctx.Value(CompanyIDKey).(string); ok {
		if parsed, err := uuid.Parse(companyIDStr); err == nil {
			tc.CompanyID = parsed
		}
	}

	return tc, nil
}

// GetTenantID extracts tenant ID from context
func GetTenantID(ctx context.Context) (uuid.UUID, error) {
	if tenantID, ok := ctx.Value(TenantIDKey).(uuid.UUID); ok {
		return tenantID, nil
	}
	if tenantIDStr, ok := ctx.Value(TenantIDKey).(string); ok {
		return uuid.Parse(tenantIDStr)
	}
	return uuid.Nil, errors.New("tenant_id not found in context")
}

// GetUserID extracts user ID from context
func GetUserID(ctx context.Context) (uuid.UUID, error) {
	if userID, ok := ctx.Value(UserIDKey).(uuid.UUID); ok {
		return userID, nil
	}
	if userIDStr, ok := ctx.Value(UserIDKey).(string); ok {
		return uuid.Parse(userIDStr)
	}
	return uuid.Nil, errors.New("user_id not found in context")
}

// GetUserLevel extracts user level from context
func GetUserLevel(ctx context.Context) int {
	if userLevel, ok := ctx.Value(UserLevelKey).(int); ok {
		return userLevel
	}
	return 0
}

// IsAdminContext checks if the context is an admin context
func IsAdminContext(ctx context.Context) bool {
	if isAdmin, ok := ctx.Value(IsAdminKey).(bool); ok {
		return isAdmin
	}
	return false
}

// TenantScopedDB wraps a GORM DB with automatic tenant context injection
type TenantScopedDB struct {
	db     *gorm.DB
	logger *logrus.Logger
}

// NewTenantScopedDB creates a new tenant-scoped database wrapper
func NewTenantScopedDB(db *gorm.DB, logger *logrus.Logger) *TenantScopedDB {
	if logger == nil {
		logger = logrus.New()
	}
	return &TenantScopedDB{
		db:     db,
		logger: logger,
	}
}

// WithContext returns a DB instance with tenant context set
func (tdb *TenantScopedDB) WithContext(ctx context.Context) (*gorm.DB, error) {
	tc, err := GetTenantContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get tenant context: %w", err)
	}

	// Start a new transaction with tenant context
	tx := tdb.db.WithContext(ctx).Begin()
	if tx.Error != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", tx.Error)
	}

	// Set tenant context in the transaction
	if err := SetTenantContextGorm(ctx, tx, tc); err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("failed to set tenant context: %w", err)
	}

	tdb.logger.WithFields(logrus.Fields{
		"tenant_id":  tc.TenantID.String(),
		"user_id":    tc.UserID.String(),
		"user_level": tc.UserLevel,
	}).Debug("Tenant context set for database operation")

	return tx, nil
}

// Transaction executes a function within a tenant-scoped transaction
func (tdb *TenantScopedDB) Transaction(ctx context.Context, fn func(tx *gorm.DB) error) error {
	tc, err := GetTenantContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get tenant context: %w", err)
	}

	return tdb.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Set tenant context at the beginning of transaction
		if err := SetTenantContextGorm(ctx, tx, tc); err != nil {
			return fmt.Errorf("failed to set tenant context: %w", err)
		}

		tdb.logger.WithFields(logrus.Fields{
			"tenant_id":  tc.TenantID.String(),
			"user_id":    tc.UserID.String(),
			"user_level": tc.UserLevel,
		}).Debug("Transaction started with tenant context")

		// Execute the business logic
		if err := fn(tx); err != nil {
			return err
		}

		return nil
	})
}

// ConnectionPoolConfig holds connection pool configuration
type ConnectionPoolConfig struct {
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
	ConnMaxIdleTime time.Duration
}

// DefaultPoolConfig returns default connection pool settings
func DefaultPoolConfig() *ConnectionPoolConfig {
	return &ConnectionPoolConfig{
		MaxOpenConns:    25,
		MaxIdleConns:    10,
		ConnMaxLifetime: 5 * time.Minute,
		ConnMaxIdleTime: 2 * time.Minute,
	}
}

// ConfigurePool configures the database connection pool
func ConfigurePool(db *sql.DB, config *ConnectionPoolConfig) {
	if config == nil {
		config = DefaultPoolConfig()
	}

	db.SetMaxOpenConns(config.MaxOpenConns)
	db.SetMaxIdleConns(config.MaxIdleConns)
	db.SetConnMaxLifetime(config.ConnMaxLifetime)
	db.SetConnMaxIdleTime(config.ConnMaxIdleTime)
}

// HealthCheck performs a database health check with tenant context
func HealthCheck(ctx context.Context, db *sql.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	return db.PingContext(ctx)
}

// HealthCheckGorm performs a database health check with GORM
func HealthCheckGorm(ctx context.Context, db *gorm.DB) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("failed to get underlying DB: %w", err)
	}

	return sqlDB.PingContext(ctx)
}
