package xsec

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// xsec_jobs backs the async job-runner for cmd/xsec-live (sync-book / rebalance
// dry-run / rebalance live / cancel-order). A rebalance can take up to ~15
// minutes (maker-first waits up to -maker-timeout per leg), so the REST API
// never runs xsec-live synchronously — it spawns a subprocess and lets the
// caller poll GET /api/v1/xsec/jobs/:id. This file owns only the DB-backed
// job record; process spawning lives in the API handlers layer (this package
// must not depend on os/exec orchestration concerns or import internal/api).

// JobStatus values for xsec_jobs.status.
const (
	JobStatusRunning = "running"
	JobStatusDone    = "done"
	JobStatusError   = "error"
)

// Job is one row of xsec_jobs.
type Job struct {
	ID         int64
	Mode       string
	Args       []string
	Status     string
	Output     string
	ExitCode   sql.NullInt64
	CreatedBy  string
	StartedAt  time.Time
	FinishedAt sql.NullTime
}

// ensureJobsSchema creates the xsec_jobs table if absent (idempotent), same
// pattern as ensureLiveBookSchema / ensureParamsSchema.
func ensureJobsSchema(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS xsec_jobs (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		mode        TEXT NOT NULL,
		args        TEXT NOT NULL,
		status      TEXT NOT NULL,
		output      TEXT NOT NULL DEFAULT '',
		exit_code   INTEGER,
		created_by  TEXT,
		started_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
		finished_at TIMESTAMP
	)`)
	if err != nil {
		return fmt.Errorf("xsec: create xsec_jobs: %w", err)
	}
	return nil
}

// EnsureJobsSchema creates the xsec_jobs table if absent (idempotent).
// Exported so the API layer can guarantee it exists at startup (e.g. before
// running crash-recovery over stale 'running' rows).
func EnsureJobsSchema(db *sql.DB) error {
	return ensureJobsSchema(db)
}

// CreateJob inserts a new job row with status=running and returns its id.
// args is the exact argv the caller will pass to the xsec-live subprocess
// (server-constructed only — see the API layer's mode->argv mapping).
func CreateJob(db *sql.DB, mode string, args []string, createdBy string) (int64, error) {
	if err := ensureJobsSchema(db); err != nil {
		return 0, err
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return 0, fmt.Errorf("xsec: marshal job args: %w", err)
	}
	res, err := db.Exec(`INSERT INTO xsec_jobs (mode, args, status, created_by, started_at)
		VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		mode, string(argsJSON), JobStatusRunning, createdBy)
	if err != nil {
		return 0, fmt.Errorf("xsec: create job: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("xsec: job id: %w", err)
	}
	return id, nil
}

// UpdateJobOutput overwrites the output column of a running job. Called
// periodically (every ~2s) by the runner while the subprocess is alive so a
// concurrent GET can see partial progress.
func UpdateJobOutput(db *sql.DB, id int64, output string) error {
	_, err := db.Exec(`UPDATE xsec_jobs SET output = ? WHERE id = ?`, output, id)
	if err != nil {
		return fmt.Errorf("xsec: update job %d output: %w", id, err)
	}
	return nil
}

// FinishJob marks a job done or errored, with its final output and exit code.
func FinishJob(db *sql.DB, id int64, status string, exitCode int, output string) error {
	_, err := db.Exec(`UPDATE xsec_jobs
		SET status = ?, output = ?, exit_code = ?, finished_at = CURRENT_TIMESTAMP
		WHERE id = ?`, status, output, exitCode, id)
	if err != nil {
		return fmt.Errorf("xsec: finish job %d: %w", id, err)
	}
	return nil
}

// scanJob scans one xsec_jobs row, decoding its JSON args column.
func scanJob(scan func(...interface{}) error) (Job, error) {
	var j Job
	var argsJSON string
	if err := scan(&j.ID, &j.Mode, &argsJSON, &j.Status, &j.Output, &j.ExitCode, &j.CreatedBy, &j.StartedAt, &j.FinishedAt); err != nil {
		return Job{}, err
	}
	if argsJSON != "" {
		if err := json.Unmarshal([]byte(argsJSON), &j.Args); err != nil {
			return Job{}, fmt.Errorf("xsec: unmarshal job %d args: %w", j.ID, err)
		}
	}
	return j, nil
}

// GetJob loads one job by id. Returns sql.ErrNoRows (unwrapped) when absent so
// callers can distinguish "not found" from other errors.
func GetJob(db *sql.DB, id int64) (Job, error) {
	if err := ensureJobsSchema(db); err != nil {
		return Job{}, err
	}
	row := db.QueryRow(`SELECT id, mode, args, status, output, exit_code, created_by, started_at, finished_at
		FROM xsec_jobs WHERE id = ?`, id)
	j, err := scanJob(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return Job{}, sql.ErrNoRows
		}
		return Job{}, fmt.Errorf("xsec: get job %d: %w", id, err)
	}
	return j, nil
}

// ListJobs returns the most recent jobs, newest first, capped at limit.
func ListJobs(db *sql.DB, limit int) ([]Job, error) {
	if err := ensureJobsSchema(db); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := db.Query(`SELECT id, mode, args, status, output, exit_code, created_by, started_at, finished_at
		FROM xsec_jobs ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, fmt.Errorf("xsec: list jobs: %w", err)
	}
	defer rows.Close()

	var out []Job
	for rows.Next() {
		j, err := scanJob(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("xsec: scan job: %w", err)
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

// AnyJobRunning reports whether a job is currently in status=running — the
// DB-backed half of the single-flight guard (the in-process mutex covers the
// race between check and insert within one server instance).
func AnyJobRunning(db *sql.DB) (Job, bool, error) {
	if err := ensureJobsSchema(db); err != nil {
		return Job{}, false, err
	}
	row := db.QueryRow(`SELECT id, mode, args, status, output, exit_code, created_by, started_at, finished_at
		FROM xsec_jobs WHERE status = ? ORDER BY id DESC LIMIT 1`, JobStatusRunning)
	j, err := scanJob(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return Job{}, false, nil
		}
		return Job{}, false, fmt.Errorf("xsec: check running job: %w", err)
	}
	return j, true, nil
}

// MarkStaleRunningAsError finds every job still marked 'running' (left behind
// by a server crash/restart mid-job — the subprocess died with the process)
// and marks it errored. Call once at server startup before accepting new job
// requests. Returns the number of rows fixed.
func MarkStaleRunningAsError(db *sql.DB) (int64, error) {
	if err := ensureJobsSchema(db); err != nil {
		return 0, err
	}
	res, err := db.Exec(`UPDATE xsec_jobs
		SET status = ?, exit_code = -1, finished_at = CURRENT_TIMESTAMP,
		    output = output || CHAR(10) || '[recovered] server restarted while this job was running; marked as error'
		WHERE status = ?`, JobStatusError, JobStatusRunning)
	if err != nil {
		return 0, fmt.Errorf("xsec: recover stale jobs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("xsec: recover stale jobs rows affected: %w", err)
	}
	return n, nil
}

// FindRecentJob returns the most recent job matching mode+status+argsJSON
// (exact JSON-array match) started within the last window, if any. Used by
// the rebalance-live dry-run-first guard: a live run must be preceded by a
// dry run with identical k/max_pos_usd overrides.
//
// The time window is applied in Go (not SQL) rather than binding a time.Time
// parameter against the TEXT column CURRENT_TIMESTAMP writes — the two would
// use different string layouts under the sqlite3 driver, and a lexicographic
// comparison across mismatched layouts is unreliable.
func FindRecentJob(db *sql.DB, mode, status string, args []string, window time.Duration) (Job, bool, error) {
	if err := ensureJobsSchema(db); err != nil {
		return Job{}, false, err
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		return Job{}, false, fmt.Errorf("xsec: marshal match args: %w", err)
	}
	row := db.QueryRow(`SELECT id, mode, args, status, output, exit_code, created_by, started_at, finished_at
		FROM xsec_jobs
		WHERE mode = ? AND status = ? AND args = ?
		ORDER BY id DESC LIMIT 1`, mode, status, string(argsJSON))
	j, err := scanJob(row.Scan)
	if err != nil {
		if err == sql.ErrNoRows {
			return Job{}, false, nil
		}
		return Job{}, false, fmt.Errorf("xsec: find recent job: %w", err)
	}
	if time.Since(j.StartedAt) > window {
		return Job{}, false, nil
	}
	return j, true, nil
}
