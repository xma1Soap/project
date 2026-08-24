package model

import (
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQuotaTelemetrySeparatesHardQuotaAndTransientRateLimit(t *testing.T) {
	quotaTelemetry = sync.Map{}
	quotaTelemetryEntries = atomic.Int64{}

	RecordChannelModelSuccess(42, "gemini-test")
	RecordChannelModelError(42, "gemini-test", QuotaTelemetryRateLimit)
	RecordChannelModelError(42, "gemini-test", QuotaTelemetryHardQuota)
	RecordChannelModelError(42, "gemini-test", QuotaTelemetryHardQuota)

	snapshots := GetChannelQuotaTelemetry(map[int]struct{}{42: {}})
	require.Len(t, snapshots, 1)
	require.Equal(t, uint64(1), snapshots[0].Successes)
	require.Equal(t, uint64(1), snapshots[0].RateLimitErrors)
	require.Equal(t, uint64(2), snapshots[0].HardQuotaErrors)
	require.Equal(t, uint64(2), snapshots[0].ConsecutiveHard)
	require.Equal(t, QuotaTelemetryHardQuota, snapshots[0].LastKind)

	RecordChannelModelSuccess(42, "gemini-test")
	snapshots = GetChannelQuotaTelemetry(map[int]struct{}{42: {}})
	require.Zero(t, snapshots[0].ConsecutiveHard)
}

func TestQuotaTelemetryHasStrictEntryAndModelBounds(t *testing.T) {
	quotaTelemetry = sync.Map{}
	quotaTelemetryEntries = atomic.Int64{}

	RecordChannelModelSuccess(1, strings.Repeat("x", maxQuotaTelemetryModel+1))
	require.Empty(t, GetChannelQuotaTelemetry(nil))

	var wait sync.WaitGroup
	for index := 0; index < maxQuotaTelemetryRoutes+100; index++ {
		wait.Add(1)
		go func(value int) {
			defer wait.Done()
			RecordChannelModelSuccess(value+1, "model-"+strconv.Itoa(value))
		}(index)
	}
	wait.Wait()
	require.Equal(t, int64(maxQuotaTelemetryRoutes), quotaTelemetryEntries.Load())
	require.Len(t, GetChannelQuotaTelemetry(nil), maxQuotaTelemetryRoutes)
}
