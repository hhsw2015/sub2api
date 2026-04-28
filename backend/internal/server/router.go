package server

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	middleware2 "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/server/routes"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/internal/web"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

const frameSrcRefreshTimeout = 5 * time.Second

// SetupRouter 配置路由器中间件和路由
// SetupRouterOptions controls optional behavior for route setup.
type SetupRouterOptions struct {
	// Embedded skips global middleware (Recovery, Logger, CORS, SecurityHeaders,
	// Frontend SPA) when sub2api is embedded inside another server (e.g. CPA)
	// that already provides these.
	Embedded bool
}

func SetupRouter(
	r *gin.Engine,
	handlers *handler.Handlers,
	jwtAuth middleware2.JWTAuthMiddleware,
	adminAuth middleware2.AdminAuthMiddleware,
	apiKeyAuth middleware2.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	cfg *config.Config,
	redisClient *redis.Client,
	opts ...SetupRouterOptions,
) *gin.Engine {
	embedded := len(opts) > 0 && opts[0].Embedded
	// 缓存 iframe 页面的 origin 列表，用于动态注入 CSP frame-src
	var cachedFrameOrigins atomic.Pointer[[]string]
	emptyOrigins := []string{}
	cachedFrameOrigins.Store(&emptyOrigins)

	refreshFrameOrigins := func() {
		ctx, cancel := context.WithTimeout(context.Background(), frameSrcRefreshTimeout)
		defer cancel()
		origins, err := settingService.GetFrameSrcOrigins(ctx)
		if err != nil {
			// 获取失败时保留已有缓存，避免 frame-src 被意外清空
			return
		}
		cachedFrameOrigins.Store(&origins)
	}
	refreshFrameOrigins() // 启动时初始化

	if !embedded {
		// 应用中间件 (skipped in embedded mode -- host server provides these)
		r.Use(middleware2.RequestLogger())
		r.Use(middleware2.Logger())
		r.Use(middleware2.CORS(cfg.CORS))
		r.Use(middleware2.SecurityHeaders(cfg.Security.CSP, func() []string {
			if p := cachedFrameOrigins.Load(); p != nil {
				return *p
			}
			return nil
		}))

		// Serve embedded frontend with settings injection if available
		if web.HasEmbeddedFrontend() {
			frontendServer, err := web.NewFrontendServer(settingService)
			if err != nil {
				log.Printf("Warning: Failed to create frontend server with settings injection: %v, using legacy mode", err)
				r.Use(web.ServeEmbeddedFrontend())
				settingService.SetOnUpdateCallback(refreshFrameOrigins)
			} else {
				settingService.SetOnUpdateCallback(func() {
					frontendServer.InvalidateCache()
					refreshFrameOrigins()
				})
				r.Use(frontendServer.Middleware())
			}
		} else {
			settingService.SetOnUpdateCallback(refreshFrameOrigins)
		}
	} else {
		settingService.SetOnUpdateCallback(refreshFrameOrigins)
	}

	// 注册路由
	registerRoutes(r, handlers, jwtAuth, adminAuth, apiKeyAuth, apiKeyService, subscriptionService, opsService, settingService, cfg, redisClient, embedded)

	return r
}

// registerRoutes 注册所有 HTTP 路由
func registerRoutes(
	r *gin.Engine,
	h *handler.Handlers,
	jwtAuth middleware2.JWTAuthMiddleware,
	adminAuth middleware2.AdminAuthMiddleware,
	apiKeyAuth middleware2.APIKeyAuthMiddleware,
	apiKeyService *service.APIKeyService,
	subscriptionService *service.SubscriptionService,
	opsService *service.OpsService,
	settingService *service.SettingService,
	cfg *config.Config,
	redisClient *redis.Client,
	embedded bool,
) {
	// 通用路由（健康检查、状态等）-- skip in embedded mode (host server provides these)
	if !embedded {
		routes.RegisterCommonRoutes(r)
	}

	// API v1
	v1 := r.Group("/api/v1")

	// 注册各模块路由
	routes.RegisterAuthRoutes(v1, h, jwtAuth, redisClient, settingService)
	routes.RegisterUserRoutes(v1, h, jwtAuth, settingService)
	routes.RegisterAdminRoutes(v1, h, adminAuth)
	routes.RegisterPaymentRoutes(v1, h.Payment, h.PaymentWebhook, h.Admin.Payment, jwtAuth, adminAuth, settingService)
}
