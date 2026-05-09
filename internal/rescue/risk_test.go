package rescue

import (
	"testing"
	"time"
)

func TestClassifyRiskUsesCreatedAtEvenWhenRecentlyPushed(t *testing.T) {
	now := time.Date(2026, 5, 9, 20, 0, 0, 0, time.UTC)
	repo := Repo{
		FullName:  "owner/legacy",
		CreatedAt: now.AddDate(0, 0, -400),
		PushedAt:  ptrTime(now.AddDate(0, 0, -1)),
		UpdatedAt: now.AddDate(0, 0, -1),
	}

	got := Classify(repo, RiskConfig{HighDays: 365, MediumDays: 180}, now)

	if got.Level != RiskHigh {
		t.Fatalf("risk level = %q, want %q", got.Level, RiskHigh)
	}
	if got.AgeDays != 400 {
		t.Fatalf("age days = %d, want 400", got.AgeDays)
	}
}

func TestClassifyRiskUsesCreatedAtForMediumRisk(t *testing.T) {
	now := time.Date(2026, 5, 9, 20, 0, 0, 0, time.UTC)
	repo := Repo{
		FullName:  "owner/docs",
		CreatedAt: now.AddDate(0, 0, -220),
		UpdatedAt: now.AddDate(0, 0, -2),
	}

	got := Classify(repo, RiskConfig{HighDays: 365, MediumDays: 180}, now)

	if got.Level != RiskMedium {
		t.Fatalf("risk level = %q, want %q", got.Level, RiskMedium)
	}
}

func ptrTime(t time.Time) *time.Time {
	return &t
}
