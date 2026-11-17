package handlers

import (
	"crypto/hmac"
	"crypto/sha1" //nolint:gosec // RFC 6238 TOTP uses HMAC-SHA1
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rantipay/trading-mcp/internal/twofa"
	"github.com/sirupsen/logrus"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// totpNow computes the current RFC 6238 code independently of the service
// under test, so a bug in the service's generator cannot hide itself.
func totpNow(t *testing.T, secret string) string {
	t.Helper()
	s := strings.ToUpper(secret)
	if pad := len(s) % 8; pad != 0 {
		s += strings.Repeat("=", 8-pad)
	}
	key, err := base32.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	msg := make([]byte, 8)
	binary.BigEndian.PutUint64(msg, uint64(time.Now().Unix()/30))
	mac := hmac.New(sha1.New, key)
	mac.Write(msg)
	h := mac.Sum(nil)
	off := h[len(h)-1] & 0x0f
	bin := binary.BigEndian.Uint32(h[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", bin%1_000_000)
}

type twofaFixture struct {
	db       *gorm.DB
	router   *gin.Engine
	secret   string
	recovery []string
}

// newTwoFAFixture mounts the enforcer on GET /guarded. The X-Test-User header
// stands in for the JWT middleware that normally sets user_id.
func newTwoFAFixture(t *testing.T) *twofaFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&userTwoFA{}); err != nil {
		t.Fatal(err)
	}
	svc := twofa.NewTOTPService("test")
	enrolled, err := svc.Enroll("enrolled-user")
	if err != nil {
		t.Fatal(err)
	}
	for _, rec := range []userTwoFA{
		{UserID: "enrolled-user", TOTPSecretEnc: []byte(enrolled.Secret), Enabled: true,
			RecoveryCodes: marshalStringSlice(twofa.HashRecoveryCodes(enrolled.RecoveryCodes))},
		{UserID: "disabled-user", TOTPSecretEnc: []byte(enrolled.Secret), Enabled: false},
	} {
		if err := db.Create(&rec).Error; err != nil {
			t.Fatal(err)
		}
	}

	log := logrus.New()
	log.SetOutput(io.Discard)
	r := gin.New()
	r.Use(func(c *gin.Context) {
		if u := c.GetHeader("X-Test-User"); u != "" {
			c.Set("user_id", u)
		}
	})
	r.GET("/guarded", NewTwoFAEnforcer(db, svc, log), func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	return &twofaFixture{db: db, router: r, secret: enrolled.Secret, recovery: enrolled.RecoveryCodes}
}

func (f *twofaFixture) do(user, code string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/guarded", nil)
	if user != "" {
		req.Header.Set("X-Test-User", user)
	}
	if code != "" {
		req.Header.Set("X-2FA-Code", code)
	}
	w := httptest.NewRecorder()
	f.router.ServeHTTP(w, req)
	return w
}

func TestTwoFAEnforcer_HTTP(t *testing.T) {
	f := newTwoFAFixture(t)

	cases := []struct {
		name     string
		user     string
		code     string
		want     int
		wantBody string
	}{
		{"unauthenticated", "", "", http.StatusUnauthorized, "UNAUTHORIZED"},
		{"not enrolled passes", "someone-else", "", http.StatusOK, "ok"},
		{"disabled 2FA passes", "disabled-user", "", http.StatusOK, "ok"},
		{"enrolled without code", "enrolled-user", "", http.StatusForbidden, "2FA_REQUIRED"},
		{"enrolled with wrong code", "enrolled-user", "000000", http.StatusForbidden, "INVALID_2FA_CODE"},
		{"enrolled with valid TOTP", "enrolled-user", totpNow(t, f.secret), http.StatusOK, "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := f.do(tc.user, tc.code)
			if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.wantBody) {
				t.Fatalf("got %d %s, want %d containing %q", w.Code, w.Body.String(), tc.want, tc.wantBody)
			}
		})
	}

	var rec userTwoFA
	if err := f.db.First(&rec, "user_id = ?", "enrolled-user").Error; err != nil {
		t.Fatal(err)
	}
	if rec.LastUsedAt == nil {
		t.Error("last_used_at not recorded after a valid code")
	}
}

func TestTwoFAEnforcer_RecoveryCodeIsSingleUse(t *testing.T) {
	f := newTwoFAFixture(t)
	code := f.recovery[0]

	if w := f.do("enrolled-user", code); w.Code != http.StatusOK {
		t.Fatalf("first use: got %d %s", w.Code, w.Body.String())
	}
	if w := f.do("enrolled-user", code); w.Code != http.StatusForbidden {
		t.Fatalf("reuse: got %d, want 403", w.Code)
	}

	var rec userTwoFA
	if err := f.db.First(&rec, "user_id = ?", "enrolled-user").Error; err != nil {
		t.Fatal(err)
	}
	if n := len(unmarshalStringSlice(rec.RecoveryCodes)); n != len(f.recovery)-1 {
		t.Errorf("remaining recovery codes = %d, want %d", n, len(f.recovery)-1)
	}
}
