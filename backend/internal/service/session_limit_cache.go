package service

import (
	"context"
	"time"
)

// SessionLimitCache manages account-level active session tracking.
// Used for Anthropic OAuth/SetupToken session count limits.
type SessionLimitCache interface {
	RegisterSession(ctx context.Context, accountID int64, sessionUUID string, maxSessions int, idleTimeout time.Duration) (allowed bool, err error)
	RefreshSession(ctx context.Context, accountID int64, sessionUUID string, idleTimeout time.Duration) error
	GetActiveSessionCount(ctx context.Context, accountID int64) (int, error)
	GetActiveSessionCountBatch(ctx context.Context, accountIDs []int64, idleTimeouts map[int64]time.Duration) (map[int64]int, error)
	IsSessionActive(ctx context.Context, accountID int64, sessionUUID string) (bool, error)
	GetWindowCost(ctx context.Context, accountID int64) (cost float64, hit bool, err error)
	SetWindowCost(ctx context.Context, accountID int64, cost float64) error
	GetWindowCostBatch(ctx context.Context, accountIDs []int64) (map[int64]float64, error)
}
