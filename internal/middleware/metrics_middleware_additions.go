package middleware

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// rateLimitHitTotal counts the number of requests that were denied by the
// per-user rate limiter.  The user_id_hash label uses the first 16 hex chars
// of SHA-256(user_id) so that individual users are identifiable in dashboards
// without storing raw identifiers.
var rateLimitHitTotal = promauto.NewCounterVec(
	prometheus.CounterOpts{
		Name: "rate_limit_hit_total",
		Help: "Total rate limit hits by user (hashed) and endpoint",
	},
	[]string{"user_id_hash", "endpoint"},
)
