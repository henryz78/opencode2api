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
	"net/url"
	"regexp"
	"strings"
	"testing"

	"opencode2api/internal/config"
	wire "opencode2api/internal/protocol"
	"opencode2api/internal/telemetry"
)

type freeTierTestTransport func(*http.Request) (*http.Response, error)

func (f freeTierTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestFreeTierRoutingAcrossClientProtocols(t *testing.T) {
	for _, lane := range []string{"anonymous-legacy", "anonymous-fallback", "key-legacy"} {
		t.Run(lane, func(t *testing.T) {
			anonymous := lane != "key-legacy"
			key, prefix := "zen-test-key", "/zen"
			if anonymous {
				key = "public"
			}
			if lane == "anonymous-fallback" {
				prefix = "/inference"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if lane == "anonymous-fallback" && r.URL.Path == "/zen/v1/chat/completions" {
					w.WriteHeader(http.StatusForbidden)
					io.WriteString(w, `{"error":{"message":"legacy lane unavailable"}}`)
					return
				}
				if r.URL.Path != prefix+"/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+key {
					t.Errorf("wrong lane: %s, auth=%q", r.URL.Path, r.Header.Get("Authorization"))
					w.WriteHeader(http.StatusForbidden)
					return
				}
				session := r.Header.Get("x-opencode-session")
				validHeaders := r.Header.Get("x-opencode-session-id") == session && r.Header.Get("x-opencode-request") == "" && regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(r.Header.Get("x-opencode-project")) && strings.HasPrefix(r.Header.Get("User-Agent"), "opencode/latest/")
				if prefix == "/zen" {
					validHeaders = r.Header.Get("x-opencode-request") != "" && regexp.MustCompile(`^prj_[0-9a-f]{24}$`).MatchString(r.Header.Get("x-opencode-project")) && strings.HasPrefix(r.Header.Get("User-Agent"), "opencode/1.18.31 ")
				}
				if !regexp.MustCompile(`^ses_[0-9a-f]{9}f[c-f][0-9a-f][0-9A-Za-z]{14}$`).MatchString(session) || !validHeaders {
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
				if prefix == "/inference" {
					for _, name := range []string{"shell", "edit", "glob", "grep", "read"} {
						if tool := tools[name]; tool == nil || len(tool["description"].(string)) < 40 || len(tool["parameters"].(map[string]any)["properties"].(map[string]any)) == 0 {
							t.Errorf("missing genuine tool schema: %s", name)
						}
					}
				} else if tools["read"] == nil || tools["shell"] != nil {
					t.Error("legacy lane must use the read tool without 2.x shell injection")
				}
				toolName := "read_file"
				if prefix == "/zen" {
					toolName = "read"
				}
				// Availability probes have no client tool and receive a read stub.
				if tool := tools[toolName]; tool == nil || tool["description"] == legacyDeprecatedReadDescription {
					w.Header().Set("Content-Type", "text/event-stream")
					io.WriteString(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				if tools[toolName]["description"] != "Look up a value" {
					t.Error("client tool was overwritten")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: {\"id\":\"chatcmpl-test\",\"model\":\"test-model-free\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"tool_calls\":[{\"index\":0,\"id\":\"call_lookup\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":\"{\\\"query\\\":\\\"hi\\\"}\"}}]},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", toolName)
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
			// Redirect the official legacy origin into the local fixture too;
			// tests must never send requests to the real free-tier service.
			fixtureURL, _ := url.Parse(upstream.URL)
			for _, proxy := range g.transports.items {
				transport := proxy.client.Transport
				proxy.client.Transport = freeTierTestTransport(func(r *http.Request) (*http.Response, error) {
					if r.URL.Host != "opencode.ai" && r.URL.Host != fixtureURL.Host {
						return nil, fmt.Errorf("unexpected external request: %s", r.URL.Host)
					}
					clone := r.Clone(r.Context())
					clone.URL.Scheme, clone.URL.Host = fixtureURL.Scheme, fixtureURL.Host
					clone.Host = fixtureURL.Host
					return transport.RoundTrip(clone)
				})
			}
			g.catalog.Replace([]string{"test-model-free"}, nil)
			for _, protocol := range []wire.Protocol{wire.Chat, wire.Responses, wire.Anthropic} {
				for _, stream := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/stream=%v", protocol, stream), func(t *testing.T) {
						payload, err := wire.ConvertRequest(wire.Chat, protocol, map[string]any{
							"model": "test-model-free", "stream": stream, "max_tokens": 64,
							"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
							"tools":    []any{map[string]any{"type": "function", "function": map[string]any{"name": "read_file", "description": "Look up a value", "parameters": map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string"}}}}}},
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
						if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "read_file") || !strings.Contains(w.Body.String(), "hi") {
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
