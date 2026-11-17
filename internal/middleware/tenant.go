// Package middleware provides HTTP middleware for the trading-mcp service,
// including tenant isolation, authentication, and request processing.
package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"

	"github.com/rantipay/trading-mcp/internal/database"
	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

// TenantConfig holds configuration for the tenant middleware
type TenantConfig struct {
	// JWTSecret is the secret key for JWT validation
	JWTSecret string

	// RequireTenant determines if tenant_id is mandatory
	RequireTenant bool

	// AllowHeaderOverride allows X-Tenant-ID header to override JWT tenant
	AllowHeaderOverride bool

	// SkipPaths are paths that should skip tenant validation
	SkipPaths []string

	// Logger for middleware logging
	Logger *logrus.Logger
}

// TenantMiddleware extracts tenant context from JWT and sets it for RLS
type TenantMiddleware struct {
	config *TenantConfig
	db     *gorm.DB
	logger *logrus.Logger
}

// NewTenantMiddleware creates a new tenant middleware instance
func NewTenantMiddleware(config *TenantConfig, db *gorm.DB) *TenantMiddleware {
	if config == nil {
		config = &TenantConfig{
			RequireTenant: true,
		}
	}

	logger := config.Logger
	if logger == nil {
		logger = logrus.New()
	}

	return &TenantMiddleware{
		config: config,
		db:     db,
		logger: logger,
	}
}

// TenantJWTClaims represents the JWT claims structure with tenant information
type TenantJWTClaims struct {
	UserID    string  `json:"user_id"`
	TenantID  string  `json:"tenant_id"`
	CompanyID string  `json:"company_id"`
	// UserLevel is the claim the RantiPay auth service actually mints
	// (pkg/jwt: `Level int json:"user_level"`). The legacy `level` claim is
	// kept only as a fallback. Reading `level` alone made this middleware set
	// user_level=0 and clobber the correct value KongJWTMiddleware already set,
	// 403-ing every real token on Level 2+ gated routes (e.g. xsec rebalance-live).
	UserLevel float64 `json:"user_level"`
	Level     float64 `json:"level"`
	Email     string  `json:"email"`
	Phone     string  `json:"phone"`
	jwt.RegisteredClaims
}

// Handler returns the Gin middleware handler for tenant extraction
func (tm *TenantMiddleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Check if path should be skipped
		if tm.shouldSkipPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		// Extract tenant context from request
		tc, err := tm.extractTenantContext(c)
		if err != nil {
			if tm.config.RequireTenant {
				tm.logger.WithError(err).WithFields(logrus.Fields{
					"path":      c.Request.URL.Path,
					"method":    c.Request.Method,
					"client_ip": c.ClientIP(),
				}).Warn("Failed to extract tenant context")

				c.JSON(http.StatusUnauthorized, gin.H{
					"success": false,
					"error": gin.H{
						"code":    "TENANT_CONTEXT_REQUIRED",
						"message": "Valid tenant context is required for this operation",
						"detail":  err.Error(),
					},
					"meta": gin.H{
						"timestamp": time.Now().Format(time.RFC3339),
					},
				})
				c.Abort()
				return
			}

			// If tenant is not required, continue without context
			c.Next()
			return
		}

		// Set tenant context in Gin context for use in handlers
		c.Set("tenant_id", tc.TenantID.String())
		c.Set("user_id", tc.UserID.String())
		c.Set("user_level", tc.UserLevel)
		c.Set("is_admin", tc.IsAdmin)
		if tc.CompanyID != uuid.Nil {
			c.Set("company_id", tc.CompanyID.String())
		}

		// Add tenant context to request context for database operations
		ctx := database.WithTenantContext(c.Request.Context(), tc)
		c.Request = c.Request.WithContext(ctx)

		// If database is configured, set session variables for RLS
		if tm.db != nil {
			if err := tm.setDatabaseContext(c.Request.Context(), tc); err != nil {
				tm.logger.WithError(err).WithFields(logrus.Fields{
					"tenant_id": tc.TenantID.String(),
					"user_id":   tc.UserID.String(),
				}).Error("Failed to set database tenant context")

				c.JSON(http.StatusInternalServerError, gin.H{
					"success": false,
					"error": gin.H{
						"code":    "DATABASE_CONTEXT_ERROR",
						"message": "Failed to initialize database context",
					},
					"meta": gin.H{
						"timestamp": time.Now().Format(time.RFC3339),
					},
				})
				c.Abort()
				return
			}
		}

		tm.logger.WithFields(logrus.Fields{
			"tenant_id":  tc.TenantID.String(),
			"user_id":    tc.UserID.String(),
			"user_level": tc.UserLevel,
			"path":       c.Request.URL.Path,
		}).Debug("Tenant context established")

		c.Next()
	}
}

// extractTenantContext extracts tenant information from JWT or headers
func (tm *TenantMiddleware) extractTenantContext(c *gin.Context) (*database.TenantContext, error) {
	tc := &database.TenantContext{}

	// First, try to extract from JWT
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		claims, err := tm.parseJWT(authHeader)
		if err != nil {
			return nil, fmt.Errorf("invalid authorization token: %w", err)
		}

		// Extract tenant ID from JWT
		if claims.TenantID != "" {
			tenantID, err := uuid.Parse(claims.TenantID)
			if err != nil {
				return nil, fmt.Errorf("invalid tenant_id in token: %w", err)
			}
			tc.TenantID = tenantID
		}

		// Extract user ID from JWT
		if claims.UserID != "" {
			userID, err := uuid.Parse(claims.UserID)
			if err != nil {
				return nil, fmt.Errorf("invalid user_id in token: %w", err)
			}
			tc.UserID = userID
		}

		// Extract company ID from JWT
		if claims.CompanyID != "" {
			companyID, err := uuid.Parse(claims.CompanyID)
			if err == nil {
				tc.CompanyID = companyID
			}
		}

		// Extract user level — prefer the `user_level` claim the auth service
		// mints, falling back to the legacy `level` claim. Never downgrade a
		// value already resolved upstream: only overwrite when we actually read
		// a level here, so a token missing both claims doesn't clobber it to 0.
		if lvl := int(claims.UserLevel); lvl > 0 {
			tc.UserLevel = lvl
		} else if lvl := int(claims.Level); lvl > 0 {
			tc.UserLevel = lvl
		}
		tc.IsAdmin = tc.UserLevel >= 4
	}

	// Allow header override if configured
	// Task 6.10: the headers are only honoured when configured explicitly or
	// when the upstream JWT middleware accepted the request through the
	// internal gateway (valid X-Gateway-Token), which sets jwt_verified=false.
	if verified, ok := c.Get("jwt_verified"); tm.config.AllowHeaderOverride || (ok && verified == false) {
		// X-Tenant-ID header can override JWT tenant
		if tenantIDHeader := c.GetHeader("X-Tenant-ID"); tenantIDHeader != "" {
			tenantID, err := uuid.Parse(tenantIDHeader)
			if err == nil {
				tc.TenantID = tenantID
			}
		}

		// X-Company-ID header can override JWT company
		if companyIDHeader := c.GetHeader("X-Company-ID"); companyIDHeader != "" {
			companyID, err := uuid.Parse(companyIDHeader)
			if err == nil {
				tc.CompanyID = companyID
			}
		}
	}

	// Validate that we have required fields
	if err := tc.Validate(); err != nil {
		return nil, err
	}

	return tc, nil
}

// parseJWT parses and validates the JWT token
func (tm *TenantMiddleware) parseJWT(authHeader string) (*TenantJWTClaims, error) {
	// Extract bearer token
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return nil, fmt.Errorf("invalid authorization header format")
	}

	tokenString := parts[1]

	// Parse and validate token
	token, err := jwt.ParseWithClaims(tokenString, &TenantJWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(tradingmw.EffectiveSecret(tm.config.JWTSecret)), nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(*TenantJWTClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}

// setDatabaseContext sets PostgreSQL session variables for RLS
func (tm *TenantMiddleware) setDatabaseContext(ctx context.Context, tc *database.TenantContext) error {
	return database.SetTenantContextGorm(ctx, tm.db, tc)
}

// shouldSkipPath checks if the path should skip tenant validation
func (tm *TenantMiddleware) shouldSkipPath(path string) bool {
	for _, skipPath := range tm.config.SkipPaths {
		if strings.HasPrefix(path, skipPath) {
			return true
		}
	}
	return false
}

// RequireTenant creates a middleware that requires tenant context
func RequireTenant(logger *logrus.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = logrus.New()
	}

	return func(c *gin.Context) {
		tenantID := c.GetString("tenant_id")
		if tenantID == "" {
			logger.WithFields(logrus.Fields{
				"path":      c.Request.URL.Path,
				"method":    c.Request.Method,
				"client_ip": c.ClientIP(),
			}).Warn("Tenant ID required but not found")

			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "TENANT_REQUIRED",
					"message": "This endpoint requires a valid tenant context",
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireUser creates a middleware that requires user context
func RequireUser(logger *logrus.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = logrus.New()
	}

	return func(c *gin.Context) {
		userID := c.GetString("user_id")
		if userID == "" {
			logger.WithFields(logrus.Fields{
				"path":      c.Request.URL.Path,
				"method":    c.Request.Method,
				"client_ip": c.ClientIP(),
			}).Warn("User ID required but not found")

			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "USER_REQUIRED",
					"message": "This endpoint requires a valid user context",
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireLevel creates a middleware that requires a minimum user level
func RequireLevel(minLevel int, logger *logrus.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = logrus.New()
	}

	return func(c *gin.Context) {
		userLevel, exists := c.Get("user_level")
		if !exists {
			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "LEVEL_REQUIRED",
					"message": "User level information is required",
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		currentLevel, ok := userLevel.(int)
		if !ok {
			c.JSON(http.StatusInternalServerError, gin.H{
				"success": false,
				"error": gin.H{
					"code":    "INVALID_LEVEL",
					"message": "Invalid user level format",
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		if currentLevel < minLevel {
			logger.WithFields(logrus.Fields{
				"path":           c.Request.URL.Path,
				"current_level":  currentLevel,
				"required_level": minLevel,
				"user_id":        c.GetString("user_id"),
				"tenant_id":      c.GetString("tenant_id"),
			}).Warn("Insufficient user level")

			c.JSON(http.StatusForbidden, gin.H{
				"success": false,
				"error": gin.H{
					"code":           "INSUFFICIENT_LEVEL",
					"message":        fmt.Sprintf("This operation requires level %d or higher", minLevel),
					"current_level":  currentLevel,
					"required_level": minLevel,
				},
				"meta": gin.H{
					"timestamp": time.Now().Format(time.RFC3339),
				},
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// RequireAdmin creates a middleware that requires admin privileges
func RequireAdmin(logger *logrus.Logger) gin.HandlerFunc {
	return RequireLevel(4, logger)
}

// GetTenantFromContext extracts tenant ID from Gin context
func GetTenantFromContext(c *gin.Context) (uuid.UUID, error) {
	tenantIDStr := c.GetString("tenant_id")
	if tenantIDStr == "" {
		return uuid.Nil, fmt.Errorf("tenant_id not found in context")
	}
	return uuid.Parse(tenantIDStr)
}

// GetUserFromContext extracts user ID from Gin context
func GetUserFromContext(c *gin.Context) (uuid.UUID, error) {
	userIDStr := c.GetString("user_id")
	if userIDStr == "" {
		return uuid.Nil, fmt.Errorf("user_id not found in context")
	}
	return uuid.Parse(userIDStr)
}

// GetUserLevelFromContext extracts user level from Gin context
func GetUserLevelFromContext(c *gin.Context) int {
	if level, exists := c.Get("user_level"); exists {
		if l, ok := level.(int); ok {
			return l
		}
	}
	return 0
}

// GetCompanyFromContext extracts company ID from Gin context
func GetCompanyFromContext(c *gin.Context) (uuid.UUID, error) {
	companyIDStr := c.GetString("company_id")
	if companyIDStr == "" {
		return uuid.Nil, fmt.Errorf("company_id not found in context")
	}
	return uuid.Parse(companyIDStr)
}

// IsAdminFromContext checks if the current user is an admin
func IsAdminFromContext(c *gin.Context) bool {
	if isAdmin, exists := c.Get("is_admin"); exists {
		if admin, ok := isAdmin.(bool); ok {
			return admin
		}
	}
	return false
}

// TenantAuditMiddleware logs all tenant-scoped operations for audit purposes
func TenantAuditMiddleware(logger *logrus.Logger) gin.HandlerFunc {
	if logger == nil {
		logger = logrus.New()
	}

	return func(c *gin.Context) {
		start := time.Now()

		// Process request
		c.Next()

		// Log audit entry after request completion
		duration := time.Since(start)

		logger.WithFields(logrus.Fields{
			"tenant_id":   c.GetString("tenant_id"),
			"user_id":     c.GetString("user_id"),
			"user_level":  c.GetInt("user_level"),
			"method":      c.Request.Method,
			"path":        c.Request.URL.Path,
			"status":      c.Writer.Status(),
			"duration_ms": duration.Milliseconds(),
			"client_ip":   c.ClientIP(),
			"user_agent":  c.Request.UserAgent(),
		}).Info("Tenant-scoped request processed")
	}
}
