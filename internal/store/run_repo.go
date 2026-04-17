package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
)

type RunRepo struct {
	db queryer
}

func NewRunRepo(db queryer) *RunRepo {
	return &RunRepo{db: db}
}

func (r *RunRepo) Create(ctx context.Context, run *model.Run) (int64, error) {
	if run == nil {
		return 0, fmt.Errorf("run is nil")
	}
	now := time.Now().UTC()
	if run.CreatedAt.IsZero() {
		run.CreatedAt = now
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = now
	}
	if !run.Status.IsValid() {
		return 0, fmt.Errorf("invalid run status: %q", run.Status)
	}

	res, err := r.db.ExecContext(ctx, `
		INSERT INTO runs (
			job_id, scheduled_at, started_at, finished_at, status, attempt,
			exit_code, error_message, log_path, result_path, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, run.JobID, encodeTime(run.ScheduledAt), encodeNullableTime(run.StartedAt), encodeNullableTime(run.FinishedAt),
		string(run.Status), run.Attempt, nullableInt(run.ExitCode), nullableString(run.ErrorMessage), nullableString(run.LogPath), nullableString(run.ResultPath),
		encodeTime(run.CreatedAt), encodeTime(run.UpdatedAt))
	if err != nil {
		return 0, fmt.Errorf("insert run: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("run last insert id: %w", err)
	}
	run.ID = id
	return id, nil
}

func (r *RunRepo) Get(ctx context.Context, id int64) (*model.Run, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, job_id, scheduled_at, started_at, finished_at, status, attempt,
			exit_code, error_message, log_path, result_path, created_at, updated_at
		FROM runs
		WHERE id = ?
	`, id)
	return scanRun(row)
}

func (r *RunRepo) List(ctx context.Context, filter RunFilter, page Page) ([]model.Run, int64, error) {
	limit, offset := page.normalize()

	var where []string
	var args []any
	if filter.JobID != nil {
		where = append(where, "job_id = ?")
		args = append(args, *filter.JobID)
	}
	if filter.Status != nil && *filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, *filter.Status)
	}
	if filter.From != nil {
		where = append(where, "scheduled_at >= ?")
		args = append(args, encodeTime(*filter.From))
	}
	if filter.To != nil {
		where = append(where, "scheduled_at <= ?")
		args = append(args, encodeTime(*filter.To))
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	var total int64
	countQuery := "SELECT COUNT(*) FROM runs " + whereClause
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count runs: %w", err)
	}

	query := `
		SELECT id, job_id, scheduled_at, started_at, finished_at, status, attempt,
			exit_code, error_message, log_path, result_path, created_at, updated_at
		FROM runs
	`
	if whereClause != "" {
		query += " " + whereClause
	}
	query += " ORDER BY scheduled_at DESC, id DESC LIMIT ? OFFSET ?"

	rows, err := r.db.QueryContext(ctx, query, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	var runs []model.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, 0, err
		}
		runs = append(runs, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate runs: %w", err)
	}
	return runs, total, nil
}

func (r *RunRepo) ListByJob(ctx context.Context, jobID int64, page Page) ([]model.Run, int64, error) {
	return r.List(ctx, RunFilter{JobID: &jobID}, page)
}

func (r *RunRepo) Update(ctx context.Context, run *model.Run) error {
	if run == nil {
		return fmt.Errorf("run is nil")
	}
	if run.ID <= 0 {
		return fmt.Errorf("run id is required")
	}
	if !run.Status.IsValid() {
		return fmt.Errorf("invalid run status: %q", run.Status)
	}
	run.UpdatedAt = time.Now().UTC()

	_, err := r.db.ExecContext(ctx, `
		UPDATE runs SET
			job_id = ?,
			scheduled_at = ?,
			started_at = ?,
			finished_at = ?,
			status = ?,
			attempt = ?,
			exit_code = ?,
			error_message = ?,
			log_path = ?,
			result_path = ?,
			updated_at = ?
		WHERE id = ?
	`, run.JobID, encodeTime(run.ScheduledAt), encodeNullableTime(run.StartedAt), encodeNullableTime(run.FinishedAt),
		string(run.Status), run.Attempt, nullableInt(run.ExitCode), nullableString(run.ErrorMessage), nullableString(run.LogPath), nullableString(run.ResultPath),
		encodeTime(run.UpdatedAt), run.ID)
	if err != nil {
		return fmt.Errorf("update run: %w", err)
	}
	return nil
}

func (r *RunRepo) UpdateStatus(ctx context.Context, id int64, status model.RunStatus, startedAt, finishedAt *time.Time, exitCode *int, errorMessage *string) error {
	if !status.IsValid() {
		return fmt.Errorf("invalid run status: %q", status)
	}
	updatedAt := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE runs SET
			status = ?,
			started_at = ?,
			finished_at = ?,
			exit_code = ?,
			error_message = ?,
			updated_at = ?
		WHERE id = ?
	`, string(status), encodeNullableTime(startedAt), encodeNullableTime(finishedAt), nullableInt(exitCode), nullableString(errorMessage), encodeTime(updatedAt), id)
	if err != nil {
		return fmt.Errorf("update run status: %w", err)
	}
	return nil
}

func (r *RunRepo) UpdateLogArtifacts(ctx context.Context, id int64, logPath, resultPath *string) error {
	updatedAt := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE runs SET
			log_path = ?,
			result_path = ?,
			updated_at = ?
		WHERE id = ?
	`, nullableString(logPath), nullableString(resultPath), encodeTime(updatedAt), id)
	if err != nil {
		return fmt.Errorf("update run artifacts: %w", err)
	}
	return nil
}

func (r *RunRepo) ListPending(ctx context.Context, limit int) ([]model.Run, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, job_id, scheduled_at, started_at, finished_at, status, attempt,
			exit_code, error_message, log_path, result_path, created_at, updated_at
		FROM runs
		WHERE status = ?
		ORDER BY scheduled_at ASC, id ASC
		LIMIT ?
	`, string(model.RunStatusPending), limit)
	if err != nil {
		return nil, fmt.Errorf("list pending runs: %w", err)
	}
	defer rows.Close()

	var runs []model.Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, *run)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending runs: %w", err)
	}
	return runs, nil
}

func scanRun(scanner interface{ Scan(...any) error }) (*model.Run, error) {
	var (
		run                                 model.Run
		status                              string
		scheduledAt                         string
		startedAt, finishedAt, errorMessage sql.NullString
		exitCode                            sql.NullInt64
		logPath, resultPath                 sql.NullString
		createdAt, updatedAt                string
	)

	if err := scanner.Scan(
		&run.ID, &run.JobID, &scheduledAt, &startedAt, &finishedAt, &status, &run.Attempt,
		&exitCode, &errorMessage, &logPath, &resultPath, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}

	run.Status = model.RunStatus(status)
	if t, err := decodeTime(scheduledAt); err != nil {
		return nil, err
	} else {
		run.ScheduledAt = t
	}
	if t, err := decodeNullableTime(startedAt); err != nil {
		return nil, err
	} else {
		run.StartedAt = t
	}
	if t, err := decodeNullableTime(finishedAt); err != nil {
		return nil, err
	} else {
		run.FinishedAt = t
	}
	if exitCode.Valid {
		v := int(exitCode.Int64)
		run.ExitCode = &v
	}
	if errorMessage.Valid {
		run.ErrorMessage = &errorMessage.String
	}
	if logPath.Valid {
		run.LogPath = &logPath.String
	}
	if resultPath.Valid {
		run.ResultPath = &resultPath.String
	}
	if t, err := decodeTime(updatedAt); err != nil {
		return nil, err
	} else {
		run.UpdatedAt = t
	}
	if t, err := decodeTime(createdAt); err != nil {
		return nil, err
	} else {
		run.CreatedAt = t
	}
	return &run, nil
}
