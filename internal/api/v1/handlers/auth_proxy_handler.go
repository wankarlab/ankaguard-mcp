// Package handlers — auth_proxy_handler.go
//
// AuthProxyHandler forwards authentication requests from the Anka Arena web UI
// to the RantiPay Auth service (default :10100). It exists so the browser only
// ever talks to the trading-mcp origin (no CORS) and so login/refresh tokens are
// obtained through a single same-origin path: /api/v1/auth/*.
//
// These routes are PUBLIC (registered on the gin engine, not the JWT-gated v1
// group): they are the entry point that mints the JWT in the first place.
package handlers

import (
	"bytes"
	"io"
	"net/http"
	"regexp"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

// AuthProxyHandler proxies login/refresh/oauth calls to the RantiPay Auth service.
type AuthProxyHandler struct {
	baseURL string
	client  *http.Client
	logger  *logrus.Logger
}

// NewAuthProxyHandler builds a proxy handler. If baseURL is empty it defaults to
// the RantiPay Auth service on localhost:10100.
func NewAuthProxyHandler(baseURL string, logger *logrus.Logger) *AuthProxyHandler {
	if baseURL == "" {
		baseURL = "http://localhost:10100"
	}
	if logger == nil {
		logger = logrus.New()
	}
	return &AuthProxyHandler{
		baseURL: baseURL,
		client:  &http.Client{Timeout: 15 * time.Second},
		logger:  logger,
	}
}

// proxy forwards the current request to the Auth service at the given upstream
// path, copying the request body and query string, and relays the upstream
// status code and JSON body back to the caller.
func (h *AuthProxyHandler) proxy(c *gin.Context, upstreamPath string) {
	var body io.Reader
	if c.Request.Body != nil {
		raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 1<<20)) // 1 MiB cap
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"success": false,
				"error":   gin.H{"code": "INVALID_REQUEST", "message": "could not read request body"},
			})
			return
		}
		body = bytes.NewReader(raw)
	}

	url := h.baseURL + upstreamPath
	if q := c.Request.URL.RawQuery; q != "" {
		url += "?" + q
	}

	req, err := http.NewRequestWithContext(c.Request.Context(), c.Request.Method, url, body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   gin.H{"code": "PROXY_ERROR", "message": "could not build upstream request"},
		})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	// Forward the real client IP so the Auth service can record it for the session.
	req.Header.Set("X-Forwarded-For", c.ClientIP())
	// Passwordless session orchestrator (D7): every step after creation is
	// authorised by the opaque session token issued by POST /auth/session.
	if tok := c.GetHeader(authSessionTokenHeader); tok != "" {
		req.Header.Set(authSessionTokenHeader, tok)
	}

	resp, err := h.client.Do(req)
	if err != nil {
		h.logger.WithError(err).WithField("upstream", url).Warn("auth proxy: upstream unreachable")
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"error":   gin.H{"code": "AUTH_UNAVAILABLE", "message": "authentication service unavailable"},
		})
		return
	}
	defer resp.Body.Close()

	payload, _ := io.ReadAll(resp.Body)
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	c.Data(resp.StatusCode, contentType, payload)
}

// LoginEmail proxies POST /api/v1/auth/login/email.
func (h *AuthProxyHandler) LoginEmail(c *gin.Context) {
	h.proxy(c, "/api/v1/auth/login/email")
}

// LoginPhone proxies POST /api/v1/auth/login/phone (step 1 — send OTP, existing user).
func (h *AuthProxyHandler) LoginPhone(c *gin.Context) {
	h.proxy(c, "/api/v1/auth/login/phone")
}

// LoginPhoneVerify proxies POST /api/v1/auth/login/phone/verify (step 2 — OTP → tokens).
func (h *AuthProxyHandler) LoginPhoneVerify(c *gin.Context) {
	h.proxy(c, "/api/v1/auth/login/phone/verify")
}

// authSessionTokenHeader carries the per-flow token of the session orchestrator.
const authSessionTokenHeader = "X-Auth-Session-Token"

// sessionIDPattern restricts the :id path parameter before it is concatenated
// into the upstream URL (no slashes, dots or encoded traversal).
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// SessionCreate proxies POST /api/v1/auth/session (passwordless login, D7):
// starts a flow and returns session_id + session_token.
func (h *AuthProxyHandler) SessionCreate(c *gin.Context) {
	h.proxy(c, "/api/v1/auth/session")
}

// SessionAnswer proxies POST /api/v1/auth/session/:id/answer (identifier, email_otp, ...).
func (h *AuthProxyHandler) SessionAnswer(c *gin.Context) {
	if id, ok := h.sessionID(c); ok {
		h.proxy(c, "/api/v1/auth/session/"+id+"/answer")
	}
}

// SessionResend proxies POST /api/v1/auth/session/:id/resend (resend the OTP).
func (h *AuthProxyHandler) SessionResend(c *gin.Context) {
	if id, ok := h.sessionID(c); ok {
		h.proxy(c, "/api/v1/auth/session/"+id+"/resend")
	}
}

func (h *AuthProxyHandler) sessionID(c *gin.Context) (string, bool) {
	id := c.Param("id")
	if !sessionIDPattern.MatchString(id) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   gin.H{"code": "INVALID_SESSION_ID", "message": "invalid session id"},
		})
		return "", false
	}
	return id, true
}

// RegisterPhone proxies POST /api/v1/auth/register/phone (send OTP for a new number).
func (h *AuthProxyHandler) RegisterPhone(c *gin.Context) {
	h.proxy(c, "/api/v1/auth/register/phone")
}

// RegisterPhoneVerify proxies POST /api/v1/auth/register/phone/verify (OTP → account + tokens).
func (h *AuthProxyHandler) RegisterPhoneVerify(c *gin.Context) {
	h.proxy(c, "/api/v1/auth/register/phone/verify")
}

// LoginSocial proxies POST /api/v1/auth/login/social.
func (h *AuthProxyHandler) LoginSocial(c *gin.Context) {
	h.proxy(c, "/api/v1/auth/login/social")
}

// Refresh proxies POST /api/v1/auth/refresh.
func (h *AuthProxyHandler) Refresh(c *gin.Context) {
	h.proxy(c, "/api/v1/auth/refresh")
}

// OAuthAuthorize proxies GET /api/v1/auth/oauth/:provider/authorize.
func (h *AuthProxyHandler) OAuthAuthorize(c *gin.Context) {
	h.proxy(c, "/api/v1/auth/oauth/"+c.Param("provider")+"/authorize")
}

// OAuthNative proxies POST /api/v1/auth/oauth/:provider/native (Google Identity
// Services web flow: the browser obtains an ID token and exchanges it here).
func (h *AuthProxyHandler) OAuthNative(c *gin.Context) {
	h.proxy(c, "/api/v1/auth/oauth/"+c.Param("provider")+"/native")
}

// Providers proxies GET /api/v1/auth/providers.
func (h *AuthProxyHandler) Providers(c *gin.Context) {
	h.proxy(c, "/api/v1/auth/providers")
}
