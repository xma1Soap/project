package model

import (
	"errors"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"gorm.io/gorm"
)

var (
	ErrAbilityRouteNotFound      = errors.New("channel route ability not found")
	ErrAbilityRouteStateConflict = errors.New("channel route ability state changed")
	ErrAbilityRouteChannelOff    = errors.New("cannot enable a route while its channel is disabled")
)

func ListChannelAbilities(channelID int) ([]Ability, error) {
	var abilities []Ability
	err := DB.Where("channel_id = ?", channelID).
		Order(commonGroupCol + " ASC, model ASC").
		Find(&abilities).Error
	return abilities, err
}

func GetChannelAbility(channelID int, group string, modelName string) (*Ability, error) {
	group = strings.TrimSpace(group)
	modelName = strings.TrimSpace(modelName)
	if channelID <= 0 || group == "" || modelName == "" {
		return nil, ErrAbilityRouteNotFound
	}
	var ability Ability
	err := DB.Where(
		"channel_id = ? AND "+commonGroupCol+" = ? AND model = ?",
		channelID,
		group,
		modelName,
	).First(&ability).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrAbilityRouteNotFound
	}
	return &ability, err
}

// SetChannelAbilityEnabled updates exactly one channel/group/model route. The
// expected state is mandatory at the HTTP boundary and makes an administrator's
// concurrent change win instead of being overwritten by the controller.
func SetChannelAbilityEnabled(
	channelID int,
	group string,
	modelName string,
	enabled bool,
	expectedEnabled bool,
) (*Ability, error) {
	group = strings.TrimSpace(group)
	modelName = strings.TrimSpace(modelName)
	if channelID <= 0 || group == "" || modelName == "" {
		return nil, ErrAbilityRouteNotFound
	}

	var updated Ability
	err := DB.Transaction(func(tx *gorm.DB) error {
		var channel Channel
		if err := tx.Select("id", "status").First(&channel, "id = ?", channelID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAbilityRouteNotFound
			}
			return err
		}
		if enabled && channel.Status != common.ChannelStatusEnabled {
			return ErrAbilityRouteChannelOff
		}

		var ability Ability
		query := tx.Where(
			"channel_id = ? AND "+commonGroupCol+" = ? AND model = ?",
			channelID,
			group,
			modelName,
		)
		if err := query.First(&ability).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrAbilityRouteNotFound
			}
			return err
		}
		if ability.Enabled != expectedEnabled {
			return ErrAbilityRouteStateConflict
		}
		if ability.Enabled == enabled {
			updated = ability
			return nil
		}

		result := tx.Model(&Ability{}).
			Where(
				"channel_id = ? AND "+commonGroupCol+" = ? AND model = ? AND enabled = ?",
				channelID,
				group,
				modelName,
				expectedEnabled,
			).
			Update("enabled", enabled)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return ErrAbilityRouteStateConflict
		}
		ability.Enabled = enabled
		updated = ability
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &updated, nil
}
