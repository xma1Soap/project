package operation_setting

import "github.com/QuantumNous/new-api/setting/config"

type EmptyResponseBillingSetting struct {
	Enabled bool `json:"enabled"`
}

var defaultEmptyResponseBillingSetting = EmptyResponseBillingSetting{}

func init() {
	config.GlobalConfig.Register("empty_response_billing", &defaultEmptyResponseBillingSetting)
}

func GetEmptyResponseBillingSetting() *EmptyResponseBillingSetting {
	return &defaultEmptyResponseBillingSetting
}
