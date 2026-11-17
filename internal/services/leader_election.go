// Package services — BotLeaderElection provides per-user distributed leader
// locking via Valkey SET NX PX so only one replica runs a given user's bot.
package services

import (
	"context"
	"fmt"
	"time"

	"github.com/valkey-io/valkey-go"
)

// BotLeaderElection manages per-user bot leader locks.
// Only the instance holding the lock should run a user's bot.
type BotLeaderElection interface {
	TryAcquire(ctx context.Context, userID string) (acquired bool, err error)
	Renew(ctx context.Context, userID string) error
	Release(ctx context.Context, userID string) error
	// RenewInterval returns how often callers should invoke Renew to keep the
	// lock alive before its TTL expires (BOT-01: the lease is never renewed
	// otherwise, causing split-brain across replicas after the TTL elapses).
	RenewInterval() time.Duration
}

// ValkeyLeaderElection implements BotLeaderElection using Valkey SET NX PX.
type ValkeyLeaderElection struct {
	client     valkey.Client
	instanceID string        // unique per replica (e.g. hostname + port)
	ttl        time.Duration // lock TTL; default 30s
	renewEvery time.Duration // renew interval; default 20s
}

// NewValkeyLeaderElection returns a ValkeyLeaderElection with sensible defaults.
// instanceID must be unique per running replica (e.g. os.Hostname() + ":port").
func NewValkeyLeaderElection(client valkey.Client, instanceID string) *ValkeyLeaderElection {
	return &ValkeyLeaderElection{
		client:     client,
		instanceID: instanceID,
		ttl:        30 * time.Second,
		renewEvery: 20 * time.Second,
	}
}

// RenewInterval returns the configured renewal interval (default 20s for a
// 30s TTL), so callers know how often to call Renew to keep the lease alive.
func (e *ValkeyLeaderElection) RenewInterval() time.Duration {
	if e.renewEvery <= 0 {
		return 20 * time.Second
	}
	return e.renewEvery
}

// leaderKey returns the Valkey key for a user's bot leader lock.
func (e *ValkeyLeaderElection) leaderKey(userID string) string {
	return fmt.Sprintf("rl:bot:leader:%s", userID)
}

// ttlMillis converts the configured TTL to milliseconds for SET PX.
func (e *ValkeyLeaderElection) ttlMillis() int64 {
	return e.ttl.Milliseconds()
}

// TryAcquire attempts to acquire the leader lock for userID using
//
//	SET rl:bot:leader:{userID} {instanceID} NX PX 30000
//
// Returns (true, nil) if the lock was acquired, (false, nil) if another
// instance already holds it, and (false, err) on a Valkey error.
func (e *ValkeyLeaderElection) TryAcquire(ctx context.Context, userID string) (bool, error) {
	key := e.leaderKey(userID)
	cmd := e.client.B().Set().Key(key).Value(e.instanceID).
		Nx().PxMilliseconds(e.ttlMillis()).Build()

	result := e.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		// Valkey returns a nil/null reply (not an error) when NX fails.
		// The valkey-go client translates that into a valkey.Nil sentinel.
		if valkey.IsValkeyNil(err) {
			return false, nil
		}
		return false, fmt.Errorf("TryAcquire: valkey SET NX failed: %w", err)
	}

	// A successful SET NX returns "OK".
	ok, err := result.ToString()
	if err != nil {
		return false, fmt.Errorf("TryAcquire: unexpected response type: %w", err)
	}
	return ok == "OK", nil
}

// Renew extends the lock TTL for userID using
//
//	SET rl:bot:leader:{userID} {instanceID} XX PX 30000
//
// Returns an error if the key no longer exists (lock was lost) or on Valkey errors.
func (e *ValkeyLeaderElection) Renew(ctx context.Context, userID string) error {
	key := e.leaderKey(userID)
	cmd := e.client.B().Set().Key(key).Value(e.instanceID).
		Xx().PxMilliseconds(e.ttlMillis()).Build()

	result := e.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		if valkey.IsValkeyNil(err) {
			return fmt.Errorf("Renew: lock for user %s no longer held (expired or stolen)", userID)
		}
		return fmt.Errorf("Renew: valkey SET XX failed: %w", err)
	}

	ok, err := result.ToString()
	if err != nil {
		return fmt.Errorf("Renew: unexpected response type: %w", err)
	}
	if ok != "OK" {
		return fmt.Errorf("Renew: lock for user %s no longer held", userID)
	}
	return nil
}

// releaseScript is a Lua script that atomically deletes the key only if its
// value matches instanceID. This prevents a slow replica from deleting a lock
// that was already re-acquired by another instance after TTL expiry.
//
//	KEYS[1] = lock key
//	ARGV[1] = expected instanceID value
const releaseScript = `
if redis.call("GET", KEYS[1]) == ARGV[1] then
    return redis.call("DEL", KEYS[1])
else
    return 0
end`

// Release releases the leader lock for userID only if this instance holds it.
// It uses a Lua script for atomicity (GET+DEL is not atomic otherwise).
// Returns nil if the lock was released or was already gone.
func (e *ValkeyLeaderElection) Release(ctx context.Context, userID string) error {
	key := e.leaderKey(userID)

	cmd := e.client.B().Eval().
		Script(releaseScript).
		Numkeys(1).
		Key(key).
		Arg(e.instanceID).
		Build()

	result := e.client.Do(ctx, cmd)
	if err := result.Error(); err != nil {
		// If the key doesn't exist (already expired), treat as success.
		if valkey.IsValkeyNil(err) {
			return nil
		}
		return fmt.Errorf("Release: eval failed: %w", err)
	}
	// Result is 1 (deleted) or 0 (not our lock). Both are acceptable.
	return nil
}

// SetLeaderElection sets or replaces the BotLeaderElection on the BotManager.
// Passing nil disables distributed locking (reverts to single-instance mode).
// This is safe to call before any bots are started; calling it while bots are
// running changes behaviour only for subsequently started bots.
func (bm *BotManager) SetLeaderElection(le BotLeaderElection) {
	bm.mu.Lock()
	defer bm.mu.Unlock()
	bm.leaderElection = le
}
