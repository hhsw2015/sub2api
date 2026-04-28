package service

// Utility constants and functions previously defined in deleted gateway service files.
// These are still referenced by account_test_service, account_usage_service, billing_service,
// metadata_userid, model_rate_limit, and error_passthrough_runtime.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/Wei-Shaw/sub2api/internal/pkg/logger"
	"github.com/tidwall/gjson"
)

const (
	claudeCodeSystemPrompt = "You are Claude Code, Anthropic's official CLI for Claude."
	chatgptCodexURL        = "https://chatgpt.com/backend-api/codex/responses"
	codexCLIUserAgent      = "codex_cli_rs/0.125.0"
	codexCLIVersion        = "0.125.0"

	antigravityDefaultRateLimitDuration = 30 * time.Second

	googleRPCStatusResourceExhausted      = "RESOURCE_EXHAUSTED"
	googleRPCStatusUnavailable            = "UNAVAILABLE"
	googleRPCTypeRetryInfo                = "type.googleapis.com/google.rpc.RetryInfo"
	googleRPCTypeErrorInfo                = "type.googleapis.com/google.rpc.ErrorInfo"
	googleRPCReasonModelCapacityExhausted = "MODEL_CAPACITY_EXHAUSTED"
	googleRPCReasonRateLimitExceeded      = "RATE_LIMIT_EXCEEDED"
)

var (
	claudeCodeUAVersionPattern = regexp.MustCompile(`(?i)^claude-cli/(\d+\.\d+\.\d+)`)
	sensitiveQueryParamRegex   = regexp.MustCompile(`(?i)([?&](?:key|client_secret|access_token|refresh_token)=)[^&"\s]+`)
)

// --- OpenAI Responses URL helpers ---

func buildOpenAIResponsesURL(base string) string {
	normalized := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(normalized, "/responses") {
		return normalized
	}
	if strings.HasSuffix(normalized, "/v1") {
		return normalized + "/responses"
	}
	return normalized + "/v1/responses"
}

func appendOpenAIResponsesRequestPathSuffix(baseURL, suffix string) string {
	trimmedBase := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	trimmedSuffix := strings.TrimSpace(suffix)
	if trimmedBase == "" || trimmedSuffix == "" {
		return trimmedBase
	}
	return trimmedBase + trimmedSuffix
}

// --- Model classification ---

func isImageGenerationModel(model string) bool {
	modelLower := strings.ToLower(model)
	modelLower = strings.TrimPrefix(modelLower, "models/")
	return modelLower == "gemini-3.1-flash-image" ||
		modelLower == "gemini-3.1-flash-image-preview" ||
		strings.HasPrefix(modelLower, "gemini-3.1-flash-image-") ||
		modelLower == "gemini-3-pro-image" ||
		modelLower == "gemini-3-pro-image-preview" ||
		strings.HasPrefix(modelLower, "gemini-3-pro-image-") ||
		modelLower == "gemini-2.5-flash-image" ||
		modelLower == "gemini-2.5-flash-image-preview" ||
		strings.HasPrefix(modelLower, "gemini-2.5-flash-image-")
}

// --- Upstream error extraction ---

func extractUpstreamErrorMessage(body []byte) string {
	if m := gjson.GetBytes(body, "error.message").String(); strings.TrimSpace(m) != "" {
		inner := strings.TrimSpace(m)
		if strings.HasPrefix(inner, "{") {
			if innerMsg := gjson.Get(inner, "error.message").String(); strings.TrimSpace(innerMsg) != "" {
				return innerMsg
			}
		}
		return m
	}
	if d := gjson.GetBytes(body, "detail").String(); strings.TrimSpace(d) != "" {
		return d
	}
	return gjson.GetBytes(body, "message").String()
}

func ExtractUpstreamErrorMessage(body []byte) string {
	return extractUpstreamErrorMessage(body)
}

func sanitizeUpstreamErrorMessage(msg string) string {
	if msg == "" {
		return msg
	}
	return sensitiveQueryParamRegex.ReplaceAllString(msg, `$1***`)
}

func hashSensitiveValueForLog(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

// --- Semver comparison ---

func CompareVersions(a, b string) int {
	aParts := parseSemver(a)
	bParts := parseSemver(b)
	for i := 0; i < 3; i++ {
		if aParts[i] < bParts[i] {
			return -1
		}
		if aParts[i] > bParts[i] {
			return 1
		}
	}
	return 0
}

func parseSemver(v string) [3]int {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	result := [3]int{0, 0, 0}
	for i := 0; i < len(parts) && i < 3; i++ {
		if parsed, err := strconv.Atoi(parts[i]); err == nil {
			result[i] = parsed
		}
	}
	return result
}

// --- Codex model normalization ---

var codexModelMap = map[string]string{
	"gpt-5.5":                    "gpt-5.5",
	"gpt-5.4":                    "gpt-5.4",
	"gpt-5.4-mini":               "gpt-5.4-mini",
	"gpt-5.4-none":               "gpt-5.4",
	"gpt-5.4-low":                "gpt-5.4",
	"gpt-5.4-medium":             "gpt-5.4",
	"gpt-5.4-high":               "gpt-5.4",
	"gpt-5.4-xhigh":              "gpt-5.4",
	"gpt-5.4-chat-latest":        "gpt-5.4",
	"gpt-5.3":                    "gpt-5.3-codex",
	"gpt-5.3-none":               "gpt-5.3-codex",
	"gpt-5.3-low":                "gpt-5.3-codex",
	"gpt-5.3-medium":             "gpt-5.3-codex",
	"gpt-5.3-high":               "gpt-5.3-codex",
	"gpt-5.3-xhigh":              "gpt-5.3-codex",
	"gpt-5.3-codex":              "gpt-5.3-codex",
	"gpt-5.3-codex-spark":        "gpt-5.3-codex-spark",
	"gpt-5.3-codex-spark-low":    "gpt-5.3-codex-spark",
	"gpt-5.3-codex-spark-medium": "gpt-5.3-codex-spark",
	"gpt-5.3-codex-spark-high":   "gpt-5.3-codex-spark",
	"gpt-5.3-codex-spark-xhigh":  "gpt-5.3-codex-spark",
	"gpt-5.3-codex-low":          "gpt-5.3-codex",
	"gpt-5.3-codex-medium":       "gpt-5.3-codex",
	"gpt-5.3-codex-high":         "gpt-5.3-codex",
	"gpt-5.3-codex-xhigh":        "gpt-5.3-codex",
	"gpt-5.2":                    "gpt-5.2",
	"gpt-5.2-none":               "gpt-5.2",
	"gpt-5.2-low":                "gpt-5.2",
	"gpt-5.2-medium":             "gpt-5.2",
	"gpt-5.2-high":               "gpt-5.2",
	"gpt-5.2-xhigh":              "gpt-5.2",
}

func getNormalizedCodexModel(modelID string) string {
	if modelID == "" {
		return ""
	}
	if mapped, ok := codexModelMap[modelID]; ok {
		return mapped
	}
	lower := strings.ToLower(modelID)
	for key, value := range codexModelMap {
		if strings.ToLower(key) == lower {
			return value
		}
	}
	return ""
}

func normalizeCodexModel(model string) string {
	model = strings.TrimSpace(model)
	if model == "" {
		return "gpt-5.4"
	}
	if isOpenAIImageGenerationModel(model) {
		return model
	}
	modelID := model
	if strings.Contains(modelID, "/") {
		parts := strings.Split(modelID, "/")
		modelID = parts[len(parts)-1]
	}
	if mapped := getNormalizedCodexModel(modelID); mapped != "" {
		return mapped
	}
	normalized := strings.ToLower(modelID)
	if strings.Contains(normalized, "gpt-5.5") {
		return "gpt-5.5"
	}
	if strings.Contains(normalized, "gpt-5.4-mini") {
		return "gpt-5.4-mini"
	}
	if strings.Contains(normalized, "gpt-5.4") {
		return "gpt-5.4"
	}
	if strings.Contains(normalized, "gpt-5.2") {
		return "gpt-5.2"
	}
	if strings.Contains(normalized, "gpt-5.3-codex-spark") {
		return "gpt-5.3-codex-spark"
	}
	if strings.Contains(normalized, "gpt-5.3-codex") || strings.Contains(normalized, "gpt-5.3") {
		return "gpt-5.3-codex"
	}
	if strings.Contains(normalized, "codex") {
		return "gpt-5.3-codex"
	}
	if strings.Contains(normalized, "gpt-5") {
		return "gpt-5.4"
	}
	return "gpt-5.4"
}

func isOpenAIImageGenerationModel(model string) bool {
	return strings.HasPrefix(strings.ToLower(model), "gpt-image-")
}

// --- Codex usage snapshot ---

type OpenAICodexUsageSnapshot struct {
	PrimaryUsedPercent          *float64 `json:"primary_used_percent,omitempty"`
	PrimaryResetAfterSeconds    *int     `json:"primary_reset_after_seconds,omitempty"`
	PrimaryWindowMinutes        *int     `json:"primary_window_minutes,omitempty"`
	SecondaryUsedPercent        *float64 `json:"secondary_used_percent,omitempty"`
	SecondaryResetAfterSeconds  *int     `json:"secondary_reset_after_seconds,omitempty"`
	SecondaryWindowMinutes      *int     `json:"secondary_window_minutes,omitempty"`
	PrimaryOverSecondaryPercent *float64 `json:"primary_over_secondary_percent,omitempty"`
	UpdatedAt                   string   `json:"updated_at,omitempty"`
}

type NormalizedCodexLimits struct {
	Used5hPercent   *float64
	Reset5hSeconds  *int
	Window5hMinutes *int
	Used7dPercent   *float64
	Reset7dSeconds  *int
	Window7dMinutes *int
}

func (s *OpenAICodexUsageSnapshot) Normalize() *NormalizedCodexLimits {
	if s == nil {
		return nil
	}
	result := &NormalizedCodexLimits{}
	primaryMins, secondaryMins := 0, 0
	hasPrimary, hasSecondary := false, false
	if s.PrimaryWindowMinutes != nil {
		primaryMins = *s.PrimaryWindowMinutes
		hasPrimary = true
	}
	if s.SecondaryWindowMinutes != nil {
		secondaryMins = *s.SecondaryWindowMinutes
		hasSecondary = true
	}
	use5hFromPrimary, use7dFromPrimary := false, false
	if hasPrimary && hasSecondary {
		if primaryMins < secondaryMins {
			use5hFromPrimary = true
		} else {
			use7dFromPrimary = true
		}
	} else if hasPrimary {
		if primaryMins <= 360 {
			use5hFromPrimary = true
		} else {
			use7dFromPrimary = true
		}
	} else if hasSecondary {
		if secondaryMins <= 360 {
			use7dFromPrimary = true
		} else {
			use5hFromPrimary = true
		}
	} else {
		use7dFromPrimary = true
	}
	if use5hFromPrimary {
		result.Used5hPercent = s.PrimaryUsedPercent
		result.Reset5hSeconds = s.PrimaryResetAfterSeconds
		result.Window5hMinutes = s.PrimaryWindowMinutes
		result.Used7dPercent = s.SecondaryUsedPercent
		result.Reset7dSeconds = s.SecondaryResetAfterSeconds
		result.Window7dMinutes = s.SecondaryWindowMinutes
	} else if use7dFromPrimary {
		result.Used7dPercent = s.PrimaryUsedPercent
		result.Reset7dSeconds = s.PrimaryResetAfterSeconds
		result.Window7dMinutes = s.PrimaryWindowMinutes
		result.Used5hPercent = s.SecondaryUsedPercent
		result.Reset5hSeconds = s.SecondaryResetAfterSeconds
		result.Window5hMinutes = s.SecondaryWindowMinutes
	}
	return result
}

func ParseCodexRateLimitHeaders(headers http.Header) *OpenAICodexUsageSnapshot {
	snapshot := &OpenAICodexUsageSnapshot{}
	hasData := false
	parseFloat := func(key string) *float64 {
		if v := headers.Get(key); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				return &f
			}
		}
		return nil
	}
	parseInt := func(key string) *int {
		if v := headers.Get(key); v != "" {
			if i, err := strconv.Atoi(v); err == nil {
				return &i
			}
		}
		return nil
	}
	if v := parseFloat("x-codex-primary-used-percent"); v != nil {
		snapshot.PrimaryUsedPercent = v
		hasData = true
	}
	if v := parseInt("x-codex-primary-reset-after-seconds"); v != nil {
		snapshot.PrimaryResetAfterSeconds = v
		hasData = true
	}
	if v := parseInt("x-codex-primary-window-minutes"); v != nil {
		snapshot.PrimaryWindowMinutes = v
		hasData = true
	}
	if v := parseFloat("x-codex-secondary-used-percent"); v != nil {
		snapshot.SecondaryUsedPercent = v
		hasData = true
	}
	if v := parseInt("x-codex-secondary-reset-after-seconds"); v != nil {
		snapshot.SecondaryResetAfterSeconds = v
		hasData = true
	}
	if v := parseInt("x-codex-secondary-window-minutes"); v != nil {
		snapshot.SecondaryWindowMinutes = v
		hasData = true
	}
	if v := parseFloat("x-codex-primary-over-secondary-limit-percent"); v != nil {
		snapshot.PrimaryOverSecondaryPercent = v
		hasData = true
	}
	if !hasData {
		return nil
	}
	snapshot.UpdatedAt = time.Now().Format(time.RFC3339)
	return snapshot
}

func codexSnapshotBaseTime(snapshot *OpenAICodexUsageSnapshot, fallback time.Time) time.Time {
	if snapshot == nil || snapshot.UpdatedAt == "" {
		return fallback
	}
	base, err := time.Parse(time.RFC3339, snapshot.UpdatedAt)
	if err != nil {
		return fallback
	}
	return base
}

func codexResetAtRFC3339(base time.Time, resetAfterSeconds *int) *string {
	if resetAfterSeconds == nil {
		return nil
	}
	sec := *resetAfterSeconds
	if sec < 0 {
		sec = 0
	}
	resetAt := base.Add(time.Duration(sec) * time.Second).Format(time.RFC3339)
	return &resetAt
}

func buildCodexUsageExtraUpdates(snapshot *OpenAICodexUsageSnapshot, fallbackNow time.Time) map[string]any {
	if snapshot == nil {
		return nil
	}
	baseTime := codexSnapshotBaseTime(snapshot, fallbackNow)
	updates := make(map[string]any)
	if snapshot.PrimaryUsedPercent != nil {
		updates["codex_primary_used_percent"] = *snapshot.PrimaryUsedPercent
	}
	if snapshot.PrimaryResetAfterSeconds != nil {
		updates["codex_primary_reset_after_seconds"] = *snapshot.PrimaryResetAfterSeconds
	}
	if snapshot.PrimaryWindowMinutes != nil {
		updates["codex_primary_window_minutes"] = *snapshot.PrimaryWindowMinutes
	}
	if snapshot.SecondaryUsedPercent != nil {
		updates["codex_secondary_used_percent"] = *snapshot.SecondaryUsedPercent
	}
	if snapshot.SecondaryResetAfterSeconds != nil {
		updates["codex_secondary_reset_after_seconds"] = *snapshot.SecondaryResetAfterSeconds
	}
	if snapshot.SecondaryWindowMinutes != nil {
		updates["codex_secondary_window_minutes"] = *snapshot.SecondaryWindowMinutes
	}
	if snapshot.PrimaryOverSecondaryPercent != nil {
		updates["codex_primary_over_secondary_percent"] = *snapshot.PrimaryOverSecondaryPercent
	}
	updates["codex_usage_updated_at"] = baseTime.Format(time.RFC3339)
	if normalized := snapshot.Normalize(); normalized != nil {
		if normalized.Used5hPercent != nil {
			updates["codex_5h_used_percent"] = *normalized.Used5hPercent
		}
		if normalized.Reset5hSeconds != nil {
			updates["codex_5h_reset_after_seconds"] = *normalized.Reset5hSeconds
		}
		if normalized.Window5hMinutes != nil {
			updates["codex_5h_window_minutes"] = *normalized.Window5hMinutes
		}
		if normalized.Used7dPercent != nil {
			updates["codex_7d_used_percent"] = *normalized.Used7dPercent
		}
		if normalized.Reset7dSeconds != nil {
			updates["codex_7d_reset_after_seconds"] = *normalized.Reset7dSeconds
		}
		if normalized.Window7dMinutes != nil {
			updates["codex_7d_window_minutes"] = *normalized.Window7dMinutes
		}
		if reset5hAt := codexResetAtRFC3339(baseTime, normalized.Reset5hSeconds); reset5hAt != nil {
			updates["codex_5h_reset_at"] = *reset5hAt
		}
		if reset7dAt := codexResetAtRFC3339(baseTime, normalized.Reset7dSeconds); reset7dAt != nil {
			updates["codex_7d_reset_at"] = *reset7dAt
		}
	}
	return updates
}

// --- Thinking model suffix ---

func applyThinkingModelSuffix(mappedModel string, thinkingEnabled bool) string {
	if !thinkingEnabled {
		return mappedModel
	}
	if mappedModel == "claude-sonnet-4-5" {
		return "claude-sonnet-4-5-thinking"
	}
	return mappedModel
}

// --- Antigravity model mapping ---

func mapAntigravityModel(account *Account, requestedModel string) string {
	if account == nil {
		return ""
	}
	mapping := account.GetModelMapping()
	if len(mapping) == 0 {
		return ""
	}
	mapped := account.GetMappedModel(requestedModel)
	if mapped != requestedModel {
		return mapped
	}
	if account.IsModelSupported(requestedModel) {
		return requestedModel
	}
	return ""
}

// --- Antigravity smart retry info ---

type antigravitySmartRetryInfo struct {
	RetryDelay               time.Duration
	ModelName                string
	IsModelCapacityExhausted bool
}

func parseAntigravitySmartRetryInfo(body []byte) *antigravitySmartRetryInfo {
	var parsed map[string]any
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil
	}
	errObj, ok := parsed["error"].(map[string]any)
	if !ok {
		return nil
	}
	status, _ := errObj["status"].(string)
	isResourceExhausted := status == googleRPCStatusResourceExhausted
	isUnavailable := status == googleRPCStatusUnavailable
	if !isResourceExhausted && !isUnavailable {
		return nil
	}
	details, ok := errObj["details"].([]any)
	if !ok {
		return nil
	}
	var retryDelay time.Duration
	var modelName string
	var hasRateLimitExceeded bool
	var hasModelCapacityExhausted bool
	for _, d := range details {
		dm, ok := d.(map[string]any)
		if !ok {
			continue
		}
		atType, _ := dm["@type"].(string)
		if atType == googleRPCTypeErrorInfo {
			if meta, ok := dm["metadata"].(map[string]any); ok {
				if model, ok := meta["model"].(string); ok {
					modelName = model
				}
			}
			if reason, ok := dm["reason"].(string); ok {
				if reason == googleRPCReasonModelCapacityExhausted {
					hasModelCapacityExhausted = true
				}
				if reason == googleRPCReasonRateLimitExceeded {
					hasRateLimitExceeded = true
				}
			}
			continue
		}
		if atType == googleRPCTypeRetryInfo {
			delay, ok := dm["retryDelay"].(string)
			if !ok || delay == "" {
				continue
			}
			dur, err := time.ParseDuration(delay)
			if err != nil {
				logger.LegacyPrintf("service.antigravity_gateway", "[Antigravity] failed to parse retryDelay: %s error=%v", delay, err)
				continue
			}
			retryDelay = dur
		}
	}
	if isResourceExhausted && !hasRateLimitExceeded {
		return nil
	}
	if isUnavailable && !hasModelCapacityExhausted {
		return nil
	}
	if modelName == "" {
		return nil
	}
	if retryDelay <= 0 {
		retryDelay = antigravityDefaultRateLimitDuration
	}
	return &antigravitySmartRetryInfo{
		RetryDelay:               retryDelay,
		ModelName:                modelName,
		IsModelCapacityExhausted: hasModelCapacityExhausted,
	}
}

// --- Logging helpers ---

func truncateForLog(b []byte, maxBytes int) string {
	if maxBytes <= 0 {
		maxBytes = 2048
	}
	if len(b) > maxBytes {
		b = b[:maxBytes]
	}
	s := string(b)
	s = strings.ReplaceAll(s, "\n", "\\n")
	s = strings.ReplaceAll(s, "\r", "\\r")
	return s
}

func extractUpstreamErrorCode(body []byte) string {
	if code := strings.TrimSpace(gjson.GetBytes(body, "error.code").String()); code != "" {
		return code
	}
	inner := strings.TrimSpace(gjson.GetBytes(body, "error.message").String())
	if !strings.HasPrefix(inner, "{") {
		return ""
	}
	if code := strings.TrimSpace(gjson.Get(inner, "error.code").String()); code != "" {
		return code
	}
	if lastBrace := strings.LastIndex(inner, "}"); lastBrace >= 0 {
		if code := strings.TrimSpace(gjson.Get(inner[:lastBrace+1], "error.code").String()); code != "" {
			return code
		}
	}
	return ""
}

// --- Gemini rate limit reset ---

var retryInRegex = regexp.MustCompile(`(?i)retry (?:in|after)\s+(\d+(?:\.\d+)?)s`)

func ParseGeminiRateLimitResetTime(body []byte) *int64 {
	errMsg := gjson.GetBytes(body, "error.message").String()
	if looksLikeGeminiDailyQuota(errMsg) {
		if ts := nextGeminiDailyResetUnix(); ts != nil {
			return ts
		}
	}
	var found *int64
	gjson.GetBytes(body, "error.details").ForEach(func(_, detail gjson.Result) bool {
		v := detail.Get("metadata.quotaResetDelay").String()
		if v == "" {
			return true
		}
		if dur, err := time.ParseDuration(v); err == nil {
			ts := time.Now().Unix() + int64(math.Ceil(dur.Seconds()))
			found = &ts
			return false
		}
		return true
	})
	if found != nil {
		return found
	}
	matches := retryInRegex.FindStringSubmatch(string(body))
	if len(matches) == 2 {
		if dur, err := time.ParseDuration(matches[1] + "s"); err == nil {
			ts := time.Now().Unix() + int64(math.Ceil(dur.Seconds()))
			return &ts
		}
	}
	return nil
}

func looksLikeGeminiDailyQuota(message string) bool {
	m := strings.ToLower(message)
	return strings.Contains(m, "per day") || strings.Contains(m, "requests per day") || (strings.Contains(m, "quota") && strings.Contains(m, "per day"))
}

func nextGeminiDailyResetUnix() *int64 {
	reset := geminiDailyResetTime(time.Now())
	ts := reset.Unix()
	return &ts
}

// --- User group rate cache metrics ---

const defaultUserGroupRateCacheTTL = 30 * time.Second

var (
	userGroupRateCacheHitTotal      atomic.Int64
	userGroupRateCacheMissTotal     atomic.Int64
	userGroupRateCacheLoadTotal     atomic.Int64
	userGroupRateCacheSFSharedTotal atomic.Int64
	userGroupRateCacheFallbackTotal atomic.Int64
)

// --- OpenAI Advanced Scheduler setting ---

const (
	openAIAdvancedSchedulerSettingKey      = "openai_advanced_scheduler_enabled"
	openAIAdvancedSchedulerSettingCacheTTL = 5 * time.Second
)

type cachedOpenAIAdvancedSchedulerSetting struct {
	enabled   bool
	expiresAt int64
}

var (
	openAIAdvancedSchedulerSettingCache atomic.Value
	openAIAdvancedSchedulerSettingSF    singleflight.Group
)

