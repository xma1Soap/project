package controller

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseQuotaSnapshotIDsIsBoundedAndDeterministic(t *testing.T) {
	ids, err := parseQuotaSnapshotIDs("9, 2,9, 5")
	require.NoError(t, err)
	require.Equal(t, []int{2, 5, 9}, ids)

	_, err = parseQuotaSnapshotIDs("1,invalid")
	require.ErrorIs(t, err, strconv.ErrSyntax)

	values := make([]string, 0, maxQuotaSnapshotChannels+1)
	for i := 1; i <= maxQuotaSnapshotChannels+1; i++ {
		values = append(values, strconv.Itoa(i))
	}
	_, err = parseQuotaSnapshotIDs(strings.Join(values, ","))
	require.ErrorContains(t, err, "too many")
}
