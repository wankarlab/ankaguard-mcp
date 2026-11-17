package websocket

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// Hub maintains the set of active clients and broadcasts messages to them.
type Hub struct {
	// clients maps client IDs to Client instances.
	clients map[string]*Client

	// channels maps channel names to sets of subscribed client IDs.
	channels map[string]map[string]*Client

	// broadcast is the channel for messages to broadcast to all clients.
	broadcast chan *WSMessage

	// register is the channel for client registration requests.
	register chan *Client

	// unregister is the channel for client unregistration requests.
	unregister chan *Client

	// roomManager handles room-based broadcasting.
	roomManager *RoomManager

	// marketPriceService handles market price subscriptions and updates.
	marketPriceService *MarketPriceService

	// mu protects concurrent access to clients and channels.
	mu sync.RWMutex

	// logger for hub operations.
	logger *slog.Logger

	// clientCount tracks the total number of connected clients.
	clientCount atomic.Int64

	// stats tracks hub statistics.
	stats *HubStats

	// done signals when the hub should stop.
	done chan struct{}
}

// HubStats contains statistics about the hub.
type HubStats struct {
	TotalConnections     int64     `json:"total_connections"`
	ActiveConnections    int64     `json:"active_connections"`
	TotalMessagesSent    int64     `json:"total_messages_sent"`
	TotalMessagesRecv    int64     `json:"total_messages_received"`
	StartTime            time.Time `json:"start_time"`
	LastBroadcastTime    time.Time `json:"last_broadcast_time"`
	mu                   sync.RWMutex
}

// NewHub creates a new Hub instance.
func NewHub(logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}

	h := &Hub{
		clients:    make(map[string]*Client),
		channels:   make(map[string]map[string]*Client),
		broadcast:  make(chan *WSMessage, 256),
		register:   make(chan *Client, 64),
		unregister: make(chan *Client, 64),
		logger:     logger.With("component", "websocket_hub"),
		stats: &HubStats{
			StartTime: time.Now(),
		},
		done: make(chan struct{}),
	}

	// Initialize the room manager with a reference to this hub.
	h.roomManager = NewRoomManager(h, logger)

	// Initialize the market price service.
	h.marketPriceService = NewMarketPriceService(h, logger)

	// Initialize channels.
	for channel := range ValidChannels {
		h.channels[channel] = make(map[string]*Client)
	}

	return h
}

// Run starts the hub's main event loop.
// This should be called in a goroutine.
func (h *Hub) Run(ctx context.Context) {
	h.logger.Info("WebSocket hub started")

	// Start the room manager.
	go h.roomManager.Run(ctx)

	// Start the market price service.
	go h.marketPriceService.Run(ctx)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			h.shutdown()
			return

		case <-h.done:
			h.shutdown()
			return

		case client := <-h.register:
			h.registerClient(client)

		case client := <-h.unregister:
			h.unregisterClient(client)

		case message := <-h.broadcast:
			h.broadcastMessage(message)

		case <-ticker.C:
			h.sendHeartbeats()
		}
	}
}

// Stop signals the hub to stop processing.
func (h *Hub) Stop() {
	close(h.done)
}

// registerClient adds a new client to the hub.
func (h *Hub) registerClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.clients[client.ID] = client
	h.clientCount.Add(1)
	h.stats.mu.Lock()
	h.stats.TotalConnections++
	h.stats.ActiveConnections = h.clientCount.Load()
	h.stats.mu.Unlock()

	h.logger.Info("client registered",
		"client_id", client.ID,
		"user_id", client.UserID,
		"total_clients", h.clientCount.Load(),
	)

	// Send connected message to client.
	connectedMsg := NewConnectedMessage(client.ID)
	client.SendMessage(connectedMsg)
}

// unregisterClient removes a client from the hub.
func (h *Hub) unregisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.clients[client.ID]; !ok {
		return // Client not registered.
	}

	// Remove from all channels.
	for _, channelClients := range h.channels {
		delete(channelClients, client.ID)
	}

	// Remove from room if in one.
	if client.RoomID != "" {
		h.roomManager.LeaveRoom(client, client.RoomID)
	}

	// Remove from market price subscriptions.
	h.marketPriceService.UnsubscribeClient(client.ID)

	delete(h.clients, client.ID)
	h.clientCount.Add(-1)

	h.stats.mu.Lock()
	h.stats.ActiveConnections = h.clientCount.Load()
	h.stats.mu.Unlock()

	// Close the client's send channel.
	client.Close()

	h.logger.Info("client unregistered",
		"client_id", client.ID,
		"user_id", client.UserID,
		"total_clients", h.clientCount.Load(),
	)
}

// broadcastMessage sends a message to all connected clients.
func (h *Hub) broadcastMessage(message *WSMessage) {
	data, err := message.Serialize()
	if err != nil {
		h.logger.Error("failed to serialize broadcast message", "error", err)
		return
	}

	h.mu.RLock()
	clientsCopy := make([]*Client, 0, len(h.clients))
	for _, client := range h.clients {
		clientsCopy = append(clientsCopy, client)
	}
	h.mu.RUnlock()

	for _, client := range clientsCopy {
		client.SendBytes(data)
	}

	h.stats.mu.Lock()
	h.stats.TotalMessagesSent += int64(len(clientsCopy))
	h.stats.LastBroadcastTime = time.Now()
	h.stats.mu.Unlock()
}

// sendHeartbeats sends heartbeat messages to all clients.
func (h *Hub) sendHeartbeats() {
	heartbeat := NewWSMessage(MessageTypeSystemHeartbeat, nil)
	data, err := heartbeat.Serialize()
	if err != nil {
		return
	}

	h.mu.RLock()
	for _, client := range h.clients {
		client.SendBytes(data)
	}
	h.mu.RUnlock()
}

// shutdown gracefully shuts down the hub.
func (h *Hub) shutdown() {
	h.logger.Info("shutting down WebSocket hub")

	h.mu.Lock()
	defer h.mu.Unlock()

	// Close all client connections.
	for _, client := range h.clients {
		client.Close()
	}

	h.clients = make(map[string]*Client)
	for channel := range h.channels {
		h.channels[channel] = make(map[string]*Client)
	}

	h.logger.Info("WebSocket hub shut down complete")
}

// Register adds a client to the hub.
func (h *Hub) Register(client *Client) {
	select {
	case h.register <- client:
	default:
		h.logger.Warn("register channel full, dropping client", "client_id", client.ID)
	}
}

// Unregister removes a client from the hub.
func (h *Hub) Unregister(client *Client) {
	select {
	case h.unregister <- client:
	default:
		h.logger.Warn("unregister channel full", "client_id", client.ID)
	}
}

// BroadcastToAll sends a message to all connected clients.
func (h *Hub) BroadcastToAll(message *WSMessage) {
	select {
	case h.broadcast <- message:
	default:
		h.logger.Warn("broadcast channel full, dropping message", "type", message.Type)
	}
}

// BroadcastNotification forwards a notification message to all connected clients.
// Satisfies notifications.notificationBroadcaster so Hub replaces the legacy websocket.Manager.
func (h *Hub) BroadcastNotification(msgType string, data interface{}) error {
	h.BroadcastToAll(NewWSMessage(msgType, data))
	return nil
}

// SendNotificationToUser sends a notification message to all connections belonging to userID.
func (h *Hub) SendNotificationToUser(userID, msgType string, data interface{}) error {
	h.SendToUser(userID, NewWSMessage(msgType, data))
	return nil
}

// BroadcastToChannel sends a message to all clients subscribed to a channel.
func (h *Hub) BroadcastToChannel(channel string, message *WSMessage) {
	data, err := message.Serialize()
	if err != nil {
		h.logger.Error("failed to serialize channel message", "error", err, "channel", channel)
		return
	}

	h.mu.RLock()
	channelClients, ok := h.channels[channel]
	if !ok {
		h.mu.RUnlock()
		return
	}

	clientsCopy := make([]*Client, 0, len(channelClients))
	for _, client := range channelClients {
		clientsCopy = append(clientsCopy, client)
	}
	h.mu.RUnlock()

	for _, client := range clientsCopy {
		client.SendBytes(data)
	}

	h.stats.mu.Lock()
	h.stats.TotalMessagesSent += int64(len(clientsCopy))
	h.stats.mu.Unlock()
}

// BroadcastToRoom sends a message to all clients in a specific room.
func (h *Hub) BroadcastToRoom(roomID string, message *WSMessage) {
	h.roomManager.BroadcastToRoom(roomID, message)
}

// SubscribeToChannel subscribes a client to a channel.
func (h *Hub) SubscribeToChannel(client *Client, channel string) bool {
	if !IsValidChannel(channel) {
		return false
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if h.channels[channel] == nil {
		h.channels[channel] = make(map[string]*Client)
	}

	h.channels[channel][client.ID] = client
	client.mu.Lock()
	client.Channels[channel] = true
	client.mu.Unlock()

	h.logger.Debug("client subscribed to channel",
		"client_id", client.ID,
		"channel", channel,
	)

	return true
}

// UnsubscribeFromChannel unsubscribes a client from a channel.
func (h *Hub) UnsubscribeFromChannel(client *Client, channel string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()

	if channelClients, ok := h.channels[channel]; ok {
		delete(channelClients, client.ID)
	}

	client.mu.Lock()
	delete(client.Channels, channel)
	client.mu.Unlock()

	h.logger.Debug("client unsubscribed from channel",
		"client_id", client.ID,
		"channel", channel,
	)

	return true
}

// SubscribeToChannels subscribes a client to multiple channels.
func (h *Hub) SubscribeToChannels(client *Client, channels []string) []string {
	subscribed := make([]string, 0, len(channels))
	for _, channel := range channels {
		if h.SubscribeToChannel(client, channel) {
			subscribed = append(subscribed, channel)
		}
	}
	return subscribed
}

// UnsubscribeFromChannels unsubscribes a client from multiple channels.
func (h *Hub) UnsubscribeFromChannels(client *Client, channels []string) []string {
	unsubscribed := make([]string, 0, len(channels))
	for _, channel := range channels {
		if h.UnsubscribeFromChannel(client, channel) {
			unsubscribed = append(unsubscribed, channel)
		}
	}
	return unsubscribed
}

// SubscribeToGroup subscribes a client to a channel group.
func (h *Hub) SubscribeToGroup(client *Client, group string) []string {
	channels := GetChannelGroup(group)
	if channels == nil {
		return nil
	}
	return h.SubscribeToChannels(client, channels)
}

// GetClientCount returns the number of connected clients.
func (h *Hub) GetClientCount() int {
	return int(h.clientCount.Load())
}

// GetChannelClientCount returns the number of clients subscribed to a channel.
func (h *Hub) GetChannelClientCount(channel string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if channelClients, ok := h.channels[channel]; ok {
		return len(channelClients)
	}
	return 0
}

// GetClient returns a client by ID.
func (h *Hub) GetClient(clientID string) *Client {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.clients[clientID]
}

// GetClientsByUserID returns all clients for a user ID.
func (h *Hub) GetClientsByUserID(userID string) []*Client {
	h.mu.RLock()
	defer h.mu.RUnlock()

	clients := make([]*Client, 0)
	for _, client := range h.clients {
		if client.UserID == userID {
			clients = append(clients, client)
		}
	}
	return clients
}

// SendToClient sends a message to a specific client.
func (h *Hub) SendToClient(clientID string, message *WSMessage) bool {
	h.mu.RLock()
	client, ok := h.clients[clientID]
	h.mu.RUnlock()

	if !ok {
		return false
	}

	client.SendMessage(message)
	return true
}

// SendToUser sends a message to all clients of a specific user.
func (h *Hub) SendToUser(userID string, message *WSMessage) int {
	clients := h.GetClientsByUserID(userID)
	for _, client := range clients {
		client.SendMessage(message)
	}
	return len(clients)
}

// GetStats returns the hub statistics.
func (h *Hub) GetStats() *HubStats {
	h.stats.mu.RLock()
	defer h.stats.mu.RUnlock()

	return &HubStats{
		TotalConnections:     h.stats.TotalConnections,
		ActiveConnections:    h.stats.ActiveConnections,
		TotalMessagesSent:    h.stats.TotalMessagesSent,
		TotalMessagesRecv:    h.stats.TotalMessagesRecv,
		StartTime:            h.stats.StartTime,
		LastBroadcastTime:    h.stats.LastBroadcastTime,
	}
}

// IncrementMessagesReceived increments the received messages counter.
func (h *Hub) IncrementMessagesReceived() {
	h.stats.mu.Lock()
	h.stats.TotalMessagesRecv++
	h.stats.mu.Unlock()
}

// RoomManager returns the hub's room manager.
func (h *Hub) RoomManager() *RoomManager {
	return h.roomManager
}

// MarketPriceService returns the hub's market price service.
func (h *Hub) MarketPriceService() *MarketPriceService {
	return h.marketPriceService
}

// IsClientInRoom checks if a client is in a specific room.
func (h *Hub) IsClientInRoom(clientID, roomID string) bool {
	return h.roomManager.IsClientInRoom(clientID, roomID)
}

// GetRoomClientCount returns the number of clients in a room.
func (h *Hub) GetRoomClientCount(roomID string) int {
	return h.roomManager.GetRoomClientCount(roomID)
}
