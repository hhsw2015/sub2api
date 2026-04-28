package service

import (
	"context"
	"time"
)

type SchedulerOutboxEvent struct {
	ID        int64
	EventType string
	AccountID *int64
	GroupID   *int64
	Payload   map[string]any
	CreatedAt time.Time
}

// SchedulerOutboxRepository provides read access to the scheduler outbox.
type SchedulerOutboxRepository interface {
	ListAfter(ctx context.Context, afterID int64, limit int) ([]SchedulerOutboxEvent, error)
	MaxID(ctx context.Context) (int64, error)
}
