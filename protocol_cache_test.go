package main

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestProtocolCapabilityCacheRoundTrip(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "config.json.capabilities.json")
	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))
	capabilities := protocolCapabilities{
		Protocols: map[Tier]map[string]Protocol{
			TierZen: {"zen-model": ProtocolChat},
			TierGo:  {"go-model": ProtocolAnthropic},
		},
		Unsupported: map[Tier]map[string]bool{
			TierZen: {"unsupported-zen": true},
			TierGo:  {},
		},
	}

	writer := &Gateway{
		catalog:             newModelCatalog(TierGo, nil),
		logger:              logger,
		capabilityCachePath: cachePath,
	}
	if err := writer.saveProtocolCapabilityCache(capabilities); err != nil {
		t.Fatalf("saveProtocolCapabilityCache returned error: %v", err)
	}

	reader := &Gateway{
		catalog:             newModelCatalog(TierGo, nil),
		logger:              logger,
		capabilityCachePath: cachePath,
	}
	if err := reader.loadProtocolCapabilityCache(); err != nil {
		t.Fatalf("loadProtocolCapabilityCache returned error: %v", err)
	}
	reader.catalog.ReplaceWithCapabilities([]string{"zen-model"}, []string{"go-model"}, nil, nil)

	zenRoute, err := reader.catalog.Route("zen-model", true, false, false)
	if err != nil || zenRoute.Protocol != ProtocolChat {
		t.Fatalf("zen route = %#v, err=%v; want Chat", zenRoute, err)
	}
	goRoute, err := reader.catalog.Route("go-model", false, true, false)
	if err != nil || goRoute.Protocol != ProtocolAnthropic {
		t.Fatalf("go route = %#v, err=%v; want Anthropic", goRoute, err)
	}
	if reader.catalog.Supported("unsupported-zen") {
		t.Fatal("unsupported cached model was exposed")
	}
}

func TestPrepareRouteBodiesUsesProtocolPerTier(t *testing.T) {
	gateway := &Gateway{cfg: Config{Upstream: UpstreamConfig{
		Zen: "https://zen.example",
		Go:  "https://go.example",
	}}}
	route := modelRoute{
		ID:       "multi-tier-model",
		Tier:     TierZen,
		Protocol: ProtocolChat,
		Protocols: map[Tier]Protocol{
			TierZen: ProtocolChat,
			TierGo:  ProtocolAnthropic,
		},
		KeyTiers: []Tier{TierZen, TierGo},
	}
	bodies, err := gateway.prepareRouteBodies(ProtocolChat, route, map[string]any{
		"model":    "multi-tier-model",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	})
	if err != nil {
		t.Fatalf("prepareRouteBodies returned error: %v", err)
	}
	if len(bodies[TierZen]) == 0 || len(bodies[TierGo]) == 0 {
		t.Fatalf("prepared bodies = %#v, want both Zen and Go bodies", bodies)
	}
	zenBody, err := decodeJSONMap(bodies[TierZen])
	if err != nil {
		t.Fatalf("decode Zen body: %v", err)
	}
	goBody, err := decodeJSONMap(bodies[TierGo])
	if err != nil {
		t.Fatalf("decode Go body: %v", err)
	}
	if _, ok := zenBody["messages"]; !ok {
		t.Fatalf("Zen body = %#v, want Chat messages", zenBody)
	}
	if _, ok := goBody["messages"]; !ok {
		t.Fatalf("Go body = %#v, want Anthropic messages", goBody)
	}
	if _, ok := goBody["max_tokens"]; !ok {
		t.Fatalf("Go body = %#v, want Anthropic max_tokens", goBody)
	}
}

func decodeJSONMap(data []byte) (map[string]any, error) {
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, err
	}
	return value, nil
}
