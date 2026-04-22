package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
)

func TestOpenCreatesParentDirectory(t *testing.T) {
	t.Parallel()

	baseDir := t.TempDir()
	path := filepath.Join(baseDir, "nested", "store.sqlite")

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() {
		_ = st.Close()
	})

	if _, err := os.Stat(filepath.Dir(path)); err != nil {
		t.Fatalf("expected parent directory to exist: %v", err)
	}

	var count int
	if err := st.DB.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'jobs'`).Scan(&count); err != nil {
		t.Fatalf("check schema: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected jobs table to exist, got %d", count)
	}
}

func TestStoreCloseHandlesNil(t *testing.T) {
	t.Parallel()

	var st *Store
	if err := st.Close(); err != nil {
		t.Fatalf("nil close: %v", err)
	}
	if err := (&Store{}).Close(); err != nil {
		t.Fatalf("empty close: %v", err)
	}
}

func TestWithinTxRollsBackOnPanic(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()

	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Fatal("expected panic from transaction callback")
			}
		}()
		_ = st.WithinTx(ctx, func(tx *TxStore) error {
			if _, err := tx.Jobs.Create(ctx, testJob("panic-job")); err != nil {
				return err
			}
			panic("boom")
		})
	}()

	if _, err := st.Jobs.GetByName(ctx, "panic-job"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected rolled back job to be absent, got: %v", err)
	}

	if _, err := st.Jobs.Create(ctx, testJob("after-panic")); err != nil {
		t.Fatalf("store should remain usable after panic rollback: %v", err)
	}
}

func TestTimeHelperEncodings(t *testing.T) {
	t.Parallel()

	ts := time.Date(2026, 4, 17, 21, 34, 56, 123456789, time.FixedZone("KST", 9*60*60))
	encoded := encodeTime(ts)
	if encoded != ts.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("unexpected encoded time: %q", encoded)
	}

	decoded, err := decodeTime(encoded)
	if err != nil {
		t.Fatalf("decode rfc3339 time: %v", err)
	}
	if !decoded.Equal(ts.UTC()) {
		t.Fatalf("unexpected decoded time: %s", decoded)
	}

	legacy, err := decodeTime("2026-04-17 12:34:56")
	if err != nil {
		t.Fatalf("decode legacy time: %v", err)
	}
	if legacy.Year() != 2026 || legacy.Month() != 4 || legacy.Day() != 17 || legacy.Hour() != 12 || legacy.Minute() != 34 || legacy.Second() != 56 {
		t.Fatalf("unexpected legacy decode: %s", legacy)
	}

	if _, err := decodeTime(""); err == nil {
		t.Fatal("expected error for empty time string")
	}
	if _, err := decodeTime("not-a-time"); err == nil {
		t.Fatal("expected error for invalid time string")
	}

	if v := encodeNullableTime(nil); v != nil {
		t.Fatalf("expected nil nullable encoding, got %v", v)
	}
	if v := encodeNullableTime(&ts); v == nil {
		t.Fatal("expected encoded nullable time")
	} else if s, ok := v.(string); !ok || s != encoded {
		t.Fatalf("unexpected nullable encoding: %#v", v)
	}

	if v, err := decodeNullableTime(sql.NullString{}); err != nil || v != nil {
		t.Fatalf("expected nil nullable decode, got %v, %v", v, err)
	}
	if v, err := decodeNullableTime(sql.NullString{String: encoded, Valid: true}); err != nil {
		t.Fatalf("decode nullable time: %v", err)
	} else if v == nil || !v.Equal(ts.UTC()) {
		t.Fatalf("unexpected nullable decode: %v", v)
	}
}

func TestValueHelperEncodings(t *testing.T) {
	t.Parallel()

	if got := boolToInt(true); got != 1 {
		t.Fatalf("expected true to encode as 1, got %d", got)
	}
	if got := boolToInt(false); got != 0 {
		t.Fatalf("expected false to encode as 0, got %d", got)
	}

	if v := nullableString(nil); v != nil {
		t.Fatalf("expected nil string encoding, got %#v", v)
	}
	s := "hello"
	if v := nullableString(&s); v == nil {
		t.Fatal("expected string encoding")
	} else if got, ok := v.(string); !ok || got != s {
		t.Fatalf("unexpected string encoding: %#v", v)
	}

	if v := nullableInt(nil); v != nil {
		t.Fatalf("expected nil int encoding, got %#v", v)
	}
	n := 42
	if v := nullableInt(&n); v == nil {
		t.Fatal("expected int encoding")
	} else if got, ok := v.(int); !ok || got != n {
		t.Fatalf("unexpected int encoding: %#v", v)
	}
}

func TestScanRunEventCorruptTime(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	ctx := context.Background()
	jobID := createTestJobForRun(t, st)
	run := testRun(jobID)
	run.Status = model.RunStatusRunning
	runID, err := st.Runs.Create(ctx, run)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}

	if _, err := st.DB.ExecContext(ctx, `
		INSERT INTO run_events (run_id, event_type, message, created_at)
		VALUES (?, ?, ?, ?)
	`, runID, string(model.RunEventTypeCreated), "bad", "not-a-time"); err != nil {
		t.Fatalf("insert corrupt event: %v", err)
	}

	if _, err := st.Events.ListByRun(ctx, runID); err == nil {
		t.Fatal("expected scan error for corrupt run event")
	}
}
