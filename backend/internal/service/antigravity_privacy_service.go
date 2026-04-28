package service

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/antigravity"
)

const (
	AntigravityPrivacySet    = "privacy_set"
	AntigravityPrivacyFailed = "privacy_set_failed"
)

// setAntigravityPrivacy calls Antigravity API to set privacy and verify the result.
func setAntigravityPrivacy(ctx context.Context, accessToken, projectID, proxyURL string) string {
	if accessToken == "" {
		return ""
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	client, err := antigravity.NewClient(proxyURL)
	if err != nil {
		slog.Warn("antigravity_privacy_client_error", "error", err.Error())
		return AntigravityPrivacyFailed
	}

	setResp, err := client.SetUserSettings(ctx, accessToken)
	if err != nil {
		slog.Warn("antigravity_privacy_set_failed", "error", err.Error())
		return AntigravityPrivacyFailed
	}
	if !setResp.IsSuccess() {
		slog.Warn("antigravity_privacy_set_response_not_empty",
			"user_settings", setResp.UserSettings,
		)
		return AntigravityPrivacyFailed
	}

	if strings.TrimSpace(projectID) == "" {
		slog.Warn("antigravity_privacy_missing_project_id")
		return AntigravityPrivacyFailed
	}
	userInfo, err := client.FetchUserInfo(ctx, accessToken, projectID)
	if err != nil {
		slog.Warn("antigravity_privacy_verify_failed", "error", err.Error())
		return AntigravityPrivacyFailed
	}
	if !userInfo.IsPrivate() {
		slog.Warn("antigravity_privacy_verify_not_private",
			"user_settings", userInfo.UserSettings,
		)
		return AntigravityPrivacyFailed
	}

	slog.Info("antigravity_privacy_set_success")
	return AntigravityPrivacySet
}

func applyAntigravityPrivacyMode(account *Account, mode string) {
	if account == nil || strings.TrimSpace(mode) == "" {
		return
	}
	extra := make(map[string]any, len(account.Extra)+1)
	for k, v := range account.Extra {
		extra[k] = v
	}
	extra["privacy_mode"] = mode
	account.Extra = extra
}
