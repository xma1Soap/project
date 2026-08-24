package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type TestPenaltySetting struct {
	Enabled         bool `json:"enabled"`
	MinPromptTokens int  `json:"min_prompt_tokens"` // 输入tokens低于此值触发惩罚
	PenaltyQuota    int  `json:"penalty_quota"`      // 惩罚扣除的额度
}

var defaultTestPenaltySetting = TestPenaltySetting{}

func init() {
	config.GlobalConfig.Register("test_penalty", &defaultTestPenaltySetting)
}

func GetTestPenaltySetting() *TestPenaltySetting {
	return &defaultTestPenaltySetting
}
