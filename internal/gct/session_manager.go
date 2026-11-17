package gct

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// CredentialFailureRecorder is the minimal interface for recording auth failures.
// It is satisfied by *credentials.CredentialService (via its RecordFailure method)
// without creating an import cycle.
type CredentialFailureRecorder interface {
	// RecordFailure records a credential failure for circuit breaker activation.
	RecordFailure(ctx context.Context, userID, exchange string) error
}

// CredentialLoader is the minimal interface that SessionManager requires to
// fetch decrypted user credentials. It is satisfied by *credentials.CredentialService
// (via its GetCredential method) without creating an import cycle between the
// gct and credentials packages.
type CredentialLoader interface {
	// GetCredential returns fully-decrypted credentials for userID on exchange.
	// Implementations must validate active/expired/circuit-breaker state before
	// returning. Returned APIKey, APISecret, and Passphrase must not be empty
	// when err is nil (except Passphrase which is optional).
	GetCredential(ctx context.Context, userID, exchange string) (DecryptedExchangeCredential, error)
}

// DecryptedExchangeCredential holds the plaintext secrets returned by a
// CredentialLoader. This is a gct-local type to avoid importing the
// credentials package (which already imports gct, causing a cycle).
type DecryptedExchangeCredential struct {
	APIKey     string
	APISecret  string
	Passphrase string
}

// SessionManager maintains a TTL-bounded cache of per-user ExchangeSessions.
// Each session owns an isolated KuCoinRESTClient so that concurrent users
// never share credential state and cannot cross-trade.
//
// Session map key format: "<userID>:<exchange>"  (e.g. "u123:kucoin")
type SessionManager struct {
	mu         sync.RWMutex
	sessions   map[string]*ExchangeSession // key: userID + ":" + exchange
	loader     CredentialLoader
	credService CredentialFailureRecorder // Optional: for recording auth failures
	ttl        time.Duration
	log        *logrus.Logger
}

// NewSessionManager creates a SessionManager.
//
//   - loader: provides decrypted credentials on demand.
//   - ttl: how long an idle session stays cached before being swept. A TTL of
//     0 disables automatic eviction (sessions live until explicitly Evict-ed).
//   - log: logger; if nil, a default logrus logger is used.
//   - credService: optional credential failure recorder (for circuit breaker). Can be nil.
func NewSessionManager(loader CredentialLoader, ttl time.Duration, log *logrus.Logger) *SessionManager {
	return NewSessionManagerWithFailureRecorder(loader, nil, ttl, log)
}

// NewSessionManagerWithFailureRecorder creates a SessionManager with an optional
// credential failure recorder for auth failure tracking.
func NewSessionManagerWithFailureRecorder(loader CredentialLoader, credService CredentialFailureRecorder, ttl time.Duration, log *logrus.Logger) *SessionManager {
	if log == nil {
		log = logrus.New()
	}
	sm := &SessionManager{
		sessions:    make(map[string]*ExchangeSession),
		loader:      loader,
		credService: credService,
		ttl:         ttl,
		log:         log,
	}

	// Start background TTL sweep only when TTL is positive.
	if ttl > 0 {
		go sm.sweepLoop(ttl)
	}

	return sm
}

// sessionKey returns the map key for a given user/exchange pair.
// Exchange names are lowercased for consistency.
func sessionKey(userID, exchange string) string {
	// Inline normalize: lowercase without external package dependency.
	norm := exchange
	for i := 0; i < len(norm); i++ {
		if norm[i] >= 'A' && norm[i] <= 'Z' {
			b := []byte(norm)
			for j := range b {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			norm = string(b)
			break
		}
	}
	return userID + ":" + norm
}

// GetSession returns a cached ExchangeSession for the given user/exchange, or
// creates a fresh one by loading credentials via the CredentialLoader.
//
// Decrypted secrets are consumed immediately to build the KuCoinRESTClient
// and are never logged or stored in plaintext beyond the call stack.
func (sm *SessionManager) GetSession(ctx context.Context, userID, exchange string) (*ExchangeSession, error) {
	if userID == "" {
		return nil, fmt.Errorf("userID is required")
	}
	if exchange == "" {
		return nil, fmt.Errorf("exchange is required")
	}

	key := sessionKey(userID, exchange)

	// Fast path: return cached non-expired session.
	sm.mu.RLock()
	session, exists := sm.sessions[key]
	sm.mu.RUnlock()

	if exists && (sm.ttl == 0 || time.Since(session.LastUsedAt()) < sm.ttl) {
		session.Touch()
		sm.log.WithFields(logrus.Fields{
			"user_id":  userID,
			"exchange": exchange,
		}).Trace("Returning cached exchange session")
		return session, nil
	}

	// Slow path: load credentials and build a new session.
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Double-check after acquiring the write lock to avoid redundant builds.
	session, exists = sm.sessions[key]
	if exists && (sm.ttl == 0 || time.Since(session.LastUsedAt()) < sm.ttl) {
		session.Touch()
		return session, nil
	}

	// Retrieve and decrypt credentials via the loader.
	// Secrets must NOT be logged.
	cred, err := sm.loader.GetCredential(ctx, userID, exchange)
	if err != nil {
		return nil, fmt.Errorf("failed to load credentials for user=%s exchange=%s: %w", userID, exchange, err)
	}

	// Build the isolated session. NewExchangeSession copies the strings into
	// the KuCoinRESTClient; we zero the local copy immediately after.
	newSession, err := NewExchangeSession(
		userID,
		exchange,
		cred.APIKey,
		cred.APISecret,
		cred.Passphrase,
		sm.log,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to build exchange session: %w", err)
	}

	// Best-effort: zero out the plaintext secrets so GC can collect the originals.
	cred.APIKey = ""
	cred.APISecret = ""
	cred.Passphrase = ""

	sm.sessions[key] = newSession

	sm.log.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": exchange,
	}).Info("Created new isolated exchange session")

	return newSession, nil
}

// Evict removes the cached session for a user/exchange so that the next call
// to GetSession will build a fresh one (e.g. after credential rotation).
func (sm *SessionManager) Evict(userID, exchange string) {
	key := sessionKey(userID, exchange)

	sm.mu.Lock()
	delete(sm.sessions, key)
	sm.mu.Unlock()

	sm.log.WithFields(logrus.Fields{
		"user_id":  userID,
		"exchange": exchange,
	}).Debug("Evicted exchange session")
}

// SetCredentialLoader replaces the active credential loader at runtime and
// evicts all cached sessions so the next GetSession call loads fresh credentials
// from the new loader.
//
// Intended for the single-startup pattern: construct the SessionManager early
// with a stub loader (before the credential DB is ready), then upgrade once the
// DB is initialised. Concurrent GetSession calls in the slow path are safe —
// they hold the write lock, which this method also acquires, so they will
// finish with the old loader before the swap takes effect. A nil loader is a
// no-op (existing loader is preserved).
func (sm *SessionManager) SetCredentialLoader(loader CredentialLoader) {
	if loader == nil {
		return
	}
	sm.mu.Lock()
	sm.loader = loader
	sm.sessions = make(map[string]*ExchangeSession)
	sm.mu.Unlock()
	sm.log.Info("SessionManager: credential loader upgraded, all cached sessions evicted")
}

// sweepExpired removes all sessions whose lastUsedAt is older than ttl.
func (sm *SessionManager) sweepExpired() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()
	evicted := 0
	for key, session := range sm.sessions {
		if now.Sub(session.LastUsedAt()) >= sm.ttl {
			delete(sm.sessions, key)
			evicted++
		}
	}

	if evicted > 0 {
		sm.log.WithField("evicted", evicted).Debug("Swept expired exchange sessions")
	}
}

// sweepLoop runs sweepExpired on a periodic ticker equal to ttl/2 (minimum 5 s).
func (sm *SessionManager) sweepLoop(ttl time.Duration) {
	interval := ttl / 2
	if interval < 5*time.Second {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		sm.sweepExpired()
	}
}
