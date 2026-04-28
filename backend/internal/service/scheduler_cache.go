package service

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	SchedulerModeSingle = "single"
	SchedulerModeMixed  = "mixed"
	SchedulerModeForced = "forced"
)

type SchedulerBucket struct {
	GroupID  int64
	Platform string
	Mode     string
}

func (b SchedulerBucket) String() string {
	return fmt.Sprintf("%d:%s:%s", b.GroupID, b.Platform, b.Mode)
}

func ParseSchedulerBucket(raw string) (SchedulerBucket, bool) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return SchedulerBucket{}, false
	}
	groupID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return SchedulerBucket{}, false
	}
	if parts[1] == "" || parts[2] == "" {
		return SchedulerBucket{}, false
	}
	return SchedulerBucket{
		GroupID:  groupID,
		Platform: parts[1],
		Mode:     parts[2],
	}, true
}

// SchedulerCache handles scheduler snapshot and account snapshot caching.
type SchedulerCache interface {
	GetSnapshot(ctx context.Context, bucket SchedulerBucket) ([]*Account, bool, error)
	SetSnapshot(ctx context.Context, bucket SchedulerBucket, accounts []Account) error
	GetAccount(ctx context.Context, accountID int64) (*Account, error)
	SetAccount(ctx context.Context, account *Account) error
	DeleteAccount(ctx context.Context, accountID int64) error
	UpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error
	TryLockBucket(ctx context.Context, bucket SchedulerBucket, ttl time.Duration) (bool, error)
	ListBuckets(ctx context.Context) ([]SchedulerBucket, error)
	GetOutboxWatermark(ctx context.Context) (int64, error)
	SetOutboxWatermark(ctx context.Context, id int64) error
}
