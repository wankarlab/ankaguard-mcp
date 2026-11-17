package master

import (
	"sync"
	"time"

	"github.com/sirupsen/logrus"
)

// ActiveBotInfo is the minimal active-bot view needed by the lifecycle reconciler.
type ActiveBotInfo struct {
	Pair        string
	StartedAt   time.Time
	Status      string
	TradesToday int
}

// BotSpawner abstracts spawning/killing bots — typically wraps BotManager.
type BotSpawner interface {
	SpawnBot(pair string, config *GeneratedConfig, capitalUSD float64) error
	KillBot(pair string, reason string) error
	ListActiveBots() []ActiveBotInfo
}

// BotPlan is the desired state for a single pair.
type BotPlan struct {
	Pair         string
	Config       *GeneratedConfig
	AllocatedUSD float64
	Score        float64 // normalized 0-1
}

// LifecycleAction describes a reconciliation outcome.
type LifecycleAction struct {
	Pair   string
	Action string
	Reason string
	Error  string
}

const (
	ActionSpawn            = "SPAWN"
	ActionKill             = "KILL"
	ActionMaintain         = "MAINTAIN"
	ActionBlockedCooldown  = "BLOCKED_COOLDOWN"
	ActionSkipBelowSpawn   = "SKIP_BELOW_SPAWN"
)

// BotLifecycle reconciles desired vs actual bot states and enforces cooldowns.
type BotLifecycle struct {
	log              *logrus.Logger
	spawner          BotSpawner
	cooldowns        map[string]time.Time
	cooldownDuration time.Duration
	spawnThreshold   float64
	killThreshold    float64
	staleTimeout     time.Duration // kill bot with 0 trades after this duration
	mu               sync.RWMutex
}

// NewBotLifecycle constructs a lifecycle reconciler.
func NewBotLifecycle(log *logrus.Logger, spawner BotSpawner, cooldownDuration time.Duration, spawnThreshold, killThreshold float64) *BotLifecycle {
	if log == nil {
		log = logrus.New()
	}
	return &BotLifecycle{
		log:              log,
		spawner:          spawner,
		cooldowns:        make(map[string]time.Time),
		cooldownDuration: cooldownDuration,
		spawnThreshold:   spawnThreshold,
		killThreshold:    killThreshold,
		staleTimeout:     20 * time.Minute, // default: kill bots with 0 trades after 20 min
	}
}

// Reconcile compares desired plans vs active bots and triggers spawn/kill actions.
func (bl *BotLifecycle) Reconcile(desired map[string]*BotPlan, activeBots []ActiveBotInfo) []LifecycleAction {
	actions := make([]LifecycleAction, 0, len(desired)+len(activeBots))
	activeSet := make(map[string]ActiveBotInfo, len(activeBots))
	for _, ab := range activeBots {
		activeSet[ab.Pair] = ab
	}

	// Pass 1: handle active bots — kill those no longer desired, below kill threshold, or stale.
	for pair, ab := range activeSet {
		plan, stillDesired := desired[pair]
		if !stillDesired {
			err := bl.spawner.KillBot(pair, "no longer in selected opportunities")
			act := LifecycleAction{Pair: pair, Action: ActionKill, Reason: "not in desired set"}
			if err != nil {
				act.Error = err.Error()
			}
			actions = append(actions, act)
			continue
		}
		if plan.Score < bl.killThreshold {
			err := bl.spawner.KillBot(pair, "score below kill threshold")
			act := LifecycleAction{Pair: pair, Action: ActionKill, Reason: "score below kill threshold"}
			if err != nil {
				act.Error = err.Error()
			}
			actions = append(actions, act)
			continue
		}
		// Stale bot: no trades executed after staleTimeout — the pair's strategies
		// aren't firing signals (e.g. UNKNOWN regime). Kill it to free the slot for
		// higher-confidence pairs that are actually generating trades.
		if bl.staleTimeout > 0 && ab.TradesToday == 0 && time.Since(ab.StartedAt) > bl.staleTimeout {
			err := bl.spawner.KillBot(pair, "stale: no trades after timeout")
			act := LifecycleAction{Pair: pair, Action: ActionKill, Reason: "stale bot: 0 trades after timeout"}
			if err != nil {
				act.Error = err.Error()
			}
			bl.log.WithFields(logrus.Fields{
				"pair":    pair,
				"uptime":  time.Since(ab.StartedAt).Round(time.Minute),
				"timeout": bl.staleTimeout,
			}).Info("lifecycle: killing stale bot (no trades generated)")
			actions = append(actions, act)
			continue
		}
		actions = append(actions, LifecycleAction{Pair: pair, Action: ActionMaintain, Reason: "still in desired set"})
	}

	// Pass 2: handle desired bots not yet active — spawn if eligible.
	for pair, plan := range desired {
		if _, alreadyActive := activeSet[pair]; alreadyActive {
			continue
		}
		if bl.IsInCooldown(pair) {
			actions = append(actions, LifecycleAction{Pair: pair, Action: ActionBlockedCooldown, Reason: "pair under cooldown"})
			continue
		}
		if plan.Score < bl.spawnThreshold {
			actions = append(actions, LifecycleAction{Pair: pair, Action: ActionSkipBelowSpawn, Reason: "score below spawn threshold"})
			continue
		}
		if plan.Config == nil {
			actions = append(actions, LifecycleAction{Pair: pair, Action: ActionSkipBelowSpawn, Reason: "nil config"})
			continue
		}
		err := bl.spawner.SpawnBot(pair, plan.Config, plan.AllocatedUSD)
		act := LifecycleAction{Pair: pair, Action: ActionSpawn, Reason: "score above spawn threshold"}
		if err != nil {
			act.Error = err.Error()
		}
		actions = append(actions, act)
	}

	bl.log.WithField("actions", len(actions)).Info("lifecycle: reconcile complete")
	return actions
}

// MarkLoss puts the pair into cooldown for the configured duration.
func (bl *BotLifecycle) MarkLoss(pair string) {
	if bl.cooldownDuration <= 0 {
		return
	}
	bl.mu.Lock()
	bl.cooldowns[pair] = time.Now().Add(bl.cooldownDuration)
	bl.mu.Unlock()
	bl.log.WithFields(logrus.Fields{
		"pair":     pair,
		"cooldown": bl.cooldownDuration,
	}).Info("lifecycle: pair marked for cooldown after loss")
}

// IsInCooldown returns true if the pair is currently under cooldown.
func (bl *BotLifecycle) IsInCooldown(pair string) bool {
	bl.mu.RLock()
	endsAt, ok := bl.cooldowns[pair]
	bl.mu.RUnlock()
	if !ok {
		return false
	}
	if time.Now().After(endsAt) {
		bl.mu.Lock()
		delete(bl.cooldowns, pair)
		bl.mu.Unlock()
		return false
	}
	return true
}
