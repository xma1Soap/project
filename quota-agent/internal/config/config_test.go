package config

import (
	"strings"
	"testing"
)

func TestConfigModesAndPoolSchedules(t *testing.T) {
	cfg := Config{
		DryRun: true,
		NewAPI: NewAPIConfig{UserID: 1},
		Channels: []ChannelPolicy{
			{
				ChannelID: 1, Model: "model-a", RequiredTag: "managed",
				RoutePool: "chat", QuotaPool: "account-a", QuotaMode: QuotaEstimate,
				Reset: ResetPolicy{Mode: "after_days", AfterDays: 2},
			},
			{
				ChannelID: 2, Model: "model-a", RequiredTag: "managed",
				RoutePool: "chat", QuotaPool: "account-a", QuotaMode: QuotaEstimate,
				Reset: ResetPolicy{Mode: "after_days", AfterDays: 2},
			},
			{
				ChannelID: 3, Model: "model-a", RequiredTag: "managed",
				RoutePool: "chat", QuotaPool: "known", QuotaMode: QuotaKnown,
				KnownCapacity: 1000, Reset: ResetPolicy{Mode: "manual"},
			},
			{
				ChannelID: 4, Model: "model-a", RequiredTag: "managed",
				RoutePool: "chat", QuotaPool: "ignored", QuotaMode: QuotaIgnore,
				Reset: ResetPolicy{Mode: "manual"},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.PollIntervalSeconds != 60 || cfg.Channels[0].Group != "default" || cfg.Channels[0].Action != ActionObserve || cfg.Channels[0].HardErrorThreshold != 3 {
		t.Fatalf("defaults were not applied: %+v", cfg)
	}
}

func TestConfigRejectsMixedQuotaPoolModes(t *testing.T) {
	cfg := Config{
		NewAPI: NewAPIConfig{UserID: 1},
		Channels: []ChannelPolicy{
			{ChannelID: 1, Model: "m", RequiredTag: "t", RoutePool: "r", QuotaPool: "q", QuotaMode: QuotaEstimate, Reset: ResetPolicy{Mode: "manual"}},
			{ChannelID: 2, Model: "m", RequiredTag: "t", RoutePool: "r", QuotaPool: "q", QuotaMode: QuotaKnown, KnownCapacity: 10, Reset: ResetPolicy{Mode: "manual"}},
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "inconsistent quota modes") {
		t.Fatalf("expected inconsistent mode error, got %v", err)
	}
}

func TestConfigRejectsImpossibleAnnualResetDate(t *testing.T) {
	cfg := Config{
		NewAPI: NewAPIConfig{UserID: 1},
		Channels: []ChannelPolicy{
			{
				ChannelID: 1, Model: "m", RequiredTag: "t", RoutePool: "r", QuotaPool: "q", QuotaMode: QuotaEstimate,
				Reset: ResetPolicy{Mode: "annual", Month: 2, Day: 30, Time: "07:30", Timezone: "Asia/Shanghai"},
			},
		},
	}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "calendar date does not exist") {
		t.Fatalf("expected impossible calendar date error, got %v", err)
	}
}
