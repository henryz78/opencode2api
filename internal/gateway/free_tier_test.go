package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"opencode2api/internal/config"
	wire "opencode2api/internal/protocol"
	"opencode2api/internal/telemetry"
)

func TestFreeTierRoutingAcrossClientProtocols(t *testing.T) {
	for _, anonymous := range []bool{true, false} {
		t.Run(fmt.Sprintf("anonymous=%v", anonymous), func(t *testing.T) {
			key, prefix := "zen-test-key", "/zen"
			if anonymous {
				key, prefix = "public", "/inference"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != prefix+"/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key {
					t.Errorf("wrong lane: %s, auth=%q", r.URL.Path, r.Header.Get("Authorization"))
					w.WriteHeader(http.StatusForbidden)
					return
				}
				session := r.Header.Get("x-opencode-session")
				if !regexp.MustCompile(`^ses_[0-9a-f]{9}f[c-f][0-9a-f][0-9A-Za-z]{14}$`).MatchString(session) || r.Header.Get("x-opencode-session-id") != session || r.Header.Get("x-opencode-request") != "" || !regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(r.Header.Get("x-opencode-project")) || !strings.HasPrefix(r.Header.Get("User-Agent"), "opencode/latest/") {
					t.Errorf("invalid free-tier identity: %v", r.Header)
					w.WriteHeader(http.StatusForbidden)
					return
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload["stream"] != true {
					t.Errorf("invalid streaming request: %v (%v)", payload, err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				tools := map[string]map[string]any{}
				for _, raw := range payload["tools"].([]any) {
					tool := raw.(map[string]any)["function"].(map[string]any)
					tools[tool["name"].(string)] = tool
				}
				for _, name := range []string{"shell", "edit", "glob", "grep", "read"} {
					if tool := tools[name]; tool == nil || len(tool["description"].(string)) < 40 || len(tool["parameters"].(map[string]any)["properties"].(map[string]any)) == 0 {
						t.Errorf("missing genuine tool schema: %s", name)
					}
				}
				if tool := tools["lookup"]; tool != nil && tool["description"] != "Look up a value" {
					t.Error("client tool was overwritten")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				io.WriteString(w, "data: {\"id\":\"chatcmpl-test\",\"model\":\"test-model-free\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_lookup\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"query\\\":\\\"hi\\\"}\"}}]},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			}))
			defer upstream.Close()
			cfg, err := config.Load("../../config.example.json")
			if err != nil {
				t.Fatal(err)
			}
			cfg.Anonymous, cfg.GoKeys = anonymous, nil
			cfg.ZenKeys = nil
			if !anonymous {
				cfg.ZenKeys = []string{key}
			}
			cfg.Upstream = config.UpstreamConfig{Zen: upstream.URL + "/zen", Go: upstream.URL + "/go", Anonymous: upstream.URL + "/inference"}
			cfg.Models.Protocols = map[string]string{"test-model-free": "chat"}
			g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
			if err != nil {
				t.Fatal(err)
			}
			g.catalog.Replace([]string{"test-model-free"}, nil)
			for _, protocol := range []wire.Protocol{wire.Chat, wire.Responses, wire.Anthropic} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/stream=%v", protocol, stream), func(t *testing.T) {
						payload, err := wire.ConvertRequest(wire.Chat, protocol, map[string]any{
							"model": "test-model-free", "stream": stream, "max_tokens": 64,
							"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
							"tools":    []any{map[string]any{"type": "function", "function": map[string]any{"name": "lookup", "description": "Look up a value", "parameters": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}}}},
						})
						if err != nil {
							t.Fatal(err)
						}
						body, _ := json.Marshal(payload)
						r := httptest.NewRequest(http.MethodPost, wire.Path(protocol), bytes.NewReader(body))
						r.Header.Set("Authorization", "Bearer "+cfg.ServerKeys[0])
						r = r.WithContext(telemetry.WithRequestMeta(r.Context(), &telemetry.RequestMeta{}))
						w := httptest.NewRecorder()
						g.Handler().ServeHTTP(w, r)
						if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "lookup") || !strings.Contains(w.Body.String(), "hi") {
							t.Fatalf("response status=%d, body=%s", w.Code, w.Body.String())
						}
						if !stream && !json.Valid(w.Body.Bytes()) {
							t.Fatal("non-streaming client received invalid JSON")
						}
						if stream && !strings.HasPrefix(w.Header().Get("Content-Type"), "text/event-stream") {
							t.Fatal("streaming client did not receive SSE")
						}
					})
				}
			}
			if result := g.probeFreeModel(context.Background(), "test-model-free"); !result.success || result.channel != map[bool]string{true: "anonymous", false: "zen"}[anonymous] {
				t.Fatalf("availability probe used wrong lane or failed: %+v", result)
			}
		})
	}
}
