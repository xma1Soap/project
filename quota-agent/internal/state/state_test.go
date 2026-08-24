package state

import "testing"

func TestEstimateUsesMedianAndConfidence(t *testing.T) {
	samples := []CapacitySample{
		{UsedQuota: 100, Complete: true},
		{UsedQuota: 102, Complete: true},
		{UsedQuota: 98, Complete: true},
		{UsedQuota: 101, Complete: true},
		{UsedQuota: 99, Complete: true},
		{UsedQuota: 9999, Complete: false},
	}
	estimate, confidence := Estimate(samples)
	if estimate != 100 || confidence != "high" {
		t.Fatalf("unexpected estimate: %d %s", estimate, confidence)
	}
}
