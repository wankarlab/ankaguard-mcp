package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

func signedToken(t *testing.T, secret string, claims *KongJWTClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString([]byte(secret))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func runMiddleware(secret, gatewayToken string, headers map[string]string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("GET", "/x", nil)
	for k, v := range headers {
		c.Request.Header.Set(k, v)
	}
	logger := logrus.New()
	logger.SetLevel(logrus.PanicLevel)
	KongJWTMiddleware(secret, gatewayToken, logger)(c)
	return c, w
}

// Task 6.10: X-Tenant-ID / X-Company-ID are ignored unless the request carries
// the valid X-Gateway-Token.
func TestTenantHeadersIgnoredWithoutGatewayToken(t *testing.T) {
	const secret = "tenant-test-secret"
	tok := signedToken(t, secret, &KongJWTClaims{UserID: "u1", UserLevel: 1, TenantID: "tenant-from-token", CompanyID: "company-from-token"})

	t.Run("no gateway token: headers ignored", func(t *testing.T) {
		c, w := runMiddleware(secret, "gw-secret", map[string]string{
			"Authorization": "Bearer " + tok,
			"X-Tenant-ID":   "victim-tenant",
			"X-Company-ID":  "victim-company",
		})
		assert.NotEqual(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "tenant-from-token", c.GetString("tenant_id"))
		assert.Equal(t, "company-from-token", c.GetString("company_id"))
	})

	t.Run("wrong gateway token: headers ignored and request still validated normally", func(t *testing.T) {
		c, w := runMiddleware(secret, "gw-secret", map[string]string{
			"Authorization":   "Bearer " + tok,
			"X-Tenant-ID":     "victim-tenant",
			"X-Gateway-Token": "not-the-token",
		})
		assert.NotEqual(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "tenant-from-token", c.GetString("tenant_id"))
	})

	t.Run("no gateway token configured: header ignored even if sent", func(t *testing.T) {
		c, _ := runMiddleware(secret, "", map[string]string{
			"Authorization":   "Bearer " + tok,
			"X-Tenant-ID":     "victim-tenant",
			"X-Gateway-Token": "anything",
		})
		assert.Equal(t, "tenant-from-token", c.GetString("tenant_id"))
	})

	t.Run("valid gateway token: headers honoured", func(t *testing.T) {
		c, w := runMiddleware(secret, "gw-secret", map[string]string{
			"Authorization":   "Bearer " + tok,
			"X-Tenant-ID":     "tenant-from-gateway",
			"X-Company-ID":    "company-from-gateway",
			"X-Gateway-Token": "gw-secret",
		})
		assert.NotEqual(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "tenant-from-gateway", c.GetString("tenant_id"))
		assert.Equal(t, "company-from-gateway", c.GetString("company_id"))
	})
}

type fakeAuthority struct {
	secret string
	deny   error
	seen   []*KongJWTClaims
}

func (f *fakeAuthority) Secret() string { return f.secret }
func (f *fakeAuthority) Authorize(c *KongJWTClaims) error {
	f.seen = append(f.seen, c)
	return f.deny
}

// Task 6.3: with a local authority the secret is read per request and the
// stateful checks apply; without one nothing changes (external mode).
func TestLocalAuthorityReplacesTheSecretPerRequest(t *testing.T) {
	t.Cleanup(func() { SetLocalAuthority(nil) })
	const staticSecret = "kong-static-secret"
	claims := func() *KongJWTClaims { return &KongJWTClaims{UserID: "op", UserLevel: 4, TokenUse: "operator_session"} }

	t.Run("external mode (no authority): the static secret validates, others do not", func(t *testing.T) {
		SetLocalAuthority(nil)
		_, w := runMiddleware(staticSecret, "", map[string]string{"Authorization": "Bearer " + signedToken(t, staticSecret, claims())})
		assert.NotEqual(t, http.StatusUnauthorized, w.Code)
		_, w = runMiddleware(staticSecret, "", map[string]string{"Authorization": "Bearer " + signedToken(t, "local-secret", claims())})
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "kong-static-secret", EffectiveSecret(staticSecret))
	})

	t.Run("standalone: the authority secret validates, the static one no longer does", func(t *testing.T) {
		auth := &fakeAuthority{secret: "local-secret-1"}
		SetLocalAuthority(auth)
		c, w := runMiddleware(staticSecret, "", map[string]string{"Authorization": "Bearer " + signedToken(t, "local-secret-1", claims())})
		assert.NotEqual(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "operator_session", c.GetString("token_use"))

		_, w = runMiddleware(staticSecret, "", map[string]string{"Authorization": "Bearer " + signedToken(t, staticSecret, claims())})
		assert.Equal(t, http.StatusUnauthorized, w.Code)
	})

	t.Run("standalone: rotating the secret applies to the very next request", func(t *testing.T) {
		auth := &fakeAuthority{secret: "before-rotation"}
		SetLocalAuthority(auth)
		old := signedToken(t, "before-rotation", claims())
		_, w := runMiddleware(staticSecret, "", map[string]string{"Authorization": "Bearer " + old})
		assert.NotEqual(t, http.StatusUnauthorized, w.Code)

		auth.secret = "after-rotation"
		_, w = runMiddleware(staticSecret, "", map[string]string{"Authorization": "Bearer " + old})
		assert.Equal(t, http.StatusUnauthorized, w.Code, "a token signed with the rotated-out secret must stop working at once")
	})

	t.Run("standalone: Authorize rejects (revoked token / old session epoch)", func(t *testing.T) {
		auth := &fakeAuthority{secret: "s", deny: errors.New("revoked")}
		SetLocalAuthority(auth)
		_, w := runMiddleware(staticSecret, "", map[string]string{"Authorization": "Bearer " + signedToken(t, "s", claims())})
		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Len(t, auth.seen, 1)
	})

	t.Run("ValidateToken (WebSocket layers) follows the same rules", func(t *testing.T) {
		auth := &fakeAuthority{secret: "ws-secret"}
		SetLocalAuthority(auth)
		_, err := ValidateToken(signedToken(t, "ws-secret", claims()), staticSecret)
		assert.NoError(t, err)
		_, err = ValidateToken(signedToken(t, staticSecret, claims()), staticSecret)
		assert.Error(t, err)
		auth.deny = errors.New("revoked")
		_, err = ValidateToken(signedToken(t, "ws-secret", claims()), staticSecret)
		assert.Error(t, err)
	})
}
