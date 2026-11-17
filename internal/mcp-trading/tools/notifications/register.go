package notifications

import (
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/server"
	"github.com/sirupsen/logrus"
)

var logger *logrus.Logger

// InitLogger initializes the package logger
func InitLogger(log *logrus.Logger) {
	if log != nil {
		logger = log
	} else {
		logger = logrus.New()
		logger.SetFormatter(&logrus.JSONFormatter{})
	}
}

// RegisterAll registers all notification management tools
func RegisterAll(srv *server.MCPServer, client *gctclient.Client) error {
	return RegisterAllWithLogger(srv, client, nil)
}

// RegisterAllWithLogger registers all notification management tools with a custom logger
func RegisterAllWithLogger(srv *server.MCPServer, client *gctclient.Client, log *logrus.Logger) error {
	// Initialize the package logger
	InitLogger(log)

	tools := []server.Tool{
		// Notification Configuration
		NewConfigureNotificationsTool(client),
		NewSetNotificationPreferencesTool(client),
		NewGetNotificationPreferencesTool(client),
		NewTestNotificationTool(client),
		NewRegisterFCMTokenTool(client),

		// Funding Rate Alerts
		NewCreateFundingRateAlertTool(client),
	}

	for _, tool := range tools {
		if err := srv.RegisterTool(tool); err != nil {
			return err
		}
	}

	return nil
}