package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
)

func TestOpenRequiresPath(t *testing.T) {
	t.Parallel()

	if _, err := Open(""); err == nil {
		t.Fatal("expected error for empty sqlite path")
	}
}

func TestOpenInitializesSchema(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)

	var count int
	for _, table := range []string{"jobs", "runs", "run_events"} {
		if err := st.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
			t.Fatalf("count table %q: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("expected table %q to exist", table)
		}
	}
}

func TestWithinTxCommitAndRollback(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	job := testJob("tx-job")
	if err := st.WithinTx(ctx, func(tx *TxStore) error {
		_, err := tx.Jobs.Create(ctx, job)
		return err
	}); err != nil {
		t.Fatalf("commit tx: %v", err)
	}

	if _, err := st.Jobs.GetByName(ctx, "tx-job"); err != nil {
		t.Fatalf("expected committed job: %v", err)
	}

	if err := st.WithinTx(ctx, func(tx *TxStore) error {
		_, err := tx.Jobs.Create(ctx, testJob("rollback-job"))
		if err != nil {
			return err
		}
		return errors.New("force rollback")
	}); err == nil {
		t.Fatal("expected rollback error")
	}

	if _, err := st.Jobs.GetByName(ctx, "rollback-job"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected rollbacked job to be absent, got: %v", err)
	}
}

func newTestStore(t *testing.T) *Store {
	t.Helper()

	path := filepath.Join(t.TempDir(), "db.sqlite")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})
	return st
}

func testJob(name string) *model.Job {
	now := time.Date(2026, 4, 17, 12, 0, 0, 0, time.UTC)
	return &model.Job{
		Name:              name,
		Enabled:           true,
		SourceType:        model.JobSourceTypeLocal,
		ImageRef:          "jobs/example:latest",
		ScheduleType:      model.ScheduleTypeCron,
		ScheduleExpr:      ptrString("* * * * *"),
		Timezone:          "UTC",
		ConcurrencyPolicy: model.ConcurrencyPolicyForbid,
		TimeoutSec:        30,
		RetryLimit:        1,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func ptrString(v string) *string {
	return &v
}
