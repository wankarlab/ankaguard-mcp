package server

import (
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/rantipay/trading-mcp/internal/mcp-trading/config"
)

// M4: the product edition leaves /api/v1/user/fcm-token unmounted.
func TestOmitDeviceRoutes(t *testing.T) {
	log := logrus.New()
	log.SetLevel(logrus.PanicLevel)
	srv := NewMCPServerForTesting(&config.Config{}, log, &TestGCTClient{Connected: true})
	_, patterns := srv.HTTPHandler()
	require.Contains(t, patterns, "/api/v1/user/fcm-token")
	srv.OmitDeviceRoutes()
	_, patterns = srv.HTTPHandler()
	require.NotContains(t, patterns, "/api/v1/user/fcm-token")
}
