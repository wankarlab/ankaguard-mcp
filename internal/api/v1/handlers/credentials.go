// Package handlers contains HTTP handlers for REST API v1
package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/credentials"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/sirupsen/logrus"
)

// CredentialsHandler handles credential management endpoints
type CredentialsHandler struct {
	credService credentials.Service
	gctClient   *gctclient.Client
	logger      *logrus.Logger
}

// NewCredentialsHandler creates a new credentials handler
func NewCredentialsHandler(credService credentials.Service, gctClient *gctclient.Client, logger *logrus.Logger) *CredentialsHandler {
	return &CredentialsHandler{
		credService: credService,
		gctClient:   gctClient,
		logger:      logger,
	}
}

// AddCredentials handles POST /api/v1/credentials
// Adds exchange API credentials for the user
func (h *CredentialsHandler) AddCredentials(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	tenantID, _ := c.Get("tenant_id")
	tenantIDStr, _ := tenantID.(string)
	if tenantIDStr == "" {
		tenantIDStr = "default"
	}

	// Level 1+ can manage their own credentials (any authenticated user).
	kongLevel, _ := middleware.GetKongLevelFromContext(c)
	_ = kongLevel

	var req models.AddCredentialsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_REQUEST", "Invalid request body", err.Error()))
		return
	}

	// Accept api_passphrase as alias for passphrase (legacy Flutter field name).
	if req.Passphrase == "" && req.APIPassphrase != "" {
		req.Passphrase = req.APIPassphrase
	}

	// Validate exchange
	if !credentials.IsValidExchange(req.Exchange) {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_EXCHANGE", "Exchange not supported"))
		return
	}

	// Check if passphrase is required
	if credentials.RequiresPassphrase(req.Exchange) && req.Passphrase == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Passphrase is required for "+req.Exchange))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": req.Exchange,
		"sandbox":  req.Sandbox,
	}).Info("Adding exchange credentials")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	input := &credentials.CreateCredentialInput{
		UserID:       userID,
		TenantID:     tenantIDStr,
		ExchangeName: req.Exchange,
		APIKey:       req.APIKey,
		APISecret:    req.APISecret,
		Passphrase:   req.Passphrase,
		Label:        req.Label,
	}

	summary, err := h.credService.AddCredential(ctx, input)
	if err != nil {
		h.logger.WithError(err).Error("Failed to add credentials")

		// Check for specific errors
		if err == credentials.ErrMaxCredentialsReached {
			c.JSON(http.StatusConflict, models.NewErrorResponse("MAX_CREDENTIALS", "Maximum number of credentials reached"))
			return
		}
		if err == credentials.ErrDuplicateCredential {
			c.JSON(http.StatusConflict, models.NewErrorResponse("DUPLICATE_CREDENTIAL", "Credential for this exchange already exists"))
			return
		}

		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("CREDENTIAL_ERROR", "Failed to add credentials"))
		return
	}

	// Configure the GCT client with the new credentials
	if h.gctClient != nil && summary.ExchangeName == "kucoin" {
		h.gctClient.SetExchangeCredentials(req.Exchange, req.APIKey, req.APISecret, req.Passphrase)
	}

	response := models.CredentialResponse{
		Exchange:   summary.ExchangeName,
		Label:      summary.Label,
		APIKeyHint: summary.APIKeyPrefix,
		Sandbox:    req.Sandbox,
		Status:     summary.ValidationStatus,
		CreatedAt:  summary.CreatedAt,
	}

	h.logger.WithField("exchange", req.Exchange).Info("Credentials added successfully")
	c.JSON(http.StatusCreated, models.NewSuccessResponse(response))
}

// ListCredentials handles GET /api/v1/credentials
// Lists all configured exchange credentials for the user
func (h *CredentialsHandler) ListCredentials(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	h.logger.WithField("user_id", userID).Debug("Listing credentials")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	summaries, err := h.credService.ListCredentials(ctx, userID)
	if err != nil {
		h.logger.WithError(err).Error("Failed to list credentials")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("CREDENTIAL_ERROR", "Failed to list credentials"))
		return
	}

	credentialResponses := make([]models.CredentialResponse, 0, len(summaries))
	for _, summary := range summaries {
		var lastUsed time.Time
		if summary.LastUsedAt != nil {
			lastUsed = *summary.LastUsedAt
		}

		credentialResponses = append(credentialResponses, models.CredentialResponse{
			Exchange:   summary.ExchangeName,
			Label:      summary.Label,
			APIKeyHint: summary.APIKeyPrefix,
			Sandbox:    false, // TODO: Add sandbox support to model
			Status:     summary.ValidationStatus,
			CreatedAt:  summary.CreatedAt,
			LastUsedAt: lastUsed,
		})
	}

	response := models.CredentialsListResponse{
		Credentials: credentialResponses,
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(response))
}

// DeleteCredentials handles DELETE /api/v1/credentials/:exchange
// Removes exchange credentials
func (h *CredentialsHandler) DeleteCredentials(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	// Level 1+ can manage their own credentials (any authenticated user).
	kongLevel, _ := middleware.GetKongLevelFromContext(c)
	_ = kongLevel

	exchange := c.Param("exchange")
	if exchange == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Exchange is required"))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": exchange,
	}).Info("Deleting credentials")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()

	err := h.credService.DeleteCredential(ctx, userID, exchange)
	if err != nil {
		h.logger.WithError(err).Error("Failed to delete credentials")

		if err == credentials.ErrCredentialNotFound {
			c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "Credential not found"))
			return
		}

		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("CREDENTIAL_ERROR", "Failed to delete credentials"))
		return
	}

	h.logger.WithField("exchange", exchange).Info("Credentials deleted successfully")
	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"exchange": exchange,
		"status":   "deleted",
		"message":  "Credentials deleted successfully",
	}))
}

// TestCredentials handles POST /api/v1/credentials/:exchange/test
// Tests if credentials are valid
func (h *CredentialsHandler) TestCredentials(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	exchange := c.Param("exchange")
	if exchange == "" {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("INVALID_REQUEST", "Exchange is required"))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": exchange,
	}).Info("Testing credentials")

	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	result, err := h.credService.TestCredential(ctx, userID, exchange)
	if err != nil {
		h.logger.WithError(err).Error("Failed to test credentials")

		if err == credentials.ErrCredentialNotFound {
			c.JSON(http.StatusNotFound, models.NewErrorResponse("NOT_FOUND", "Credential not found"))
			return
		}
		if err == credentials.ErrNoGCTClient {
			c.JSON(http.StatusServiceUnavailable, models.NewErrorResponse("SERVICE_UNAVAILABLE", "Trading service not available"))
			return
		}

		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("TEST_ERROR", "Failed to test credentials"))
		return
	}

	status := "invalid"
	if result.Success {
		status = "valid"
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"exchange":      exchange,
		"valid":         result.Success,
		"status":        status,
		"message":       result.Message,
		"permissions":   result.Permissions,
		"response_time": result.ResponseTime,
		"tested_at":     result.TestedAt,
	}))
}

// VerifyPermissions checks exchange API key permissions (withdraw, ip-lock).
// POST /api/v1/credentials/:exchange/verify-permissions
// TODO: delegated to credentials service VerifyNoWithdraw
func (h *CredentialsHandler) VerifyPermissions(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, gin.H{"error": "not yet implemented"})
}

// GetSupportedExchanges handles GET /api/v1/credentials/exchanges
// Returns list of supported exchanges
func (h *CredentialsHandler) GetSupportedExchanges(c *gin.Context) {
	exchanges := []gin.H{
		{"name": "kucoin", "display_name": "KuCoin", "requires_passphrase": true, "features": []string{"spot", "futures"}},
		{"name": "binance", "display_name": "Binance", "requires_passphrase": false, "features": []string{"spot", "futures", "margin"}},
		{"name": "coinbase", "display_name": "Coinbase", "requires_passphrase": true, "features": []string{"spot"}},
		{"name": "kraken", "display_name": "Kraken", "requires_passphrase": false, "features": []string{"spot", "futures"}},
		{"name": "bybit", "display_name": "Bybit", "requires_passphrase": false, "features": []string{"spot", "futures"}},
		{"name": "okx", "display_name": "OKX", "requires_passphrase": true, "features": []string{"spot", "futures"}},
		{"name": "bitget", "display_name": "Bitget", "requires_passphrase": true, "features": []string{"spot", "futures"}},
	}

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"exchanges": exchanges,
	}))
}
