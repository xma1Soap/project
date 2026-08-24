package model

import (
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	QuotaTelemetrySuccess   = "success"
	QuotaTelemetryHardQuota = "hard_quota"
	QuotaTelemetryRateLimit = "rate_limit"
	QuotaTelemetryOther     = "other_error"
	maxQuotaTelemetryRoutes = 10_000
	maxQuotaTelemetryModel  = 256
)

type quotaTelemetryKey struct {
	ChannelID int
	Model     string
}

type quotaTelemetryCounter struct {
	Successes       atomic.Uint64
	HardQuotaErrors atomic.Uint64
	RateLimitErrors atomic.Uint64
	OtherErrors     atomic.Uint64
	ConsecutiveHard atomic.Uint64
	LastEventUnix   atomic.Int64
	LastKind        atomic.Uint32
}

type ChannelQuotaTelemetrySnapshot struct {
	ChannelID       int    `json:"channel_id"`
	Model           string `json:"model"`
	Successes       uint64 `json:"successes"`
	HardQuotaErrors uint64 `json:"hard_quota_errors"`
	RateLimitErrors uint64 `json:"rate_limit_errors"`
	OtherErrors     uint64 `json:"other_errors"`
	ConsecutiveHard uint64 `json:"consecutive_hard"`
	LastEventUnix   int64  `json:"last_event_unix"`
	LastKind        string `json:"last_kind"`
}

type ChannelQuotaUsageSnapshot struct {
	ChannelID int     `json:"channel_id" gorm:"column:channel_id"`
	Status    int     `json:"status"`
	UsedQuota int64   `json:"used_quota"`
	Tag       *string `json:"tag"`
}

var quotaTelemetry sync.Map
var quotaTelemetryEntries atomic.Int64

func RecordChannelModelSuccess(channelID int, modelName string) {
	counter := quotaTelemetryCounterFor(channelID, modelName)
	if counter == nil {
		return
	}
	counter.Successes.Add(1)
	counter.ConsecutiveHard.Store(0)
	counter.LastKind.Store(1)
	counter.LastEventUnix.Store(time.Now().Unix())
}

func RecordChannelModelError(channelID int, modelName string, kind string) {
	counter := quotaTelemetryCounterFor(channelID, modelName)
	if counter == nil {
		return
	}
	switch kind {
	case QuotaTelemetryHardQuota:
		counter.HardQuotaErrors.Add(1)
		counter.ConsecutiveHard.Add(1)
		counter.LastKind.Store(2)
	case QuotaTelemetryRateLimit:
		counter.RateLimitErrors.Add(1)
		counter.ConsecutiveHard.Store(0)
		counter.LastKind.Store(3)
	default:
		counter.OtherErrors.Add(1)
		counter.ConsecutiveHard.Store(0)
		counter.LastKind.Store(4)
	}
	counter.LastEventUnix.Store(time.Now().Unix())
}

func GetChannelQuotaTelemetry(channelIDs map[int]struct{}) []ChannelQuotaTelemetrySnapshot {
	result := make([]ChannelQuotaTelemetrySnapshot, 0)
	quotaTelemetry.Range(func(rawKey, rawCounter any) bool {
		key := rawKey.(quotaTelemetryKey)
		if len(channelIDs) > 0 {
			if _, ok := channelIDs[key.ChannelID]; !ok {
				return true
			}
		}
		counter := rawCounter.(*quotaTelemetryCounter)
		result = append(result, ChannelQuotaTelemetrySnapshot{
			ChannelID:       key.ChannelID,
			Model:           key.Model,
			Successes:       counter.Successes.Load(),
			HardQuotaErrors: counter.HardQuotaErrors.Load(),
			RateLimitErrors: counter.RateLimitErrors.Load(),
			OtherErrors:     counter.OtherErrors.Load(),
			ConsecutiveHard: counter.ConsecutiveHard.Load(),
			LastEventUnix:   counter.LastEventUnix.Load(),
			LastKind:        quotaTelemetryKind(counter.LastKind.Load()),
		})
		return true
	})
	sort.Slice(result, func(i, j int) bool {
		if result[i].ChannelID != result[j].ChannelID {
			return result[i].ChannelID < result[j].ChannelID
		}
		return result[i].Model < result[j].Model
	})
	return result
}

func GetChannelQuotaUsageSnapshots(channelIDs []int) ([]ChannelQuotaUsageSnapshot, error) {
	rows := make([]ChannelQuotaUsageSnapshot, 0, len(channelIDs))
	err := DB.Model(&Channel{}).
		Select("id AS channel_id", "status", "used_quota", "tag").
		Where("id IN ?", channelIDs).
		Order("id ASC").
		Scan(&rows).Error
	return rows, err
}

func quotaTelemetryCounterFor(channelID int, modelName string) *quotaTelemetryCounter {
	modelName = strings.TrimSpace(modelName)
	if channelID <= 0 || modelName == "" || len(modelName) > maxQuotaTelemetryModel {
		return nil
	}
	key := quotaTelemetryKey{ChannelID: channelID, Model: modelName}
	if existing, ok := quotaTelemetry.Load(key); ok {
		return existing.(*quotaTelemetryCounter)
	}
	for {
		current := quotaTelemetryEntries.Load()
		if current >= maxQuotaTelemetryRoutes {
			return nil
		}
		if quotaTelemetryEntries.CompareAndSwap(current, current+1) {
			break
		}
	}
	candidate := &quotaTelemetryCounter{}
	actual, loaded := quotaTelemetry.LoadOrStore(key, candidate)
	if loaded {
		quotaTelemetryEntries.Add(-1)
		return actual.(*quotaTelemetryCounter)
	}
	return candidate
}

func quotaTelemetryKind(value uint32) string {
	switch value {
	case 1:
		return QuotaTelemetrySuccess
	case 2:
		return QuotaTelemetryHardQuota
	case 3:
		return QuotaTelemetryRateLimit
	case 4:
		return QuotaTelemetryOther
	default:
		return ""
	}
}
