package dynamic

import (
	"github.com/google/uuid"
	"github.com/rantipay/trading-mcp/internal/config/dynamic/domain"
)

// Subscriber is a registered callback that receives change notifications for a parameter group.
type Subscriber struct {
	ID      string
	Group   string // registry group name, or "*" for all groups
	Handler func(changes []Change)
}

// Change describes a single parameter value transition.
type Change struct {
	Key       string
	ScopeType domain.ScopeType
	ScopeID   string
	OldValue  string
	NewValue  string
}

// Subscribe registers a callback for changes affecting parameters in the given group.
// Use "*" as group to receive notifications for every group.
// Returns an unsubscribe function that removes the registration.
func (s *ConfigStore) Subscribe(group string, h func(changes []Change)) (unsubscribe func()) {
	sub := Subscriber{
		ID:      uuid.NewString(),
		Group:   group,
		Handler: h,
	}

	s.muSubs.Lock()
	s.subscribers[group] = append(s.subscribers[group], sub)
	s.muSubs.Unlock()

	return func() {
		s.muSubs.Lock()
		defer s.muSubs.Unlock()
		subs := s.subscribers[group]
		for i, existing := range subs {
			if existing.ID == sub.ID {
				s.subscribers[group] = append(subs[:i], subs[i+1:]...)
				return
			}
		}
	}
}

// notify dispatches change notifications to all matching subscribers asynchronously.
// Each handler is called in its own goroutine so a slow handler never blocks the writer.
func (s *ConfigStore) notify(changes []Change) {
	if len(changes) == 0 {
		return
	}

	snap := s.snap.Load()

	// Group changes by registry group name so subscribers receive only relevant changes.
	byGroup := make(map[string][]Change)
	for _, c := range changes {
		grp := ""
		if reg, ok := snap.registry[c.Key]; ok {
			grp = reg.GroupName
		}
		byGroup[grp] = append(byGroup[grp], c)
	}

	s.muSubs.RLock()
	defer s.muSubs.RUnlock()

	for grp, gChanges := range byGroup {
		// Collect subscribers for this specific group plus catch-all "*".
		subs := make([]Subscriber, 0, len(s.subscribers[grp])+len(s.subscribers["*"]))
		subs = append(subs, s.subscribers[grp]...)
		subs = append(subs, s.subscribers["*"]...)

		for _, sub := range subs {
			// Capture loop variables for the goroutine.
			sub := sub
			gChanges := gChanges
			go sub.Handler(gChanges)
		}
	}
}
