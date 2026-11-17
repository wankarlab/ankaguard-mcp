package middleware

import (
	"net/http"
	"testing"
)

// Launch routes enforce level and feature inside their handlers; the path
// rule must not raise shadow reads (e.g. positions) above level 1.
func TestRequiredLevelForLaunchPaths(t *testing.T) {
	cases := []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/api/v1/launch/positions", 1},
		{http.MethodGet, "/api/v1/launch/positions/12", 1},
		{http.MethodPost, "/api/v1/launch/kill-switch", 1},
		// Outside launch the blanket rules still apply.
		{http.MethodGet, "/api/v1/positions", 3},
		{http.MethodGet, "/api/v1/bot/positions", 3},
		{http.MethodPost, "/api/v1/orders", 3},
	}
	for _, c := range cases {
		if got := getRequiredLevelForPath(c.method, c.path); got != c.want {
			t.Errorf("%s %s: level %d, want %d", c.method, c.path, got, c.want)
		}
	}
}
