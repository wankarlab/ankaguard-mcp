// Package websocket provides WebSocket server functionality for real-time
// communication in the IATB trading system.
package websocket

import (
	"encoding/json"
	"time"
)

// Message types for WebSocket communication.
// These follow the pattern: category.action
const (
	// System events
	MessageTypeSystemConnected  = "system.connected"
	MessageTypeSystemHeartbeat  = "system.heartbeat"
	MessageTypeSystemDisconnect = "system.disconnect"

	// Authentication events
	MessageTypeAuth       = "auth"
	MessageTypeAuthResult = "auth_result"

	// Subscription events
	MessageTypeSubscribe    = "subscribe"
	MessageTypeSubscribed   = "subscribed"
	MessageTypeUnsubscribe  = "unsubscribe"
	MessageTypeUnsubscribed = "unsubscribed"

	// Round events
	MessageTypeRoundStarted      = "round.started"
	MessageTypeRoundVoting       = "round.voting"
	MessageTypeRoundCountdown    = "round.countdown"
	MessageTypeRoundVotingClosed = "round.voting_closed"
	MessageTypeRoundCompleted    = "round.completed"
	MessageTypeRoundCancelled    = "round.cancelled"
	MessageTypeRoundPhaseChange  = "round.phase_changed"

	// Vote events
	MessageTypeVoteReceived  = "vote.received"
	MessageTypeVoteUpdate    = "vote.update"
	MessageTypeVoteMilestone = "vote.milestone"

	// Trade events
	MessageTypeTradeExecuted = "trade.executed"
	MessageTypeTradeClosed   = "trade.closed"

	// P&L events
	MessageTypePnLUpdate = "pnl.update"

	// Narrator events
	MessageTypeNarratorSpeech      = "narrator.speech"
	MessageTypeNarratorAudioStream = "narrator.audio_stream"

	// Leaderboard events
	MessageTypeLeaderboardUpdate = "leaderboard.update"

	// Chat events
	MessageTypeChatMessage = "chat.message"

	// Viewer events
	MessageTypeViewerCount = "viewer.count"

	// Market price events
	MessageTypeMarketPriceUpdate   = "market_price.update"
	MessageTypeMarketPriceSnapshot = "market_price.snapshot"
	MessageTypeMarketPriceError    = "market_price.error"

	// Client-to-server events
	MessageTypePing     = "ping"
	MessageTypePong     = "pong"
	MessageTypeGetState = "get_state"
	MessageTypeState    = "state"

	// Error event
	MessageTypeError = "error"
)

// Available channels for subscription.
const (
	ChannelRounds       = "rounds"
	ChannelVotes        = "votes"
	ChannelTrades       = "trades"
	ChannelPnL          = "pnl"
	ChannelNarrator     = "narrator"
	ChannelLeaderboard  = "leaderboard"
	ChannelSystem       = "system"
	ChannelChat         = "chat"
	ChannelMarketPrices = "market_prices"
)

// Channel groups for convenience subscriptions.
var ChannelGroups = map[string][]string{
	"overlay":   {ChannelRounds, ChannelVotes, ChannelTrades, ChannelPnL, ChannelNarrator},
	"analytics": {ChannelVotes, ChannelTrades, ChannelLeaderboard},
	"minimal":   {ChannelRounds, ChannelTrades},
	"all":       {ChannelRounds, ChannelVotes, ChannelTrades, ChannelPnL, ChannelNarrator, ChannelLeaderboard, ChannelSystem, ChannelChat, ChannelMarketPrices},
}

// ValidChannels contains all valid channel names.
var ValidChannels = map[string]bool{
	ChannelRounds:       true,
	ChannelVotes:        true,
	ChannelTrades:       true,
	ChannelPnL:          true,
	ChannelNarrator:     true,
	ChannelLeaderboard:  true,
	ChannelSystem:       true,
	ChannelChat:         true,
	ChannelMarketPrices: true,
}

// IsValidChannel checks if a channel name is valid.
func IsValidChannel(channel string) bool {
	return ValidChannels[channel]
}

// GetChannelGroup returns the channels for a group name.
func GetChannelGroup(group string) []string {
	if channels, ok := ChannelGroups[group]; ok {
		return channels
	}
	return nil
}

// WSMessage represents a WebSocket message.
type WSMessage struct {
	// Type is the message type (e.g., "round.started", "vote.update").
	Type string `json:"type"`

	// ID is an optional message ID for acknowledgment.
	ID string `json:"id,omitempty"`

	// Timestamp is when the message was created.
	Timestamp time.Time `json:"timestamp"`

	// Payload contains the message data.
	Payload any `json:"payload,omitempty"`
}

// NewWSMessage creates a new WebSocket message.
func NewWSMessage(msgType string, payload any) *WSMessage {
	return &WSMessage{
		Type:      msgType,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

// NewWSMessageWithID creates a new WebSocket message with an ID.
func NewWSMessageWithID(id, msgType string, payload any) *WSMessage {
	return &WSMessage{
		ID:        id,
		Type:      msgType,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

// Serialize serializes the message to JSON bytes.
func (m *WSMessage) Serialize() ([]byte, error) {
	return json.Marshal(m)
}

// MustSerialize serializes the message, panicking on error.
func (m *WSMessage) MustSerialize() []byte {
	data, err := m.Serialize()
	if err != nil {
		panic(err)
	}
	return data
}

// ParseWSMessage parses a WebSocket message from JSON bytes.
func ParseWSMessage(data []byte) (*WSMessage, error) {
	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// IncomingMessage represents a message received from a client.
type IncomingMessage struct {
	Type     string   `json:"type"`
	Token    string   `json:"token,omitempty"`
	Channels []string `json:"channels,omitempty"`
	Groups   []string `json:"groups,omitempty"`
	Symbols  []string `json:"symbols,omitempty"` // For market_prices channel
	Mode     string   `json:"mode,omitempty"`    // For portfolio mode
}

// ParseIncomingMessage parses an incoming client message.
func ParseIncomingMessage(data []byte) (*IncomingMessage, error) {
	var msg IncomingMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

// ErrorPayload represents an error message payload.
type ErrorPayload struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	Details    any    `json:"details,omitempty"`
	RetryAfter int    `json:"retry_after,omitempty"` // milliseconds
}

// NewErrorMessage creates a new error message.
func NewErrorMessage(code, message string) *WSMessage {
	return NewWSMessage(MessageTypeError, &ErrorPayload{
		Code:    code,
		Message: message,
	})
}

// NewErrorMessageWithDetails creates a new error message with details.
func NewErrorMessageWithDetails(code, message string, details any) *WSMessage {
	return NewWSMessage(MessageTypeError, &ErrorPayload{
		Code:    code,
		Message: message,
		Details: details,
	})
}

// NewRateLimitError creates a rate limit error message.
func NewRateLimitError(retryAfterMs int) *WSMessage {
	return NewWSMessage(MessageTypeError, &ErrorPayload{
		Code:       "RATE_LIMITED",
		Message:    "Too many messages",
		RetryAfter: retryAfterMs,
	})
}

// Error codes.
const (
	ErrorCodeAuthRequired    = "AUTH_REQUIRED"
	ErrorCodeAuthInvalid     = "AUTH_INVALID_TOKEN"
	ErrorCodeSubscriptionInv = "SUBSCRIPTION_INVALID"
	ErrorCodeRateLimited     = "RATE_LIMITED"
	ErrorCodeInvalidMessage  = "INVALID_MESSAGE"
	ErrorCodeInternalError   = "INTERNAL_ERROR"
)

// ConnectedPayload is the payload for system.connected messages.
type ConnectedPayload struct {
	ConnectionID    string `json:"connection_id"`
	ServerTime      string `json:"server_time"`
	ProtocolVersion string `json:"protocol_version"`
}

// NewConnectedMessage creates a new system.connected message.
func NewConnectedMessage(connectionID string) *WSMessage {
	return NewWSMessage(MessageTypeSystemConnected, &ConnectedPayload{
		ConnectionID:    connectionID,
		ServerTime:      time.Now().UTC().Format(time.RFC3339),
		ProtocolVersion: "1.0",
	})
}

// AuthResultPayload is the payload for auth_result messages.
type AuthResultPayload struct {
	Success bool         `json:"success"`
	User    *UserInfo    `json:"user,omitempty"`
	Error   *ErrorDetail `json:"error,omitempty"`
}

// UserInfo contains authenticated user information.
type UserInfo struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	Permissions []string `json:"permissions"`
}

// ErrorDetail contains error details for auth_result.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// NewAuthSuccessMessage creates a successful auth result message.
func NewAuthSuccessMessage(userID, username string, permissions []string) *WSMessage {
	return NewWSMessage(MessageTypeAuthResult, &AuthResultPayload{
		Success: true,
		User: &UserInfo{
			ID:          userID,
			Username:    username,
			Permissions: permissions,
		},
	})
}

// NewAuthFailureMessage creates a failed auth result message.
func NewAuthFailureMessage(code, message string) *WSMessage {
	return NewWSMessage(MessageTypeAuthResult, &AuthResultPayload{
		Success: false,
		Error: &ErrorDetail{
			Code:    code,
			Message: message,
		},
	})
}

// SubscribedPayload is the payload for subscribed messages.
type SubscribedPayload struct {
	Channels []string `json:"channels"`
}

// NewSubscribedMessage creates a subscribed confirmation message.
func NewSubscribedMessage(channels []string) *WSMessage {
	return NewWSMessage(MessageTypeSubscribed, &SubscribedPayload{
		Channels: channels,
	})
}

// NewUnsubscribedMessage creates an unsubscribed confirmation message.
func NewUnsubscribedMessage(channels []string) *WSMessage {
	return NewWSMessage(MessageTypeUnsubscribed, &SubscribedPayload{
		Channels: channels,
	})
}

// PongMessage creates a pong response message.
func NewPongMessage() *WSMessage {
	return NewWSMessage(MessageTypePong, nil)
}

// RoundStartedPayload is the payload for round.started messages.
type RoundStartedPayload struct {
	RoundID         string  `json:"round_id"`
	Symbol          string  `json:"symbol"`
	DurationSeconds int     `json:"duration_seconds"`
	EndTime         string  `json:"end_time"`
	InitialPrice    float64 `json:"initial_price,omitempty"`
	Leverage        int     `json:"leverage"`
	LongScore       int64   `json:"long_score"`
	ShortScore      int64   `json:"short_score"`
}

// VoteReceivedPayload is the payload for vote.received messages.
type VoteReceivedPayload struct {
	VoteID        string    `json:"vote_id"`
	RoundID       string    `json:"round_id"`
	User          *VoteUser `json:"user"`
	Side          string    `json:"side"`
	Amount        float64   `json:"amount"`
	WeightedScore int64     `json:"weighted_score"`
	LongScore     int64     `json:"long_score"`
	ShortScore    int64     `json:"short_score"`
	IsHighlighted bool      `json:"is_highlighted"`
}

// VoteUser contains voter information.
type VoteUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Platform string `json:"platform"`
}

// VoteUpdatePayload is the payload for vote.update messages.
type VoteUpdatePayload struct {
	RoundID         string  `json:"round_id"`
	LongVotes       int64   `json:"long_votes"`
	ShortVotes      int64   `json:"short_votes"`
	LongAmount      string  `json:"long_amount"`
	ShortAmount     string  `json:"short_amount"`
	TotalVotes      int64   `json:"total_votes"`
	TotalAmount     string  `json:"total_amount"`
	LongPercentage  float64 `json:"long_percentage"`
	ShortPercentage float64 `json:"short_percentage"`
}

// TradeExecutedPayload is the payload for trade.executed messages.
type TradeExecutedPayload struct {
	TradeID    string  `json:"trade_id"`
	RoundID    string  `json:"round_id"`
	Symbol     string  `json:"symbol"`
	Side       string  `json:"side"`
	EntryPrice float64 `json:"entry_price"`
	Amount     float64 `json:"amount"`
	Leverage   int     `json:"leverage"`
	StopLoss   float64 `json:"stop_loss,omitempty"`
	TakeProfit float64 `json:"take_profit,omitempty"`
	Status     string  `json:"status"`
}

// TradeClosedPayload is the payload for trade.closed messages.
type TradeClosedPayload struct {
	TradeID         string  `json:"trade_id"`
	RoundID         string  `json:"round_id"`
	ExitPrice       float64 `json:"exit_price"`
	ExitReason      string  `json:"exit_reason"`
	PnL             float64 `json:"pnl"`
	PnLPercentage   float64 `json:"pnl_percentage"`
	DurationSeconds int     `json:"duration_seconds"`
}

// PnLUpdatePayload is the payload for pnl.update messages.
type PnLUpdatePayload struct {
	TradeID              string  `json:"trade_id"`
	CurrentPrice         float64 `json:"current_price"`
	EntryPrice           float64 `json:"entry_price"`
	UnrealizedPnL        float64 `json:"unrealized_pnl"`
	UnrealizedPnLPercent float64 `json:"unrealized_pnl_percentage"`
	DistanceToSL         float64 `json:"distance_to_sl,omitempty"`
	DistanceToTP         float64 `json:"distance_to_tp,omitempty"`
}

// NarratorSpeechPayload is the payload for narrator.speech messages.
type NarratorSpeechPayload struct {
	SpeechID   string `json:"speech_id"`
	Text       string `json:"text"`
	AudioURL   string `json:"audio_url,omitempty"`
	DurationMs int    `json:"duration_ms,omitempty"`
	Trigger    string `json:"trigger,omitempty"`
	Priority   string `json:"priority,omitempty"`
}

// RoundCompletedPayload is the payload for round.completed messages.
type RoundCompletedPayload struct {
	RoundID         string  `json:"round_id"`
	Result          string  `json:"result"` // "win" or "loss"
	PnL             float64 `json:"pnl"`
	PnLPercentage   float64 `json:"pnl_percentage"`
	EntryPrice      float64 `json:"entry_price"`
	ExitPrice       float64 `json:"exit_price"`
	DurationSeconds int     `json:"duration_seconds"`
}

// ViewerCountPayload is the payload for viewer.count messages.
type ViewerCountPayload struct {
	RoomID      string `json:"room_id"`
	Count       int    `json:"count"`
	Authenticated int  `json:"authenticated"`
	Anonymous   int    `json:"anonymous"`
}

// NewViewerCountMessage creates a viewer count update message.
func NewViewerCountMessage(roomID string, total, authenticated, anonymous int) *WSMessage {
	return NewWSMessage(MessageTypeViewerCount, &ViewerCountPayload{
		RoomID:        roomID,
		Count:         total,
		Authenticated: authenticated,
		Anonymous:     anonymous,
	})
}

// StatePayload is the payload for state messages (response to get_state).
type StatePayload struct {
	ActiveRound *ActiveRoundState `json:"active_round,omitempty"`
	OpenTrade   *OpenTradeState   `json:"open_trade,omitempty"`
	DailyPnL    float64           `json:"daily_pnl"`
}

// ActiveRoundState contains the current round state.
type ActiveRoundState struct {
	RoundID          string `json:"round_id"`
	Phase            string `json:"phase"`
	LongScore        int64  `json:"long_score"`
	ShortScore       int64  `json:"short_score"`
	SecondsRemaining int    `json:"seconds_remaining"`
}

// OpenTradeState contains the current open trade state.
type OpenTradeState struct {
	TradeID      string  `json:"trade_id"`
	Symbol       string  `json:"symbol"`
	Side         string  `json:"side"`
	EntryPrice   float64 `json:"entry_price"`
	CurrentPrice float64 `json:"current_price"`
	PnL          float64 `json:"pnl"`
	PnLPercent   float64 `json:"pnl_percent"`
}

// LeaderboardUpdatePayload is the payload for leaderboard.update messages.
type LeaderboardUpdatePayload struct {
	Type    string             `json:"type"` // "daily", "weekly", "all_time"
	Entries []LeaderboardEntry `json:"entries"`
}

// LeaderboardEntry represents a single leaderboard entry.
type LeaderboardEntry struct {
	Rank          int     `json:"rank"`
	UserID        string  `json:"user_id"`
	Username      string  `json:"username"`
	TotalDonated  float64 `json:"total_donated"`
	VoteCount     int     `json:"vote_count"`
	WinRate       float64 `json:"win_rate"`
}

// ChatMessagePayload is the payload for chat.message messages.
type ChatMessagePayload struct {
	MessageID string `json:"message_id"`
	UserID    string `json:"user_id"`
	Username  string `json:"username"`
	Platform  string `json:"platform"`
	Content   string `json:"content"`
	IsMod     bool   `json:"is_mod"`
}

// MarketPriceUpdatePayload is the payload for market_price.update messages.
type MarketPriceUpdatePayload struct {
	Symbol            string  `json:"symbol"`              // Trading pair (e.g., "BTC-USDT")
	Price             float64 `json:"price"`               // Current market price
	Bid               float64 `json:"bid,omitempty"`       // Best bid price (optional)
	Ask               float64 `json:"ask,omitempty"`       // Best ask price (optional)
	Volume24h         float64 `json:"volume_24h,omitempty"` // 24-hour trading volume (optional)
	Change24h         float64 `json:"change_24h,omitempty"` // 24-hour price change absolute (optional)
	Change24hPercent  float64 `json:"change_24h_percent,omitempty"` // 24-hour price change percentage (optional)
	High24h           float64 `json:"high_24h,omitempty"`   // 24-hour high (optional)
	Low24h            float64 `json:"low_24h,omitempty"`    // 24-hour low (optional)
	LastUpdate        string  `json:"last_update"`          // ISO 8601 timestamp of price update
	Source            string  `json:"source"`               // Exchange source (e.g., "kucoin", "binance")
}

// MarketPriceSnapshotPayload is the payload for market_price.snapshot messages.
type MarketPriceSnapshotPayload struct {
	Symbols []MarketPriceData `json:"symbols"`
}

// MarketPriceData contains price data for a single symbol.
type MarketPriceData struct {
	Symbol     string  `json:"symbol"`
	Price      float64 `json:"price"`
	LastUpdate string  `json:"last_update"`
	Source     string  `json:"source"`
}

// MarketPriceErrorPayload is the payload for market_price.error messages.
type MarketPriceErrorPayload struct {
	Symbol     string `json:"symbol"`
	Error      string `json:"error"`
	Source     string `json:"source"`
	RetryAfter int    `json:"retry_after,omitempty"` // seconds
}

// NewMarketPriceUpdateMessage creates a market price update message.
func NewMarketPriceUpdateMessage(payload *MarketPriceUpdatePayload) *WSMessage {
	return &WSMessage{
		Type:      MessageTypeMarketPriceUpdate,
		Timestamp: time.Now().UTC(),
		Payload:   payload,
	}
}

// NewMarketPriceSnapshotMessage creates a market price snapshot message.
func NewMarketPriceSnapshotMessage(symbols []MarketPriceData) *WSMessage {
	return &WSMessage{
		Type:      MessageTypeMarketPriceSnapshot,
		Timestamp: time.Now().UTC(),
		Payload: &MarketPriceSnapshotPayload{
			Symbols: symbols,
		},
	}
}

// NewMarketPriceErrorMessage creates a market price error message.
func NewMarketPriceErrorMessage(symbol, errorMsg, source string, retryAfter int) *WSMessage {
	return &WSMessage{
		Type:      MessageTypeMarketPriceError,
		Timestamp: time.Now().UTC(),
		Payload: &MarketPriceErrorPayload{
			Symbol:     symbol,
			Error:      errorMsg,
			Source:     source,
			RetryAfter: retryAfter,
		},
	}
}
