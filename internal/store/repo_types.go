package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/hoonzinope/go-job-runner/internal/model"
)

type queryer interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Page struct {
	Page int
	Size int
}

func (p Page) normalize() (limit int, offset int) {
	if p.Page <= 0 {
		p.Page = 1
	}
	if p.Size <= 0 {
		p.Size = 20
	}
	return p.Size, (p.Page - 1) * p.Size
}

type JobFilter struct {
	Enabled      *bool
	ScheduleType *model.ScheduleType
	Name         string
}

type RunFilter struct {
	JobID  *int64
	Status *model.RunStatus
	From   *time.Time
	To     *time.Time
}
