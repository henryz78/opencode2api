package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func newGatewayTest(t *testing.T, upstream string, anonymous bool, protocols map[string]string, zenKeys []string) (*Gateway, *Monitor) {
	t.Helper()
	monitor := NewMonitor()
	cfg := Config{
		ServerKeys: []string{"local-server-key"},
		ZenKeys:    zenKeys,
		Anonymous:  anonymous,
		Proxies:    []string{"direct"},
		Upstream:   UpstreamConfig{Zen: upstream, Go: upstream},
		Retry:      RetryConfig{MaxAttempts: 2, TimeoutSeconds: 5},
		Models:     ModelsConfig{RefreshSeconds: 300, Protocols: protocols},
		Performance: PerformanceConfig{
			MaxIdleConns: 16, MaxIdleConnsPerHost: 16, IdleConnTimeoutSeconds: 30,
			ConnectTimeoutSeconds: 2, FailureCooldownSeconds: 1,
		},
		Prefer: TierZen,
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gateway, err := NewGateway(cfg, logger, monitor)
	if err != nil {
		t.Fatalf("NewGateway returned error: %v", err)
	}
	return gateway, monitor
}

func gatewayRequest(t *testing.T, handler http.Handler, method, path string, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	request.Header.Set("Authorization", "Bearer local-server-key")
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeGatewayResponse(t *testing.T, recorder *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var response map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode gateway response: %v; body=%s", err, recorder.Body.String())
	}
	return response
}

func writeTestJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}

func TestGatewayChatToResponsesRetriesIdentifierAndConvertsResponse(t *testing.T) {
	const model = "responses-test-model"
	var mu sync.Mutex
	var calls []map[string]any
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("upstream path = %s, want /v1/responses", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer upstream-key" {
			t.Errorf("upstream authorization = %q, want upstream key", got)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		mu.Lock()
		calls = append(calls, payload)
		attempt := len(calls)
		mu.Unlock()
		if attempt == 1 {
			w.WriteHeader(http.StatusBadRequest)
			writeTestJSON(w, map[string]any{"error": map[string]any{"message": "This request requires an end-user identifier. Provide a non-empty safety_identifier (or user) field."}})
			return
		}
		writeTestJSON(w, map[string]any{
			"id": "resp_test", "object": "response", "created_at": 1, "model": model,
			"output": []any{map[string]any{
				"id": "msg_test", "type": "message", "status": "completed", "role": "assistant",
				"content": []any{map[string]any{"type": "output_text", "text": "hello", "annotations": []any{}}},
			}},
			"usage": map[string]any{"input_tokens": 3, "output_tokens": 2, "total_tokens": 5},
		})
	}))
	defer upstream.Close()

	gateway, _ := newGatewayTest(t, upstream.URL, false, map[string]string{model: string(ProtocolResponses)}, []string{"upstream-key"})
	gateway.catalog.Replace([]string{model}, nil)

	recorder := gatewayRequest(t, gateway.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    model,
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("gateway status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeGatewayResponse(t, recorder)
	choices, ok := response["choices"].([]any)
	if !ok || len(choices) != 1 {
		t.Fatalf("choices = %#v, want one Chat choice", response["choices"])
	}
	message, ok := choices[0].(map[string]any)["message"].(map[string]any)
	if !ok || message["content"] != "hello" {
		t.Fatalf("converted message = %#v, want hello", message)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(calls) != 2 {
		t.Fatalf("upstream calls = %d, want identifier retry", len(calls))
	}
	if _, exists := calls[0]["safety_identifier"]; exists {
		t.Fatal("first request unexpectedly contained a generated safety_identifier")
	}
	identifier, ok := calls[1]["safety_identifier"].(string)
	if !ok || identifier == "" {
		t.Fatalf("retry safety_identifier = %#v, want non-empty value", calls[1]["safety_identifier"])
	}
	if identifier != stableID("safety", "opencode2api:local-server-key") {
		t.Fatalf("retry safety_identifier = %q, want stable credential-derived value", identifier)
	}
}

func TestGatewayChatToAnthropicConvertsResponse(t *testing.T) {
	const model = "claude-test-model"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Errorf("upstream path = %s, want /v1/messages", r.URL.Path)
		}
		if got := r.Header.Get("x-api-key"); got != "upstream-key" {
			t.Errorf("Anthropic upstream key = %q, want upstream key", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("Anthropic version = %q, want 2023-06-01", got)
		}
		writeTestJSON(w, map[string]any{
			"id": "msg_test", "type": "message", "role": "assistant", "model": model,
			"content":     []any{map[string]any{"type": "text", "text": "anthropic hello"}},
			"stop_reason": "end_turn",
			"usage":       map[string]any{"input_tokens": 2, "output_tokens": 1},
		})
	}))
	defer upstream.Close()

	gateway, _ := newGatewayTest(t, upstream.URL, false, map[string]string{model: string(ProtocolAnthropic)}, []string{"upstream-key"})
	gateway.catalog.Replace([]string{model}, nil)

	recorder := gatewayRequest(t, gateway.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    model,
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("gateway Anthropic status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeGatewayResponse(t, recorder)
	choices := response["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "anthropic hello" {
		t.Fatalf("converted Anthropic message = %#v", message)
	}
}

func TestGatewayTranscodesResponsesStreamToChat(t *testing.T) {
	const model = "responses-stream-model"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("upstream path = %s, want /v1/responses", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		created, _ := json.Marshal(map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_stream", "model": model}})
		delta, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": "hello"})
		completed, _ := json.Marshal(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_stream", "model": model, "usage": map[string]any{"input_tokens": 2, "output_tokens": 1, "total_tokens": 3}}})
		_, _ = fmt.Fprintf(w, "event: response.created\ndata: %s\n\nevent: response.output_text.delta\ndata: %s\n\nevent: response.completed\ndata: %s\n\n", created, delta, completed)
	}))
	defer upstream.Close()

	gateway, _ := newGatewayTest(t, upstream.URL, false, map[string]string{model: string(ProtocolResponses)}, []string{"upstream-key"})
	gateway.catalog.Replace([]string{model}, nil)

	recorder := gatewayRequest(t, gateway.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    model,
		"stream":   true,
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
		"user":     "client-user",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("gateway stream status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("stream content type = %q, want event stream", got)
	}
	body := recorder.Body.String()
	for _, want := range []string{"hello", "[DONE]"} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream body does not contain %q: %s", want, body)
		}
	}
}

func TestGatewayConvertsResponsesFunctionCallToChat(t *testing.T) {
	const model = "responses-tool-model"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode upstream request: %v", err)
			return
		}
		if _, ok := payload["tools"]; !ok {
			t.Error("converted Responses request omitted tools")
		}
		arguments, _ := json.Marshal(map[string]string{"q": "hi"})
		writeTestJSON(w, map[string]any{
			"id": "resp_tool", "object": "response", "created_at": 1, "model": model,
			"output": []any{map[string]any{"id": "fc_item", "type": "function_call", "status": "completed", "call_id": "call_lookup", "name": "lookup", "arguments": string(arguments)}},
			"usage":  map[string]any{"input_tokens": 4, "output_tokens": 3, "total_tokens": 7},
		})
	}))
	defer upstream.Close()

	gateway, _ := newGatewayTest(t, upstream.URL, false, map[string]string{model: string(ProtocolResponses)}, []string{"upstream-key"})
	gateway.catalog.Replace([]string{model}, nil)

	recorder := gatewayRequest(t, gateway.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    model,
		"messages": []any{map[string]any{"role": "user", "content": "Look this up"}},
		"tools": []any{map[string]any{
			"type":     "function",
			"function": map[string]any{"name": "lookup", "description": "look up a value", "parameters": map[string]any{"type": "object"}},
		}},
		"tool_choice": "auto",
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("gateway tool status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeGatewayResponse(t, recorder)
	choices := response["choices"].([]any)
	choice := choices[0].(map[string]any)
	message := choice["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]any)
	toolCall := toolCalls[0].(map[string]any)
	function := toolCall["function"].(map[string]any)
	arguments, _ := json.Marshal(map[string]string{"q": "hi"})
	if function["name"] != "lookup" || function["arguments"] != string(arguments) {
		t.Fatalf("converted tool call = %#v", toolCall)
	}
	if choice["finish_reason"] != "tool_calls" {
		t.Fatalf("finish_reason = %#v, want tool_calls", choice["finish_reason"])
	}
}

func TestGatewayAnonymousRouteUsesPublicAndFiltersPaidModels(t *testing.T) {
	const freeModel = "free-model"
	const paidModel = "paid-model"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("upstream path = %s, want /v1/chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer public" {
			t.Errorf("anonymous authorization = %q, want Bearer public", got)
		}
		writeTestJSON(w, map[string]any{
			"id": "chat_public", "object": "chat.completion", "model": freeModel,
			"choices": []any{map[string]any{"index": 0, "message": map[string]any{"role": "assistant", "content": "anonymous ok"}, "finish_reason": "stop"}},
		})
	}))
	defer upstream.Close()

	gateway, _ := newGatewayTest(t, upstream.URL, true, nil, nil)
	gateway.catalog.Replace([]string{freeModel, paidModel}, nil)

	modelsRecorder := gatewayRequest(t, gateway.Handler(), http.MethodGet, "/v1/models", nil)
	if modelsRecorder.Code != http.StatusOK {
		t.Fatalf("models status = %d, want 200", modelsRecorder.Code)
	}
	models := decodeGatewayResponse(t, modelsRecorder)["data"].([]any)
	if len(models) != 1 || models[0].(map[string]any)["id"] != freeModel {
		t.Fatalf("anonymous model list = %#v, want only %s", models, freeModel)
	}

	recorder := gatewayRequest(t, gateway.Handler(), http.MethodPost, "/v1/chat/completions", map[string]any{
		"model":    freeModel,
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	})
	if recorder.Code != http.StatusOK {
		t.Fatalf("anonymous gateway status = %d, want 200; body=%s", recorder.Code, recorder.Body.String())
	}
	response := decodeGatewayResponse(t, recorder)
	choices := response["choices"].([]any)
	message := choices[0].(map[string]any)["message"].(map[string]any)
	if message["content"] != "anonymous ok" {
		t.Fatalf("anonymous response = %#v", message)
	}
}
