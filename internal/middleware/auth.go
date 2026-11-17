// Package middleware provides HTTP middleware for authentication and authorization.
// This package provides a unified authentication middleware that can be used across
// all services (MCP Server, Gateway, REST API) with configurable options.
package middleware

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
)

// AuthConfig holds configuration for the authentication middleware
type AuthConfig struct {
	// JWTSecret is the secret key for JWT validation (from Kong Gateway)
	JWTSecret string

	// Enabled determines if authentication is required
	Enabled bool

	// SkipPaths are paths that should skip authentication (e.g., /health, /metrics)
	SkipPaths []string

	// RequireLevel is the minimum user level required (0 = no level check)
	RequireLevel int

	// AllowInternalGateway allows requests with X-Internal-Gateway header to bypass auth
	AllowInternalGateway bool

	// InternalGatewayToken is the token required for internal gateway requests
	InternalGatewayToken string

	// AuthServiceURL is the URL of the Auth Service for OAuth operations
	AuthServiceURL string

	// Logger for middleware logging
	Logger *logrus.Logger
}

// OAuthProvider represents a supported OAuth provider
type OAuthProvider string

const (
	OAuthProviderGoogle OAuthProvider = "google"
	OAuthProviderTikTok OAuthProvider = "tiktok"
	OAuthProviderApple  OAuthProvider = "apple"
)

// OAuthConfig holds configuration for OAuth providers
type OAuthConfig struct {
	// AuthServiceURL is the base URL of the Auth Service
	AuthServiceURL string

	// SupportedProviders lists the enabled OAuth providers
	SupportedProviders []OAuthProvider

	// Logger for OAuth operations
	Logger *logrus.Logger
}

// DefaultOAuthConfig returns default OAuth configuration
func DefaultOAuthConfig() *OAuthConfig {
	return &OAuthConfig{
		AuthServiceURL:     "http://localhost:10100", // SEC-9: Auth service is :10100, not :10101 (Company)
		SupportedProviders: []OAuthProvider{OAuthProviderGoogle},
	}
}

// DefaultAuthConfig returns a default configuration for the auth middleware
func DefaultAuthConfig() *AuthConfig {
	return &AuthConfig{
		Enabled:              false, // Disabled by default for backwards compatibility
		SkipPaths:            []string{"/health", "/metrics", "/ready", "/info"},
		RequireLevel:         0,
		AllowInternalGateway: true,
		// No default token: must be supplied from configuration (env). Empty
		// disables the internal-gateway bypass (fail closed).
		InternalGatewayToken: "",
	}
}

// JWTClaims represents the JWT claims structure from Kong Gateway
type JWTClaims struct {
	UserID    string  `json:"user_id"`
	TenantID  string  `json:"tenant_id"`
	CompanyID string  `json:"company_id"`
	Level     float64 `json:"level"`
	Email     string  `json:"email"`
	Phone     string  `json:"phone"`
	jwt.RegisteredClaims
}

// AuthMiddleware provides JWT authentication for Gin handlers
type AuthMiddleware struct {
	config   *AuthConfig
	logger   *logrus.Logger
	verifier JWTVerifier // optional; if non-nil, delegates token verification
}

// NewAuthMiddleware creates a new authentication middleware instance
func NewAuthMiddleware(config *AuthConfig) *AuthMiddleware {
	if config == nil {
		config = DefaultAuthConfig()
	}

	logger := config.Logger
	if logger == nil {
		logger = logrus.New()
	}

	return &AuthMiddleware{
		config: config,
		logger: logger,
	}
}

// WithVerifier returns a copy of the middleware that delegates JWT verification
// to the provided JWTVerifier instead of the built-in HMAC path.
// This enables transparent swapping between HMAC and OIDC verifiers (H1.0/H1.3).
func (am *AuthMiddleware) WithVerifier(v JWTVerifier) *AuthMiddleware {
	return &AuthMiddleware{
		config:   am.config,
		logger:   am.logger,
		verifier: v,
	}
}

// Handler returns the Gin middleware handler for authentication
func (am *AuthMiddleware) Handler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Skip if auth is disabled
		if !am.config.Enabled {
			c.Next()
			return
		}

		// Check if path should be skipped
		if am.shouldSkipPath(c.Request.URL.Path) {
			c.Next()
			return
		}

		// Check for internal gateway bypass
		if am.config.AllowInternalGateway && am.isInternalGatewayRequest(c) {
			am.logger.WithFields(logrus.Fields{
				"path":   c.Request.URL.Path,
				"method": c.Request.Method,
			}).Debug("Internal gateway request - bypassing auth")
			c.Next()
			return
		}

		// Extract and validate JWT
		claims, err := am.extractAndValidateJWT(c)
		if err != nil {
			am.respondUnauthorized(c, err.Error())
			return
		}

		// Check user level if required
		userLevel := int(claims.Level)
		if am.config.RequireLevel > 0 && userLevel < am.config.RequireLevel {
			am.respondForbidden(c, "Insufficient permissions", userLevel, am.config.RequireLevel)
			return
		}

		// Set context values for downstream handlers
		am.setContextValues(c, claims)

		am.logger.WithFields(logrus.Fields{
			"user_id":   claims.UserID,
			"tenant_id": claims.TenantID,
			"level":     userLevel,
			"path":      c.Request.URL.Path,
		}).Debug("Authentication successful")

		c.Next()
	}
}

// OptionalHandler returns a middleware that extracts JWT claims if present but doesn't require auth
func (am *AuthMiddleware) OptionalHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Try to extract JWT if present
		authHeader := c.GetHeader("Authorization")
		if authHeader != "" {
			claims, err := am.parseJWT(authHeader)
			if err == nil {
				am.setContextValues(c, claims)
			}
		}

		// Also check for header overrides
		if tenantID := c.GetHeader("X-Tenant-ID"); tenantID != "" {
			c.Set("tenant_id", tenantID)
		}
		if companyID := c.GetHeader("X-Company-ID"); companyID != "" {
			c.Set("company_id", companyID)
		}

		c.Next()
	}
}

// RequireLevel returns a middleware that requires a minimum user level
func (am *AuthMiddleware) RequireLevel(minLevel int) gin.HandlerFunc {
	return func(c *gin.Context) {
		userLevel, exists := c.Get("user_level")
		if !exists {
			am.respondForbidden(c, "User level not found", 0, minLevel)
			return
		}

		currentLevel, ok := userLevel.(int)
		if !ok {
			am.respondForbidden(c, "Invalid user level format", 0, minLevel)
			return
		}

		if currentLevel < minLevel {
			am.respondForbidden(c, "Insufficient permissions", currentLevel, minLevel)
			return
		}

		c.Next()
	}
}

// RequireFeature returns a middleware that requires access to a specific feature
func (am *AuthMiddleware) RequireFeature(feature string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userLevel, exists := c.Get("user_level")
		if !exists {
			am.respondFeatureDenied(c, feature, "User level not found")
			return
		}

		currentLevel, ok := userLevel.(int)
		if !ok {
			am.respondFeatureDenied(c, feature, "Invalid user level format")
			return
		}

		// Feature to level mapping
		requiredLevel := am.getRequiredLevelForFeature(feature)
		if currentLevel < requiredLevel {
			am.respondFeatureDenied(c, feature, fmt.Sprintf("Requires level %d, current level %d", requiredLevel, currentLevel))
			return
		}

		c.Next()
	}
}

// extractAndValidateJWT extracts and validates the JWT from the Authorization header
func (am *AuthMiddleware) extractAndValidateJWT(c *gin.Context) (*JWTClaims, error) {
	authHeader := c.GetHeader("Authorization")
	if authHeader == "" {
		return nil, fmt.Errorf("missing Authorization header")
	}

	return am.parseJWT(authHeader)
}

// parseJWT parses and validates the JWT token.
// When a JWTVerifier is configured it delegates to verifier.Verify for
// provider-agnostic validation (e.g. OIDC). Otherwise it falls back to the
// built-in HMAC path for backward compatibility.
func (am *AuthMiddleware) parseJWT(authHeader string) (*JWTClaims, error) {
	// Extract bearer token
	parts := strings.SplitN(authHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return nil, fmt.Errorf("invalid Authorization header format")
	}

	tokenString := parts[1]

	// Delegate to injected verifier when available (H1.3).
	// The JWTVerifier interface returns jwt.MapClaims; we map them to JWTClaims.
	if am.verifier != nil {
		mapClaims, err := am.verifier.Verify(context.Background(), tokenString)
		if err != nil {
			return nil, fmt.Errorf("token verification failed: %w", err)
		}
		return jwtClaimsFromMap(mapClaims), nil
	}

	// Default HMAC path (backward-compatible)
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return []byte(am.config.JWTSecret), nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(*JWTClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}

// jwtClaimsFromMap converts jwt.MapClaims (returned by JWTVerifier) to *JWTClaims.
func jwtClaimsFromMap(m jwt.MapClaims) *JWTClaims {
	getString := func(key string) string {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}
	getFloat := func(key string) float64 {
		if v, ok := m[key]; ok {
			switch n := v.(type) {
			case float64:
				return n
			case int:
				return float64(n)
			}
		}
		return 0
	}

	return &JWTClaims{
		UserID:    getString("sub"),
		TenantID:  getString("tenant_id"),
		CompanyID: getString("company_id"),
		Level:     getFloat("level"),
		Email:     getString("email"),
		Phone:     getString("phone"),
	}
}

// setContextValues sets the extracted claims in the Gin context
func (am *AuthMiddleware) setContextValues(c *gin.Context, claims *JWTClaims) {
	c.Set("user_id", claims.UserID)
	c.Set("user_level", int(claims.Level))
	c.Set("kong_level", int(claims.Level)) // Alias for compatibility

	// Set tenant_id from JWT or header override
	tenantID := c.GetHeader("X-Tenant-ID")
	if tenantID == "" {
		tenantID = claims.TenantID
	}
	c.Set("tenant_id", tenantID)

	// Set company_id from JWT or header override
	companyID := c.GetHeader("X-Company-ID")
	if companyID == "" {
		companyID = claims.CompanyID
	}
	c.Set("company_id", companyID)

	// Set additional claims
	if claims.Email != "" {
		c.Set("email", claims.Email)
	}
	if claims.Phone != "" {
		c.Set("phone", claims.Phone)
	}

	// Add to request context for downstream services
	ctx := context.WithValue(c.Request.Context(), "user_id", claims.UserID)
	ctx = context.WithValue(ctx, "tenant_id", tenantID)
	ctx = context.WithValue(ctx, "user_level", int(claims.Level))
	c.Request = c.Request.WithContext(ctx)
}

// shouldSkipPath checks if the path should skip authentication
func (am *AuthMiddleware) shouldSkipPath(path string) bool {
	for _, skipPath := range am.config.SkipPaths {
		if strings.HasPrefix(path, skipPath) {
			return true
		}
	}
	return false
}

// isInternalGatewayRequest checks if the request is from the internal gateway
func (am *AuthMiddleware) isInternalGatewayRequest(c *gin.Context) bool {
	if c.GetHeader("X-Internal-Gateway") != "true" {
		return false
	}

	// Fail closed: only trust the internal-gateway bypass when a shared token is
	// configured, and compare it in constant time.
	if am.config.InternalGatewayToken == "" {
		return false
	}
	return subtle.ConstantTimeCompare(
		[]byte(c.GetHeader("X-Gateway-Token")),
		[]byte(am.config.InternalGatewayToken),
	) == 1
}

// getRequiredLevelForFeature returns the required level for a feature
func (am *AuthMiddleware) getRequiredLevelForFeature(feature string) int {
	featureLevels := map[string]int{
		"basic_trading":      1,
		"market_data":        1,
		"signals":            2,
		"backtesting":        2,
		"real_trading":       3,
		"risk_management":    3,
		"ai_analysis":        3,
		"admin":              4,
		"bulk_operations":    4,
		"enterprise_reports": 4,
	}

	if level, exists := featureLevels[feature]; exists {
		return level
	}
	return 1 // Default to basic level
}

// Response helpers

func (am *AuthMiddleware) respondUnauthorized(c *gin.Context, message string) {
	am.logger.WithFields(logrus.Fields{
		"path":      c.Request.URL.Path,
		"method":    c.Request.Method,
		"client_ip": c.ClientIP(),
		"error":     message,
	}).Warn("Authentication failed")

	c.JSON(http.StatusUnauthorized, gin.H{
		"success": false,
		"error": gin.H{
			"code":    "UNAUTHORIZED",
			"message": message,
		},
		"meta": gin.H{
			"timestamp": time.Now().Format(time.RFC3339),
		},
	})
	c.Abort()
}

func (am *AuthMiddleware) respondForbidden(c *gin.Context, message string, currentLevel, requiredLevel int) {
	am.logger.WithFields(logrus.Fields{
		"path":           c.Request.URL.Path,
		"current_level":  currentLevel,
		"required_level": requiredLevel,
		"user_id":        c.GetString("user_id"),
	}).Warn("Authorization failed - insufficient level")

	c.JSON(http.StatusForbidden, gin.H{
		"success": false,
		"error": gin.H{
			"code":           "FORBIDDEN",
			"message":        message,
			"current_level":  currentLevel,
			"required_level": requiredLevel,
		},
		"meta": gin.H{
			"timestamp": time.Now().Format(time.RFC3339),
		},
	})
	c.Abort()
}

func (am *AuthMiddleware) respondFeatureDenied(c *gin.Context, feature, message string) {
	am.logger.WithFields(logrus.Fields{
		"path":    c.Request.URL.Path,
		"feature": feature,
		"user_id": c.GetString("user_id"),
	}).Warn("Feature access denied")

	c.JSON(http.StatusForbidden, gin.H{
		"success": false,
		"error": gin.H{
			"code":    "FEATURE_ACCESS_DENIED",
			"message": message,
			"feature": feature,
		},
		"meta": gin.H{
			"timestamp": time.Now().Format(time.RFC3339),
		},
	})
	c.Abort()
}

// Helper functions for extracting values from context
// Note: These use Auth prefix to avoid conflicts with tenant.go helpers

// AuthGetUserID extracts user ID from Gin context
func AuthGetUserID(c *gin.Context) (string, bool) {
	userID, exists := c.Get("user_id")
	if !exists {
		return "", false
	}
	str, ok := userID.(string)
	return str, ok
}

// AuthGetUserLevel extracts user level from Gin context
func AuthGetUserLevel(c *gin.Context) (int, bool) {
	level, exists := c.Get("user_level")
	if !exists {
		return 0, false
	}
	l, ok := level.(int)
	return l, ok
}

// AuthGetTenantID extracts tenant ID from Gin context
func AuthGetTenantID(c *gin.Context) (string, bool) {
	tenantID, exists := c.Get("tenant_id")
	if !exists {
		return "", false
	}
	str, ok := tenantID.(string)
	return str, ok
}

// AuthGetCompanyID extracts company ID from Gin context
func AuthGetCompanyID(c *gin.Context) (string, bool) {
	companyID, exists := c.Get("company_id")
	if !exists {
		return "", false
	}
	str, ok := companyID.(string)
	return str, ok
}

// AuthIsAuthenticated checks if the request has valid authentication
func AuthIsAuthenticated(c *gin.Context) bool {
	_, exists := c.Get("user_id")
	return exists
}

// AuthHasMinimumLevel checks if the user has at least the specified level
func AuthHasMinimumLevel(c *gin.Context, minLevel int) bool {
	level, exists := AuthGetUserLevel(c)
	if !exists {
		return false
	}
	return level >= minLevel
}

// =============================================================================
// OAuth / Social Authentication Support
// =============================================================================

// OAuthService provides OAuth authentication functionality
type OAuthService struct {
	config *OAuthConfig
	logger *logrus.Logger
	client *http.Client
}

// NewOAuthService creates a new OAuth service instance
func NewOAuthService(config *OAuthConfig) *OAuthService {
	if config == nil {
		config = DefaultOAuthConfig()
	}

	logger := config.Logger
	if logger == nil {
		logger = logrus.New()
	}

	return &OAuthService{
		config: config,
		logger: logger,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// OAuthAuthorizationResponse represents the response from OAuth authorization endpoint
type OAuthAuthorizationResponse struct {
	AuthorizationURL string `json:"authorization_url"`
	State            string `json:"state"`
	ClientID         string `json:"client_id,omitempty"`
}

// OAuthTokenResponse represents the response from OAuth token exchange
type OAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token,omitempty"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token,omitempty"`
}

// SocialAuthRequest represents a social authentication request
type SocialAuthRequest struct {
	Provider         string `json:"provider" binding:"required"`
	Code             string `json:"code" binding:"required"`
	State            string `json:"state" binding:"required"`
	AcceptTerms      bool   `json:"accept_terms"`
	AcceptPrivacy    bool   `json:"accept_privacy"`
	MarketingConsent bool   `json:"marketing_consent"`
}

// SocialAuthResponse represents the response from social authentication
type SocialAuthResponse struct {
	Success      bool        `json:"success"`
	AccessToken  string      `json:"access_token,omitempty"`
	RefreshToken string      `json:"refresh_token,omitempty"`
	ExpiresIn    int         `json:"expires_in,omitempty"`
	TokenType    string      `json:"token_type,omitempty"`
	User         *SocialUser `json:"user,omitempty"`
	Error        string      `json:"error,omitempty"`
	Message      string      `json:"message,omitempty"`
}

// SocialUser represents user information from social provider
type SocialUser struct {
	ID        string `json:"id"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Picture   string `json:"picture,omitempty"`
	Provider  string `json:"provider"`
	TenantID  string `json:"tenant_id,omitempty"`
	CompanyID string `json:"company_id,omitempty"`
	Level     int    `json:"level"`
}

// GetAuthorizationURL returns the OAuth authorization URL for a provider
func (s *OAuthService) GetAuthorizationURL(provider OAuthProvider) (*OAuthAuthorizationResponse, error) {
	if !s.isProviderSupported(provider) {
		return nil, fmt.Errorf("unsupported OAuth provider: %s", provider)
	}

	url := fmt.Sprintf("%s/api/v1/auth/oauth/%s/authorize", s.config.AuthServiceURL, provider)

	resp, err := s.client.Get(url)
	if err != nil {
		s.logger.WithError(err).WithField("provider", provider).Error("Failed to get OAuth authorization URL")
		return nil, fmt.Errorf("failed to get authorization URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth service returned status %d", resp.StatusCode)
	}

	var authResp OAuthAuthorizationResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	return &authResp, nil
}

// ExchangeCode exchanges an authorization code for tokens
func (s *OAuthService) ExchangeCode(req *SocialAuthRequest) (*SocialAuthResponse, error) {
	provider := OAuthProvider(req.Provider)
	if !s.isProviderSupported(provider) {
		return nil, fmt.Errorf("unsupported OAuth provider: %s", req.Provider)
	}

	url := fmt.Sprintf("%s/api/v1/auth/login/social", s.config.AuthServiceURL)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		s.logger.WithError(err).WithField("provider", req.Provider).Error("Failed to exchange OAuth code")
		return nil, fmt.Errorf("failed to exchange code: %w", err)
	}
	defer resp.Body.Close()

	var authResp SocialAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return &authResp, fmt.Errorf("auth service returned status %d: %s", resp.StatusCode, authResp.Error)
	}

	return &authResp, nil
}

// RegisterSocial registers a new user via social authentication
func (s *OAuthService) RegisterSocial(req *SocialAuthRequest) (*SocialAuthResponse, error) {
	provider := OAuthProvider(req.Provider)
	if !s.isProviderSupported(provider) {
		return nil, fmt.Errorf("unsupported OAuth provider: %s", req.Provider)
	}

	url := fmt.Sprintf("%s/api/v1/auth/register/social", s.config.AuthServiceURL)

	body, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	httpReq, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(httpReq)
	if err != nil {
		s.logger.WithError(err).WithField("provider", req.Provider).Error("Failed to register via social auth")
		return nil, fmt.Errorf("failed to register: %w", err)
	}
	defer resp.Body.Close()

	var authResp SocialAuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return &authResp, fmt.Errorf("auth service returned status %d: %s", resp.StatusCode, authResp.Error)
	}

	return &authResp, nil
}

// isProviderSupported checks if a provider is in the supported list
func (s *OAuthService) isProviderSupported(provider OAuthProvider) bool {
	for _, p := range s.config.SupportedProviders {
		if p == provider {
			return true
		}
	}
	return false
}

// GetSupportedProviders returns the list of supported OAuth providers
func (s *OAuthService) GetSupportedProviders() []OAuthProvider {
	return s.config.SupportedProviders
}

// =============================================================================
// OAuth Gin Handlers
// =============================================================================

// OAuthHandlers provides HTTP handlers for OAuth operations
type OAuthHandlers struct {
	service *OAuthService
	logger  *logrus.Logger
}

// NewOAuthHandlers creates new OAuth handlers
func NewOAuthHandlers(service *OAuthService) *OAuthHandlers {
	logger := logrus.New()
	if service != nil && service.logger != nil {
		logger = service.logger
	}

	return &OAuthHandlers{
		service: service,
		logger:  logger,
	}
}

// HandleGetAuthorizationURL handles GET /auth/oauth/:provider/authorize
func (h *OAuthHandlers) HandleGetAuthorizationURL(c *gin.Context) {
	provider := OAuthProvider(c.Param("provider"))

	authResp, err := h.service.GetAuthorizationURL(provider)
	if err != nil {
		h.logger.WithError(err).WithField("provider", provider).Error("Failed to get authorization URL")
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "OAUTH_ERROR",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":           true,
		"authorization_url": authResp.AuthorizationURL,
		"state":             authResp.State,
		"provider":          provider,
	})
}

// HandleSocialLogin handles POST /auth/login/social
func (h *OAuthHandlers) HandleSocialLogin(c *gin.Context) {
	var req SocialAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "INVALID_REQUEST",
				"message": err.Error(),
			},
		})
		return
	}

	authResp, err := h.service.ExchangeCode(&req)
	if err != nil {
		h.logger.WithError(err).WithField("provider", req.Provider).Error("Social login failed")
		statusCode := http.StatusInternalServerError
		if authResp != nil {
			c.JSON(statusCode, authResp)
			return
		}
		c.JSON(statusCode, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "OAUTH_ERROR",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusOK, authResp)
}

// HandleSocialRegister handles POST /auth/register/social
func (h *OAuthHandlers) HandleSocialRegister(c *gin.Context) {
	var req SocialAuthRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "INVALID_REQUEST",
				"message": err.Error(),
			},
		})
		return
	}

	// Validate terms acceptance for registration
	if !req.AcceptTerms || !req.AcceptPrivacy {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "TERMS_NOT_ACCEPTED",
				"message": "You must accept terms and privacy policy to register",
			},
		})
		return
	}

	authResp, err := h.service.RegisterSocial(&req)
	if err != nil {
		h.logger.WithError(err).WithField("provider", req.Provider).Error("Social registration failed")
		statusCode := http.StatusInternalServerError
		if authResp != nil {
			c.JSON(statusCode, authResp)
			return
		}
		c.JSON(statusCode, gin.H{
			"success": false,
			"error": gin.H{
				"code":    "OAUTH_ERROR",
				"message": err.Error(),
			},
		})
		return
	}

	c.JSON(http.StatusCreated, authResp)
}

// HandleGetProviders handles GET /auth/providers
func (h *OAuthHandlers) HandleGetProviders(c *gin.Context) {
	providers := h.service.GetSupportedProviders()

	providerList := make([]gin.H, len(providers))
	for i, p := range providers {
		providerList[i] = gin.H{
			"id":   string(p),
			"name": getProviderDisplayName(p),
			"icon": getProviderIcon(p),
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   true,
		"providers": providerList,
	})
}

// RegisterOAuthRoutes registers OAuth routes on a Gin router group
func (h *OAuthHandlers) RegisterOAuthRoutes(group *gin.RouterGroup) {
	auth := group.Group("/auth")
	{
		// OAuth authorization
		auth.GET("/oauth/:provider/authorize", h.HandleGetAuthorizationURL)

		// Social authentication
		auth.POST("/login/social", h.HandleSocialLogin)
		auth.POST("/register/social", h.HandleSocialRegister)

		// Provider info
		auth.GET("/providers", h.HandleGetProviders)
	}
}

// Helper functions for provider display

func getProviderDisplayName(provider OAuthProvider) string {
	names := map[OAuthProvider]string{
		OAuthProviderGoogle: "Google",
		OAuthProviderTikTok: "TikTok",
		OAuthProviderApple:  "Apple",
	}
	if name, ok := names[provider]; ok {
		return name
	}
	return string(provider)
}

func getProviderIcon(provider OAuthProvider) string {
	icons := map[OAuthProvider]string{
		OAuthProviderGoogle: "google",
		OAuthProviderTikTok: "tiktok",
		OAuthProviderApple:  "apple",
	}
	if icon, ok := icons[provider]; ok {
		return icon
	}
	return "default"
}
