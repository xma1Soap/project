package model

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupAbilityRouteTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&Channel{}, &Ability{}))
	t.Cleanup(func() {
		DB = previousDB
		sqlDB, dbErr := db.DB()
		if dbErr == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func seedAbilityRoute(t *testing.T, db *gorm.DB) {
	t.Helper()
	priority := int64(0)
	require.NoError(t, db.Create(&Channel{
		Id: 68, Name: "managed", Status: common.ChannelStatusEnabled,
		Group: "default", Models: "v4p,v4p-search", Priority: &priority,
	}).Error)
	require.NoError(t, db.Create(&[]Ability{
		{ChannelId: 68, Group: "default", Model: "v4p", Enabled: true, Priority: &priority},
		{ChannelId: 68, Group: "default", Model: "v4p-search", Enabled: true, Priority: &priority},
	}).Error)
}

func TestSetChannelAbilityEnabledIsExactAndCompareAndSwap(t *testing.T) {
	db := setupAbilityRouteTestDB(t)
	seedAbilityRoute(t, db)

	updated, err := SetChannelAbilityEnabled(68, "default", "v4p", false, true)
	require.NoError(t, err)
	require.False(t, updated.Enabled)

	other, err := GetChannelAbility(68, "default", "v4p-search")
	require.NoError(t, err)
	require.True(t, other.Enabled)

	_, err = SetChannelAbilityEnabled(68, "default", "v4p", true, true)
	require.ErrorIs(t, err, ErrAbilityRouteStateConflict)
}

func TestSetChannelAbilityEnabledWillNotEnableRouteOnDisabledChannel(t *testing.T) {
	db := setupAbilityRouteTestDB(t)
	seedAbilityRoute(t, db)
	require.NoError(t, db.Model(&Channel{}).Where("id = ?", 68).Update("status", common.ChannelStatusManuallyDisabled).Error)
	require.NoError(t, db.Model(&Ability{}).Where("channel_id = ? AND model = ?", 68, "v4p").Update("enabled", false).Error)

	_, err := SetChannelAbilityEnabled(68, "default", "v4p", true, false)
	require.True(t, errors.Is(err, ErrAbilityRouteChannelOff))
}

func TestMemoryChannelCacheRespectsDisabledAbility(t *testing.T) {
	db := setupAbilityRouteTestDB(t)
	seedAbilityRoute(t, db)
	previousMemoryCache := common.MemoryCacheEnabled
	common.MemoryCacheEnabled = true
	channelsIDM = make(map[int]*Channel)
	t.Cleanup(func() {
		common.MemoryCacheEnabled = previousMemoryCache
		group2model2channels = nil
		channelsIDM = nil
	})

	InitChannelCache()
	channel, err := GetRandomSatisfiedChannel("default", "v4p", 0, "")
	require.NoError(t, err)
	require.NotNil(t, channel)

	_, err = SetChannelAbilityEnabled(68, "default", "v4p", false, true)
	require.NoError(t, err)
	InitChannelCache()
	channel, err = GetRandomSatisfiedChannel("default", "v4p", 0, "")
	require.NoError(t, err)
	require.Nil(t, channel)
}

func TestChannelModelCooldownSkipsOnlyTheFailedRoute(t *testing.T) {
	db := setupAbilityRouteTestDB(t)
	seedAbilityRoute(t, db)
	priority := int64(0)
	require.NoError(t, db.Create(&Channel{
		Id: 69, Name: "fallback", Status: common.ChannelStatusEnabled,
		Group: "default", Models: "v4p", Priority: &priority,
	}).Error)
	require.NoError(t, db.Create(&Ability{
		ChannelId: 69, Group: "default", Model: "v4p", Enabled: true, Priority: &priority,
	}).Error)

	previousEnabled := setting.ChannelCooldownEnabled
	previousMemoryCache := common.MemoryCacheEnabled
	setting.ChannelCooldownEnabled = true
	common.MemoryCacheEnabled = false
	channelModelCooldown.Lock()
	channelModelCooldown.m = make(map[channelModelCooldownKey]int64)
	channelModelCooldown.Unlock()
	t.Cleanup(func() {
		setting.ChannelCooldownEnabled = previousEnabled
		common.MemoryCacheEnabled = previousMemoryCache
		channelModelCooldown.Lock()
		channelModelCooldown.m = make(map[channelModelCooldownKey]int64)
		channelModelCooldown.Unlock()
	})

	SetChannelModelCooldown(68, "v4p", 300)
	selected, err := GetChannel("default", "v4p", 0, "")
	require.NoError(t, err)
	require.Equal(t, 69, selected.Id)

	// A different model on channel 68 remains routable, and no persistent
	// ability switch is changed by the transient cooldown.
	selected, err = GetChannel("default", "v4p-search", 0, "")
	require.NoError(t, err)
	require.Equal(t, 68, selected.Id)
	ability, err := GetChannelAbility(68, "default", "v4p")
	require.NoError(t, err)
	require.True(t, ability.Enabled)
}
