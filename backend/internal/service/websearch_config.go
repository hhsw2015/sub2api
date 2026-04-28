package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/websearch"
	"golang.org/x/sync/singleflight"
)

// WebSearchEmulationConfig holds the global web search emulation configuration.
type WebSearchEmulationConfig struct {
	Enabled   bool                      `json:"enabled"`
	Providers []WebSearchProviderConfig `json:"providers"`
}

// WebSearchProviderConfig describes a single search provider (Brave or Tavily).
type WebSearchProviderConfig struct {
	Type             string `json:"type"`
	APIKey           string `json:"api_key,omitempty"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	QuotaLimit       *int64 `json:"quota_limit"`
	SubscribedAt     *int64 `json:"subscribed_at,omitempty"`
	QuotaUsed        int64  `json:"quota_used,omitempty"`
	ProxyID          *int64 `json:"proxy_id"`
	ExpiresAt        *int64 `json:"expires_at,omitempty"`
}

// --- Validation ---

const maxWebSearchProviders = 10

var validProviderTypes = map[string]bool{
	websearch.ProviderTypeBrave:  true,
	websearch.ProviderTypeTavily: true,
}

func validateWebSearchConfig(cfg *WebSearchEmulationConfig) error {
	if cfg == nil {
		return nil
	}
	if len(cfg.Providers) > maxWebSearchProviders {
		return fmt.Errorf("too many providers (max %d)", maxWebSearchProviders)
	}
	seen := make(map[string]bool, len(cfg.Providers))
	for i, p := range cfg.Providers {
		if !validProviderTypes[p.Type] {
			return fmt.Errorf("provider[%d]: invalid type %q", i, p.Type)
		}
		if p.QuotaLimit != nil && *p.QuotaLimit < 0 {
			return fmt.Errorf("provider[%d]: quota_limit must be > 0 or null", i)
		}
		if seen[p.Type] {
			return fmt.Errorf("provider[%d]: duplicate type %q", i, p.Type)
		}
		seen[p.Type] = true
	}
	return nil
}

// --- In-process cache ---

const sfKeyWebSearchConfig = "web_search_emulation_config"

type cachedWebSearchEmulationConfig struct {
	config    *WebSearchEmulationConfig
	expiresAt int64
}

var webSearchEmulationCache atomic.Value
var webSearchEmulationSF singleflight.Group

const (
	webSearchEmulationCacheTTL  = 60 * time.Second
	webSearchEmulationErrorTTL  = 5 * time.Second
	webSearchEmulationDBTimeout = 5 * time.Second
)

// GetWebSearchEmulationConfig returns the configuration with in-process cache + singleflight.
func (s *SettingService) GetWebSearchEmulationConfig(ctx context.Context) (*WebSearchEmulationConfig, error) {
	if cached := webSearchEmulationCache.Load(); cached != nil {
		if c, ok := cached.(*cachedWebSearchEmulationConfig); ok && time.Now().UnixNano() < c.expiresAt {
			return c.config, nil
		}
	}
	result, err, _ := webSearchEmulationSF.Do(sfKeyWebSearchConfig, func() (any, error) {
		return s.loadWebSearchConfigFromDB()
	})
	if err != nil {
		return &WebSearchEmulationConfig{}, err
	}
	if cfg, ok := result.(*WebSearchEmulationConfig); ok {
		return cfg, nil
	}
	return &WebSearchEmulationConfig{}, nil
}

func (s *SettingService) loadWebSearchConfigFromDB() (*WebSearchEmulationConfig, error) {
	dbCtx, cancel := context.WithTimeout(context.Background(), webSearchEmulationDBTimeout)
	defer cancel()

	raw, err := s.settingRepo.GetValue(dbCtx, SettingKeyWebSearchEmulationConfig)
	if err != nil {
		webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{
			config:    &WebSearchEmulationConfig{},
			expiresAt: time.Now().Add(webSearchEmulationErrorTTL).UnixNano(),
		})
		return &WebSearchEmulationConfig{}, err
	}
	cfg := parseWebSearchConfigJSON(raw)
	webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{
		config:    cfg,
		expiresAt: time.Now().Add(webSearchEmulationCacheTTL).UnixNano(),
	})
	return cfg, nil
}

func parseWebSearchConfigJSON(raw string) *WebSearchEmulationConfig {
	cfg := &WebSearchEmulationConfig{}
	if raw == "" {
		return cfg
	}
	if err := json.Unmarshal([]byte(raw), cfg); err != nil {
		slog.Warn("websearch: failed to parse config JSON", "error", err)
		return &WebSearchEmulationConfig{}
	}
	return cfg
}

// SaveWebSearchEmulationConfig validates and persists the configuration.
func (s *SettingService) SaveWebSearchEmulationConfig(ctx context.Context, cfg *WebSearchEmulationConfig) error {
	if err := validateWebSearchConfig(cfg); err != nil {
		return infraerrors.BadRequest("INVALID_WEB_SEARCH_CONFIG", err.Error())
	}
	s.mergeExistingAPIKeys(ctx, cfg)

	if cfg.Enabled {
		for _, p := range cfg.Providers {
			if p.APIKey == "" {
				return infraerrors.BadRequest("MISSING_API_KEY",
					fmt.Sprintf("provider %s has no API key configured", p.Type))
			}
		}
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("websearch: marshal config: %w", err)
	}
	if err := s.settingRepo.Set(ctx, SettingKeyWebSearchEmulationConfig, string(data)); err != nil {
		return fmt.Errorf("websearch: save config: %w", err)
	}
	webSearchEmulationSF.Forget(sfKeyWebSearchConfig)
	webSearchEmulationCache.Store(&cachedWebSearchEmulationConfig{
		config:    cfg,
		expiresAt: time.Now().Add(webSearchEmulationCacheTTL).UnixNano(),
	})

	s.rebuildWebSearchManager(ctx)
	return nil
}

func (s *SettingService) mergeExistingAPIKeys(ctx context.Context, cfg *WebSearchEmulationConfig) {
	existing, _ := s.getWebSearchEmulationConfigRaw(ctx)
	if existing == nil || cfg == nil {
		return
	}
	existingByType := make(map[string]string, len(existing.Providers))
	for _, p := range existing.Providers {
		if p.APIKey != "" {
			existingByType[p.Type] = p.APIKey
		}
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].APIKey == "" {
			if key, ok := existingByType[cfg.Providers[i].Type]; ok {
				cfg.Providers[i].APIKey = key
			}
		}
	}
}

func (s *SettingService) getWebSearchEmulationConfigRaw(ctx context.Context) (*WebSearchEmulationConfig, error) {
	raw, err := s.settingRepo.GetValue(ctx, SettingKeyWebSearchEmulationConfig)
	if err != nil {
		return nil, err
	}
	return parseWebSearchConfigJSON(raw), nil
}

// IsWebSearchEmulationEnabled is a quick check for whether the global switch is on.
func (s *SettingService) IsWebSearchEmulationEnabled(ctx context.Context) bool {
	cfg, err := s.GetWebSearchEmulationConfig(ctx)
	if err != nil {
		return false
	}
	return cfg.Enabled && len(cfg.Providers) > 0
}

// SetWebSearchManagerBuilder injects a callback that creates and wires a websearch.Manager.
func (s *SettingService) SetWebSearchManagerBuilder(ctx context.Context, builder WebSearchManagerBuilder) {
	s.webSearchManagerBuilder = builder
	s.rebuildWebSearchManager(ctx)
}

func (s *SettingService) rebuildWebSearchManager(ctx context.Context) {
	if s.webSearchManagerBuilder == nil {
		return
	}
	cfg, err := s.GetWebSearchEmulationConfig(ctx)
	if err != nil {
		SetWebSearchManager(nil)
		return
	}
	proxyURLs := s.resolveProviderProxyURLs(ctx, cfg)
	s.webSearchManagerBuilder(cfg, proxyURLs)
}

func (s *SettingService) resolveProviderProxyURLs(ctx context.Context, cfg *WebSearchEmulationConfig) map[int64]string {
	if cfg == nil || s.proxyRepo == nil {
		return nil
	}
	var ids []int64
	for _, p := range cfg.Providers {
		if p.ProxyID != nil && *p.ProxyID > 0 {
			ids = append(ids, *p.ProxyID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	proxies, err := s.proxyRepo.ListByIDs(ctx, ids)
	if err != nil {
		slog.Warn("websearch: failed to resolve proxy URLs", "error", err)
		return nil
	}
	result := make(map[int64]string, len(proxies))
	for _, px := range proxies {
		result[px.ID] = px.URL()
	}
	return result
}

// --- Global websearch.Manager singleton ---

var (
	globalWebSearchManager atomic.Pointer[websearch.Manager]
	webSearchManagerMu     sync.Mutex
)

const webSearchDefaultMaxResults = 5

// SetWebSearchManager wires the websearch.Manager into the service layer (goroutine-safe).
func SetWebSearchManager(m *websearch.Manager) {
	webSearchManagerMu.Lock()
	defer webSearchManagerMu.Unlock()
	globalWebSearchManager.Store(m)
}

func getWebSearchManager() *websearch.Manager {
	return globalWebSearchManager.Load()
}

// WebSearchTestResult holds the result of a search test.
type WebSearchTestResult struct {
	Provider string                   `json:"provider"`
	Results  []websearch.SearchResult `json:"results"`
	Query    string                   `json:"query"`
}

const testSearchTimeout = 15 * time.Second

// TestWebSearch executes a test search using the currently configured Manager.
func TestWebSearch(ctx context.Context, query string) (*WebSearchTestResult, error) {
	mgr := getWebSearchManager()
	if mgr == nil {
		return nil, fmt.Errorf("web search: manager not initialized, save config first")
	}
	testCtx, cancel := context.WithTimeout(ctx, testSearchTimeout)
	defer cancel()
	resp, providerName, err := mgr.TestSearch(testCtx, websearch.SearchRequest{
		Query:      query,
		MaxResults: webSearchDefaultMaxResults,
	})
	if err != nil {
		return nil, err
	}
	return &WebSearchTestResult{
		Provider: providerName,
		Results:  resp.Results,
		Query:    resp.Query,
	}, nil
}

// PopulateWebSearchUsage returns a copy with quota usage populated from Redis.
func PopulateWebSearchUsage(ctx context.Context, cfg *WebSearchEmulationConfig) *WebSearchEmulationConfig {
	if cfg == nil {
		return nil
	}
	out := *cfg
	out.Providers = make([]WebSearchProviderConfig, len(cfg.Providers))

	mgr := getWebSearchManager()

	for i, p := range cfg.Providers {
		out.Providers[i] = p
		out.Providers[i].APIKeyConfigured = p.APIKey != ""

		if mgr != nil {
			used, _ := mgr.GetUsage(ctx, p.Type)
			out.Providers[i].QuotaUsed = used
		}
	}
	return &out
}

// ResetWebSearchUsage deletes the Redis quota key for the given provider type.
func ResetWebSearchUsage(ctx context.Context, providerType string) error {
	mgr := getWebSearchManager()
	if mgr == nil {
		return fmt.Errorf("web search manager not initialized")
	}
	return mgr.ResetUsage(ctx, providerType)
}

// SanitizeWebSearchConfig returns a copy with api_key fields masked and quota usage populated.
func SanitizeWebSearchConfig(ctx context.Context, cfg *WebSearchEmulationConfig) *WebSearchEmulationConfig {
	if cfg == nil {
		return nil
	}
	out := *cfg
	out.Providers = make([]WebSearchProviderConfig, len(cfg.Providers))

	mgr := getWebSearchManager()

	for i, p := range cfg.Providers {
		out.Providers[i] = p
		out.Providers[i].APIKeyConfigured = p.APIKey != ""
		out.Providers[i].APIKey = ""

		if mgr != nil {
			used, _ := mgr.GetUsage(ctx, p.Type)
			out.Providers[i].QuotaUsed = used
		}
	}
	return &out
}
