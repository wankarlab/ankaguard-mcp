package xsec

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

func newJobsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateJob_AndGetJob(t *testing.T) {
	db := newJobsTestDB(t)

	id, err := CreateJob(db, "sync-book", []string{"-db", "data/trading.db", "-sync-book"}, "user-1")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if id == 0 {
		t.Fatalf("expected non-zero job id")
	}

	j, err := GetJob(db, id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if j.Mode != "sync-book" || j.Status != JobStatusRunning || j.CreatedBy != "user-1" {
		t.Fatalf("unexpected job: %+v", j)
	}
	if len(j.Args) != 3 || j.Args[2] != "-sync-book" {
		t.Fatalf("unexpected args: %v", j.Args)
	}
}

func TestGetJob_NotFound(t *testing.T) {
	db := newJobsTestDB(t)
	if _, err := GetJob(db, 999); err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestFinishJob_UpdatesStatusOutputExitCode(t *testing.T) {
	db := newJobsTestDB(t)
	id, err := CreateJob(db, "rebalance-dry", []string{"-db", "data/trading.db", "-rebalance", "-target-source", "compute"}, "user-1")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := FinishJob(db, id, JobStatusDone, 0, "plan printed"); err != nil {
		t.Fatalf("finish job: %v", err)
	}
	j, err := GetJob(db, id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if j.Status != JobStatusDone {
		t.Fatalf("expected status done, got %s", j.Status)
	}
	if !j.ExitCode.Valid || j.ExitCode.Int64 != 0 {
		t.Fatalf("expected exit code 0, got %+v", j.ExitCode)
	}
	if j.Output != "plan printed" {
		t.Fatalf("expected output to be updated, got %q", j.Output)
	}
	if !j.FinishedAt.Valid {
		t.Fatalf("expected finished_at to be set")
	}
}

func TestUpdateJobOutput_WhileRunning(t *testing.T) {
	db := newJobsTestDB(t)
	id, err := CreateJob(db, "sync-book", []string{"-db", "data/trading.db", "-sync-book"}, "user-1")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	if err := UpdateJobOutput(db, id, "partial output so far"); err != nil {
		t.Fatalf("update output: %v", err)
	}
	j, err := GetJob(db, id)
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	if j.Output != "partial output so far" {
		t.Fatalf("expected partial output, got %q", j.Output)
	}
	if j.Status != JobStatusRunning {
		t.Fatalf("expected status still running, got %s", j.Status)
	}
}

func TestAnyJobRunning(t *testing.T) {
	db := newJobsTestDB(t)

	if _, running, err := AnyJobRunning(db); err != nil || running {
		t.Fatalf("expected no running job, got running=%v err=%v", running, err)
	}

	id, err := CreateJob(db, "sync-book", []string{"-db", "data/trading.db", "-sync-book"}, "user-1")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	j, running, err := AnyJobRunning(db)
	if err != nil {
		t.Fatalf("any job running: %v", err)
	}
	if !running || j.ID != id {
		t.Fatalf("expected running job %d, got running=%v id=%d", id, running, j.ID)
	}

	if err := FinishJob(db, id, JobStatusDone, 0, "done"); err != nil {
		t.Fatalf("finish job: %v", err)
	}
	if _, running, err := AnyJobRunning(db); err != nil || running {
		t.Fatalf("expected no running job after finish, got running=%v err=%v", running, err)
	}
}

func TestListJobs_NewestFirstAndLimit(t *testing.T) {
	db := newJobsTestDB(t)

	var ids []int64
	for i := 0; i < 3; i++ {
		id, err := CreateJob(db, "sync-book", []string{"-db", "data/trading.db", "-sync-book"}, "user-1")
		if err != nil {
			t.Fatalf("create job %d: %v", i, err)
		}
		ids = append(ids, id)
	}

	jobs, err := ListJobs(db, 2)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("expected 2 jobs (limit), got %d", len(jobs))
	}
	if jobs[0].ID != ids[2] || jobs[1].ID != ids[1] {
		t.Fatalf("expected newest-first order [%d, %d], got [%d, %d]", ids[2], ids[1], jobs[0].ID, jobs[1].ID)
	}
}

func TestMarkStaleRunningAsError_RecoversCrashedJobs(t *testing.T) {
	db := newJobsTestDB(t)

	runningID, err := CreateJob(db, "rebalance-live", []string{"-db", "data/trading.db", "-rebalance", "-target-source", "compute", "-live", "-i-verified-futures-sizing"}, "user-1")
	if err != nil {
		t.Fatalf("create running job: %v", err)
	}
	doneID, err := CreateJob(db, "sync-book", []string{"-db", "data/trading.db", "-sync-book"}, "user-1")
	if err != nil {
		t.Fatalf("create job to finish: %v", err)
	}
	if err := FinishJob(db, doneID, JobStatusDone, 0, "ok"); err != nil {
		t.Fatalf("finish job: %v", err)
	}

	n, err := MarkStaleRunningAsError(db)
	if err != nil {
		t.Fatalf("mark stale running: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 recovered row, got %d", n)
	}

	recovered, err := GetJob(db, runningID)
	if err != nil {
		t.Fatalf("get recovered job: %v", err)
	}
	if recovered.Status != JobStatusError {
		t.Fatalf("expected recovered job status error, got %s", recovered.Status)
	}

	untouched, err := GetJob(db, doneID)
	if err != nil {
		t.Fatalf("get done job: %v", err)
	}
	if untouched.Status != JobStatusDone {
		t.Fatalf("expected done job to remain done, got %s", untouched.Status)
	}
}

func TestFindRecentJob_MatchesModeStatusArgsWithinWindow(t *testing.T) {
	db := newJobsTestDB(t)

	dryArgs := []string{"-db", "data/trading.db", "-rebalance", "-target-source", "compute", "-k", "5", "-max-pos-usd", "3"}
	id, err := CreateJob(db, "rebalance-dry", dryArgs, "user-1")
	if err != nil {
		t.Fatalf("create dry job: %v", err)
	}
	if err := FinishJob(db, id, JobStatusDone, 0, "plan"); err != nil {
		t.Fatalf("finish dry job: %v", err)
	}

	// Exact match, generous window: found.
	j, found, err := FindRecentJob(db, "rebalance-dry", JobStatusDone, dryArgs, 10*time.Minute)
	if err != nil {
		t.Fatalf("find recent job: %v", err)
	}
	if !found || j.ID != id {
		t.Fatalf("expected to find dry job %d, found=%v j=%+v", id, found, j)
	}

	// Different overrides: not found.
	otherArgs := []string{"-db", "data/trading.db", "-rebalance", "-target-source", "compute", "-k", "7", "-max-pos-usd", "3"}
	if _, found, err := FindRecentJob(db, "rebalance-dry", JobStatusDone, otherArgs, 10*time.Minute); err != nil {
		t.Fatalf("find recent job (other args): %v", err)
	} else if found {
		t.Fatalf("expected no match for different overrides")
	}

	// Zero window: outside the window, not found.
	if _, found, err := FindRecentJob(db, "rebalance-dry", JobStatusDone, dryArgs, 0); err != nil {
		t.Fatalf("find recent job (zero window): %v", err)
	} else if found {
		t.Fatalf("expected no match with zero window")
	}
}
