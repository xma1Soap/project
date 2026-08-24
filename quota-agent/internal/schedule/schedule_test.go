package schedule

import (
	"testing"
	"time"

	"github.com/xma1Soap/project/quota-agent/internal/config"
)

func TestNextSupportsAfterDaysAndFixedDate(t *testing.T) {
	start := time.Date(2026, 8, 24, 4, 0, 0, 0, time.UTC)
	after, scheduled, err := Next(config.ResetPolicy{Mode: "after_days", AfterDays: 3}, start)
	if err != nil || !scheduled || !after.Equal(start.Add(72*time.Hour)) {
		t.Fatalf("unexpected after_days result: %v %v %v", after, scheduled, err)
	}

	fixed, scheduled, err := Next(config.ResetPolicy{Mode: "fixed_at", FixedAt: "2026-09-01T08:30:00+08:00"}, start)
	if err != nil || !scheduled || fixed.Format(time.RFC3339) != "2026-09-01T08:30:00+08:00" {
		t.Fatalf("unexpected fixed_at result: %v %v %v", fixed, scheduled, err)
	}
}

func TestNextAnnualRollsToNextYear(t *testing.T) {
	start := time.Date(2026, 12, 31, 20, 0, 0, 0, time.FixedZone("CST", 8*3600))
	next, scheduled, err := Next(config.ResetPolicy{Mode: "annual", Month: 8, Day: 24, Time: "07:30", Timezone: "Asia/Shanghai"}, start)
	if err != nil || !scheduled || next.In(time.FixedZone("CST", 8*3600)).Year() != 2027 {
		t.Fatalf("unexpected annual result: %v %v %v", next, scheduled, err)
	}
}

func TestNextAnnualLeapDayFindsNextLeapYear(t *testing.T) {
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	next, scheduled, err := Next(config.ResetPolicy{Mode: "annual", Month: 2, Day: 29, Time: "07:30", Timezone: "Asia/Shanghai"}, start)
	if err != nil || !scheduled || next.In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04") != "2028-02-29 07:30" {
		t.Fatalf("unexpected leap-day result: %v %v %v", next, scheduled, err)
	}
}

func TestFixedResetCannotPrecedeExhaustion(t *testing.T) {
	start := time.Date(2026, 8, 24, 4, 0, 0, 0, time.UTC)
	_, _, err := Next(config.ResetPolicy{Mode: "fixed_at", FixedAt: "2026-08-24T03:00:00Z"}, start)
	if err == nil {
		t.Fatal("expected a past fixed reset to be rejected")
	}
}
