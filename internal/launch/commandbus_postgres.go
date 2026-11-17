package launch

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// commandsChannel is the NOTIFY channel; the payload is the command id.
const commandsChannel = "launch_commands"

// PostgresCommandBus implements CommandBus over the launch.commands table and
// LISTEN/NOTIFY. Notifications are only a wake-up signal: the table is the
// source of truth, and a periodic poll picks up anything a lost notification
// (e.g. during a reconnect) would have missed.
type PostgresCommandBus struct {
	pool         *pgxpool.Pool
	pollInterval time.Duration
	staleClaim   time.Duration
}

// NewPostgresCommandBus returns a bus on pool. pollInterval bounds how late a
// command can be picked up without a notification; staleClaim is how long a
// claimed-but-unfinished command waits before it is requeued (its consumer is
// assumed dead). Zero values use 5s and 5m.
func NewPostgresCommandBus(pool *pgxpool.Pool, pollInterval, staleClaim time.Duration) *PostgresCommandBus {
	if pollInterval <= 0 {
		pollInterval = 5 * time.Second
	}
	if staleClaim <= 0 {
		staleClaim = 5 * time.Minute
	}
	return &PostgresCommandBus{pool: pool, pollInterval: pollInterval, staleClaim: staleClaim}
}

// Publish inserts the command and notifies consumers in one transaction, so a
// notification is never sent for a row that was rolled back.
func (b *PostgresCommandBus) Publish(ctx context.Context, cmd Command) (int64, error) {
	if err := cmd.Validate(); err != nil {
		return 0, err
	}
	payload := cmd.Payload
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	userID := cmd.UserID
	if userID == "" {
		userID = "default"
	}

	tx, err := b.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("launch command publish: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO launch.commands (kind, payload, actor, reason, user_id)
		 VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		string(cmd.Kind), payload, cmd.Actor, cmd.Reason, userID).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("launch command publish: insert: %w", err)
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, commandsChannel, strconv.FormatInt(id, 10)); err != nil {
		return 0, fmt.Errorf("launch command publish: notify: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("launch command publish: commit: %w", err)
	}
	return id, nil
}

// Consume listens for commands and handles them until ctx is cancelled. It
// returns ctx.Err() on cancellation, or an error if the listener cannot be
// established.
func (b *PostgresCommandBus) Consume(ctx context.Context, handle CommandHandler) error {
	if _, err := b.pool.Exec(ctx,
		`UPDATE launch.commands SET status = 'pending', claimed_at = NULL
		 WHERE status = 'claimed' AND claimed_at < NOW() - $1::interval`,
		fmt.Sprintf("%d seconds", int(b.staleClaim.Seconds()))); err != nil {
		return fmt.Errorf("launch command consume: requeue stale claims: %w", err)
	}

	conn, err := b.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("launch command consume: acquire listener: %w", err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+commandsChannel); err != nil {
		return fmt.Errorf("launch command consume: listen: %w", err)
	}

	for {
		if err := b.drain(ctx, handle); err != nil {
			return err
		}
		waitCtx, cancel := context.WithTimeout(ctx, b.pollInterval)
		_, err := conn.Conn().WaitForNotification(waitCtx)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("launch command consume: wait: %w", err)
		}
	}
}

// drain claims and handles pending commands one at a time, oldest first.
func (b *PostgresCommandBus) drain(ctx context.Context, handle CommandHandler) error {
	for {
		cmd, ok, err := b.claim(ctx)
		if err != nil || !ok {
			return err
		}
		status, result := "done", ""
		if herr := handle(ctx, cmd); herr != nil {
			status, result = "failed", herr.Error()
		}
		// Record the outcome even if ctx was cancelled mid-handle.
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		_, err = b.pool.Exec(finishCtx,
			`UPDATE launch.commands SET status = $2, result = $3, finished_at = NOW() WHERE id = $1`,
			cmd.ID, status, result)
		cancel()
		if err != nil {
			return fmt.Errorf("launch command %d: record outcome: %w", cmd.ID, err)
		}
	}
}

func (b *PostgresCommandBus) claim(ctx context.Context) (Command, bool, error) {
	var cmd Command
	var kind string
	err := b.pool.QueryRow(ctx,
		`UPDATE launch.commands SET status = 'claimed', claimed_at = NOW()
		 WHERE id = (
		     SELECT id FROM launch.commands WHERE status = 'pending'
		     ORDER BY id FOR UPDATE SKIP LOCKED LIMIT 1)
		 RETURNING id, kind, payload, actor, reason, user_id, created_at`).
		Scan(&cmd.ID, &kind, &cmd.Payload, &cmd.Actor, &cmd.Reason, &cmd.UserID, &cmd.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Command{}, false, nil
	}
	if err != nil {
		return Command{}, false, fmt.Errorf("launch command claim: %w", err)
	}
	cmd.Kind = CommandKind(kind)
	return cmd, true, nil
}
