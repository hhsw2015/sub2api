package service

import "time"

const (
	// creditsExhaustedKey is the model_rate_limits key marking credits exhausted.
	creditsExhaustedKey      = "AICredits"
	creditsExhaustedDuration = 5 * time.Hour
)

// isCreditsExhausted checks if the account's AICredits rate limit key is active.
func (a *Account) isCreditsExhausted() bool {
	if a == nil {
		return false
	}
	return a.isRateLimitActiveForKey(creditsExhaustedKey)
}
