package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type UABlacklistSetting struct {
	Enabled  bool     `json:"enabled"`
	Keywords []string `json:"keywords"` // UA关键词列表，模糊匹配
}

var defaultUABlacklistSetting = UABlacklistSetting{
	Keywords: []string{},
}

func init() {
	config.GlobalConfig.Register("ua_blacklist", &defaultUABlacklistSetting)
}

func GetUABlacklistSetting() *UABlacklistSetting {
	return &defaultUABlacklistSetting
}
