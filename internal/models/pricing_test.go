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
	cases := []struct {
		model, source string
		allowed       bool
	}{
		{"big-pickle", "metadata_free", true},
		{"paid-model-free", "metadata_paid", false},
		{"retired-free", "metadata_deprecated", false},
		{"unknown-cost-free", "metadata_cost_unknown", false},
		{"new-model-free", "name_free", true},
		{"new-model", "metadata_model_missing", false},
	}
	protocols := make(map[string]string, len(cases))
	var names []string
	for _, tc := range cases {
		protocols[tc.model] = "chat"
		names = append(names, tc.model)
	}
	catalog := NewCatalog("zen", protocols)
	catalog.SetPricingStore(store)
	catalog.SetFreeOnly(true)
	catalog.Replace(names, nil)
	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			got := store.Decide(tc.model)
			if got.Allowed != tc.allowed || got.Source != tc.source {
				t.Fatalf("Decide(%q) = %+v; want allowed=%v, source=%s", tc.model, got, tc.allowed, tc.source)
			}
			if _, err := catalog.Route(tc.model, true, false, false); (err == nil) != tc.allowed {
				t.Fatalf("free_only route for %q with a Zen key: error=%v, want allowed=%v", tc.model, err, tc.allowed)
			}
		})
	}
}
