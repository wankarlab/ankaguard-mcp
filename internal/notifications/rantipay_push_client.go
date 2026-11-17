package notifications

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

// RantiPayPushClient sends push notifications through the RantiPay
// notificationpush microservice (FCM/APNS) instead of trading-mcp's own no-op
// push path. trading-mcp does NOT manage device tokens — it only needs the
// target user's user_id; the notificationpush `devices` table already holds the
// tokens the mobile app registered under that user.
//
// Wire contract (mirrors internal/chat/client/notification_client.go in the
// platform monorepo): a self-issued service JWT (HS256, shared RANTIPAY_JWT
// secret, issuer "rantipay-auth-service") authenticates the /api/v1 calls, and
// the X-Internal-Api-Key header lets those handlers skip the per-device
// ownership check so a backend service can push to the owner's devices.
//
// Every method degrades soft: a disabled/misconfigured client, an unreachable
// service, or a user with no registered devices logs at Debug and returns nil,
// so a background monitor never breaks on notification failure.
type RantiPayPushClient struct {
	baseURL        string
	jwtSecret      string
	internalAPIKey string
	httpClient     *http.Client
	log            *logrus.Logger
	enabled        bool
}

const (
	pushJWTIssuer   = "rantipay-auth-service"
	pushTenantID    = "default"
	pushServiceID   = "trading-mcp"
	pushHTTPTimeout = 8 * time.Second
	pushTokenTTL    = 15 * time.Minute
	pushServiceLvl  = 4
)

// deviceTokenInfo / notificationDevice mirror the notificationpush
// GET /api/v1/users/:id/devices response shape.
type deviceTokenInfo struct {
	Token    string `json:"token"`
	Provider string `json:"provider"` // fcm, apns, webpush
	IsValid  bool   `json:"is_valid"`
}

type notificationDevice struct {
	ID       string          `json:"id"`
	UserID   string          `json:"user_id"`
	Platform string          `json:"platform"`
	Status   string          `json:"status"`
	Token    deviceTokenInfo `json:"token_info"`
}

// sendNotificationRequest is the POST /api/v1/notifications/send payload.
type sendNotificationRequest struct {
	DeviceToken string                 `json:"device_token"`
	Provider    string                 `json:"provider,omitempty"`
	Title       string                 `json:"title"`
	Body        string                 `json:"body"`
	Data        map[string]interface{} `json:"data,omitempty"`
	Priority    string                 `json:"priority,omitempty"`
}

// NewRantiPayPushClient builds a client. It is enabled only when both baseURL
// and jwtSecret are present (the /api/v1 send/lookup endpoints require a valid
// service JWT). Otherwise Enabled() is false and SendToUser is a logged no-op.
func NewRantiPayPushClient(baseURL, jwtSecret, internalAPIKey string, log *logrus.Logger) *RantiPayPushClient {
	if log == nil {
		log = logrus.New()
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return &RantiPayPushClient{
		baseURL:        baseURL,
		jwtSecret:      strings.TrimSpace(jwtSecret),
		internalAPIKey: strings.TrimSpace(internalAPIKey),
		httpClient:     &http.Client{Timeout: pushHTTPTimeout},
		log:            log,
		enabled:        baseURL != "" && strings.TrimSpace(jwtSecret) != "",
	}
}

// Enabled reports whether the client is configured to actually deliver.
func (c *RantiPayPushClient) Enabled() bool { return c != nil && c.enabled }

// SendToUser delivers a push to every active device of userID. Best-effort:
// on any failure it logs and returns the first error for the caller to log,
// but the caller (the alert monitor) treats a non-nil return as non-fatal.
func (c *RantiPayPushClient) SendToUser(ctx context.Context, userID, title, body string, data map[string]interface{}) error {
	if !c.Enabled() {
		c.log.Debug("push: client disabled (no NOTIFICATION_SERVICE_URL / RANTIPAY_JWT_SECRET), skipping")
		return nil
	}
	if strings.TrimSpace(userID) == "" {
		c.log.Debug("push: empty userID, skipping")
		return nil
	}

	devices, err := c.getUserDevices(ctx, userID)
	if err != nil {
		c.log.WithError(err).Debug("push: device lookup failed, skipping delivery")
		return nil
	}
	if len(devices) == 0 {
		c.log.WithField("user_id", userID).Debug("push: user has no active devices, skipping")
		return nil
	}

	var firstErr error
	sent := 0
	for _, d := range devices {
		tok := strings.TrimSpace(d.Token.Token)
		if tok == "" || !d.Token.IsValid {
			continue
		}
		if err := c.sendOne(ctx, tok, d.Token.Provider, title, body, data); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			c.log.WithError(err).WithField("provider", d.Token.Provider).Debug("push: send to device failed")
			continue
		}
		sent++
	}

	c.log.WithFields(logrus.Fields{"user_id": userID, "devices": len(devices), "sent": sent}).
		Debug("push: delivery attempt complete")
	if sent == 0 {
		return firstErr // may be nil if all devices were invalid
	}
	return nil
}

// mintServiceToken issues a short-lived HS256 service JWT whose claims match
// what the notificationpush auth middleware validates (type=access, issuer
// rantipay-auth-service, sub/user_id/tenant_id present).
func (c *RantiPayPushClient) mintServiceToken() (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"user_id":    pushServiceID,
		"tenant_id":  pushTenantID,
		"company_id": "",
		"user_level": pushServiceLvl,
		"type":       "access",
		"iss":        pushJWTIssuer,
		"sub":        pushServiceID,
		"iat":        now.Unix(),
		"nbf":        now.Unix(),
		"exp":        now.Add(pushTokenTTL).Unix(),
		"jti":        uuid.NewString(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(c.jwtSecret))
}

// setAuth attaches the service JWT + internal service-to-service headers every
// internal-gated notificationpush handler requires.
func (c *RantiPayPushClient) setAuth(req *http.Request) error {
	token, err := c.mintServiceToken()
	if err != nil {
		return fmt.Errorf("mint service token: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Tenant-ID", pushTenantID)
	req.Header.Set("X-Internal-Call", "trading-mcp")
	if c.internalAPIKey != "" {
		req.Header.Set("X-Internal-Api-Key", c.internalAPIKey)
	}
	return nil
}

func (c *RantiPayPushClient) getUserDevices(ctx context.Context, userID string) ([]notificationDevice, error) {
	url := fmt.Sprintf("%s/api/v1/users/%s/devices", c.baseURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if err := c.setAuth(req); err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("device lookup %d: %s", resp.StatusCode, string(body))
	}

	// Response may wrap in {"devices":[...]} or {"data":[...]}.
	var envelope struct {
		Devices []notificationDevice `json:"devices"`
		Data    []notificationDevice `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if len(envelope.Devices) > 0 {
		return envelope.Devices, nil
	}
	return envelope.Data, nil
}

func (c *RantiPayPushClient) sendOne(ctx context.Context, deviceToken, provider, title, body string, data map[string]interface{}) error {
	payload := sendNotificationRequest{
		DeviceToken: deviceToken,
		Provider:    provider,
		Title:       title,
		Body:        body,
		Data:        data,
		Priority:    "high",
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/v1/notifications/send", bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if err := c.setAuth(req); err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("send %d: %s", resp.StatusCode, string(rb))
	}
	// notificationpush returns HTTP 200 even on provider failure — check body.
	var result struct {
		Success bool   `json:"success"`
		Error   string `json:"error"`
	}
	if json.Unmarshal(rb, &result) == nil && !result.Success && result.Error != "" {
		return fmt.Errorf("provider delivery failed: %s", result.Error)
	}
	return nil
}
