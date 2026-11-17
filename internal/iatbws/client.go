package websocket

import (
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	// writeWait is the time allowed to write a message to the peer.
	writeWait = 10 * time.Second

	// pongWait is the time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second

	// pingPeriod is the period to send pings to peer. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10

	// maxMessageSize is the maximum message size allowed from peer.
	maxMessageSize = 16384 // 16KB

	// sendBufferSize is the size of the client send buffer.
	sendBufferSize = 256

	// rateLimitWindow is the time window for rate limiting.
	rateLimitWindow = time.Minute

	// rateLimitMax is the maximum messages allowed per window.
	rateLimitMax = 100
)

// Client represents a connected WebSocket client.
type Client struct {
	// ID is the unique identifier for this client connection.
	ID string

	// UserID is the authenticated user ID (empty if anonymous).
	UserID string

	// Username is the user's display name.
	Username string

	// RoomID is the ID of the room the client has joined.
	RoomID string

	// Channels is the set of channels the client is subscribed to.
	Channels map[string]bool

	// IsAuthenticated indicates whether the client has authenticated.
	IsAuthenticated bool

	// Permissions contains the client's permissions.
	Permissions []string

	// Platform is the client's platform (tiktok, twitch, youtube, direct).
	Platform string

	// hub is a reference to the hub managing this client.
	hub *Hub

	// conn is the WebSocket connection.
	conn *websocket.Conn

	// send is a buffered channel for outbound messages.
	send chan []byte

	// closed indicates if the client connection is closed.
	closed atomic.Bool

	// closedChan is used to signal connection closure.
	closedChan chan struct{}

	// mu protects concurrent access to client fields.
	mu sync.RWMutex

	// logger for client operations.
	logger *slog.Logger

	// connectedAt is when the client connected.
	connectedAt time.Time

	// lastMessageAt is when the last message was received.
	lastMessageAt time.Time

	// messageCount is the number of messages received in the current window.
	messageCount int

	// windowStart is the start of the current rate limit window.
	windowStart time.Time

	// metadata stores additional client information.
	metadata map[string]any
}

// NewClient creates a new WebSocket client.
func NewClient(hub *Hub, conn *websocket.Conn, logger *slog.Logger) *Client {
	if logger == nil {
		logger = slog.Default()
	}

	clientID := uuid.New().String()

	return &Client{
		ID:          clientID,
		Channels:    make(map[string]bool),
		Permissions: []string{"view"},
		hub:         hub,
		conn:        conn,
		send:        make(chan []byte, sendBufferSize),
		closedChan:  make(chan struct{}),
		logger:      logger.With("component", "websocket_client", "client_id", clientID),
		connectedAt: time.Now(),
		windowStart: time.Now(),
		metadata:    make(map[string]any),
	}
}

// ReadPump pumps messages from the WebSocket connection to the hub.
// This method runs in its own goroutine and handles incoming messages.
func (c *Client) ReadPump() {
	defer func() {
		c.hub.Unregister(c)
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				c.logger.Warn("unexpected close error", "error", err)
			}
			return
		}

		// Check rate limit.
		if !c.checkRateLimit() {
			c.SendMessage(NewRateLimitError(5000))
			continue
		}

		c.lastMessageAt = time.Now()
		c.hub.IncrementMessagesReceived()

		// Process the message.
		c.handleMessage(message)
	}
}

// WritePump pumps messages from the hub to the WebSocket connection.
// This method runs in its own goroutine and handles outgoing messages.
func (c *Client) WritePump() {
	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case <-c.closedChan:
			return

		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				// The hub closed the channel.
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued messages to the current WebSocket frame.
			n := len(c.send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage processes an incoming message from the client.
func (c *Client) handleMessage(data []byte) {
	msg, err := ParseIncomingMessage(data)
	if err != nil {
		c.SendMessage(NewErrorMessage(ErrorCodeInvalidMessage, "Failed to parse message"))
		return
	}

	switch msg.Type {
	case MessageTypeAuth:
		c.handleAuth(msg)

	case MessageTypeSubscribe:
		c.handleSubscribe(msg)

	case MessageTypeUnsubscribe:
		c.handleUnsubscribe(msg)

	case MessageTypePing:
		c.SendMessage(NewPongMessage())

	case MessageTypeGetState:
		c.handleGetState()

	default:
		c.logger.Debug("unknown message type", "type", msg.Type)
		c.SendMessage(NewErrorMessage(ErrorCodeInvalidMessage, "Unknown message type: "+msg.Type))
	}
}

// handleAuth processes an authentication request.
func (c *Client) handleAuth(msg *IncomingMessage) {
	if msg.Token == "" {
		c.SendMessage(NewAuthFailureMessage(ErrorCodeAuthInvalid, "Token is required"))
		return
	}

	// TODO: Validate the JWT token with the auth service.
	// For now, we'll accept any non-empty token for development.
	// In production, this should call the auth service to validate the token.

	// Simulated token validation - in production, parse and validate JWT.
	// Example: claims, err := authService.ValidateToken(msg.Token)

	c.mu.Lock()
	c.IsAuthenticated = true
	c.UserID = "user-" + uuid.New().String()[:8] // Placeholder
	c.Username = "authenticated_user"              // Placeholder
	c.Permissions = []string{"view", "vote"}
	c.mu.Unlock()

	c.logger.Info("client authenticated", "user_id", c.UserID)

	c.SendMessage(NewAuthSuccessMessage(c.UserID, c.Username, c.Permissions))
}

// handleSubscribe processes a subscription request.
func (c *Client) handleSubscribe(msg *IncomingMessage) {
	subscribed := make([]string, 0)

	// Process channel groups first.
	for _, group := range msg.Groups {
		channels := c.hub.SubscribeToGroup(c, group)
		subscribed = append(subscribed, channels...)
	}

	// Process individual channels.
	for _, channel := range msg.Channels {
		if !IsValidChannel(channel) {
			continue
		}
		if c.hub.SubscribeToChannel(c, channel) {
			subscribed = append(subscribed, channel)
		}

		// Handle market_prices channel with symbol subscriptions.
		if channel == ChannelMarketPrices && len(msg.Symbols) > 0 {
			c.handleMarketPriceSubscription(msg.Symbols)
		}
	}

	// Remove duplicates.
	seen := make(map[string]bool)
	unique := make([]string, 0, len(subscribed))
	for _, ch := range subscribed {
		if !seen[ch] {
			seen[ch] = true
			unique = append(unique, ch)
		}
	}

	// Send subscription confirmation.
	response := NewSubscribedMessage(unique)
	
	// Add symbols to response if subscribing to market_prices.
	if len(msg.Symbols) > 0 {
		// Create a custom response with symbols.
		payload := map[string]interface{}{
			"channels": unique,
			"symbols":  msg.Symbols,
		}
		response = &WSMessage{
			Type:      MessageTypeSubscribed,
			Timestamp: time.Now().UTC(),
			Payload:   payload,
		}
	}

	c.SendMessage(response)
}

// handleMarketPriceSubscription handles subscription to specific market price symbols.
func (c *Client) handleMarketPriceSubscription(symbols []string) {
	marketPriceService := c.hub.MarketPriceService()

	// Subscribe to each symbol.
	for _, symbol := range symbols {
		marketPriceService.SubscribeToSymbol(c.ID, symbol)
	}

	// Send initial snapshot of current prices.
	// This will trigger the price adapter to start sending updates for these symbols.
	snapshot := marketPriceService.GetSnapshot(symbols)
	
	// Always send a snapshot message, even if empty initially.
	// The price adapter will populate prices for subscribed symbols.
	snapshotMsg := NewMarketPriceSnapshotMessage(snapshot)
	c.SendMessage(snapshotMsg)

	c.logger.Debug("client subscribed to market prices",
		"client_id", c.ID,
		"symbols", symbols,
		"snapshot_count", len(snapshot),
	)
}

// handleUnsubscribe processes an unsubscription request.
func (c *Client) handleUnsubscribe(msg *IncomingMessage) {
	unsubscribed := c.hub.UnsubscribeFromChannels(c, msg.Channels)
	c.SendMessage(NewUnsubscribedMessage(unsubscribed))
}

// handleGetState processes a state request.
func (c *Client) handleGetState() {
	// TODO: Get actual state from the game service.
	// For now, return an empty state.
	state := &StatePayload{
		ActiveRound: nil,
		OpenTrade:   nil,
		DailyPnL:    0,
	}

	c.SendMessage(NewWSMessage(MessageTypeState, state))
}

// checkRateLimit checks if the client has exceeded the rate limit.
func (c *Client) checkRateLimit() bool {
	now := time.Now()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Reset window if it has expired.
	if now.Sub(c.windowStart) > rateLimitWindow {
		c.windowStart = now
		c.messageCount = 0
	}

	c.messageCount++

	return c.messageCount <= rateLimitMax
}

// SendMessage sends a WebSocket message to the client.
func (c *Client) SendMessage(message *WSMessage) {
	data, err := message.Serialize()
	if err != nil {
		c.logger.Error("failed to serialize message", "error", err)
		return
	}
	c.SendBytes(data)
}

// SendBytes sends raw bytes to the client.
func (c *Client) SendBytes(data []byte) {
	if c.closed.Load() {
		return
	}

	select {
	case c.send <- data:
	default:
		c.logger.Warn("send buffer full, dropping message")
	}
}

// SendError sends an error message to the client.
func (c *Client) SendError(code, message string) {
	c.SendMessage(NewErrorMessage(code, message))
}

// Close closes the client connection.
func (c *Client) Close() {
	if c.closed.Swap(true) {
		return // Already closed.
	}

	close(c.closedChan)

	// Don't close send channel here - let it drain naturally.
	// The channel will be garbage collected when the client is removed.
}

// IsClosed returns whether the client connection is closed.
func (c *Client) IsClosed() bool {
	return c.closed.Load()
}

// GetConnectedDuration returns how long the client has been connected.
func (c *Client) GetConnectedDuration() time.Duration {
	return time.Since(c.connectedAt)
}

// SetUserInfo sets the user information for an authenticated client.
func (c *Client) SetUserInfo(userID, username, platform string, permissions []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.UserID = userID
	c.Username = username
	c.Platform = platform
	c.IsAuthenticated = true
	if permissions != nil {
		c.Permissions = permissions
	}
}

// HasPermission checks if the client has a specific permission.
func (c *Client) HasPermission(permission string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, p := range c.Permissions {
		if p == permission {
			return true
		}
	}
	return false
}

// SetMetadata sets a metadata value for the client.
func (c *Client) SetMetadata(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.metadata[key] = value
}

// GetMetadata gets a metadata value from the client.
func (c *Client) GetMetadata(key string) (any, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	val, ok := c.metadata[key]
	return val, ok
}

// GetSubscribedChannels returns the list of channels the client is subscribed to.
func (c *Client) GetSubscribedChannels() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	channels := make([]string, 0, len(c.Channels))
	for ch := range c.Channels {
		channels = append(channels, ch)
	}
	return channels
}

// IsSubscribedTo checks if the client is subscribed to a channel.
func (c *Client) IsSubscribedTo(channel string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Channels[channel]
}

// JoinRoom joins a room (stream).
func (c *Client) JoinRoom(roomID string) {
	c.mu.Lock()
	c.RoomID = roomID
	c.mu.Unlock()

	c.hub.roomManager.JoinRoom(c, roomID)
}

// LeaveRoom leaves the current room.
func (c *Client) LeaveRoom() {
	c.mu.Lock()
	roomID := c.RoomID
	c.RoomID = ""
	c.mu.Unlock()

	if roomID != "" {
		c.hub.roomManager.LeaveRoom(c, roomID)
	}
}

// GetRoomID returns the client's current room ID.
func (c *Client) GetRoomID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.RoomID
}

// ClientInfo contains information about a client for external use.
type ClientInfo struct {
	ID              string        `json:"id"`
	UserID          string        `json:"user_id,omitempty"`
	Username        string        `json:"username,omitempty"`
	RoomID          string        `json:"room_id,omitempty"`
	IsAuthenticated bool          `json:"is_authenticated"`
	Platform        string        `json:"platform,omitempty"`
	Channels        []string      `json:"channels"`
	ConnectedAt     time.Time     `json:"connected_at"`
	Duration        time.Duration `json:"duration"`
}

// GetInfo returns information about the client.
func (c *Client) GetInfo() *ClientInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	channels := make([]string, 0, len(c.Channels))
	for ch := range c.Channels {
		channels = append(channels, ch)
	}

	return &ClientInfo{
		ID:              c.ID,
		UserID:          c.UserID,
		Username:        c.Username,
		RoomID:          c.RoomID,
		IsAuthenticated: c.IsAuthenticated,
		Platform:        c.Platform,
		Channels:        channels,
		ConnectedAt:     c.connectedAt,
		Duration:        time.Since(c.connectedAt),
	}
}
