package rescue

import "time"

func Classify(repo Repo, cfg RiskConfig, now time.Time) RiskResult {
	createdAt := repo.CreatedAt
	if createdAt.IsZero() {
		createdAt = repo.UpdatedAt
	}

	days := int(now.Sub(createdAt).Hours() / 24)
	switch {
	case days > cfg.HighDays:
		return RiskResult{Level: RiskHigh, AgeDays: days, CreatedAt: createdAt}
	case days > cfg.MediumDays:
		return RiskResult{Level: RiskMedium, AgeDays: days, CreatedAt: createdAt}
	default:
		return RiskResult{Level: RiskSafe, AgeDays: days, CreatedAt: createdAt}
	}
}

func DefaultRiskConfig() RiskConfig {
	return RiskConfig{HighDays: 365, MediumDays: 180}
}
