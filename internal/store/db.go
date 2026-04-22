package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	DB     *sql.DB
	Jobs   *JobRepo
	Runs   *RunRepo
	Events *EventRepo
}

type TxStore struct {
	Tx     *sql.Tx
	Jobs   *JobRepo
	Runs   *RunRepo
	Events *EventRepo
}

func Open(path string) (*Store, error) {
	if path == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}

	rawPath := strings.TrimPrefix(path, "file:")
	if dir := filepath.Dir(rawPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create sqlite directory: %w", err)
		}
	}

	dsn := path
	if !strings.HasPrefix(dsn, "file:") {
		dsn = "file:" + dsn
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite db: %w", err)
	}

	// Keep a single connection so connection-scoped PRAGMAs are consistently applied.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if err := initialize(db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &Store{
		DB:     db,
		Jobs:   NewJobRepo(db),
		Runs:   NewRunRepo(db),
		Events: NewEventRepo(db),
	}, nil
}

func (s *Store) Close() error {
	if s == nil || s.DB == nil {
		return nil
	}
	return s.DB.Close()
}

func (s *Store) WithinTx(ctx context.Context, fn func(*TxStore) error) (err error) {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	txStore := &TxStore{
		Tx:     tx,
		Jobs:   NewJobRepo(tx),
		Runs:   NewRunRepo(tx),
		Events: NewEventRepo(tx),
	}

	err = fn(txStore)
	if err != nil {
		return err
	}
	err = tx.Commit()
	return err
}

func initialize(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA foreign_keys=ON;",
		"PRAGMA busy_timeout=5000;",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("apply sqlite pragma %q: %w", pragma, err)
		}
	}

	schema := []string{
		`CREATE TABLE IF NOT EXISTS jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL UNIQUE,
			description TEXT,
			enabled INTEGER NOT NULL DEFAULT 1,
			source_type TEXT NOT NULL,
			image_ref TEXT NOT NULL,
			image_digest TEXT,
			schedule_type TEXT NOT NULL,
			schedule_expr TEXT,
			interval_sec INTEGER,
			timezone TEXT NOT NULL DEFAULT 'UTC',
			concurrency_policy TEXT NOT NULL DEFAULT 'forbid',
			timeout_sec INTEGER NOT NULL DEFAULT 0,
			retry_limit INTEGER NOT NULL DEFAULT 0,
			next_run_at TEXT,
			last_scheduled_at TEXT,
			params_json TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS runs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			job_id INTEGER NOT NULL,
			scheduled_at TEXT NOT NULL,
			started_at TEXT,
			finished_at TEXT,
			status TEXT NOT NULL,
			attempt INTEGER NOT NULL DEFAULT 0,
			exit_code INTEGER,
			error_message TEXT,
			log_path TEXT,
			result_path TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			FOREIGN KEY(job_id) REFERENCES jobs(id) ON DELETE CASCADE,
			UNIQUE(job_id, scheduled_at, attempt)
		);`,
		`CREATE TABLE IF NOT EXISTS run_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id INTEGER NOT NULL,
			event_type TEXT NOT NULL,
			message TEXT,
			created_at TEXT NOT NULL,
			FOREIGN KEY(run_id) REFERENCES runs(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_next_run ON jobs(next_run_at);`,
		`CREATE INDEX IF NOT EXISTS idx_runs_status ON runs(status);`,
		`CREATE INDEX IF NOT EXISTS idx_runs_pending ON runs(status, scheduled_at);`,
		`CREATE INDEX IF NOT EXISTS idx_runs_job ON runs(job_id);`,
		`CREATE INDEX IF NOT EXISTS idx_events_run ON run_events(run_id);`,
	}

	for _, stmt := range schema {
		if _, err := db.Exec(stmt); err != nil {
			return fmt.Errorf("initialize schema: %w", err)
		}
	}

	return nil
}

func encodeTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func encodeNullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	value := encodeTime(*t)
	return value
}

func decodeTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, fmt.Errorf("empty time value")
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC(), nil
	}
	if t, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
		return t.UTC(), nil
	}
	return time.Time{}, fmt.Errorf("parse time %q", s)
}

func decodeNullableTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid || s.String == "" {
		return nil, nil
	}
	t, err := decodeTime(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}
