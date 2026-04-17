package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
)

type EventRepo struct {
	db queryer
}

func NewEventRepo(db queryer) *EventRepo {
	return &EventRepo{db: db}
}

func (r *EventRepo) Create(ctx context.Context, event *model.RunEvent) (int64, error) {
	if event == nil {
		return 0, fmt.Errorf("event is nil")
	}
	if !event.EventType.IsValid() {
		return 0, fmt.Errorf("invalid run event type: %q", event.EventType)
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}

	res, err := r.db.ExecContext(ctx, `
		INSERT INTO run_events (run_id, event_type, message, created_at)
		VALUES (?, ?, ?, ?)
	`, event.RunID, string(event.EventType), nullableString(event.Message), encodeTime(event.CreatedAt))
	if err != nil {
		return 0, fmt.Errorf("insert run event: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("run event last insert id: %w", err)
	}
	event.ID = id
	return id, nil
}

func (r *EventRepo) ListByRun(ctx context.Context, runID int64) ([]model.RunEvent, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, run_id, event_type, message, created_at
		FROM run_events
		WHERE run_id = ?
		ORDER BY created_at ASC, id ASC
	`, runID)
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	defer rows.Close()

	var events []model.RunEvent
	for rows.Next() {
		event, err := scanRunEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, *event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate run events: %w", err)
	}
	return events, nil
}

func scanRunEvent(scanner interface{ Scan(...any) error }) (*model.RunEvent, error) {
	var (
		event     model.RunEvent
		eventType string
		message   sql.NullString
		createdAt string
	)

	if err := scanner.Scan(&event.ID, &event.RunID, &eventType, &message, &createdAt); err != nil {
		return nil, err
	}
	event.EventType = model.RunEventType(eventType)
	if message.Valid {
		event.Message = &message.String
	}
	t, err := decodeTime(createdAt)
	if err != nil {
		return nil, err
	}
	event.CreatedAt = t
	return &event, nil
}
