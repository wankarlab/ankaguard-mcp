package websocket

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Room represents a WebSocket room for broadcasting to a group of clients.
// Rooms are typically associated with a stream or trading session.
type Room struct {
	// ID is the unique identifier for the room.
	ID string

	// Name is the display name of the room.
	Name string

	// StreamID is the associated stream ID (if applicable).
	StreamID string

	// clients maps client IDs to Client instances in this room.
	clients map[string]*Client

	// metadata stores additional room information.
	metadata map[string]any

	// createdAt is when the room was created.
	createdAt time.Time

	// lastActivityAt is when the room had its last activity.
	lastActivityAt time.Time

	// mu protects concurrent access to room fields.
	mu sync.RWMutex
}

// NewRoom creates a new Room instance.
func NewRoom(id, name, streamID string) *Room {
	now := time.Now()
	return &Room{
		ID:             id,
		Name:           name,
		StreamID:       streamID,
		clients:        make(map[string]*Client),
		metadata:       make(map[string]any),
		createdAt:      now,
		lastActivityAt: now,
	}
}

// AddClient adds a client to the room.
func (r *Room) AddClient(client *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.clients[client.ID] = client
	r.lastActivityAt = time.Now()
}

// RemoveClient removes a client from the room.
func (r *Room) RemoveClient(clientID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.clients, clientID)
	r.lastActivityAt = time.Now()
}

// HasClient checks if a client is in the room.
func (r *Room) HasClient(clientID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.clients[clientID]
	return ok
}

// GetClientCount returns the number of clients in the room.
func (r *Room) GetClientCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.clients)
}

// GetClients returns a copy of all clients in the room.
func (r *Room) GetClients() []*Client {
	r.mu.RLock()
	defer r.mu.RUnlock()

	clients := make([]*Client, 0, len(r.clients))
	for _, client := range r.clients {
		clients = append(clients, client)
	}
	return clients
}

// GetClientIDs returns the IDs of all clients in the room.
func (r *Room) GetClientIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	ids := make([]string, 0, len(r.clients))
	for id := range r.clients {
		ids = append(ids, id)
	}
	return ids
}

// GetAuthenticatedCount returns the number of authenticated clients.
func (r *Room) GetAuthenticatedCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, client := range r.clients {
		if client.IsAuthenticated {
			count++
		}
	}
	return count
}

// GetAnonymousCount returns the number of anonymous clients.
func (r *Room) GetAnonymousCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := 0
	for _, client := range r.clients {
		if !client.IsAuthenticated {
			count++
		}
	}
	return count
}

// Broadcast sends a message to all clients in the room.
func (r *Room) Broadcast(message *WSMessage) {
	data, err := message.Serialize()
	if err != nil {
		return
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, client := range r.clients {
		client.SendBytes(data)
	}
	r.lastActivityAt = time.Now()
}

// BroadcastExcept sends a message to all clients except the specified one.
func (r *Room) BroadcastExcept(message *WSMessage, excludeClientID string) {
	data, err := message.Serialize()
	if err != nil {
		return
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	for id, client := range r.clients {
		if id != excludeClientID {
			client.SendBytes(data)
		}
	}
	r.lastActivityAt = time.Now()
}

// SetMetadata sets a metadata value for the room.
func (r *Room) SetMetadata(key string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.metadata[key] = value
}

// GetMetadata gets a metadata value from the room.
func (r *Room) GetMetadata(key string) (any, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	val, ok := r.metadata[key]
	return val, ok
}

// IsEmpty returns whether the room has no clients.
func (r *Room) IsEmpty() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return len(r.clients) == 0
}

// GetCreatedAt returns when the room was created.
func (r *Room) GetCreatedAt() time.Time {
	return r.createdAt
}

// GetLastActivityAt returns when the room had its last activity.
func (r *Room) GetLastActivityAt() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return r.lastActivityAt
}

// RoomInfo contains information about a room for external use.
type RoomInfo struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	StreamID           string    `json:"stream_id,omitempty"`
	ClientCount        int       `json:"client_count"`
	AuthenticatedCount int       `json:"authenticated_count"`
	AnonymousCount     int       `json:"anonymous_count"`
	CreatedAt          time.Time `json:"created_at"`
	LastActivityAt     time.Time `json:"last_activity_at"`
}

// GetInfo returns information about the room.
func (r *Room) GetInfo() *RoomInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	authCount := 0
	anonCount := 0
	for _, client := range r.clients {
		if client.IsAuthenticated {
			authCount++
		} else {
			anonCount++
		}
	}

	return &RoomInfo{
		ID:                 r.ID,
		Name:               r.Name,
		StreamID:           r.StreamID,
		ClientCount:        len(r.clients),
		AuthenticatedCount: authCount,
		AnonymousCount:     anonCount,
		CreatedAt:          r.createdAt,
		LastActivityAt:     r.lastActivityAt,
	}
}

// RoomManager manages WebSocket rooms.
type RoomManager struct {
	// rooms maps room IDs to Room instances.
	rooms map[string]*Room

	// hub is a reference to the WebSocket hub.
	hub *Hub

	// mu protects concurrent access to rooms.
	mu sync.RWMutex

	// logger for room manager operations.
	logger *slog.Logger

	// cleanupInterval is how often to clean up empty rooms.
	cleanupInterval time.Duration

	// roomIdleTimeout is how long a room can be empty before cleanup.
	roomIdleTimeout time.Duration
}

// NewRoomManager creates a new RoomManager instance.
func NewRoomManager(hub *Hub, logger *slog.Logger) *RoomManager {
	if logger == nil {
		logger = slog.Default()
	}

	return &RoomManager{
		rooms:           make(map[string]*Room),
		hub:             hub,
		logger:          logger.With("component", "room_manager"),
		cleanupInterval: 5 * time.Minute,
		roomIdleTimeout: 30 * time.Minute,
	}
}

// Run starts the room manager's cleanup routine.
func (rm *RoomManager) Run(ctx context.Context) {
	ticker := time.NewTicker(rm.cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rm.cleanupEmptyRooms()
		}
	}
}

// cleanupEmptyRooms removes rooms that have been empty for too long.
func (rm *RoomManager) cleanupEmptyRooms() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	now := time.Now()
	for id, room := range rm.rooms {
		if room.IsEmpty() && now.Sub(room.GetLastActivityAt()) > rm.roomIdleTimeout {
			delete(rm.rooms, id)
			rm.logger.Info("cleaned up empty room", "room_id", id)
		}
	}
}

// GetOrCreateRoom gets an existing room or creates a new one.
func (rm *RoomManager) GetOrCreateRoom(roomID, name, streamID string) *Room {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if room, ok := rm.rooms[roomID]; ok {
		return room
	}

	room := NewRoom(roomID, name, streamID)
	rm.rooms[roomID] = room
	rm.logger.Info("created new room", "room_id", roomID, "name", name)

	return room
}

// GetRoom returns a room by ID.
func (rm *RoomManager) GetRoom(roomID string) *Room {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	return rm.rooms[roomID]
}

// DeleteRoom removes a room.
func (rm *RoomManager) DeleteRoom(roomID string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	if room, ok := rm.rooms[roomID]; ok {
		// Notify clients in the room.
		room.Broadcast(NewWSMessage(MessageTypeSystemDisconnect, map[string]any{
			"reason": "room_closed",
		}))
		delete(rm.rooms, roomID)
		rm.logger.Info("deleted room", "room_id", roomID)
	}
}

// JoinRoom adds a client to a room.
func (rm *RoomManager) JoinRoom(client *Client, roomID string) {
	room := rm.GetOrCreateRoom(roomID, roomID, "")

	// Leave current room if in one.
	if client.RoomID != "" && client.RoomID != roomID {
		rm.LeaveRoom(client, client.RoomID)
	}

	room.AddClient(client)

	rm.logger.Info("client joined room",
		"client_id", client.ID,
		"room_id", roomID,
		"client_count", room.GetClientCount(),
	)

	// Broadcast viewer count update.
	rm.broadcastViewerCount(room)
}

// LeaveRoom removes a client from a room.
func (rm *RoomManager) LeaveRoom(client *Client, roomID string) {
	rm.mu.RLock()
	room, ok := rm.rooms[roomID]
	rm.mu.RUnlock()

	if !ok {
		return
	}

	room.RemoveClient(client.ID)

	rm.logger.Info("client left room",
		"client_id", client.ID,
		"room_id", roomID,
		"client_count", room.GetClientCount(),
	)

	// Broadcast viewer count update.
	rm.broadcastViewerCount(room)
}

// broadcastViewerCount broadcasts the viewer count to a room.
func (rm *RoomManager) broadcastViewerCount(room *Room) {
	total := room.GetClientCount()
	authenticated := room.GetAuthenticatedCount()
	anonymous := room.GetAnonymousCount()

	message := NewViewerCountMessage(room.ID, total, authenticated, anonymous)
	room.Broadcast(message)
}

// BroadcastToRoom sends a message to all clients in a room.
func (rm *RoomManager) BroadcastToRoom(roomID string, message *WSMessage) {
	rm.mu.RLock()
	room, ok := rm.rooms[roomID]
	rm.mu.RUnlock()

	if !ok {
		return
	}

	room.Broadcast(message)
}

// BroadcastToRoomExcept sends a message to all clients in a room except one.
func (rm *RoomManager) BroadcastToRoomExcept(roomID string, message *WSMessage, excludeClientID string) {
	rm.mu.RLock()
	room, ok := rm.rooms[roomID]
	rm.mu.RUnlock()

	if !ok {
		return
	}

	room.BroadcastExcept(message, excludeClientID)
}

// IsClientInRoom checks if a client is in a room.
func (rm *RoomManager) IsClientInRoom(clientID, roomID string) bool {
	rm.mu.RLock()
	room, ok := rm.rooms[roomID]
	rm.mu.RUnlock()

	if !ok {
		return false
	}

	return room.HasClient(clientID)
}

// GetRoomClientCount returns the number of clients in a room.
func (rm *RoomManager) GetRoomClientCount(roomID string) int {
	rm.mu.RLock()
	room, ok := rm.rooms[roomID]
	rm.mu.RUnlock()

	if !ok {
		return 0
	}

	return room.GetClientCount()
}

// GetAllRooms returns information about all rooms.
func (rm *RoomManager) GetAllRooms() []*RoomInfo {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	rooms := make([]*RoomInfo, 0, len(rm.rooms))
	for _, room := range rm.rooms {
		rooms = append(rooms, room.GetInfo())
	}
	return rooms
}

// GetRoomCount returns the total number of rooms.
func (rm *RoomManager) GetRoomCount() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	return len(rm.rooms)
}

// GetTotalClientsInRooms returns the total number of clients across all rooms.
func (rm *RoomManager) GetTotalClientsInRooms() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	total := 0
	for _, room := range rm.rooms {
		total += room.GetClientCount()
	}
	return total
}

// SetRoomMetadata sets metadata for a room.
func (rm *RoomManager) SetRoomMetadata(roomID, key string, value any) bool {
	rm.mu.RLock()
	room, ok := rm.rooms[roomID]
	rm.mu.RUnlock()

	if !ok {
		return false
	}

	room.SetMetadata(key, value)
	return true
}

// GetRoomMetadata gets metadata from a room.
func (rm *RoomManager) GetRoomMetadata(roomID, key string) (any, bool) {
	rm.mu.RLock()
	room, ok := rm.rooms[roomID]
	rm.mu.RUnlock()

	if !ok {
		return nil, false
	}

	return room.GetMetadata(key)
}

// RoomManagerStats contains statistics about the room manager.
type RoomManagerStats struct {
	TotalRooms      int `json:"total_rooms"`
	TotalClients    int `json:"total_clients"`
	ActiveRooms     int `json:"active_rooms"`     // Rooms with at least one client.
	EmptyRooms      int `json:"empty_rooms"`
}

// GetStats returns statistics about the room manager.
func (rm *RoomManager) GetStats() *RoomManagerStats {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	stats := &RoomManagerStats{
		TotalRooms: len(rm.rooms),
	}

	for _, room := range rm.rooms {
		count := room.GetClientCount()
		stats.TotalClients += count
		if count > 0 {
			stats.ActiveRooms++
		} else {
			stats.EmptyRooms++
		}
	}

	return stats
}
