package gateway

import (
	"bytes"
	"compress/gzip"
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
)

type freeTierTestTransport func(*http.Request) (*http.Response, error)

func (f freeTierTestTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestFreeTierRoutingAcrossClientProtocols(t *testing.T) {
	for _, lane := range []string{"anonymous-v2", "key-v1"} {
		t.Run(lane, func(t *testing.T) {
			anonymous := lane != "key-v1"
			key, prefix := "zen-test-key", "/zen"
			if anonymous {
				key = "public"
			}
			if lane == "anonymous-v2" {
				prefix = "/inference"
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				expectedPaths := map[string]bool{
					prefix + "/v1/chat/completions": true,
					prefix + "/v1/responses":        true,
					prefix + "/v1/messages":         true,
				}
				if prefix == "/inference" {
					expectedPaths = map[string]bool{
						"/inference/openai/v1/chat/completions": true,
						"/inference/openai/v1/responses":        true,
						"/inference/anthropic/v1/messages":      true,
					}
				}
				if !expectedPaths[r.URL.Path] || r.Header.Get("Authorization") != "Bearer "+key {
					t.Errorf("wrong lane: %s, auth=%q", r.URL.Path, r.Header.Get("Authorization"))
					w.WriteHeader(http.StatusForbidden)
					return
				}
				session := r.Header.Get("x-opencode-session")
				validHeaders := r.Header.Get("x-opencode-session-id") == session && r.Header.Get("x-opencode-request") == "" && regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(r.Header.Get("x-opencode-project")) && strings.HasPrefix(r.Header.Get("User-Agent"), "opencode/latest/")
				if prefix == "/zen" {
					validHeaders = regexp.MustCompile(`^msg_[0-9a-f]{12}[0-9A-Za-z]{14}$`).MatchString(r.Header.Get("x-opencode-request")) && r.Header.Get("x-opencode-session-id") == session && regexp.MustCompile(`^[0-9a-f]{40}$`).MatchString(r.Header.Get("x-opencode-project")) && r.Header.Get("User-Agent") == "opencode/1.19.3 ai-sdk/provider-utils/4.0.23 runtime/bun/1.3.14"
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
					tool := raw.(map[string]any)
					if function, ok := tool["function"].(map[string]any); ok {
						tool = function
					}
					tools[tool["name"].(string)] = tool
				}
				if prefix == "/inference" {
					for _, name := range []string{"shell", "edit", "glob", "grep", "read"} {
						tool := tools[name]
						schema, _ := tool["parameters"].(map[string]any)
						if schema == nil {
							schema, _ = tool["input_schema"].(map[string]any)
						}
						properties, _ := schema["properties"].(map[string]any)
						description, _ := tool["description"].(string)
						if tool == nil || len(description) < 40 || len(properties) == 0 {
							t.Errorf("missing genuine tool schema: %s", name)
						}
					}
				} else if tools["read"] == nil || tools["shell"] != nil {
					t.Error("legacy lane must use the read tool without 2.x shell injection")
				}
				if tools["read"] == nil || tools["read_file"] != nil {
					t.Error("official read tool missing or client tool name leaked")
				}
				w.Header().Set("Content-Type", "text/event-stream")
				w.Header().Set("Content-Encoding", "gzip")
				compressed := gzip.NewWriter(w)
				defer compressed.Close()
				switch {
				case strings.HasSuffix(r.URL.Path, "/messages"):
					fmt.Fprint(compressed, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg-test\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"test-model-free\",\"content\":[],\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n")
					fmt.Fprint(compressed, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
					fmt.Fprint(compressed, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"read hi\"}}\n\n")
					fmt.Fprint(compressed, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
					fmt.Fprint(compressed, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n")
					fmt.Fprint(compressed, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
				case strings.HasSuffix(r.URL.Path, "/responses"):
					fmt.Fprint(compressed, "event: response.created\ndata: {\"type\":\"response.created\",\"response\":{\"id\":\"resp-test\",\"model\":\"test-model-free\",\"status\":\"in_progress\"}}\n\n")
					fmt.Fprint(compressed, "event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"read hi\",\"item_id\":\"msg-test\",\"output_index\":0,\"content_index\":0}\n\n")
					fmt.Fprint(compressed, "event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp-test\",\"model\":\"test-model-free\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"read hi\",\"annotations\":[]}]}]}}\n\n")
				default:
					fmt.Fprint(compressed, "data: {\"id\":\"chatcmpl-test\",\"model\":\"test-model-free\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"read hi\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
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
			g, err := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			// Redirect the official legacy origin into the local fixture too;
			// tests must never send requests to the real free-tier service.
			fixtureURL, _ := url.Parse(upstream.URL)
			for _, proxy := range g.transports.items {
				for _, client := range []*http.Client{proxy.client, proxy.bun} {
					transport := client.Transport
					client.Transport = freeTierTestTransport(func(r *http.Request) (*http.Response, error) {
						if r.URL.Host != "opencode.ai" && r.URL.Host != fixtureURL.Host {
							return nil, fmt.Errorf("unexpected external request: %s", r.URL.Host)
						}
						clone := r.Clone(r.Context())
						clone.URL.Scheme, clone.URL.Host = fixtureURL.Scheme, fixtureURL.Host
						clone.Host = fixtureURL.Host
						return transport.RoundTrip(clone)
					})
				}
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
						w := httptest.NewRecorder()
						g.Handler().ServeHTTP(w, r)
						if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "read") || !strings.Contains(w.Body.String(), "hi") {
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
