package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
)

type JobRepo struct {
	db queryer
}

func NewJobRepo(db queryer) *JobRepo {
	return &JobRepo{db: db}
}

func (r *JobRepo) Create(ctx context.Context, job *model.Job) (int64, error) {
	if job == nil {
		return 0, fmt.Errorf("job is nil")
	}
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	if job.Timezone == "" {
		job.Timezone = "UTC"
	}
	if !job.SourceType.IsValid() {
		return 0, fmt.Errorf("invalid job source type: %q", job.SourceType)
	}
	if !job.ScheduleType.IsValid() {
		return 0, fmt.Errorf("invalid schedule type: %q", job.ScheduleType)
	}
	if !job.ConcurrencyPolicy.IsValid() {
		return 0, fmt.Errorf("invalid concurrency policy: %q", job.ConcurrencyPolicy)
	}

	res, err := r.db.ExecContext(ctx, `
		INSERT INTO jobs (
			name, description, enabled, source_type, image_ref, image_digest,
			schedule_type, schedule_expr, interval_sec, timezone,
			concurrency_policy, timeout_sec, retry_limit, next_run_at,
			last_scheduled_at, params_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, job.Name, nullableString(job.Description), boolToInt(job.Enabled), string(job.SourceType), job.ImageRef, nullableString(job.ImageDigest),
		string(job.ScheduleType), nullableString(job.ScheduleExpr), nullableInt(job.IntervalSec), job.Timezone,
		string(job.ConcurrencyPolicy), job.TimeoutSec, job.RetryLimit, encodeNullableTime(job.NextRunAt),
		encodeNullableTime(job.LastScheduledAt), nullableString(job.ParamsJSON), encodeTime(job.CreatedAt), encodeTime(job.UpdatedAt))
	if err != nil {
		return 0, fmt.Errorf("insert job: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("job last insert id: %w", err)
	}
	job.ID = id
	return id, nil
}

func (r *JobRepo) Get(ctx context.Context, id int64) (*model.Job, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, description, enabled, source_type, image_ref, image_digest,
			schedule_type, schedule_expr, interval_sec, timezone,
			concurrency_policy, timeout_sec, retry_limit, next_run_at,
			last_scheduled_at, params_json, created_at, updated_at
		FROM jobs
		WHERE id = ?
	`, id)
	return scanJob(row)
}

func (r *JobRepo) GetByName(ctx context.Context, name string) (*model.Job, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, name, description, enabled, source_type, image_ref, image_digest,
			schedule_type, schedule_expr, interval_sec, timezone,
			concurrency_policy, timeout_sec, retry_limit, next_run_at,
			last_scheduled_at, params_json, created_at, updated_at
		FROM jobs
		WHERE name = ?
	`, name)
	return scanJob(row)
}

func (r *JobRepo) List(ctx context.Context, filter JobFilter, page Page) ([]model.Job, int64, error) {
	limit, offset := page.normalize()

	var where []string
	var args []any
	if filter.Enabled != nil {
		where = append(where, "enabled = ?")
		args = append(args, boolToInt(*filter.Enabled))
	}
	if filter.ScheduleType != nil && *filter.ScheduleType != "" {
		where = append(where, "schedule_type = ?")
		args = append(args, string(*filter.ScheduleType))
	}
	if filter.Name != "" {
		where = append(where, "name LIKE ?")
		args = append(args, "%"+filter.Name+"%")
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	var total int64
	countQuery := "SELECT COUNT(*) FROM jobs " + whereClause
	if err := r.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count jobs: %w", err)
	}

	query := `
		SELECT id, name, description, enabled, source_type, image_ref, image_digest,
			schedule_type, schedule_expr, interval_sec, timezone,
			concurrency_policy, timeout_sec, retry_limit, next_run_at,
			last_scheduled_at, params_json, created_at, updated_at
		FROM jobs
	`
	if whereClause != "" {
		query += " " + whereClause
	}
	query += " ORDER BY id DESC LIMIT ? OFFSET ?"

	rows, err := r.db.QueryContext(ctx, query, append(args, limit, offset)...)
	if err != nil {
		return nil, 0, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var jobs []model.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, 0, err
		}
		jobs = append(jobs, *job)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate jobs: %w", err)
	}
	return jobs, total, nil
}

func (r *JobRepo) Update(ctx context.Context, job *model.Job) error {
	if job == nil {
		return fmt.Errorf("job is nil")
	}
	if job.ID <= 0 {
		return fmt.Errorf("job id is required")
	}
	if job.Timezone == "" {
		job.Timezone = "UTC"
	}
	if !job.SourceType.IsValid() {
		return fmt.Errorf("invalid job source type: %q", job.SourceType)
	}
	if !job.ScheduleType.IsValid() {
		return fmt.Errorf("invalid schedule type: %q", job.ScheduleType)
	}
	if !job.ConcurrencyPolicy.IsValid() {
		return fmt.Errorf("invalid concurrency policy: %q", job.ConcurrencyPolicy)
	}
	job.UpdatedAt = time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE jobs SET
			name = ?,
			description = ?,
			enabled = ?,
			source_type = ?,
			image_ref = ?,
			image_digest = ?,
			schedule_type = ?,
			schedule_expr = ?,
			interval_sec = ?,
			timezone = ?,
			concurrency_policy = ?,
			timeout_sec = ?,
			retry_limit = ?,
			next_run_at = ?,
			last_scheduled_at = ?,
			params_json = ?,
			updated_at = ?
		WHERE id = ?
	`, job.Name, nullableString(job.Description), boolToInt(job.Enabled), string(job.SourceType), job.ImageRef, nullableString(job.ImageDigest),
		string(job.ScheduleType), nullableString(job.ScheduleExpr), nullableInt(job.IntervalSec), job.Timezone, string(job.ConcurrencyPolicy),
		job.TimeoutSec, job.RetryLimit, encodeNullableTime(job.NextRunAt), encodeNullableTime(job.LastScheduledAt),
		nullableString(job.ParamsJSON), encodeTime(job.UpdatedAt), job.ID)
	if err != nil {
		return fmt.Errorf("update job: %w", err)
	}
	return nil
}

func (r *JobRepo) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM jobs WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete job: %w", err)
	}
	return nil
}

func (r *JobRepo) UpdateScheduling(ctx context.Context, id int64, nextRunAt *time.Time, lastScheduledAt *time.Time) error {
	updatedAt := time.Now().UTC()
	_, err := r.db.ExecContext(ctx, `
		UPDATE jobs
		SET next_run_at = ?, last_scheduled_at = ?, updated_at = ?
		WHERE id = ?
	`, encodeNullableTime(nextRunAt), encodeNullableTime(lastScheduledAt), encodeTime(updatedAt), id)
	if err != nil {
		return fmt.Errorf("update job scheduling: %w", err)
	}
	return nil
}

func (r *JobRepo) ListDue(ctx context.Context, before time.Time) ([]model.Job, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, name, description, enabled, source_type, image_ref, image_digest,
			schedule_type, schedule_expr, interval_sec, timezone,
			concurrency_policy, timeout_sec, retry_limit, next_run_at,
			last_scheduled_at, params_json, created_at, updated_at
		FROM jobs
		WHERE enabled = 1 AND next_run_at IS NOT NULL AND next_run_at <= ?
		ORDER BY next_run_at ASC, id ASC
	`, encodeTime(before))
	if err != nil {
		return nil, fmt.Errorf("list due jobs: %w", err)
	}
	defer rows.Close()

	var jobs []model.Job
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, *job)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate due jobs: %w", err)
	}
	return jobs, nil
}

func scanJob(scanner interface{ Scan(...any) error }) (*model.Job, error) {
	var (
		job                                     model.Job
		enabled                                 int
		sourceType, scheduleType, concurrency    string
		description, imageDigest                 sql.NullString
		scheduleExpr, nextRunAt, lastScheduledAt sql.NullString
		intervalSec                             sql.NullInt64
		paramsJSON                              sql.NullString
		createdAt, updatedAt                     string
	)

	if err := scanner.Scan(
		&job.ID, &job.Name, &description, &enabled, &sourceType, &job.ImageRef, &imageDigest,
		&scheduleType, &scheduleExpr, &intervalSec, &job.Timezone,
		&concurrency, &job.TimeoutSec, &job.RetryLimit, &nextRunAt,
		&lastScheduledAt, &paramsJSON, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}

	job.Enabled = enabled != 0
	job.SourceType = model.JobSourceType(sourceType)
	job.ScheduleType = model.ScheduleType(scheduleType)
	job.ConcurrencyPolicy = model.ConcurrencyPolicy(concurrency)

	if description.Valid {
		job.Description = &description.String
	}
	if imageDigest.Valid {
		job.ImageDigest = &imageDigest.String
	}
	if scheduleExpr.Valid {
		job.ScheduleExpr = &scheduleExpr.String
	}
	if intervalSec.Valid {
		v := int(intervalSec.Int64)
		job.IntervalSec = &v
	}
	if t, err := decodeNullableTime(nextRunAt); err != nil {
		return nil, err
	} else {
		job.NextRunAt = t
	}
	if t, err := decodeNullableTime(lastScheduledAt); err != nil {
		return nil, err
	} else {
		job.LastScheduledAt = t
	}
	if paramsJSON.Valid {
		job.ParamsJSON = &paramsJSON.String
	}
	if t, err := decodeTime(createdAt); err != nil {
		return nil, err
	} else {
		job.CreatedAt = t
	}
	if t, err := decodeTime(updatedAt); err != nil {
		return nil, err
	} else {
		job.UpdatedAt = t
	}

	return &job, nil
}

func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func nullableString(v *string) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}
