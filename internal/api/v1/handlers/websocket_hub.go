// Package handlers contains HTTP handlers for REST API v1
package handlers

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/sirupsen/logrus"
)

// ChannelType represents different subscription channels
type ChannelType string

const (
	// ChannelTicker for ticker updates (e.g., "ticker:BTC-USDT")
	ChannelTicker ChannelType = "ticker"
	// ChannelTrades for trade updates
	ChannelTrades ChannelType = "trades"
	// ChannelOrderbook for orderbook updates
	ChannelOrderbook ChannelType = "orderbook"
	// ChannelBot for bot status updates
	ChannelBot ChannelType = "bot"
	// ChannelPositions for position updates
	ChannelPositions ChannelType = "positions"
	// ChannelOrders for order updates
	ChannelOrders ChannelType = "orders"
	// ChannelBalances for balance updates
	ChannelBalances ChannelType = "balances"
)

// WSMessage represents a WebSocket message
type WSMessage struct {
	Type      string          `json:"type"`
	Channel   string          `json:"channel,omitempty"`
	Data      json.RawMessage `json:"data,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	RequestID string          `json:"request_id,omitempty"`
}

// WSSubscribeRequest represents a subscription request
type WSSubscribeRequest struct {
	Action   string   `json:"action"` // "subscribe" or "unsubscribe"
	Channels []string `json:"channels"`
}

// WSAuthRequest represents an authentication request
type WSAuthRequest struct {
	Action string `json:"action"` // "auth"
	Token  string `json:"token"`
}

// WSErrorMessage represents an error response
type WSErrorMessage struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// Client represents a connected WebSocket client
type Client struct {
	ID            string
	UserID        string
	TenantID      string
	CompanyID     string
	KongLevel     int
	Conn          *websocket.Conn
	Hub           *Hub
	Send          chan []byte
	Subscriptions map[string]bool
	Authenticated bool
	mu            sync.RWMutex
	lastPing      time.Time
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewClient creates a new WebSocket client
func NewClient(hub *Hub, conn *websocket.Conn) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{
		ID:            uuid.New().String(),
		Conn:          conn,
		Hub:           hub,
		Send:          make(chan []byte, 256),
		Subscriptions: make(map[string]bool),
		Authenticated: false,
		lastPing:      time.Now(),
		ctx:           ctx,
		cancel:        cancel,
	}
}

// Subscribe adds a channel subscription
func (c *Client) Subscribe(channel string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Subscriptions[channel] = true
}

// Unsubscribe removes a channel subscription
func (c *Client) Unsubscribe(channel string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.Subscriptions, channel)
}

// IsSubscribed checks if the client is subscribed to a channel
func (c *Client) IsSubscribed(channel string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.Subscriptions[channel]
}

// GetSubscriptions returns all subscribed channels
func (c *Client) GetSubscriptions() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	channels := make([]string, 0, len(c.Subscriptions))
	for ch := range c.Subscriptions {
		channels = append(channels, ch)
	}
	return channels
}

// SetAuthenticated marks the client as authenticated with user details
func (c *Client) SetAuthenticated(userID, tenantID, companyID string, kongLevel int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Authenticated = true
	c.UserID = userID
	c.TenantID = tenantID
	c.CompanyID = companyID
	c.KongLevel = kongLevel
}

// Close cleans up client resources
func (c *Client) Close() {
	c.cancel()
	c.Conn.Close()
}

// Hub manages WebSocket connections and message broadcasting
type Hub struct {
	// Registered clients by user ID for targeted messaging
	clients    map[string]map[*Client]bool
	allClients map[*Client]bool

	// Channel subscribers: channel -> clients
	channels map[string]map[*Client]bool

	// Register/unregister channels
	register   chan *Client
	unregister chan *Client

	// Broadcast to specific channels
	broadcast chan *BroadcastMessage

	// Logger
	logger *logrus.Logger

	// Mutex for thread-safe operations
	mu sync.RWMutex

	// Context for shutdown
	ctx    context.Context
	cancel context.CancelFunc
}

// BroadcastMessage represents a message to broadcast
type BroadcastMessage struct {
	Channel string
	UserID  string // Optional: if set, only send to this user
	Data    []byte
}

// NewHub creates a new WebSocket hub
func NewHub(logger *logrus.Logger) *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		clients:    make(map[string]map[*Client]bool),
		allClients: make(map[*Client]bool),
		channels:   make(map[string]map[*Client]bool),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan *BroadcastMessage, 256),
		logger:     logger,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Run starts the hub's main loop
func (h *Hub) Run() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			h.logger.Info("WebSocket hub shutting down")
			h.closeAllClients()
			return

		case client := <-h.register:
			h.registerClient(client)

		case client := <-h.unregister:
			h.unregisterClient(client)

		case message := <-h.broadcast:
			h.broadcastMessage(message)

		case <-ticker.C:
			h.cleanupStaleConnections()
		}
	}
}

// Stop shuts down the hub
func (h *Hub) Stop() {
	h.cancel()
}

// Register adds a client to the hub
func (h *Hub) Register(client *Client) {
	h.register <- client
}

// Unregister removes a client from the hub
func (h *Hub) Unregister(client *Client) {
	h.unregister <- client
}

// Broadcast sends a message to all subscribers of a channel
func (h *Hub) Broadcast(channel string, data []byte) {
	h.broadcast <- &BroadcastMessage{
		Channel: channel,
		Data:    data,
	}
}

// BroadcastToUser sends a message to a specific user's connections
func (h *Hub) BroadcastToUser(userID, channel string, data []byte) {
	h.broadcast <- &BroadcastMessage{
		Channel: channel,
		UserID:  userID,
		Data:    data,
	}
}

// SubscribeClient subscribes a client to a channel
func (h *Hub) SubscribeClient(client *Client, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.channels[channel] == nil {
		h.channels[channel] = make(map[*Client]bool)
	}
	h.channels[channel][client] = true
	client.Subscribe(channel)

	h.logger.WithFields(logrus.Fields{
		"client_id": client.ID,
		"user_id":   client.UserID,
		"channel":   channel,
	}).Debug("Client subscribed to channel")
}

// UnsubscribeClient unsubscribes a client from a channel
func (h *Hub) UnsubscribeClient(client *Client, channel string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.channels[channel] != nil {
		delete(h.channels[channel], client)
		if len(h.channels[channel]) == 0 {
			delete(h.channels, channel)
		}
	}
	client.Unsubscribe(channel)

	h.logger.WithFields(logrus.Fields{
		"client_id": client.ID,
		"user_id":   client.UserID,
		"channel":   channel,
	}).Debug("Client unsubscribed from channel")
}

// GetClientCount returns the total number of connected clients
func (h *Hub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.allClients)
}

// GetChannelSubscriberCount returns the number of subscribers for a channel
func (h *Hub) GetChannelSubscriberCount(channel string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.channels[channel])
}

// GetUserClientCount returns the number of connections for a user
func (h *Hub) GetUserClientCount(userID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients[userID])
}

// registerClient handles client registration
func (h *Hub) registerClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.allClients[client] = true

	// Add to user-specific map if authenticated
	if client.Authenticated && client.UserID != "" {
		if h.clients[client.UserID] == nil {
			h.clients[client.UserID] = make(map[*Client]bool)
		}
		h.clients[client.UserID][client] = true
	}

	h.logger.WithFields(logrus.Fields{
		"client_id":     client.ID,
		"user_id":       client.UserID,
		"authenticated": client.Authenticated,
		"total_clients": len(h.allClients),
	}).Info("Client registered")
}

// unregisterClient handles client unregistration
func (h *Hub) unregisterClient(client *Client) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if _, ok := h.allClients[client]; ok {
		delete(h.allClients, client)

		// Remove from user-specific map
		if client.UserID != "" {
			if userClients, ok := h.clients[client.UserID]; ok {
				delete(userClients, client)
				if len(userClients) == 0 {
					delete(h.clients, client.UserID)
				}
			}
		}

		// Remove from all channel subscriptions
		for channel := range client.Subscriptions {
			if channelClients, ok := h.channels[channel]; ok {
				delete(channelClients, client)
				if len(channelClients) == 0 {
					delete(h.channels, channel)
				}
			}
		}

		// Close the send channel
		close(client.Send)

		h.logger.WithFields(logrus.Fields{
			"client_id":     client.ID,
			"user_id":       client.UserID,
			"total_clients": len(h.allClients),
		}).Info("Client unregistered")
	}
}

// broadcastMessage sends a message to appropriate clients
func (h *Hub) broadcastMessage(msg *BroadcastMessage) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	// If user-specific, only send to that user's clients
	if msg.UserID != "" {
		if userClients, ok := h.clients[msg.UserID]; ok {
			for client := range userClients {
				if client.IsSubscribed(msg.Channel) {
					h.sendToClient(client, msg.Data)
				}
			}
		}
		return
	}

	// Broadcast to all subscribers of the channel
	if channelClients, ok := h.channels[msg.Channel]; ok {
		for client := range channelClients {
			h.sendToClient(client, msg.Data)
		}
	}
}

// sendToClient safely sends data to a client
func (h *Hub) sendToClient(client *Client, data []byte) {
	select {
	case client.Send <- data:
	default:
		// Client's buffer is full, skip this message
		h.logger.WithField("client_id", client.ID).Warn("Client send buffer full, dropping message")
	}
}

// closeAllClients closes all connected clients
func (h *Hub) closeAllClients() {
	h.mu.Lock()
	defer h.mu.Unlock()

	for client := range h.allClients {
		close(client.Send)
		client.Close()
	}
	h.allClients = make(map[*Client]bool)
	h.clients = make(map[string]map[*Client]bool)
	h.channels = make(map[string]map[*Client]bool)
}

// cleanupStaleConnections removes clients that haven't sent a ping recently
func (h *Hub) cleanupStaleConnections() {
	h.mu.Lock()
	staleClients := make([]*Client, 0)
	threshold := time.Now().Add(-90 * time.Second)

	for client := range h.allClients {
		if client.lastPing.Before(threshold) {
			staleClients = append(staleClients, client)
		}
	}
	h.mu.Unlock()

	// Unregister stale clients outside the lock
	for _, client := range staleClients {
		h.logger.WithFields(logrus.Fields{
			"client_id": client.ID,
			"user_id":   client.UserID,
			"last_ping": client.lastPing,
		}).Warn("Removing stale WebSocket connection")
		h.Unregister(client)
		client.Close()
	}
}
