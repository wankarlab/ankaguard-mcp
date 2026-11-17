package middleware

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rantipay/trading-mcp/internal/subscription"
	"github.com/sirupsen/logrus"
)

// fakeSubscriptionRepo implements only what Service.CheckFeatureAccess
// reaches; any other method panics through the nil embedded interface.
type fakeSubscriptionRepo struct {
	subscription.Repository
	subs map[uuid.UUID]*subscription.UserSubscription
	err  map[uuid.UUID]error
}

func (r *fakeSubscriptionRepo) GetByUserID(_ context.Context, id uuid.UUID) (*subscription.UserSubscription, error) {
	if err := r.err[id]; err != nil {
		return nil, err
	}
	if s, ok := r.subs[id]; ok {
		return s, nil
	}
	return nil, subscription.ErrSubscriptionNotFound
}

func (r *fakeSubscriptionRepo) Create(_ context.Context, s *subscription.UserSubscription) error {
	r.subs[s.UserID] = s
	return nil
}

func (r *fakeSubscriptionRepo) LogEvent(context.Context, *subscription.SubscriptionEvent) error {
	return nil
}

func TestRequireFeature_HTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	past := time.Now().Add(-time.Hour)
	future := time.Now().Add(24 * time.Hour)
	var (
		pro      = uuid.New()
		basic    = uuid.New()
		expired  = uuid.New()
		inactive = uuid.New()
		unknown  = uuid.New()
		broken   = uuid.New()
	)
	repo := &fakeSubscriptionRepo{
		subs: map[uuid.UUID]*subscription.UserSubscription{
			pro:      {UserID: pro, Tier: subscription.TierPro, IsActive: true, ExpiresAt: &future, Features: []subscription.Feature{subscription.FeatureRealTrading}},
			basic:    {UserID: basic, Tier: subscription.TierBasic, IsActive: true, ExpiresAt: &future, Features: []subscription.Feature{subscription.FeatureBacktesting}},
			expired:  {UserID: expired, Tier: subscription.TierPro, IsActive: true, ExpiresAt: &past, Features: []subscription.Feature{subscription.FeatureRealTrading}},
			inactive: {UserID: inactive, Tier: subscription.TierPro, IsActive: false, ExpiresAt: &future, Features: []subscription.Feature{subscription.FeatureRealTrading}},
		},
		err: map[uuid.UUID]error{broken: errors.New("db down")},
	}
	log := logrus.New()
	log.SetOutput(io.Discard)
	mw := NewSubscriptionMiddleware(subscription.NewService(repo, log, nil), log)

	r := gin.New()
	r.Use(func(c *gin.Context) {
		if u := c.GetHeader("X-Test-User"); u != "" {
			c.Set("user_id", u)
		}
	})
	r.POST("/real", mw.RequireFeature(subscription.FeatureRealTrading), func(c *gin.Context) { c.String(http.StatusOK, "ok") })

	cases := []struct {
		name     string
		user     string
		want     int
		wantBody string
	}{
		{"no user", "", http.StatusUnauthorized, "USER_REQUIRED"},
		{"malformed user id", "not-a-uuid", http.StatusUnauthorized, "USER_REQUIRED"},
		{"tier with feature", pro.String(), http.StatusOK, "ok"},
		{"tier without feature", basic.String(), http.StatusForbidden, "FEATURE_NOT_AVAILABLE"},
		{"expired subscription", expired.String(), http.StatusForbidden, "SUBSCRIPTION_EXPIRED"},
		{"inactive subscription", inactive.String(), http.StatusForbidden, "SUBSCRIPTION_INACTIVE"},
		{"unknown user gets free tier", unknown.String(), http.StatusForbidden, "FEATURE_NOT_AVAILABLE"},
		{"repository error fails closed", broken.String(), http.StatusForbidden, "FEATURE_NOT_AVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/real", nil)
			if tc.user != "" {
				req.Header.Set("X-Test-User", tc.user)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want || !strings.Contains(w.Body.String(), tc.wantBody) {
				t.Fatalf("got %d %s, want %d containing %q", w.Code, w.Body.String(), tc.want, tc.wantBody)
			}
			if tc.want == http.StatusForbidden && !strings.Contains(w.Body.String(), `"required_feature":"real_trading"`) {
				t.Errorf("403 body does not name the required feature: %s", w.Body.String())
			}
		})
	}
}
