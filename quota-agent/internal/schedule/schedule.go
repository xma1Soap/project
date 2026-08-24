package schedule

import (
	"errors"
	"time"

	"github.com/xma1Soap/project/quota-agent/internal/config"
)

func Next(policy config.ResetPolicy, exhaustedAt time.Time) (time.Time, bool, error) {
	if exhaustedAt.IsZero() {
		return time.Time{}, false, errors.New("exhaustedAt is required")
	}
	switch policy.Mode {
	case "manual":
		return time.Time{}, false, nil
	case "after_days":
		return exhaustedAt.Add(time.Duration(policy.AfterDays) * 24 * time.Hour), true, nil
	case "fixed_at":
		value, err := time.Parse(time.RFC3339, policy.FixedAt)
		if err != nil {
			return time.Time{}, false, err
		}
		if !value.After(exhaustedAt) {
			return time.Time{}, false, errors.New("fixed reset time must be after exhaustion")
		}
		return value, true, nil
	case "daily":
		return nextCalendar(exhaustedAt, policy, false)
	case "annual":
		return nextCalendar(exhaustedAt, policy, true)
	default:
		return time.Time{}, false, errors.New("unsupported reset mode")
	}
}

func nextCalendar(exhaustedAt time.Time, policy config.ResetPolicy, annual bool) (time.Time, bool, error) {
	location, err := time.LoadLocation(policy.Timezone)
	if err != nil {
		return time.Time{}, false, err
	}
	clock, err := time.Parse("15:04", policy.Time)
	if err != nil {
		return time.Time{}, false, err
	}
	local := exhaustedAt.In(location)
	year, month, day := local.Date()
	if annual {
		month, day = time.Month(policy.Month), policy.Day
		for offset := 0; offset <= 8; offset++ {
			candidate := time.Date(year+offset, month, day, clock.Hour(), clock.Minute(), 0, 0, location)
			if candidate.Month() != month || candidate.Day() != day || !candidate.After(local) {
				continue
			}
			return candidate.UTC(), true, nil
		}
		return time.Time{}, false, errors.New("no valid annual reset date found")
	}
	candidate := time.Date(year, month, day, clock.Hour(), clock.Minute(), 0, 0, location)
	if !candidate.After(local) {
		candidate = candidate.AddDate(0, 0, 1)
	}
	if candidate.Month() != month || candidate.Day() != day {
		return time.Time{}, false, errors.New("reset calendar date does not exist")
	}
	return candidate.UTC(), true, nil
}
