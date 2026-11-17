// Package handlers contains HTTP handlers for REST API v1
package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/rantipay/trading-mcp/internal/twofa"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// TwoFAHandler handles TOTP enrollment and verification endpoints.
type TwoFAHandler struct {
	totp   *twofa.TOTPService
	db     *gorm.DB
	logger *logrus.Logger
}

// NewTwoFAHandler creates a new TwoFAHandler.
func NewTwoFAHandler(totpSvc *twofa.TOTPService, db *gorm.DB, logger *logrus.Logger) *TwoFAHandler {
	return &TwoFAHandler{
		totp:   totpSvc,
		db:     db,
		logger: logger,
	}
}

// userTwoFA is the GORM model that mirrors the user_twofa table.
// totp_secret_enc stores the encrypted seed bytes; in this handler the raw
// base32 secret is written directly — encryption via KMS belongs at the
// infrastructure/envelope-key layer.
type userTwoFA struct {
	UserID        string     `gorm:"column:user_id;primaryKey"`
	TOTPSecretEnc []byte     `gorm:"column:totp_secret_enc"`
	KMSKeyID      string     `gorm:"column:kms_key_id"`
	RecoveryCodes []byte     `gorm:"column:recovery_codes"`
	Enabled       bool       `gorm:"column:enabled;default:false"`
	EnrolledAt    *time.Time `gorm:"column:enrolled_at"`
	LastUsedAt    *time.Time `gorm:"column:last_used_at"`
	CreatedAt     time.Time  `gorm:"column:created_at;autoCreateTime"`
	UpdatedAt     time.Time  `gorm:"column:updated_at;autoUpdateTime"`
}

// TableName maps the struct to the user_twofa table.
func (userTwoFA) TableName() string { return "user_twofa" }

// setupResponse is returned by POST /twofa/setup.
type setupResponse struct {
	QRCodeURL     string   `json:"qr_code_url"`
	Secret        string   `json:"secret"`
	RecoveryCodes []string `json:"recovery_codes"`
}

// verifyRequest is the body for POST /twofa/verify and POST /twofa/disable.
type verifyRequest struct {
	Code string `json:"code" binding:"required"`
}

// recoverRequest is the body for POST /twofa/recover.
type recoverRequest struct {
	Code string `json:"code" binding:"required"`
}

// Setup handles POST /twofa/setup.
// Generates a new TOTP seed and recovery codes but does NOT activate 2FA yet.
// The caller must call POST /twofa/verify with a valid OTP to activate.
func (h *TwoFAHandler) Setup(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	result, err := h.totp.Enroll(userID)
	if err != nil {
		h.logger.WithError(err).WithField("user_id", userID).Error("Failed to enroll TOTP")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("TOTP_ENROLL_ERROR", "Failed to generate TOTP secret"))
		return
	}

	hashedCodes := twofa.HashRecoveryCodes(result.RecoveryCodes)
	codesJSON := marshalStringSlice(hashedCodes)

	record := &userTwoFA{
		UserID:        userID,
		TOTPSecretEnc: []byte(result.Secret),
		RecoveryCodes: codesJSON,
		Enabled:       false,
	}

	// Upsert: re-running setup overwrites the pending (not yet activated) seed.
	if err := h.db.Save(record).Error; err != nil {
		h.logger.WithError(err).WithField("user_id", userID).Error("Failed to persist TOTP seed")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("DB_ERROR", "Failed to save TOTP seed"))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id": userID,
		"event":   "twofa_setup",
	}).Info("TOTP setup initiated")

	c.JSON(http.StatusOK, models.NewSuccessResponse(setupResponse{
		QRCodeURL:     result.QRCodeURL,
		Secret:        result.Secret,
		RecoveryCodes: result.RecoveryCodes,
	}))
}

// Verify handles POST /twofa/verify.
// Activates 2FA for the user after validating the first TOTP code.
// Body: {"code":"123456"}
func (h *TwoFAHandler) Verify(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	var req verifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_REQUEST", "Missing or invalid body", err.Error()))
		return
	}

	var record userTwoFA
	if err := h.db.Where("user_id = ?", userID).First(&record).Error; err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("NOT_ENROLLED", "TOTP setup not initiated; call /twofa/setup first"))
		return
	}

	secret := string(record.TOTPSecretEnc)
	if !h.totp.Verify(secret, req.Code) {
		h.logger.WithFields(logrus.Fields{
			"user_id": userID,
			"event":   "twofa_verify_failed",
		}).Warn("Invalid TOTP code during activation")
		c.JSON(http.StatusForbidden, models.NewErrorResponse("INVALID_CODE", "TOTP code is invalid or expired"))
		return
	}

	now := time.Now()
	if err := h.db.Model(&record).Updates(map[string]interface{}{
		"enabled":      true,
		"enrolled_at":  now,
		"last_used_at": now,
	}).Error; err != nil {
		h.logger.WithError(err).WithField("user_id", userID).Error("Failed to activate 2FA")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("DB_ERROR", "Failed to activate 2FA"))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id": userID,
		"event":   "twofa_activated",
	}).Info("2FA activated for user")

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"message": "2FA successfully activated",
		"enabled": true,
	}))
}

// Disable handles POST /twofa/disable.
// Disables 2FA after validating a TOTP code or a recovery code.
// Body: {"code":"123456"}
func (h *TwoFAHandler) Disable(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	var req verifyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_REQUEST", "Missing or invalid body", err.Error()))
		return
	}

	var record userTwoFA
	if err := h.db.Where("user_id = ? AND enabled = true", userID).First(&record).Error; err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("NOT_ENABLED", "2FA is not enabled for this account"))
		return
	}

	secret := string(record.TOTPSecretEnc)
	hashedCodes := unmarshalStringSlice(record.RecoveryCodes)

	totpValid := h.totp.Verify(secret, req.Code)
	recoveryValid, _ := h.totp.VerifyRecoveryCode(hashedCodes, req.Code)

	if !totpValid && !recoveryValid {
		h.logger.WithFields(logrus.Fields{
			"user_id": userID,
			"event":   "twofa_disable_failed",
		}).Warn("Invalid code during 2FA disable attempt")
		c.JSON(http.StatusForbidden, models.NewErrorResponse("INVALID_CODE", "TOTP or recovery code is invalid"))
		return
	}

	if err := h.db.Model(&record).Updates(map[string]interface{}{
		"enabled":         false,
		"totp_secret_enc": []byte(""),
		"recovery_codes":  marshalStringSlice(nil),
	}).Error; err != nil {
		h.logger.WithError(err).WithField("user_id", userID).Error("Failed to disable 2FA")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("DB_ERROR", "Failed to disable 2FA"))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id": userID,
		"event":   "twofa_disabled",
	}).Warn("2FA disabled for user")

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"message": "2FA successfully disabled",
		"enabled": false,
	}))
}

// Recover handles POST /twofa/recover.
// Authenticates the user via a single-use recovery code.
// Body: {"code":"abc12345"}
func (h *TwoFAHandler) Recover(c *gin.Context) {
	userID, exists := middleware.GetUserIDFromContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
		return
	}

	var req recoverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponseWithDetails("INVALID_REQUEST", "Missing or invalid body", err.Error()))
		return
	}

	var record userTwoFA
	if err := h.db.Where("user_id = ? AND enabled = true", userID).First(&record).Error; err != nil {
		c.JSON(http.StatusBadRequest, models.NewErrorResponse("NOT_ENABLED", "2FA is not enabled for this account"))
		return
	}

	hashedCodes := unmarshalStringSlice(record.RecoveryCodes)
	valid, remaining := h.totp.VerifyRecoveryCode(hashedCodes, req.Code)
	if !valid {
		h.logger.WithFields(logrus.Fields{
			"user_id": userID,
			"event":   "twofa_recovery_failed",
		}).Warn("Invalid recovery code attempt")
		c.JSON(http.StatusForbidden, models.NewErrorResponse("INVALID_RECOVERY_CODE", "Recovery code is invalid or already used"))
		return
	}

	now := time.Now()
	if err := h.db.Model(&record).Updates(map[string]interface{}{
		"recovery_codes": marshalStringSlice(remaining),
		"last_used_at":   now,
	}).Error; err != nil {
		h.logger.WithError(err).WithField("user_id", userID).Error("Failed to update recovery codes")
		c.JSON(http.StatusInternalServerError, models.NewErrorResponse("DB_ERROR", "Failed to consume recovery code"))
		return
	}

	h.logger.WithFields(logrus.Fields{
		"user_id":         userID,
		"event":           "twofa_recovery_used",
		"remaining_codes": len(remaining),
	}).Warn("Recovery code used for 2FA authentication")

	c.JSON(http.StatusOK, models.NewSuccessResponse(gin.H{
		"message":         "Recovery code accepted",
		"remaining_codes": len(remaining),
	}))
}

// NewTwoFAEnforcer returns a gin.HandlerFunc that enforces step-up TOTP
// verification for critical mutations (BOT-04 fix: TwoFAMiddleware previously
// defaulted to a no-op in main.go, so this real check was never installed).
//
// Enforcement is opt-in per user, not global: an account that has never
// enrolled (or has 2FA disabled) is let through unchanged — there is no
// migration path to force every existing account through a step-up code it
// was never asked to set up. An account with 2FA Enabled=true MUST present a
// valid current TOTP code (or a single-use recovery code) in the
// X-2FA-Code header on every request this middleware guards; otherwise the
// request is aborted with 403.
func NewTwoFAEnforcer(db *gorm.DB, totpSvc *twofa.TOTPService, logger *logrus.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID, exists := middleware.GetUserIDFromContext(c)
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "User not authenticated"))
			return
		}

		var record userTwoFA
		if err := db.Where("user_id = ? AND enabled = true", userID).First(&record).Error; err != nil {
			// Not enrolled / not enabled — 2FA is opt-in, let the request through.
			c.Next()
			return
		}

		code := c.GetHeader("X-2FA-Code")
		if code == "" {
			c.AbortWithStatusJSON(http.StatusForbidden, models.NewErrorResponse("2FA_REQUIRED", "This account has 2FA enabled; provide a valid code in the X-2FA-Code header"))
			return
		}

		secret := string(record.TOTPSecretEnc)
		if totpSvc.Verify(secret, code) {
			_ = db.Model(&record).Update("last_used_at", time.Now()).Error
			c.Next()
			return
		}

		hashedCodes := unmarshalStringSlice(record.RecoveryCodes)
		if valid, remaining := totpSvc.VerifyRecoveryCode(hashedCodes, code); valid {
			if err := db.Model(&record).Updates(map[string]interface{}{
				"recovery_codes": marshalStringSlice(remaining),
				"last_used_at":   time.Now(),
			}).Error; err != nil {
				logger.WithError(err).WithField("user_id", userID).Error("2FA enforcement: failed to persist consumed recovery code")
			}
			logger.WithField("user_id", userID).Warn("2FA enforcement: recovery code consumed for step-up auth")
			c.Next()
			return
		}

		logger.WithFields(logrus.Fields{
			"user_id": userID,
			"path":    c.Request.URL.Path,
			"event":   "twofa_enforce_failed",
		}).Warn("2FA enforcement: invalid step-up code, request blocked")
		c.AbortWithStatusJSON(http.StatusForbidden, models.NewErrorResponse("INVALID_2FA_CODE", "2FA code is invalid or expired"))
	}
}

// =============================================================================
// Helpers for JSON marshal/unmarshal of string slices stored as JSONB/bytea.
// =============================================================================

func marshalStringSlice(ss []string) []byte {
	if ss == nil {
		ss = []string{}
	}
	b, _ := json.Marshal(ss)
	return b
}

func unmarshalStringSlice(data []byte) []string {
	var ss []string
	if len(data) == 0 {
		return ss
	}
	_ = json.Unmarshal(data, &ss)
	return ss
}
