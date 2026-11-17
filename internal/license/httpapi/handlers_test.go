package httpapi

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/license"
)

func setup(t *testing.T) (*gin.Engine, ed25519.PrivateKey) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	m := license.NewManager(license.Options{
		Verifier: license.NewVerifierWithKey(pub), DataDir: t.TempDir(),
		ProBuild: func() bool { return true },
	})
	r := gin.New()
	deny := func(c *gin.Context) {
		if c.GetHeader("X-Op") != "1" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"code": "UNAUTHORIZED"})
		}
	}
	(&Handler{Manager: m}).Register(r.Group("/api/v1"), deny)
	return r, priv
}

func do(r http.Handler, method, body string, op bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/v1/license", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if op {
		req.Header.Set("X-Op", "1")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLicenseEndpoints(t *testing.T) {
	r, priv := setup(t)
	if w := do(r, "GET", "", false); w.Code != 401 {
		t.Fatalf("operator middleware not applied: %d", w.Code)
	}
	var st license.LicenseStatus
	w := do(r, "GET", "", true)
	json.Unmarshal(w.Body.Bytes(), &st)
	if w.Code != 200 || st.State != "invalid" || st.Edition != "community" {
		t.Fatalf("initial: %d %s", w.Code, w.Body)
	}

	// Invalid document: 400 with reason, previous (none) kept.
	w = do(r, "PUT", `{"license":"nonsense"}`, true)
	var errBody map[string]string
	json.Unmarshal(w.Body.Bytes(), &errBody)
	if w.Code != 400 || errBody["code"] != "LICENSE_INVALID" || errBody["reason"] != "malformed" {
		t.Fatalf("invalid put: %d %s", w.Code, w.Body)
	}

	now := time.Now()
	doc, _ := license.Sign(license.Payload{ID: "L1", Holder: "ACME", Edition: "pro", IssuedAt: now.Add(-time.Hour), ExpiresAt: now.AddDate(1, 0, 0)}, priv)
	w = do(r, "PUT", `{"license":"`+doc+`"}`, true)
	json.Unmarshal(w.Body.Bytes(), &st)
	if w.Code != 200 || st.State != "valid" || st.Edition != "pro" {
		t.Fatalf("put: %d %s", w.Code, w.Body)
	}
	if strings.Contains(w.Body.String(), strings.Split(doc, ".")[1]) {
		t.Fatal("response leaked the document")
	}

	// A later bad PUT keeps the installed license.
	do(r, "PUT", `{"license":"a.b"}`, true)
	w = do(r, "GET", "", true)
	json.Unmarshal(w.Body.Bytes(), &st)
	if st.State != "valid" {
		t.Fatalf("license lost: %s", w.Body)
	}
}
