package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestOptionalKongJWTMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	const secret = "s3cret"
	mint := func(sec string) string {
		tok, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"sub": "u1", "exp": time.Now().Add(time.Hour).Unix(),
		}).SignedString([]byte(sec))
		return tok
	}
	r := gin.New()
	r.GET("/x", OptionalKongJWTMiddleware(secret, "", nil), func(c *gin.Context) {
		c.JSON(200, gin.H{"auth": IsAuthenticated(c), "uid": c.GetString("user_id")})
	})
	cases := []struct {
		name, header string
		wantAuth     bool
	}{
		{"missing", "", false},
		{"garbage", "Bearer xxx", false},
		{"bad format", "Token abc", false},
		{"wrong signature", "Bearer " + mint("other"), false},
		{"valid", "Bearer " + mint(secret), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/x", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != 200 {
				t.Fatalf("code %d, optional middleware must never abort", w.Code)
			}
			got := w.Body.String()
			want := `{"auth":false,"uid":""}`
			if tc.wantAuth {
				want = `{"auth":true,"uid":"u1"}`
			}
			if got != want {
				t.Errorf("got %s want %s", got, want)
			}
		})
	}
}
