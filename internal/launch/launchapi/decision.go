package launchapi

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Sentinel errors of Decide.
var (
	ErrOpportunityExpired = errors.New("launchapi: opportunity expired")
	ErrAlreadyDecided     = errors.New("launchapi: opportunity already decided")
	ErrNotPending         = errors.New("launchapi: opportunity is not pending approval")
)

// DecisionChannel is the NOTIFY channel launch-bot listens on.
const DecisionChannel = "launch_decisions"

// Decision is the result of a successful Decide.
type Decision struct {
	DecisionID    int64  `json:"decision_id"`
	OpportunityID int64  `json:"opportunity_id"`
	Action        string `json:"action"`
}

// ValidCategory reports whether c is an accepted reason category.
func ValidCategory(c string) bool {
	switch c {
	case "narrative", "team", "community", "chart", "other":
		return true
	}
	return false
}

// Decide records a human approval or rejection of a pending opportunity. The
// opportunity row is locked for the duration so two operators cannot both
// decide. launch-bot consumes the decision (NOTIFY launch_decisions) and opens
// the manual or counterfactual position; nothing is traded here.
//
// An opportunity past its expiry is marked EXPIRED (plus an 'expire'
// decision so launch-bot still opens its counterfactual position) and
// ErrOpportunityExpired is returned after that change is committed.
func Decide(ctx context.Context, pool *pgxpool.Pool, opportunityID int64, action, category, comment, actor string) (Decision, error) {
	dbAction := "reject"
	newStatus := "REJECTED_MANUAL"
	if action == "approve" {
		dbAction = "approve"
		newStatus = "APPROVED"
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return Decision{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	var status, userID string
	var expired bool
	var decided bool
	err = tx.QueryRow(ctx,
		`SELECT status, user_id, (expires_at IS NOT NULL AND expires_at <= now()),
		        EXISTS (SELECT 1 FROM launch.decisions d WHERE d.opportunity_id = o.id)
		 FROM launch.opportunities o WHERE o.id = $1 FOR UPDATE`, opportunityID).
		Scan(&status, &userID, &expired, &decided)
	if errors.Is(err, pgx.ErrNoRows) {
		return Decision{}, ErrNotFound
	}
	if err != nil {
		return Decision{}, err
	}
	if decided {
		return Decision{}, ErrAlreadyDecided
	}
	if status != "PENDING_APPROVAL" {
		return Decision{}, ErrNotPending
	}

	if expired {
		if _, err := tx.Exec(ctx,
			`UPDATE launch.opportunities SET status = 'EXPIRED', decided_at = now() WHERE id = $1`, opportunityID); err != nil {
			return Decision{}, err
		}
		var did int64
		err := tx.QueryRow(ctx,
			`INSERT INTO launch.decisions (opportunity_id, action, actor, snapshot, user_id)
			 SELECT id, 'expire', 'system:api', market_snapshot, user_id FROM launch.opportunities WHERE id = $1
			 ON CONFLICT (opportunity_id) DO NOTHING RETURNING id`, opportunityID).Scan(&did)
		if err == nil {
			if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, DecisionChannel, strconv.FormatInt(did, 10)); err != nil {
				return Decision{}, err
			}
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return Decision{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return Decision{}, err
		}
		return Decision{}, ErrOpportunityExpired
	}

	var commentArg any
	if comment != "" {
		commentArg = comment
	}
	var id int64
	err = tx.QueryRow(ctx,
		`INSERT INTO launch.decisions (opportunity_id, action, category, comment, actor, snapshot, user_id)
		 SELECT id, $2, $3, $4, $5, market_snapshot, user_id FROM launch.opportunities WHERE id = $1
		 RETURNING id`, opportunityID, dbAction, category, commentArg, actor).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return Decision{}, ErrAlreadyDecided
		}
		return Decision{}, err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE launch.opportunities SET status = $2, decided_at = now() WHERE id = $1`, opportunityID, newStatus); err != nil {
		return Decision{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, DecisionChannel, strconv.FormatInt(id, 10)); err != nil {
		return Decision{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Decision{}, err
	}
	return Decision{DecisionID: id, OpportunityID: opportunityID, Action: dbAction}, nil
}
