// Package embed provides an entry point for embedding sub2api's commercial layer
// into an external application (e.g. CPA) that manages its own gin.Engine and HTTP server.
package embed

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/Wei-Shaw/sub2api/internal/payment"
	"github.com/Wei-Shaw/sub2api/internal/repository"
	"github.com/Wei-Shaw/sub2api/internal/server"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"

	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
)

// Init initializes the sub2api commercial layer on the given gin engine.
// configDir is the directory containing sub2api's config.yaml.
// Returns a cleanup function that stops all background services.
func Init(engine *gin.Engine, configDir string) (cleanup func(), err error) {
	// Point config loader at the provided directory.
	if err = os.Setenv("DATA_DIR", configDir); err != nil {
		return nil, err
	}

	configConfig, err := config.ProvideConfig()
	if err != nil {
		return nil, err
	}

	// --- Infrastructure ---

	client, err := repository.ProvideEnt(configConfig)
	if err != nil {
		return nil, err
	}

	db, err := repository.ProvideSQLDB(client)
	if err != nil {
		return nil, err
	}

	redisClient := repository.ProvideRedis(configConfig)

	// --- Repositories ---

	userRepository := repository.NewUserRepository(client, db)
	redeemCodeRepository := repository.NewRedeemCodeRepository(client)
	refreshTokenCache := repository.NewRefreshTokenCache(redisClient)
	settingRepository := repository.NewSettingRepository(client)
	groupRepository := repository.NewGroupRepository(client, db)
	proxyRepository := repository.NewProxyRepository(client, db)
	emailCache := repository.NewEmailCache(redisClient)
	promoCodeRepository := repository.NewPromoCodeRepository(client)
	billingCache := repository.NewBillingCache(redisClient)
	userSubscriptionRepository := repository.NewUserSubscriptionRepository(client)
	apiKeyRepository := repository.NewAPIKeyRepository(client, db)
	userRPMCache := repository.NewUserRPMCache(redisClient)
	userGroupRateRepository := repository.NewUserGroupRateRepository(db)
	apiKeyCache := repository.NewAPIKeyCache(redisClient)
	redeemCache := repository.NewRedeemCache(redisClient)
	usageLogRepository := repository.NewUsageLogRepository(client, db)
	announcementRepository := repository.NewAnnouncementRepository(client)
	announcementReadRepository := repository.NewAnnouncementReadRepository(client)
	channelMonitorRepository := repository.NewChannelMonitorRepository(client, db)
	dashboardAggregationRepository := repository.NewDashboardAggregationRepository(db)
	dashboardStatsCache := repository.NewDashboardCache(redisClient, configConfig)
	schedulerCache := repository.ProvideSchedulerCache(redisClient, configConfig)
	accountRepository := repository.NewAccountRepository(client, db, schedulerCache)
	proxyExitInfoProber := repository.NewProxyExitInfoProber(configConfig)
	proxyLatencyCache := repository.NewProxyLatencyCache(redisClient)
	concurrencyCache := repository.ProvideConcurrencyCache(redisClient, configConfig)
	sessionLimitCache := repository.ProvideSessionLimitCache(redisClient, configConfig)
	rpmCache := repository.NewRPMCache(redisClient)
	claudeOAuthClient := repository.NewClaudeOAuthClient()
	openAIOAuthClient := repository.NewOpenAIOAuthClient()
	geminiOAuthClient := repository.NewGeminiOAuthClient(configConfig)
	geminiCliCodeAssistClient := repository.NewGeminiCliCodeAssistClient()
	driveClient := repository.NewGeminiDriveClient()
	tempUnschedCache := repository.NewTempUnschedCache(redisClient)
	timeoutCounterCache := repository.NewTimeoutCounterCache(redisClient)
	openAI403CounterCache := repository.NewOpenAI403CounterCache(redisClient)
	geminiTokenCache := repository.NewGeminiTokenCache(redisClient)
	httpUpstream := repository.NewHTTPUpstream(configConfig)
	claudeUsageFetcher := repository.NewClaudeUsageFetcher(httpUpstream)
	identityCache := repository.NewIdentityCache(redisClient)
	tlsFingerprintProfileRepository := repository.NewTLSFingerprintProfileRepository(client)
	tlsFingerprintProfileCache := repository.NewTLSFingerprintProfileCache(redisClient)
	opsRepository := repository.NewOpsRepository(db)
	updateCache := repository.NewUpdateCache(redisClient)
	gitHubReleaseClient := repository.ProvideGitHubReleaseClient(configConfig)
	idempotencyRepository := repository.NewIdempotencyRepository(client, db)
	usageCleanupRepository := repository.NewUsageCleanupRepository(client, db)
	userAttributeDefinitionRepository := repository.NewUserAttributeDefinitionRepository(client)
	userAttributeValueRepository := repository.NewUserAttributeValueRepository(client)
	errorPassthroughRepository := repository.NewErrorPassthroughRepository(client)
	errorPassthroughCache := repository.NewErrorPassthroughCache(redisClient)
	backupObjectStoreFactory := repository.NewS3BackupStoreFactory()
	dbDumper := repository.NewPgDumper(configConfig)
	pricingRemoteClient := repository.ProvidePricingRemoteClient(configConfig)
	channelRepository := repository.NewChannelRepository(db)
	scheduledTestPlanRepository := repository.NewScheduledTestPlanRepository(db)
	scheduledTestResultRepository := repository.NewScheduledTestResultRepository(db)
	channelMonitorRequestTemplateRepository := repository.NewChannelMonitorRequestTemplateRepository(client, db)
	turnstileVerifier := repository.NewTurnstileVerifier()

	// --- Encryptor ---

	secretEncryptor, err := repository.NewAESEncryptor(configConfig)
	if err != nil {
		return nil, err
	}

	totpCache := repository.NewTotpCache(redisClient)

	// --- Services ---

	settingService := service.ProvideSettingService(settingRepository, groupRepository, proxyRepository, configConfig)
	emailService := service.NewEmailService(settingRepository, emailCache)
	turnstileService := service.NewTurnstileService(settingService, turnstileVerifier)
	emailQueueService := service.ProvideEmailQueueService(emailService)

	billingCacheService := service.ProvideBillingCacheService(billingCache, userRepository, userSubscriptionRepository, apiKeyRepository, userRPMCache, userGroupRateRepository, configConfig)
	apiKeyService := service.NewAPIKeyService(apiKeyRepository, userRepository, groupRepository, userSubscriptionRepository, userGroupRateRepository, apiKeyCache, configConfig)
	apiKeyAuthCacheInvalidator := service.ProvideAPIKeyAuthCacheInvalidator(apiKeyService)
	promoService := service.NewPromoService(promoCodeRepository, userRepository, billingCacheService, client, apiKeyAuthCacheInvalidator)
	subscriptionService := service.NewSubscriptionService(groupRepository, userSubscriptionRepository, billingCacheService, client, configConfig)
	affiliateRepository := repository.NewAffiliateRepository(client, db)
	affiliateService := service.NewAffiliateService(affiliateRepository, settingService, apiKeyAuthCacheInvalidator, billingCacheService)
	authService := service.NewAuthService(client, userRepository, redeemCodeRepository, refreshTokenCache, configConfig, settingService, emailService, turnstileService, emailQueueService, promoService, subscriptionService, affiliateService)
	userService := service.NewUserService(userRepository, settingRepository, apiKeyAuthCacheInvalidator, billingCache)
	redeemService := service.NewRedeemService(redeemCodeRepository, userRepository, subscriptionService, redeemCache, billingCacheService, client, apiKeyAuthCacheInvalidator)
	totpService := service.NewTotpService(userRepository, secretEncryptor, totpCache, settingService, emailService, emailQueueService)
	usageService := service.NewUsageService(usageLogRepository, userRepository, client, apiKeyAuthCacheInvalidator)
	announcementService := service.NewAnnouncementService(announcementRepository, announcementReadRepository, userRepository, userSubscriptionRepository)
	channelMonitorService := service.ProvideChannelMonitorService(channelMonitorRepository, secretEncryptor)
	dashboardService := service.NewDashboardService(usageLogRepository, dashboardAggregationRepository, dashboardStatsCache, configConfig)

	timingWheelService, err := service.ProvideTimingWheelService()
	if err != nil {
		return nil, err
	}

	dashboardAggregationService := service.ProvideDashboardAggregationService(dashboardAggregationRepository, timingWheelService, configConfig)
	privacyClientFactory := repository.CreatePrivacyReqClient
	adminService := service.NewAdminService(userRepository, groupRepository, accountRepository, proxyRepository, apiKeyRepository, redeemCodeRepository, userGroupRateRepository, userRPMCache, billingCacheService, proxyExitInfoProber, proxyLatencyCache, apiKeyAuthCacheInvalidator, client, settingService, subscriptionService, userSubscriptionRepository, privacyClientFactory)
	concurrencyService := service.ProvideConcurrencyService(concurrencyCache, accountRepository, configConfig)
	groupCapacityService := service.NewGroupCapacityService(accountRepository, groupRepository, concurrencyService, sessionLimitCache, rpmCache)
	oAuthService := service.NewOAuthService(proxyRepository, claudeOAuthClient)
	openAIOAuthService := service.NewOpenAIOAuthService(proxyRepository, openAIOAuthClient)
	geminiOAuthService := service.NewGeminiOAuthService(proxyRepository, geminiOAuthClient, geminiCliCodeAssistClient, driveClient, configConfig)
	antigravityOAuthService := service.NewAntigravityOAuthService(proxyRepository)
	geminiQuotaService := service.NewGeminiQuotaService(configConfig, settingRepository)
	compositeTokenCacheInvalidator := service.NewCompositeTokenCacheInvalidator(geminiTokenCache)
	rateLimitService := service.ProvideRateLimitService(accountRepository, usageLogRepository, configConfig, geminiQuotaService, tempUnschedCache, timeoutCounterCache, openAI403CounterCache, settingService, compositeTokenCacheInvalidator)
	antigravityQuotaFetcher := service.NewAntigravityQuotaFetcher(proxyRepository)
	usageCache := service.NewUsageCache()
	tlsFingerprintProfileService := service.NewTLSFingerprintProfileService(tlsFingerprintProfileRepository, tlsFingerprintProfileCache)
	accountUsageService := service.NewAccountUsageService(accountRepository, usageLogRepository, claudeUsageFetcher, geminiQuotaService, antigravityQuotaFetcher, usageCache, identityCache, tlsFingerprintProfileService)
	oAuthRefreshAPI := service.ProvideOAuthRefreshAPI(accountRepository, geminiTokenCache)
	geminiTokenProvider := service.ProvideGeminiTokenProvider(accountRepository, geminiTokenCache, geminiOAuthService, oAuthRefreshAPI)
	accountTestService := service.NewAccountTestService(accountRepository, geminiTokenProvider, httpUpstream, configConfig, tlsFingerprintProfileService)
	crsSyncService := service.NewCRSSyncService(accountRepository, proxyRepository, oAuthService, openAIOAuthService, geminiOAuthService, configConfig)

	pricingService, err := service.ProvidePricingService(configConfig, pricingRemoteClient)
	if err != nil {
		return nil, err
	}

	billingService := service.NewBillingService(configConfig, pricingService)
	channelService := service.NewChannelService(channelRepository, groupRepository, apiKeyAuthCacheInvalidator, pricingService)
	opsSystemLogSink := service.ProvideOpsSystemLogSink(opsRepository)
	opsService := service.NewOpsService(opsRepository, settingRepository, configConfig, accountRepository, userRepository, concurrencyService, opsSystemLogSink)

	encryptionKey, err := payment.ProvideEncryptionKey(configConfig)
	if err != nil {
		return nil, err
	}

	paymentConfigService := service.ProvidePaymentConfigService(client, settingRepository, encryptionKey)
	registry := payment.ProvideRegistry()
	defaultLoadBalancer := payment.ProvideDefaultLoadBalancer(client, encryptionKey)
	paymentService := service.NewPaymentService(client, registry, defaultLoadBalancer, redeemService, subscriptionService, paymentConfigService, userRepository, groupRepository, affiliateService)

	serviceBuildInfo := service.BuildInfo{
		Version:   "embedded",
		BuildType: "embedded",
	}
	updateService := service.ProvideUpdateService(updateCache, gitHubReleaseClient, serviceBuildInfo)
	systemOperationLockService := service.ProvideSystemOperationLockService(idempotencyRepository, configConfig)
	usageCleanupService := service.ProvideUsageCleanupService(usageCleanupRepository, timingWheelService, dashboardAggregationService, configConfig)
	userAttributeService := service.NewUserAttributeService(userAttributeDefinitionRepository, userAttributeValueRepository)
	errorPassthroughService := service.NewErrorPassthroughService(errorPassthroughRepository, errorPassthroughCache)
	scheduledTestService := service.ProvideScheduledTestService(scheduledTestPlanRepository, scheduledTestResultRepository)
	backupService := service.ProvideBackupService(settingRepository, configConfig, secretEncryptor, backupObjectStoreFactory, dbDumper)
	dataManagementService := service.NewDataManagementService()
	channelMonitorRequestTemplateService := service.NewChannelMonitorRequestTemplateService(channelMonitorRequestTemplateRepository)
	usageRecordWorkerPool := service.NewUsageRecordWorkerPool(configConfig)
	idempotencyCoordinator := service.ProvideIdempotencyCoordinator(idempotencyRepository, configConfig)
	idempotencyCleanupService := service.ProvideIdempotencyCleanupService(idempotencyRepository, configConfig)

	// --- Handlers ---

	buildInfo := handler.BuildInfo{
		Version:   "embedded",
		BuildType: "embedded",
	}

	authHandler := handler.NewAuthHandler(configConfig, authService, userService, settingService, promoService, redeemService, totpService)
	userHandler := handler.NewUserHandler(userService, authService, emailService, emailCache, affiliateService)
	apiKeyHandler := handler.NewAPIKeyHandler(apiKeyService)
	usageHandler := handler.NewUsageHandler(usageService, apiKeyService)
	redeemHandler := handler.NewRedeemHandler(redeemService)
	subscriptionHandler := handler.NewSubscriptionHandler(subscriptionService)
	announcementHandler := handler.NewAnnouncementHandler(announcementService)
	channelMonitorUserHandler := handler.NewChannelMonitorUserHandler(channelMonitorService, settingService)
	systemHandler := handler.ProvideSystemHandler(updateService, systemOperationLockService)
	handlerSettingHandler := handler.ProvideSettingHandler(settingService, buildInfo)
	totpHandler := handler.NewTotpHandler(totpService)
	handlerPaymentHandler := handler.NewPaymentHandler(paymentService, paymentConfigService, channelService)
	paymentWebhookHandler := handler.NewPaymentWebhookHandler(paymentService, registry)
	availableChannelHandler := handler.NewAvailableChannelHandler(channelService, apiKeyService, settingService)

	// --- Admin Handlers ---

	dashboardHandler := admin.NewDashboardHandler(dashboardService, dashboardAggregationService)
	adminUserHandler := admin.NewUserHandler(adminService, concurrencyService)
	groupHandler := admin.NewGroupHandler(adminService, dashboardService, groupCapacityService)
	accountHandler := admin.NewAccountHandler(adminService, oAuthService, openAIOAuthService, geminiOAuthService, antigravityOAuthService, rateLimitService, accountUsageService, accountTestService, concurrencyService, crsSyncService, sessionLimitCache, rpmCache, compositeTokenCacheInvalidator)
	adminAnnouncementHandler := admin.NewAnnouncementHandler(announcementService)
	dataManagementHandler := admin.NewDataManagementHandler(dataManagementService)
	backupHandler := admin.NewBackupHandler(backupService, userService)
	oAuthHandler := admin.NewOAuthHandler(oAuthService)
	openAIOAuthHandler := admin.NewOpenAIOAuthHandler(openAIOAuthService, adminService)
	geminiOAuthHandler := admin.NewGeminiOAuthHandler(geminiOAuthService)
	antigravityOAuthHandler := admin.NewAntigravityOAuthHandler(antigravityOAuthService)
	proxyHandler := admin.NewProxyHandler(adminService)
	adminRedeemHandler := admin.NewRedeemHandler(adminService, redeemService)
	promoHandler := admin.NewPromoHandler(promoService)
	settingHandler := admin.NewSettingHandler(settingService, emailService, turnstileService, opsService, paymentConfigService, paymentService)
	opsHandler := admin.NewOpsHandler(opsService)
	adminSubscriptionHandler := admin.NewSubscriptionHandler(subscriptionService)
	adminUsageHandler := admin.NewUsageHandler(usageService, apiKeyService, adminService, usageCleanupService)
	userAttributeHandler := admin.NewUserAttributeHandler(userAttributeService)
	errorPassthroughHandler := admin.NewErrorPassthroughHandler(errorPassthroughService)
	tlsFingerprintProfileHandler := admin.NewTLSFingerprintProfileHandler(tlsFingerprintProfileService)
	adminAPIKeyHandler := admin.NewAdminAPIKeyHandler(adminService)
	scheduledTestHandler := admin.NewScheduledTestHandler(scheduledTestService)
	channelHandler := admin.NewChannelHandler(channelService, billingService)
	channelMonitorHandler := admin.NewChannelMonitorHandler(channelMonitorService)
	channelMonitorRequestTemplateHandler := admin.NewChannelMonitorRequestTemplateHandler(channelMonitorRequestTemplateService)
	paymentHandler := admin.NewPaymentHandler(paymentService, paymentConfigService)
	affiliateHandler := admin.NewAffiliateHandler(affiliateService, adminService)

	adminHandlers := handler.ProvideAdminHandlers(dashboardHandler, adminUserHandler, groupHandler, accountHandler, adminAnnouncementHandler, dataManagementHandler, backupHandler, oAuthHandler, openAIOAuthHandler, geminiOAuthHandler, antigravityOAuthHandler, proxyHandler, adminRedeemHandler, promoHandler, settingHandler, opsHandler, systemHandler, adminSubscriptionHandler, adminUsageHandler, userAttributeHandler, errorPassthroughHandler, tlsFingerprintProfileHandler, adminAPIKeyHandler, scheduledTestHandler, channelHandler, channelMonitorHandler, channelMonitorRequestTemplateHandler, paymentHandler, affiliateHandler)

	handlers := handler.ProvideHandlers(authHandler, userHandler, apiKeyHandler, usageHandler, redeemHandler, subscriptionHandler, announcementHandler, channelMonitorUserHandler, adminHandlers, handlerSettingHandler, totpHandler, handlerPaymentHandler, paymentWebhookHandler, availableChannelHandler, idempotencyCoordinator, idempotencyCleanupService)

	// --- Middleware ---

	jwtAuthMiddleware := middleware.NewJWTAuthMiddleware(authService, userService)
	adminAuthMiddleware := middleware.NewAdminAuthMiddleware(authService, userService, settingService)
	apiKeyAuthMiddleware := middleware.NewAPIKeyAuthMiddleware(apiKeyService, subscriptionService, configConfig)

	// --- Mount routes on the provided engine ---

	server.SetupRouter(engine, handlers, jwtAuthMiddleware, adminAuthMiddleware, apiKeyAuthMiddleware, apiKeyService, subscriptionService, opsService, settingService, configConfig, redisClient)

	// --- Background services ---

	opsMetricsCollector := service.ProvideOpsMetricsCollector(opsRepository, settingRepository, accountRepository, concurrencyService, db, redisClient, configConfig)
	opsAggregationService := service.ProvideOpsAggregationService(opsRepository, settingRepository, db, redisClient, configConfig)
	opsAlertEvaluatorService := service.ProvideOpsAlertEvaluatorService(opsService, opsRepository, emailService, redisClient, configConfig)
	opsCleanupService := service.ProvideOpsCleanupService(opsRepository, db, redisClient, configConfig, channelMonitorService)
	opsScheduledReportService := service.ProvideOpsScheduledReportService(opsService, userService, emailService, redisClient, configConfig)
	tokenRefreshService := service.ProvideTokenRefreshService(accountRepository, oAuthService, openAIOAuthService, geminiOAuthService, antigravityOAuthService, compositeTokenCacheInvalidator, schedulerCache, configConfig, tempUnschedCache, privacyClientFactory, proxyRepository, oAuthRefreshAPI)
	accountExpiryService := service.ProvideAccountExpiryService(accountRepository)
	subscriptionExpiryService := service.ProvideSubscriptionExpiryService(userSubscriptionRepository)
	scheduledTestRunnerService := service.ProvideScheduledTestRunnerService(scheduledTestPlanRepository, scheduledTestService, accountTestService, rateLimitService, configConfig)
	paymentOrderExpiryService := service.ProvidePaymentOrderExpiryService(paymentService)
	channelMonitorRunner := service.ProvideChannelMonitorRunner(channelMonitorService, settingService)

	// --- Build cleanup function ---

	cleanup = func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		type cleanupStep struct {
			name string
			fn   func() error
		}

		parallelSteps := []cleanupStep{
			{"OpsScheduledReportService", func() error {
				if opsScheduledReportService != nil {
					opsScheduledReportService.Stop()
				}
				return nil
			}},
			{"OpsCleanupService", func() error {
				if opsCleanupService != nil {
					opsCleanupService.Stop()
				}
				return nil
			}},
			{"OpsSystemLogSink", func() error {
				if opsSystemLogSink != nil {
					opsSystemLogSink.Stop()
				}
				return nil
			}},
			{"OpsAlertEvaluatorService", func() error {
				if opsAlertEvaluatorService != nil {
					opsAlertEvaluatorService.Stop()
				}
				return nil
			}},
			{"OpsAggregationService", func() error {
				if opsAggregationService != nil {
					opsAggregationService.Stop()
				}
				return nil
			}},
			{"OpsMetricsCollector", func() error {
				if opsMetricsCollector != nil {
					opsMetricsCollector.Stop()
				}
				return nil
			}},
			{"UsageCleanupService", func() error {
				if usageCleanupService != nil {
					usageCleanupService.Stop()
				}
				return nil
			}},
			{"IdempotencyCleanupService", func() error {
				if idempotencyCleanupService != nil {
					idempotencyCleanupService.Stop()
				}
				return nil
			}},
			{"TokenRefreshService", func() error {
				tokenRefreshService.Stop()
				return nil
			}},
			{"AccountExpiryService", func() error {
				accountExpiryService.Stop()
				return nil
			}},
			{"SubscriptionExpiryService", func() error {
				subscriptionExpiryService.Stop()
				return nil
			}},
			{"SubscriptionService", func() error {
				if subscriptionService != nil {
					subscriptionService.Stop()
				}
				return nil
			}},
			{"PricingService", func() error {
				pricingService.Stop()
				return nil
			}},
			{"EmailQueueService", func() error {
				emailQueueService.Stop()
				return nil
			}},
			{"BillingCacheService", func() error {
				billingCacheService.Stop()
				return nil
			}},
			{"UsageRecordWorkerPool", func() error {
				if usageRecordWorkerPool != nil {
					usageRecordWorkerPool.Stop()
				}
				return nil
			}},
			{"OAuthService", func() error {
				oAuthService.Stop()
				return nil
			}},
			{"OpenAIOAuthService", func() error {
				openAIOAuthService.Stop()
				return nil
			}},
			{"GeminiOAuthService", func() error {
				geminiOAuthService.Stop()
				return nil
			}},
			{"AntigravityOAuthService", func() error {
				antigravityOAuthService.Stop()
				return nil
			}},
			{"ScheduledTestRunnerService", func() error {
				if scheduledTestRunnerService != nil {
					scheduledTestRunnerService.Stop()
				}
				return nil
			}},
			{"BackupService", func() error {
				if backupService != nil {
					backupService.Stop()
				}
				return nil
			}},
			{"PaymentOrderExpiryService", func() error {
				if paymentOrderExpiryService != nil {
					paymentOrderExpiryService.Stop()
				}
				return nil
			}},
			{"ChannelMonitorRunner", func() error {
				if channelMonitorRunner != nil {
					channelMonitorRunner.Stop()
				}
				return nil
			}},
		}

		infraSteps := []cleanupStep{
			{"Redis", func() error {
				if redisClient == nil {
					return nil
				}
				return redisClient.Close()
			}},
			{"Ent", func() error {
				if client == nil {
					return nil
				}
				return client.Close()
			}},
		}

		runParallel := func(steps []cleanupStep) {
			var wg sync.WaitGroup
			for i := range steps {
				step := steps[i]
				wg.Add(1)
				go func() {
					defer wg.Done()
					if err := step.fn(); err != nil {
						log.Printf("[sub2api Cleanup] %s failed: %v", step.name, err)
						return
					}
					log.Printf("[sub2api Cleanup] %s succeeded", step.name)
				}()
			}
			wg.Wait()
		}

		runSequential := func(steps []cleanupStep) {
			for i := range steps {
				step := steps[i]
				if err := step.fn(); err != nil {
					log.Printf("[sub2api Cleanup] %s failed: %v", step.name, err)
					continue
				}
				log.Printf("[sub2api Cleanup] %s succeeded", step.name)
			}
		}

		runParallel(parallelSteps)
		runSequential(infraSteps)

		select {
		case <-ctx.Done():
			log.Printf("[sub2api Cleanup] Warning: cleanup timed out after 10 seconds")
		default:
			log.Printf("[sub2api Cleanup] All cleanup steps completed")
		}
	}

	return cleanup, nil
}
