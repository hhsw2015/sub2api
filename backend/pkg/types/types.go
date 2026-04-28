// Package types exposes key types for external integration.
package types

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"go.uber.org/zap"
)

// Re-export types needed by CPA commercial layer
type BillingService = service.BillingService
type BillingCacheService = service.BillingCacheService
type UsageTokens = service.UsageTokens
type AuthSubject = middleware.AuthSubject

var GetAuthSubjectFromContext = middleware.GetAuthSubjectFromContext

func GetLogger() *zap.Logger {
	return logger.L()
}
