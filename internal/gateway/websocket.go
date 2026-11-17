package gateway

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	metricsMiddleware "github.com/rantipay/trading-mcp/internal/middleware"
	tradingmw "github.com/rantipay/trading-mcp/internal/trading/middleware"
	"github.com/sirupsen/logrus"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin: func(r *http.Request) bool {
		return true // Allow all origins for development
	},
}

// WSMessage representa un mensaje de WebSocket
type WSMessage struct {
	Type      string      `json:"type"`
	Channel   string      `json:"channel,omitempty"`
	Data      interface{} `json:"data,omitempty"`
	Timestamp string      `json:"timestamp"`
	Error     string      `json:"error,omitempty"`
}

// WSClient representa un cliente WebSocket conectado
type WSClient struct {
	ID            string
	Conn          *websocket.Conn
	Send          chan []byte
	Subscriptions map[string]bool
	mu            sync.RWMutex
}

// WSHub maneja todos los clientes WebSocket
type WSHub struct {
	clients    map[*WSClient]bool
	broadcast  chan []byte
	register   chan *WSClient
	unregister chan *WSClient
	mu         sync.RWMutex
	logger     *logrus.Logger
}

// NewWSHub crea un nuevo hub de WebSocket
func NewWSHub(logger *logrus.Logger) *WSHub {
	return &WSHub{
		clients:    make(map[*WSClient]bool),
		broadcast:  make(chan []byte, 256),
		register:   make(chan *WSClient),
		unregister: make(chan *WSClient),
		logger:     logger,
	}
}

// Run inicia el hub de WebSocket
func (h *WSHub) Run() {
	for {
		select {
		case client := <-h.register:
			h.mu.Lock()
			h.clients[client] = true
			clientCount := len(h.clients)
			h.mu.Unlock()
			h.logger.WithField("client_id", client.ID).Info("WebSocket client connected")

			// Record WebSocket connection metrics
			if mm := metricsMiddleware.GetMetricsMiddleware(); mm != nil {
				mm.RecordWebSocketConnection("opened")
				mm.SetWebSocketActiveConnections(float64(clientCount))
			}

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.Send)
				h.logger.WithField("client_id", client.ID).Info("WebSocket client disconnected")
			}
			clientCount := len(h.clients)
			h.mu.Unlock()

			// Record WebSocket disconnection metrics
			if mm := metricsMiddleware.GetMetricsMiddleware(); mm != nil {
				mm.RecordWebSocketConnection("closed")
				mm.SetWebSocketActiveConnections(float64(clientCount))
			}

		case message := <-h.broadcast:
			h.mu.RLock()
			for client := range h.clients {
				select {
				case client.Send <- message:
				default:
					close(client.Send)
					delete(h.clients, client)
				}
			}
			h.mu.RUnlock()
		}
	}
}

// BroadcastToChannel envía un mensaje a todos los clientes suscritos a un canal
func (h *WSHub) BroadcastToChannel(channel string, messageType string, data interface{}) {
	msg := WSMessage{
		Type:      messageType,
		Channel:   channel,
		Data:      data,
		Timestamp: time.Now().Format(time.RFC3339),
	}

	jsonData, err := json.Marshal(msg)
	if err != nil {
		h.logger.WithError(err).Error("Failed to marshal WebSocket message")
		return
	}

	h.mu.RLock()
	defer h.mu.RUnlock()

	for client := range h.clients {
		client.mu.RLock()
		subscribed := client.Subscriptions[channel]
		client.mu.RUnlock()

		if subscribed {
			select {
			case client.Send <- jsonData:
			default:
				// Client buffer full, skip
			}
		}
	}
}

// GetClientCount retorna el número de clientes conectados
func (h *WSHub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// handleWebSocket proxies WebSocket connections to MCP Server
func (g *Gateway) handleWebSocket(c *gin.Context) {
	// Validate the JWT before upgrading when authentication is enabled. The
	// token may come in the "token" query parameter (browsers cannot set
	// headers on a WebSocket) or in the Authorization header.
	var wsToken string
	if g.authEnabled() {
		wsToken = c.Query("token")
		if wsToken == "" {
			parts := strings.SplitN(c.GetHeader("Authorization"), " ", 2)
			if len(parts) == 2 && strings.ToLower(parts[0]) == "bearer" {
				wsToken = parts[1]
			}
		}
		if wsToken == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "code": "UNAUTHORIZED"})
			return
		}
		if _, err := tradingmw.ValidateToken(wsToken, g.jwtSecret); err != nil {
			g.logger.WithError(err).Warn("WebSocket connection rejected: JWT validation failed")
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized", "code": "UNAUTHORIZED"})
			return
		}
	}

	// Create WebSocket connection to MCP Server
	mcpWSURL := strings.Replace(g.config.MCPServerURL, "http://", "ws://", 1)
	mcpWSURL = strings.Replace(mcpWSURL, "https://", "wss://", 1)
	mcpWSURL += "/ws"

	// Forward query parameters
	if c.Request.URL.RawQuery != "" {
		mcpWSURL += "?" + c.Request.URL.RawQuery
	}

	g.logger.WithFields(logrus.Fields{
		"client_ip":  c.ClientIP(),
		"mcp_ws_url": mcpWSURL,
		"user_agent": c.Request.UserAgent(),
	}).Info("Proxying WebSocket connection to MCP Server")

	// Upgrade client connection
	clientConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		g.logger.WithError(err).Error("Failed to upgrade client WebSocket connection")
		if mm := metricsMiddleware.GetMetricsMiddleware(); mm != nil {
			mm.RecordWebSocketConnection("failed")
		}
		return
	}
	defer clientConn.Close()

	// Connect to MCP Server WebSocket
	// The backend validates the token again, so forward it.
	var dialHeader http.Header
	if wsToken != "" {
		dialHeader = http.Header{"Authorization": []string{"Bearer " + wsToken}}
	}
	mcpConn, _, err := websocket.DefaultDialer.Dial(mcpWSURL, dialHeader)
	if err != nil {
		g.logger.WithError(err).Error("Failed to connect to MCP Server WebSocket")
		clientConn.WriteMessage(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.CloseInternalServerErr, "Backend unavailable"))
		return
	}
	defer mcpConn.Close()

	g.logger.Info("WebSocket proxy connection established")

	// Record successful connection
	if mm := metricsMiddleware.GetMetricsMiddleware(); mm != nil {
		mm.RecordWebSocketConnection("opened")
		clientCount := g.wsHub.GetClientCount() + 1
		mm.SetWebSocketActiveConnections(float64(clientCount))
	}

	// Proxy messages bidirectionally
	errChan := make(chan error, 2)

	// Client -> MCP Server
	go func() {
		for {
			messageType, message, err := clientConn.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}
			if err := mcpConn.WriteMessage(messageType, message); err != nil {
				errChan <- err
				return
			}
		}
	}()

	// MCP Server -> Client
	go func() {
		for {
			messageType, message, err := mcpConn.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}
			if err := clientConn.WriteMessage(messageType, message); err != nil {
				errChan <- err
				return
			}
		}
	}()

	// Wait for error or disconnection
	err = <-errChan
	if err != nil && !websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
		g.logger.WithError(err).Warn("WebSocket proxy error")
	}

	// Record disconnection
	if mm := metricsMiddleware.GetMetricsMiddleware(); mm != nil {
		mm.RecordWebSocketConnection("closed")
		clientCount := g.wsHub.GetClientCount()
		mm.SetWebSocketActiveConnections(float64(clientCount))
	}

	g.logger.Info("WebSocket proxy connection closed")
}

// readPump lee mensajes del cliente WebSocket
func (c *WSClient) readPump(hub *WSHub, logger *logrus.Logger) {
	defer func() {
		hub.unregister <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		_, message, err := c.Conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logger.WithError(err).Warn("WebSocket unexpected close")
			}
			break
		}

		// Parse message
		var msg WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			logger.WithError(err).Warn("Failed to parse WebSocket message")
			continue
		}

		// Handle message
		c.handleMessage(&msg, hub, logger)
	}
}

// writePump escribe mensajes al cliente WebSocket
func (c *WSClient) writePump(logger *logrus.Logger) {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.Send:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			w, err := c.Conn.NextWriter(websocket.TextMessage)
			if err != nil {
				return
			}
			w.Write(message)

			// Add queued messages to current websocket message
			n := len(c.Send)
			for i := 0; i < n; i++ {
				w.Write([]byte{'\n'})
				w.Write(<-c.Send)
			}

			if err := w.Close(); err != nil {
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage maneja los mensajes recibidos del cliente
func (c *WSClient) handleMessage(msg *WSMessage, hub *WSHub, logger *logrus.Logger) {
	switch msg.Type {
	case "subscribe":
		if msg.Channel != "" {
			c.mu.Lock()
			c.Subscriptions[msg.Channel] = true
			c.mu.Unlock()

			logger.WithFields(logrus.Fields{
				"client_id": c.ID,
				"channel":   msg.Channel,
			}).Info("Client subscribed to channel")

			// Send confirmation
			response := WSMessage{
				Type:      "subscribed",
				Channel:   msg.Channel,
				Timestamp: time.Now().Format(time.RFC3339),
			}
			c.sendMessage(response)
		}

	case "unsubscribe":
		if msg.Channel != "" {
			c.mu.Lock()
			delete(c.Subscriptions, msg.Channel)
			c.mu.Unlock()

			logger.WithFields(logrus.Fields{
				"client_id": c.ID,
				"channel":   msg.Channel,
			}).Info("Client unsubscribed from channel")

			// Send confirmation
			response := WSMessage{
				Type:      "unsubscribed",
				Channel:   msg.Channel,
				Timestamp: time.Now().Format(time.RFC3339),
			}
			c.sendMessage(response)
		}

	case "ping":
		// Respond with pong
		response := WSMessage{
			Type:      "pong",
			Timestamp: time.Now().Format(time.RFC3339),
		}
		c.sendMessage(response)

	default:
		logger.WithField("type", msg.Type).Warn("Unknown WebSocket message type")
	}
}

// sendMessage envía un mensaje al cliente
func (c *WSClient) sendMessage(msg WSMessage) {
	data, err := json.Marshal(msg)
	if err != nil {
		return
	}

	select {
	case c.Send <- data:
	default:
		// Buffer full, skip
	}
}

// generateClientID genera un ID único para el cliente
func generateClientID() string {
	return fmt.Sprintf("client_%d", time.Now().UnixNano())
}
