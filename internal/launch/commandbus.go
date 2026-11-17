package launch

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// CommandKind enumerates operator commands sent from trading-mcp-server
// (API/UI) to the launch-bot process.
type CommandKind string

const (
	CommandKillSwitch    CommandKind = "kill_switch"
	CommandResume        CommandKind = "resume"
	CommandApprove       CommandKind = "approve"
	CommandClosePosition CommandKind = "close_position"
)

func (k CommandKind) valid() bool {
	switch k {
	case CommandKillSwitch, CommandResume, CommandApprove, CommandClosePosition:
		return true
	}
	return false
}

// Command is one operator instruction. The stored row is its audit record, so
// Actor and Reason are mandatory.
type Command struct {
	ID        int64
	Kind      CommandKind
	Payload   json.RawMessage
	Actor     string
	Reason    string
	UserID    string
	CreatedAt time.Time
}

// Validate checks the fields required before a command may be published.
func (c Command) Validate() error {
	if !c.Kind.valid() {
		return errors.New("launch command: unknown kind " + string(c.Kind))
	}
	if c.Actor == "" {
		return errors.New("launch command: actor is required")
	}
	if c.Reason == "" {
		return errors.New("launch command: reason is required")
	}
	if len(c.Payload) > 0 && !json.Valid(c.Payload) {
		return errors.New("launch command: payload is not valid JSON")
	}
	return nil
}

// CommandHandler processes one claimed command. A returned error marks the
// command failed with that message; it is not retried automatically.
type CommandHandler func(ctx context.Context, cmd Command) error

// CommandBus carries commands between processes (tech-stack.md §7 decision 3).
type CommandBus interface {
	// Publish stores the command and wakes the consumer; returns its id.
	Publish(ctx context.Context, cmd Command) (int64, error)
	// Consume claims and handles commands until ctx is cancelled. Each command
	// is handled at most once even with several consumers.
	Consume(ctx context.Context, handle CommandHandler) error
}
