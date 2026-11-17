package websocket

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/gorilla/websocket"
	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
)

// upgrader is the WebSocket upgrader with configured options.
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		// In production, this should validate the origin.
		// For development, we allow all origins.
		// TODO: Configure allowed origins from environment.
		return true
	},
	EnableCompression: true,
}

// Handler handles WebSocket connections.
type Handler struct {
	hub    *Hub
	logger *slog.Logger

	// jwtSecret validates the HMAC signature of tokens presented on connect
	// (HTTP-05). Set via SetJWTSecret once the platform JWT secret is
	// available (same value as KongJWTMiddleware's secret elsewhere in this
	// service). Empty means auth is not configured: connections are accepted
	// unauthenticated (IsAuthenticated stays false), matching the previous
	// behavior for callers/tests that never wire a secret.
	jwtSecret string

	// requireAuth rejects (HTTP 401, before the upgrade) any connection that
	// does not present a valid token. Set with SetRequireAuth.
	requireAuth bool
}

// NewHandler creates a new WebSocket handler.
func NewHandler(hub *Hub, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}

	return &Handler{
		hub:    hub,
		logger: logger.With("component", "websocket_handler"),
	}
}

// SetJWTSecret configures the HMAC secret used by authenticateClient to
// validate connection tokens (HTTP-05). Reuse the same secret as
// KongJWTMiddleware/WebSocketHandler elsewhere in this service — do not
// introduce a second independent secret.
func (h *Handler) SetJWTSecret(secret string) {
	h.jwtSecret = secret
}

// SetRequireAuth makes the handler reject connections without a valid JWT
// (secure defaults, task 6.8). The token is read from the "token" query
// parameter or the Authorization header and validated with the same rules as
// the REST middleware, including the local authority in standalone mode.
func (h *Handler) SetRequireAuth(require bool) {
	h.requireAuth = require
}

// authorize reports whether the connection attempt may proceed.
func (h *Handler) authorize(token string) bool {
	if !h.requireAuth {
		return true
	}
	if token == "" {
		return false
	}
	_, err := tradingmw.ValidateToken(token, h.jwtSecret)
	return err == nil
}

// wsClaims mirrors the claim shape issued by the platform auth service /
// KongJWTMiddleware (sub/user_level/tenant_id + standard registered claims).
type wsClaims struct {
	UserID    string   `json:"sub"`
	UserLevel float64  `json:"user_level"`
	Level     float64  `json:"level"`
	TenantID  string   `json:"tenant_id"`
	Username  string   `json:"username"`
	Platform  string   `json:"platform"`
	Perms     []string `json:"permissions"`
	jwt.RegisteredClaims
}

// HandleWebSocket handles the WebSocket upgrade and connection.
// This is the main entry point for WebSocket connections.
func (h *Handler) HandleWebSocket(c *gin.Context) {
	// Get authentication token from query param or header.
	token := h.extractToken(c)
	if !h.authorize(token) {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "code": "UNAUTHORIZED"})
		return
	}

	// Get stream/room ID from query param.
	streamID := c.Query("stream_id")
	roomID := c.Query("room_id")
	if roomID == "" {
		roomID = streamID
	}

	// Get tenant ID from header or query param.
	tenantID := c.GetHeader("X-Tenant-ID")
	if tenantID == "" {
		tenantID = c.Query("tenant")
	}

	// Get client metadata.
	clientID := c.Query("client_id")
	clientVersion := c.Query("client_version")
	platform := c.Query("platform")

	// Upgrade the HTTP connection to WebSocket.
	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		h.logger.Error("failed to upgrade connection", "error", err)
		return
	}

	// Create a new client.
	client := NewClient(h.hub, conn, h.logger)

	// Set client metadata.
	if clientID != "" {
		client.SetMetadata("client_id", clientID)
	}
	if clientVersion != "" {
		client.SetMetadata("client_version", clientVersion)
	}
	if tenantID != "" {
		client.SetMetadata("tenant_id", tenantID)
	}
	if platform != "" {
		client.Platform = platform
	}

	h.logger.Info("new WebSocket connection",
		"client_id", client.ID,
		"remote_addr", c.ClientIP(),
		"user_agent", c.Request.UserAgent(),
		"room_id", roomID,
		"platform", platform,
	)

	// Authenticate if token provided.
	if token != "" {
		h.authenticateClient(client, token)
	}

	// Register the client with the hub.
	h.hub.Register(client)

	// Join room if specified.
	if roomID != "" {
		client.JoinRoom(roomID)
	}

	// Start the client pumps in goroutines.
	go client.WritePump()
	go client.ReadPump()
}

// extractToken extracts the authentication token from the request.
func (h *Handler) extractToken(c *gin.Context) string {
	// Check query parameter first.
	token := c.Query("token")
	if token != "" {
		return token
	}

	// Check Authorization header.
	authHeader := c.GetHeader("Authorization")
	if authHeader != "" {
		// Handle "Bearer <token>" format.
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
			return parts[1]
		}
		return authHeader
	}

	return ""
}

// authenticateClient authenticates a client with the given token (HTTP-05).
// It validates the JWT signature/expiry against h.jwtSecret and, on success,
// marks the client authenticated with the identity/permissions from the
// token. On any failure (bad signature, expired, no secret configured) the
// client is left unauthenticated — callers that gate actions on
// client.IsAuthenticated (see rooms.go) will correctly refuse it.
func (h *Handler) authenticateClient(client *Client, token string) {
	if h.jwtSecret == "" {
		h.logger.Warn("websocket auth attempted but no JWT secret configured; leaving client unauthenticated", "client_id", client.ID)
		return
	}

	// Signature, expiry and (standalone mode) epoch/revocation are checked by
	// the shared validator; the extra WebSocket claims are then read from the
	// already verified token.
	if _, err := tradingmw.ValidateToken(token, h.jwtSecret); err != nil {
		h.logger.Warn("websocket token validation failed", "client_id", client.ID, "error", err)
		return
	}
	claims := &wsClaims{}
	if _, _, err := jwt.NewParser(jwt.WithoutClaimsValidation()).ParseUnverified(token, claims); err != nil {
		h.logger.Warn("websocket token has unexpected claims shape", "client_id", client.ID)
		return
	}

	userID := claims.UserID
	if userID == "" {
		userID = claims.Subject
	}
	if userID == "" {
		h.logger.Warn("websocket token has no subject/user id", "client_id", client.ID)
		return
	}

	client.SetUserInfo(userID, claims.Username, claims.Platform, claims.Perms)
	h.logger.Info("websocket client authenticated", "client_id", client.ID, "user_id", userID)
}

// HandleStats returns WebSocket statistics.
func (h *Handler) HandleStats(c *gin.Context) {
	stats := h.hub.GetStats()
	roomStats := h.hub.RoomManager().GetStats()

	c.JSON(http.StatusOK, gin.H{
		"hub": gin.H{
			"total_connections":  stats.TotalConnections,
			"active_connections": stats.ActiveConnections,
			"messages_sent":      stats.TotalMessagesSent,
			"messages_received":  stats.TotalMessagesRecv,
			"uptime_seconds":     int64(stats.StartTime.Unix()),
		},
		"rooms": gin.H{
			"total_rooms":   roomStats.TotalRooms,
			"active_rooms":  roomStats.ActiveRooms,
			"empty_rooms":   roomStats.EmptyRooms,
			"total_clients": roomStats.TotalClients,
		},
		"channels": h.getChannelStats(),
	})
}

// getChannelStats returns statistics about channel subscriptions.
func (h *Handler) getChannelStats() map[string]int {
	stats := make(map[string]int)
	for channel := range ValidChannels {
		stats[channel] = h.hub.GetChannelClientCount(channel)
	}
	return stats
}

// HandleChannels returns available WebSocket channels.
func (h *Handler) HandleChannels(c *gin.Context) {
	channels := make([]map[string]any, 0, len(ValidChannels))

	channelDescriptions := map[string]string{
		ChannelRounds:      "Round lifecycle events (start, end, results)",
		ChannelVotes:       "Vote updates and milestones",
		ChannelTrades:      "Trade execution and closure events",
		ChannelPnL:         "Real-time P&L updates for open positions",
		ChannelNarrator:    "AI narrator speech events",
		ChannelLeaderboard: "Leaderboard changes and updates",
		ChannelSystem:      "System events (connected, heartbeat)",
		ChannelChat:        "Chat messages from viewers",
	}

	for channel := range ValidChannels {
		channels = append(channels, map[string]any{
			"name":        channel,
			"description": channelDescriptions[channel],
			"subscribers": h.hub.GetChannelClientCount(channel),
		})
	}

	groups := make([]map[string]any, 0, len(ChannelGroups))
	groupDescriptions := map[string]string{
		"overlay":   "All events needed for the stream overlay",
		"analytics": "Events for analytics and tracking",
		"minimal":   "Minimal events for lightweight clients",
		"all":       "All available channels",
	}

	for group, groupChannels := range ChannelGroups {
		groups = append(groups, map[string]any{
			"name":        group,
			"description": groupDescriptions[group],
			"channels":    groupChannels,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"channels": channels,
		"groups":   groups,
	})
}

// HandleRooms returns information about active rooms.
func (h *Handler) HandleRooms(c *gin.Context) {
	rooms := h.hub.RoomManager().GetAllRooms()

	c.JSON(http.StatusOK, gin.H{
		"rooms": rooms,
		"total": len(rooms),
	})
}

// HandleRoomInfo returns information about a specific room.
func (h *Handler) HandleRoomInfo(c *gin.Context) {
	roomID := c.Param("roomId")
	if roomID == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_REQUEST",
				"message": "Room ID is required",
			},
		})
		return
	}

	room := h.hub.RoomManager().GetRoom(roomID)
	if room == nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": gin.H{
				"code":    "NOT_FOUND",
				"message": "Room not found",
			},
		})
		return
	}

	c.JSON(http.StatusOK, room.GetInfo())
}

// HandleBroadcast broadcasts a message to all clients (admin endpoint).
func (h *Handler) HandleBroadcast(c *gin.Context) {
	var req struct {
		Type    string `json:"type" binding:"required"`
		Payload any    `json:"payload"`
		Channel string `json:"channel"`
		RoomID  string `json:"room_id"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": gin.H{
				"code":    "INVALID_REQUEST",
				"message": err.Error(),
			},
		})
		return
	}

	message := NewWSMessage(req.Type, req.Payload)

	var targetCount int
	if req.RoomID != "" {
		h.hub.BroadcastToRoom(req.RoomID, message)
		targetCount = h.hub.GetRoomClientCount(req.RoomID)
	} else if req.Channel != "" {
		h.hub.BroadcastToChannel(req.Channel, message)
		targetCount = h.hub.GetChannelClientCount(req.Channel)
	} else {
		h.hub.BroadcastToAll(message)
		targetCount = h.hub.GetClientCount()
	}

	c.JSON(http.StatusOK, gin.H{
		"success":       true,
		"clients_count": targetCount,
		"message_type":  req.Type,
	})
}

// HandleClients returns information about connected clients (admin endpoint).
func (h *Handler) HandleClients(c *gin.Context) {
	// This endpoint should be protected by admin authentication.
	// Returning limited information for security.

	c.JSON(http.StatusOK, gin.H{
		"total_clients": h.hub.GetClientCount(),
		"channel_stats": h.getChannelStats(),
	})
}

// RegisterRoutes registers WebSocket-related routes on a Gin router.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// WebSocket endpoint.
	r.GET("/ws", h.HandleWebSocket)

	// WebSocket API endpoints.
	wsAPI := r.Group("/api/v1/ws")
	{
		wsAPI.GET("/channels", h.HandleChannels)
		wsAPI.GET("/stats", h.HandleStats)
		wsAPI.GET("/rooms", h.HandleRooms)
		wsAPI.GET("/rooms/:roomId", h.HandleRoomInfo)
		wsAPI.GET("/clients", h.HandleClients)
		wsAPI.POST("/broadcast", h.HandleBroadcast)
	}
}

// RegisterRoutesWithGroup registers WebSocket-related routes on a Gin router group.
func (h *Handler) RegisterRoutesWithGroup(r *gin.RouterGroup) {
	// WebSocket API endpoints.
	r.GET("/channels", h.HandleChannels)
	r.GET("/stats", h.HandleStats)
	r.GET("/rooms", h.HandleRooms)
	r.GET("/rooms/:roomId", h.HandleRoomInfo)
	r.GET("/clients", h.HandleClients)
	r.POST("/broadcast", h.HandleBroadcast)
}

// SetAllowedOrigins sets the allowed origins for WebSocket connections.
func SetAllowedOrigins(origins []string) {
	if len(origins) == 0 {
		return
	}

	allowedOrigins := make(map[string]bool)
	for _, origin := range origins {
		allowedOrigins[origin] = true
	}

	upgrader.CheckOrigin = func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}
		return allowedOrigins[origin]
	}
}

// SetBufferSizes sets the read and write buffer sizes for WebSocket connections.
func SetBufferSizes(readSize, writeSize int) {
	if readSize > 0 {
		upgrader.ReadBufferSize = readSize
	}
	if writeSize > 0 {
		upgrader.WriteBufferSize = writeSize
	}
}

// SetCompression enables or disables compression for WebSocket connections.
func SetCompression(enabled bool) {
	upgrader.EnableCompression = enabled
}

// HandleWebSocketHTTP is an adapter for standard http.Handler (non-Gin).
// This allows the WebSocket handler to work with http.ServeMux.
func (h *Handler) HandleWebSocketHTTP(w http.ResponseWriter, r *http.Request) {
	// Get authentication token from query param or header.
	token := r.URL.Query().Get("token")
	if token == "" {
		authHeader := r.Header.Get("Authorization")
		if authHeader != "" {
			// Handle "Bearer <token>" format.
			parts := strings.SplitN(authHeader, " ", 2)
			if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
				token = parts[1]
			} else {
				token = authHeader
			}
		}
	}

	if !h.authorize(token) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized","code":"UNAUTHORIZED"}`))
		return
	}

	// Get stream/room ID from query param.
	streamID := r.URL.Query().Get("stream_id")
	roomID := r.URL.Query().Get("room_id")
	if roomID == "" {
		roomID = streamID
	}

	// Get tenant ID from header or query param.
	tenantID := r.Header.Get("X-Tenant-ID")
	if tenantID == "" {
		tenantID = r.URL.Query().Get("tenant")
	}

	// Get client metadata.
	clientID := r.URL.Query().Get("client_id")
	clientVersion := r.URL.Query().Get("client_version")
	platform := r.URL.Query().Get("platform")

	// Upgrade the HTTP connection to WebSocket.
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.Error("failed to upgrade connection", "error", err)
		return
	}

	// Create a new client.
	client := NewClient(h.hub, conn, h.logger)

	// Set client metadata.
	if clientID != "" {
		client.SetMetadata("client_id", clientID)
	}
	if clientVersion != "" {
		client.SetMetadata("client_version", clientVersion)
	}
	if tenantID != "" {
		client.SetMetadata("tenant_id", tenantID)
	}
	if platform != "" {
		client.Platform = platform
	}

	h.logger.Info("new WebSocket connection",
		"client_id", client.ID,
		"remote_addr", r.RemoteAddr,
		"user_agent", r.UserAgent(),
		"room_id", roomID,
		"platform", platform,
	)

	// Authenticate if token provided.
	if token != "" {
		h.authenticateClient(client, token)
	}

	// Register the client with the hub.
	h.hub.Register(client)

	// Join room if specified.
	if roomID != "" {
		client.JoinRoom(roomID)
	}

	// Start the client pumps in goroutines.
	go client.WritePump()
	go client.ReadPump()
}

// HandleStatsHTTP is an adapter for standard http.Handler (non-Gin).
func (h *Handler) HandleStatsHTTP(w http.ResponseWriter, r *http.Request) {
	stats := h.hub.GetStats()
	roomStats := h.hub.RoomManager().GetStats()

	response := map[string]interface{}{
		"hub": map[string]interface{}{
			"total_connections":  stats.TotalConnections,
			"active_connections": stats.ActiveConnections,
			"messages_sent":      stats.TotalMessagesSent,
			"messages_received":  stats.TotalMessagesRecv,
			"uptime_seconds":     int64(stats.StartTime.Unix()),
		},
		"rooms": map[string]interface{}{
			"total_rooms":   roomStats.TotalRooms,
			"active_rooms":  roomStats.ActiveRooms,
			"empty_rooms":   roomStats.EmptyRooms,
			"total_clients": roomStats.TotalClients,
		},
		"channels": h.getChannelStatsHTTP(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// getChannelStatsHTTP returns statistics about channel subscriptions.
func (h *Handler) getChannelStatsHTTP() map[string]int {
	stats := make(map[string]int)
	for channel := range ValidChannels {
		stats[channel] = h.hub.GetChannelClientCount(channel)
	}
	return stats
}

// HandleChannelsHTTP is an adapter for standard http.Handler (non-Gin).
func (h *Handler) HandleChannelsHTTP(w http.ResponseWriter, r *http.Request) {
	channels := make([]map[string]interface{}, 0, len(ValidChannels))

	channelDescriptions := map[string]string{
		ChannelRounds:      "Round lifecycle events (start, end, results)",
		ChannelVotes:       "Vote updates and milestones",
		ChannelTrades:      "Trade execution and closure events",
		ChannelPnL:         "Real-time P&L updates for open positions",
		ChannelNarrator:    "AI narrator speech events",
		ChannelLeaderboard: "Leaderboard changes and updates",
		ChannelSystem:      "System events (connected, heartbeat)",
		ChannelChat:        "Chat messages from viewers",
	}

	for channel := range ValidChannels {
		channels = append(channels, map[string]interface{}{
			"name":        channel,
			"description": channelDescriptions[channel],
			"subscribers": h.hub.GetChannelClientCount(channel),
		})
	}

	groups := make([]map[string]interface{}, 0, len(ChannelGroups))
	groupDescriptions := map[string]string{
		"overlay":   "All events needed for the stream overlay",
		"analytics": "Events for analytics and tracking",
		"minimal":   "Minimal events for lightweight clients",
		"all":       "All available channels",
	}

	for group, groupChannels := range ChannelGroups {
		groups = append(groups, map[string]interface{}{
			"name":        group,
			"description": groupDescriptions[group],
			"channels":    groupChannels,
		})
	}

	response := map[string]interface{}{
		"channels": channels,
		"groups":   groups,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// HandleRoomsHTTP is an adapter for standard http.Handler (non-Gin).
func (h *Handler) HandleRoomsHTTP(w http.ResponseWriter, r *http.Request) {
	rooms := h.hub.RoomManager().GetAllRooms()

	response := map[string]interface{}{
		"rooms": rooms,
		"total": len(rooms),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
