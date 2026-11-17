package localauth

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// BasePath is where the routes are mounted.
const BasePath = "/api/v1/auth/local"

// RegisterRoutes mounts the local auth routes on rg (expected to be the
// /api/v1/auth/local group, outside any JWT middleware: login and mode are
// public and the token routes authenticate the operator session themselves).
func (s *Service) RegisterRoutes(rg *gin.RouterGroup) {
	rg.GET("/mode", s.handleMode)
	rg.POST("/login", s.handleLogin)
	rg.GET("/tokens", s.handleListTokens)
	rg.POST("/tokens", s.handleCreateToken)
	rg.DELETE("/tokens/:id", s.handleRevokeToken)
}

// HTTPHandler returns a standalone http.Handler serving BasePath/*.
func (s *Service) HTTPHandler() http.Handler {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	s.RegisterRoutes(r.Group(BasePath))
	return r
}

func (s *Service) handleMode(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"mode": s.mode, "password_set": s.Standalone() && s.PasswordSet()})
}

// externalGuard answers 501 in external mode; returns true when it answered.
func (s *Service) externalGuard(c *gin.Context) bool {
	if s.Standalone() {
		return false
	}
	c.JSON(http.StatusNotImplemented, gin.H{"error": "local authentication is not available", "code": "AUTH_MODE_EXTERNAL"})
	return true
}

func sourceAddr(c *gin.Context) string {
	// RemoteAddr only: forwarded headers are client-controlled and would let an
	// attacker dodge the per-source limit.
	host, _, err := net.SplitHostPort(c.Request.RemoteAddr)
	if err != nil {
		return c.Request.RemoteAddr
	}
	return host
}

func (s *Service) handleLogin(c *gin.Context) {
	if s.externalGuard(c) {
		return
	}
	src := sourceAddr(c)
	if rem, blocked := s.limiter.blocked(src); blocked {
		s.log.WithField("source", src).Warn("localauth: login attempt rejected, locked out")
		c.Header("Retry-After", strconv.Itoa(int(rem.Seconds())+1))
		c.JSON(http.StatusTooManyRequests, gin.H{"error": "too many failed attempts, try again later", "code": "LOGIN_LOCKED"})
		return
	}
	// Many failures from all sources together slow every attempt down but
	// never lock the operator out (only the failing source is locked).
	s.limiter.wait(c.Request.Context())
	var req struct {
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "code": "BAD_REQUEST"})
		return
	}
	err := s.verifyPassword(req.Password)
	if errors.Is(err, ErrPasswordNotSet) {
		s.log.WithField("source", src).Warn("localauth: login attempt before password was set")
		c.JSON(http.StatusConflict, gin.H{"error": "operator password is not set; run `operator set-password` on the server machine", "code": "PASSWORD_NOT_SET"})
		return
	}
	if err != nil {
		s.limiter.fail(src)
		s.log.WithField("source", src).Warn("localauth: failed login attempt")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials", "code": "INVALID_CREDENTIALS"})
		return
	}
	tok, exp, err := s.issueOperatorSession()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not issue session", "code": "INTERNAL"})
		return
	}
	s.limiter.success(src)
	s.log.WithField("source", src).Info("localauth: operator login succeeded")
	c.JSON(http.StatusOK, gin.H{
		"token": tok, "token_type": "Bearer", "expires_at": exp, "profile": ProfileLive, "level": OperatorLevel,
	})
}

// requireOperator verifies the bearer token is a live operator session.
func (s *Service) requireOperator(c *gin.Context) bool {
	h := c.GetHeader("Authorization")
	raw := ""
	if len(h) > 7 && strings.EqualFold(h[:7], "bearer ") {
		raw = strings.TrimSpace(h[7:])
	}
	if raw == "" {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required", "code": "UNAUTHORIZED"})
		return false
	}
	p, err := s.CheckToken(raw)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "authentication required", "code": "UNAUTHORIZED"})
		return false
	}
	if !p.IsOperator {
		s.log.WithFields(map[string]interface{}{"jti": p.JTI, "profile": p.Profile, "path": c.Request.URL.Path}).
			Warn("localauth: agent token attempted operator-only action")
		c.JSON(http.StatusForbidden, gin.H{"error": "operator session required", "code": "OPERATOR_REQUIRED"})
		return false
	}
	return true
}

func (s *Service) handleListTokens(c *gin.Context) {
	if s.externalGuard(c) || !s.requireOperator(c) {
		return
	}
	list, err := s.ListAgentTokens()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not list tokens", "code": "INTERNAL"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"tokens": list})
}

func (s *Service) handleCreateToken(c *gin.Context) {
	if s.externalGuard(c) || !s.requireOperator(c) {
		return
	}
	var req struct {
		Name          string `json:"name"`
		Profile       string `json:"profile"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "code": "BAD_REQUEST"})
		return
	}
	tok, info, err := s.CreateAgentToken(req.Name, req.Profile, req.ExpiresInDays)
	if errors.Is(err, ErrInvalidTokenRequest) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error(), "code": "INVALID_TOKEN_REQUEST"})
		return
	}
	if errors.Is(err, ErrLicenseRequired) {
		c.JSON(http.StatusPaymentRequired, gin.H{"error": err.Error(), "code": "LICENSE_REQUIRED"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create token", "code": "INTERNAL"})
		return
	}
	// The value is returned only here and cannot be recovered later.
	c.JSON(http.StatusCreated, gin.H{"token": tok, "token_type": "Bearer", "agent_token": info})
}

func (s *Service) handleRevokeToken(c *gin.Context) {
	if s.externalGuard(c) || !s.requireOperator(c) {
		return
	}
	err := s.RevokeAgentToken(c.Param("id"))
	if errors.Is(err, ErrTokenNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "token not found", "code": "NOT_FOUND"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not revoke token", "code": "INTERNAL"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"revoked": true})
}
