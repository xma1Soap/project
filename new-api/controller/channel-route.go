package controller

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/model"
	"github.com/gin-gonic/gin"
)

type updateChannelRouteRequest struct {
	ChannelID       int    `json:"channel_id"`
	Group           string `json:"group"`
	Model           string `json:"model"`
	Enabled         bool   `json:"enabled"`
	ExpectedEnabled *bool  `json:"expected_enabled"`
}

// GetChannelRoutes exposes non-secret ability state for the station controller.
func GetChannelRoutes(c *gin.Context) {
	channelID, err := strconv.Atoi(c.Param("id"))
	if err != nil || channelID <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid channel id"})
		return
	}
	if _, err = model.GetChannelById(channelID, false); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"success": false, "message": "channel not found"})
		return
	}
	abilities, err := model.ListChannelAbilities(channelID)
	if err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, abilities)
}

// UpdateChannelRoute atomically changes one channel/group/model ability. It is
// intentionally root-only at the router and never falls back to channel status.
func UpdateChannelRoute(c *gin.Context) {
	request := updateChannelRouteRequest{}
	if err := c.ShouldBindJSON(&request); err != nil ||
		request.ChannelID <= 0 || strings.TrimSpace(request.Group) == "" ||
		strings.TrimSpace(request.Model) == "" || request.ExpectedEnabled == nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "message": "invalid route request"})
		return
	}

	ability, err := model.SetChannelAbilityEnabled(
		request.ChannelID,
		request.Group,
		request.Model,
		request.Enabled,
		*request.ExpectedEnabled,
	)
	if err != nil {
		switch {
		case errors.Is(err, model.ErrAbilityRouteNotFound):
			c.JSON(http.StatusNotFound, gin.H{"success": false, "message": err.Error()})
		case errors.Is(err, model.ErrAbilityRouteStateConflict), errors.Is(err, model.ErrAbilityRouteChannelOff):
			c.JSON(http.StatusConflict, gin.H{"success": false, "message": err.Error()})
		default:
			common.ApiError(c, err)
		}
		return
	}

	model.InitChannelCache()
	recordManageAudit(c, "channel.route_status", map[string]interface{}{
		"channel_id": request.ChannelID,
		"group":      strings.TrimSpace(request.Group),
		"model":      strings.TrimSpace(request.Model),
		"enabled":    request.Enabled,
	})
	common.ApiSuccess(c, ability)
}
