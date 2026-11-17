// Package handlers contains HTTP handlers for REST API v1
package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	"github.com/rantipay/trading-mcp/internal/api/v1/models"
	"github.com/rantipay/trading-mcp/internal/mcp-trading/gctclient"
	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/sirupsen/logrus"
)

const (
	// Time allowed to write a message to the peer
	writeWait = 10 * time.Second

	// Time allowed to read the next pong message from the peer
	pongWait = 60 * time.Second

	// Send pings to peer with this period (must be less than pongWait)
	pingPeriod = (pongWait * 9) / 10

	// Maximum message size allowed from peer
	maxMessageSize = 4096
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// In production, implement proper origin checking
		// For now, allow all origins for development
		return true
	},
}

// WebSocketHandler handles WebSocket connections for real-time data
type WebSocketHandler struct {
	hub       *Hub
	gctClient *gctclient.Client
	jwtSecret string
	logger    *logrus.Logger
}

// WebSocketHandlerConfig configures the WebSocket handler
type WebSocketHandlerConfig struct {
	Hub       *Hub
	GCTClient *gctclient.Client
	JWTSecret string
	Logger    *logrus.Logger
}

// NewWebSocketHandler creates a new WebSocket handler
func NewWebSocketHandler(cfg *WebSocketHandlerConfig) *WebSocketHandler {
	logger := cfg.Logger
	if logger == nil {
		logger = logrus.New()
	}

	return &WebSocketHandler{
		hub:       cfg.Hub,
		gctClient: cfg.GCTClient,
		jwtSecret: cfg.JWTSecret,
		logger:    logger,
	}
}

// HandleWebSocket handles WebSocket upgrade and connection
// @Summary WebSocket endpoint for real-time data streaming
// @Description Establishes a WebSocket connection for real-time updates
// @Tags websocket
// @Accept json
// @Produce json
// @Param token query string false "JWT token for authentication"
// @Success 101 {string} string "Switching Protocols"
// @Failure 401 {object} models.APIResponse "Unauthorized"
// @Router /api/v1/stream [get]
func (h *WebSocketHandler) HandleWebSocket(c *gin.Context) {
	// Try to get token from query parameter
	token := c.Query("token")

	// Pre-authenticate if token provided in query
	var userID, tenantID, companyID string
	var kongLevel int
	var preAuthenticated bool

	if token != "" {
		claims, err := h.validateJWT(token)
		if err != nil {
			h.logger.WithError(err).Warn("Invalid token in WebSocket query parameter")
			c.JSON(http.StatusUnauthorized, models.NewErrorResponse("UNAUTHORIZED", "Invalid token"))
			return
		}
		userID = claims.UserID
		tenantID = claims.TenantID
		companyID = claims.CompanyID
		kongLevel = int(claims.Level)
		preAuthenticated = true
	}

	// Upgrade HTTP connection to WebSocket
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.WithError(err).Error("Failed to upgrade WebSocket connection")
		return
	}

	// Create client
	client := NewClient(h.hub, conn)
	if preAuthenticated {
		client.SetAuthenticated(userID, tenantID, companyID, kongLevel)
	}

	// Register client with hub
	h.hub.Register(client)

	// Send welcome message
	h.sendWelcome(client, preAuthenticated)

	// Start read and write goroutines
	go h.writePump(client)
	go h.readPump(client)
}

// sendWelcome sends a welcome message to the client
func (h *WebSocketHandler) sendWelcome(client *Client, authenticated bool) {
	welcome := WSMessage{
		Type:      "welcome",
		Timestamp: time.Now(),
		Data:      json.RawMessage(fmt.Sprintf(`{"client_id":"%s","authenticated":%t}`, client.ID, authenticated)),
	}

	data, _ := json.Marshal(welcome)
	select {
	case client.Send <- data:
	default:
		h.logger.Warn("Failed to send welcome message")
	}
}

// readPump pumps messages from the WebSocket connection to the hub
func (h *WebSocketHandler) readPump(client *Client) {
	defer func() {
		h.hub.Unregister(client)
		client.Close()
	}()

	client.Conn.SetReadLimit(maxMessageSize)
	client.Conn.SetReadDeadline(time.Now().Add(pongWait))
	client.Conn.SetPongHandler(func(string) error {
		client.lastPing = time.Now()
		client.Conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		select {
		case <-client.ctx.Done():
			return
		default:
		}

		_, message, err := client.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				h.logger.WithError(err).WithField("client_id", client.ID).Warn("WebSocket read error")
			}
			return
		}

		// Update last ping time on any message
		client.lastPing = time.Now()

		// Process the message
		h.processMessage(client, message)
	}
}

// writePump pumps messages from the hub to the WebSocket connection
func (h *WebSocketHandler) writePump(client *Client) {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		client.Close()
	}()

	for {
		select {
		case <-client.ctx.Done():
			return

		case message, ok := <-client.Send:
			client.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// Hub closed the channel
				client.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := client.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Batch queued messages if any
			n := len(client.Send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-client.Send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			client.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := client.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// processMessage handles incoming WebSocket messages
func (h *WebSocketHandler) processMessage(client *Client, message []byte) {
	// Parse the message type
	var baseMsg struct {
		Action string `json:"action"`
	}

	if err := json.Unmarshal(message, &baseMsg); err != nil {
		h.sendError(client, "INVALID_MESSAGE", "Invalid JSON message", "")
		return
	}

	switch baseMsg.Action {
	case "auth":
		h.handleAuth(client, message)
	case "subscribe":
		h.handleSubscribe(client, message)
	case "unsubscribe":
		h.handleUnsubscribe(client, message)
	case "ping":
		h.handlePing(client)
	default:
		h.sendError(client, "UNKNOWN_ACTION", fmt.Sprintf("Unknown action: %s", baseMsg.Action), "")
	}
}

// handleAuth handles authentication messages
func (h *WebSocketHandler) handleAuth(client *Client, message []byte) {
	var authReq WSAuthRequest
	if err := json.Unmarshal(message, &authReq); err != nil {
		h.sendError(client, "INVALID_AUTH", "Invalid auth request", "")
		return
	}

	if authReq.Token == "" {
		h.sendError(client, "MISSING_TOKEN", "Token is required", "")
		return
	}

	claims, err := h.validateJWT(authReq.Token)
	if err != nil {
		h.sendError(client, "INVALID_TOKEN", "Token validation failed", err.Error())
		return
	}

	// Update client authentication
	client.SetAuthenticated(claims.UserID, claims.TenantID, claims.CompanyID, int(claims.Level))

	// Re-register to update user mapping in hub
	h.hub.Unregister(client)
	h.hub.Register(client)

	// Send success response
	response := WSMessage{
		Type:      "auth_success",
		Timestamp: time.Now(),
		Data: json.RawMessage(fmt.Sprintf(
			`{"user_id":"%s","tenant_id":"%s","level":%d}`,
			claims.UserID, claims.TenantID, int(claims.Level),
		)),
	}
	data, _ := json.Marshal(response)
	client.Send <- data

	h.logger.WithFields(logrus.Fields{
		"client_id": client.ID,
		"user_id":   claims.UserID,
	}).Info("Client authenticated via WebSocket")
}

// handleSubscribe handles subscription requests
func (h *WebSocketHandler) handleSubscribe(client *Client, message []byte) {
	if !client.Authenticated {
		h.sendError(client, "UNAUTHORIZED", "Authentication required", "")
		return
	}

	var subReq WSSubscribeRequest
	if err := json.Unmarshal(message, &subReq); err != nil {
		h.sendError(client, "INVALID_SUBSCRIBE", "Invalid subscribe request", "")
		return
	}

	subscribedChannels := make([]string, 0)
	failedChannels := make([]string, 0)

	for _, channel := range subReq.Channels {
		// Validate channel format and permissions
		if err := h.validateChannel(client, channel); err != nil {
			h.logger.WithFields(logrus.Fields{
				"client_id": client.ID,
				"channel":   channel,
				"error":     err.Error(),
			}).Warn("Channel validation failed")
			failedChannels = append(failedChannels, channel)
			continue
		}

		// Subscribe client to channel
		h.hub.SubscribeClient(client, channel)
		subscribedChannels = append(subscribedChannels, channel)
	}

	// Send subscription response
	responseData := map[string]interface{}{
		"subscribed": subscribedChannels,
	}
	if len(failedChannels) > 0 {
		responseData["failed"] = failedChannels
	}

	respBytes, _ := json.Marshal(responseData)
	response := WSMessage{
		Type:      "subscribed",
		Timestamp: time.Now(),
		Data:      respBytes,
	}
	data, _ := json.Marshal(response)
	client.Send <- data

	h.logger.WithFields(logrus.Fields{
		"client_id":  client.ID,
		"user_id":    client.UserID,
		"subscribed": subscribedChannels,
		"failed":     failedChannels,
	}).Info("Client subscription processed")
}

// handleUnsubscribe handles unsubscription requests
func (h *WebSocketHandler) handleUnsubscribe(client *Client, message []byte) {
	var subReq WSSubscribeRequest
	if err := json.Unmarshal(message, &subReq); err != nil {
		h.sendError(client, "INVALID_UNSUBSCRIBE", "Invalid unsubscribe request", "")
		return
	}

	for _, channel := range subReq.Channels {
		h.hub.UnsubscribeClient(client, channel)
	}

	// Send unsubscription response
	respBytes, _ := json.Marshal(map[string]interface{}{
		"unsubscribed": subReq.Channels,
	})
	response := WSMessage{
		Type:      "unsubscribed",
		Timestamp: time.Now(),
		Data:      respBytes,
	}
	data, _ := json.Marshal(response)
	client.Send <- data
}

// handlePing handles ping messages
func (h *WebSocketHandler) handlePing(client *Client) {
	response := WSMessage{
		Type:      "pong",
		Timestamp: time.Now(),
	}
	data, _ := json.Marshal(response)
	client.Send <- data
}

// validateChannel validates a channel subscription request
func (h *WebSocketHandler) validateChannel(client *Client, channel string) error {
	parts := strings.SplitN(channel, ":", 2)
	if len(parts) == 0 {
		return fmt.Errorf("invalid channel format")
	}

	channelType := ChannelType(parts[0])

	switch channelType {
	case ChannelTicker, ChannelTrades, ChannelOrderbook:
		// Market data channels require a pair (e.g., "ticker:BTC-USDT")
		if len(parts) < 2 || parts[1] == "" {
			return fmt.Errorf("market data channels require a pair, e.g., ticker:BTC-USDT")
		}
		// Validate pair format (basic validation)
		pair := strings.ToUpper(parts[1])
		if !strings.Contains(pair, "-") && !strings.Contains(pair, "/") {
			return fmt.Errorf("invalid pair format: %s", pair)
		}
		return nil

	case ChannelBot:
		// Bot channels require level 2+ and either "all" or a specific bot ID
		if client.KongLevel < 2 {
			return fmt.Errorf("insufficient permissions for bot channel")
		}
		if len(parts) < 2 || parts[1] == "" {
			return fmt.Errorf("bot channel requires bot ID or 'all'")
		}
		return nil

	case ChannelPositions:
		// Positions require level 3+
		if client.KongLevel < 3 {
			return fmt.Errorf("insufficient permissions for positions channel")
		}
		return nil

	case ChannelOrders:
		// Orders require level 3+
		if client.KongLevel < 3 {
			return fmt.Errorf("insufficient permissions for orders channel")
		}
		return nil

	case ChannelBalances:
		// Balances require level 2+
		if client.KongLevel < 2 {
			return fmt.Errorf("insufficient permissions for balances channel")
		}
		return nil

	default:
		return fmt.Errorf("unknown channel type: %s", parts[0])
	}
}

// sendError sends an error message to the client
func (h *WebSocketHandler) sendError(client *Client, code, message, details string) {
	errMsg := WSErrorMessage{
		Code:    code,
		Message: message,
		Details: details,
	}
	errBytes, _ := json.Marshal(errMsg)

	response := WSMessage{
		Type:      "error",
		Timestamp: time.Now(),
		Data:      errBytes,
	}
	data, _ := json.Marshal(response)

	select {
	case client.Send <- data:
	default:
		h.logger.Warn("Failed to send error message to client")
	}
}

// JWTClaims for WebSocket authentication
type JWTClaims struct {
	UserID    string  `json:"user_id"`
	Level     float64 `json:"level"`
	TenantID  string  `json:"tenant_id"`
	CompanyID string  `json:"company_id"`
	Email     string  `json:"email"`
	jwt.RegisteredClaims
}

// validateJWT validates a JWT token and returns claims
func (h *WebSocketHandler) validateJWT(tokenString string) (*JWTClaims, error) {
	// Standalone mode: the local authority supplies the secret and applies the
	// session epoch / agent revocation rules. External mode is unchanged.
	if tradingmw.HasLocalAuthority() {
		if _, err := tradingmw.ValidateToken(tokenString, h.jwtSecret); err != nil {
			return nil, err
		}
	}
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return []byte(tradingmw.EffectiveSecret(h.jwtSecret)), nil
	})

	if err != nil {
		return nil, err
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	claims, ok := token.Claims.(*JWTClaims)
	if !ok {
		return nil, fmt.Errorf("invalid claims")
	}

	return claims, nil
}

// GetHub returns the WebSocket hub
func (h *WebSocketHandler) GetHub() *Hub {
	return h.hub
}
