package models

import (
	"testing"
	"time"
)

func TestAnonymousEligibilityUsesAuthoritativeCosts(t *testing.T) {
	zero, paid := 0.0, 1.0
	store := &PricingStore{
		updatedAt: time.Now(),
		models: map[string]Price{
			"big-pickle":        {Input: &zero, Output: &zero},
			"paid-model-free":   {Input: &paid, Output: &zero},
			"retired-free":      {Input: &zero, Output: &zero, Deprecated: true},
			"unknown-cost-free": {Input: &zero},
		},
	}
	for _, tc := range []struct {
		model, source string
		allowed       bool
	}{
		{"big-pickle", "metadata_free", true},
		{"paid-model-free", "metadata_paid", false},
		{"retired-free", "metadata_deprecated", false},
		{"unknown-cost-free", "metadata_cost_unknown", false},
		{"new-model-free", "name_free", true},
		{"new-model", "metadata_model_missing", false},
	} {
		t.Run(tc.model, func(t *testing.T) {
			got := store.Decide(tc.model)
			if got.Allowed != tc.allowed || got.Source != tc.source {
				t.Fatalf("Decide(%q) = %+v; want allowed=%v, source=%s", tc.model, got, tc.allowed, tc.source)
			}
		})
	}
}
