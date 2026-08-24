package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/service"
	"github.com/QuantumNous/new-api/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestIsUpstreamQuotaError(t *testing.T) {
	tests := []struct {
		name string
		err  *types.NewAPIError
		want bool
	}{
		{name: "http 429", err: types.NewErrorWithStatusCode(errors.New("busy"), types.ErrorCodeBadResponseStatusCode, http.StatusTooManyRequests), want: true},
		{name: "provider quota text", err: types.NewErrorWithStatusCode(errors.New("RESOURCE_EXHAUSTED"), types.ErrorCodeBadResponseStatusCode, http.StatusForbidden), want: true},
		{name: "ordinary upstream error", err: types.NewErrorWithStatusCode(errors.New("bad gateway"), types.ErrorCodeBadResponseStatusCode, http.StatusBadGateway), want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, isUpstreamQuotaError(test.err))
		})
	}
}

func TestShouldRetryNeverSplicesAnActiveStream(t *testing.T) {
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	err := types.NewErrorWithStatusCode(
		errors.New("quota exhausted"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusTooManyRequests,
	)
	require.True(t, shouldRetry(ctx, err, 2))

	_, writeErr := ctx.Writer.Write([]byte("data"))
	require.NoError(t, writeErr)
	require.False(t, shouldRetry(ctx, err, 2))
}

func TestQuotaErrorBypassesLegacyWholeChannelAutoDisable(t *testing.T) {
	previous := common.AutomaticDisableChannelEnabled
	common.AutomaticDisableChannelEnabled = true
	t.Cleanup(func() { common.AutomaticDisableChannelEnabled = previous })

	quotaErr := types.NewErrorWithStatusCode(
		errors.New("quota exhausted"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusUnauthorized,
	)
	require.True(t, service.ShouldDisableChannel(quotaErr))
	require.True(t, isUpstreamQuotaError(quotaErr))
	require.False(t, shouldAutoDisableAfterRelayError(quotaErr))

	ordinaryAuthErr := types.NewErrorWithStatusCode(
		errors.New("invalid credential"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusUnauthorized,
	)
	require.True(t, service.ShouldDisableChannel(ordinaryAuthErr))
	require.False(t, isUpstreamQuotaError(ordinaryAuthErr))
	require.True(t, shouldAutoDisableAfterRelayError(ordinaryAuthErr))
}
