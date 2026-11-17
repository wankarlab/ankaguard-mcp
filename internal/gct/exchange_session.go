package gct

import (
	"fmt"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"
)

// ExchangeSession owns an isolated, per-user KuCoin REST client + futures
// manager. Orders are signed exclusively with the user's own keys — no shared
// mutable credential state, so concurrent users cannot cross-trade.
//
// lastUsedAt is stored as an atomic UnixNano timestamp so the SessionManager
// fast path (which calls Touch/LastUsedAt under a read lock) and the background
// TTL sweep can access it concurrently without a data race.
type ExchangeSession struct {
	userID         string
	exchange       string
	kucoinClient   *KuCoinRESTClient
	futuresManager *FuturesManager
	createdAt      time.Time
	lastUsedAt     atomic.Int64 // UnixNano; read/written concurrently
}

// NewExchangeSession builds a dedicated KuCoinRESTClient and FuturesManager
// from already-decrypted credentials. The caller is responsible for zeroing
// out key/secret/passphrase strings after this call returns.
func NewExchangeSession(userID, exchange, apiKey, apiSecret, passphrase string, logger *logrus.Logger) (*ExchangeSession, error) {
	if userID == "" {
		return nil, fmt.Errorf("userID is required")
	}
	if exchange == "" {
		return nil, fmt.Errorf("exchange is required")
	}
	if apiKey == "" || apiSecret == "" {
		return nil, fmt.Errorf("apiKey and apiSecret are required")
	}
	if logger == nil {
		logger = logrus.New()
	}

	// Build a fresh REST client scoped exclusively to this user.
	kc := NewKuCoinRESTClient()
	kc.SetCredentials(apiKey, apiSecret, passphrase)

	// Build a futures manager on top of the isolated REST client.
	fm, err := NewFuturesManager(kc, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create futures manager for session (user=%s exchange=%s): %w", userID, exchange, err)
	}

	now := time.Now()
	s := &ExchangeSession{
		userID:         userID,
		exchange:       exchange,
		kucoinClient:   kc,
		futuresManager: fm,
		createdAt:      now,
	}
	s.lastUsedAt.Store(now.UnixNano())
	return s, nil
}

// KuCoinClient returns the isolated REST client for this session.
func (s *ExchangeSession) KuCoinClient() *KuCoinRESTClient {
	return s.kucoinClient
}

// FuturesManager returns the futures manager for this session.
func (s *ExchangeSession) FuturesManager() *FuturesManager {
	return s.futuresManager
}

// Touch updates lastUsedAt to now. Call this on every use to prevent TTL
// eviction. Safe for concurrent use (atomic store).
func (s *ExchangeSession) Touch() {
	s.lastUsedAt.Store(time.Now().UnixNano())
}

// UserID returns the user identifier associated with this session.
func (s *ExchangeSession) UserID() string {
	return s.userID
}

// Exchange returns the exchange name for this session.
func (s *ExchangeSession) Exchange() string {
	return s.exchange
}

// CreatedAt returns when the session was created.
func (s *ExchangeSession) CreatedAt() time.Time {
	return s.createdAt
}

// LastUsedAt returns when the session was last used. Safe for concurrent use
// (atomic load).
func (s *ExchangeSession) LastUsedAt() time.Time {
	return time.Unix(0, s.lastUsedAt.Load())
}
