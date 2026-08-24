package controller

import (
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

const maxQuotaSnapshotChannels = 500

func GetChannelQuotaSnapshot(c *gin.Context) {
	ids, err := parseQuotaSnapshotIDs(c.Query("ids"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": err.Error()})
		return
	}
	channels, err := model.GetChannelQuotaUsageSnapshots(ids)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	filter := make(map[int]struct{}, len(ids))
	for _, id := range ids {
		filter[id] = struct{}{}
	}
	common.ApiSuccess(c, gin.H{
		"generated_at": time.Now().Unix(),
		"channels":     channels,
		"telemetry":    model.GetChannelQuotaTelemetry(filter),
	})
}

func parseQuotaSnapshotIDs(value string) ([]int, error) {
	parts := strings.Split(value, ",")
	unique := make(map[int]struct{}, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		id, err := strconv.Atoi(trimmed)
		if err != nil || id <= 0 {
			return nil, strconv.ErrSyntax
		}
		unique[id] = struct{}{}
		if len(unique) > maxQuotaSnapshotChannels {
			return nil, &quotaSnapshotLimitError{}
		}
	}
	if len(unique) == 0 {
		return nil, strconv.ErrSyntax
	}
	ids := make([]int, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	return ids, nil
}

type quotaSnapshotLimitError struct{}

func (*quotaSnapshotLimitError) Error() string {
	return "too many channel ids"
}
