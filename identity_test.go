package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEnsureProviderEndUserIdentifier(t *testing.T) {
	request := httptest.NewRequest("POST", "http://gateway.test/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer local-server-key")

	responses := map[string]any{"model": "muse-spark-1.2-contributor-free"}
	ensureProviderEndUserIdentifier(request, ProtocolResponses, responses)
	identifier, ok := responses["safety_identifier"].(string)
	if !ok || identifier == "" {
		t.Fatalf("safety_identifier = %#v, want a generated identifier", responses["safety_identifier"])
	}
	if identifier != stableID("safety", "opencode2api:local-server-key") {
		t.Fatalf("safety_identifier = %q, want stable credential-derived identifier", identifier)
	}

	chat := map[string]any{"model": "chat-model"}
	ensureProviderEndUserIdentifier(request, ProtocolChat, chat)
	if got := chat["user"]; got != identifier {
		t.Fatalf("chat user = %#v, want %q", got, identifier)
	}

	anthropic := map[string]any{"model": "claude-model"}
	ensureProviderEndUserIdentifier(request, ProtocolAnthropic, anthropic)
	if _, exists := anthropic["user"]; exists {
		t.Fatal("anthropic request unexpectedly received user")
	}
	if _, exists := anthropic["safety_identifier"]; exists {
		t.Fatal("anthropic request unexpectedly received safety_identifier")
	}
}

func TestEnsureProviderEndUserIdentifierPreservesClientValue(t *testing.T) {
	request := httptest.NewRequest("POST", "http://gateway.test/v1/responses", nil)
	request.Header.Set("x-api-key", "local-server-key")

	user := map[string]any{"model": "chat-model", "user": "client-user"}
	ensureProviderEndUserIdentifier(request, ProtocolResponses, user)
	if got := user["user"]; got != "client-user" {
		t.Fatalf("user = %#v, want client-user", got)
	}
	if _, exists := user["safety_identifier"]; exists {
		t.Fatal("client user request unexpectedly received a generated safety_identifier")
	}

	safety := map[string]any{"model": "responses-model", "safety_identifier": "client-safety"}
	ensureProviderEndUserIdentifier(request, ProtocolResponses, safety)
	if got := safety["safety_identifier"]; got != "client-safety" {
		t.Fatalf("safety_identifier = %#v, want client-safety", got)
	}
}

func TestFallbackIdentifierSurvivesChatToResponsesConversion(t *testing.T) {
	request := httptest.NewRequest("POST", "http://gateway.test/v1/chat/completions", nil)
	request.Header.Set("Authorization", "Bearer local-server-key")
	payload := map[string]any{
		"model":    "muse-spark-1.2-contributor-free",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}

	ensureProviderEndUserIdentifier(request, ProtocolResponses, payload)
	converted, err := prepareUpstreamRequest(ProtocolChat, ProtocolResponses, payload, "https://zen.example")
	if err != nil {
		t.Fatalf("prepareUpstreamRequest returned error: %v", err)
	}
	if got := converted["safety_identifier"]; got != payload["safety_identifier"] {
		t.Fatalf("converted safety_identifier = %#v, want %#v", got, payload["safety_identifier"])
	}
	if _, exists := converted["user"]; exists {
		t.Fatal("converted request unexpectedly contained an empty user field")
	}
}

func TestPrepareUpstreamRequestDoesNotInjectIdentifierForOrdinaryRequest(t *testing.T) {
	input := map[string]any{
		"model":    "ordinary-responses-model",
		"input":    "Hi",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}
	converted, err := prepareUpstreamRequest(ProtocolResponses, ProtocolResponses, input, "https://zen.example")
	if err != nil {
		t.Fatalf("prepareUpstreamRequest returned error: %v", err)
	}
	if _, exists := converted["safety_identifier"]; exists {
		t.Fatal("ordinary request unexpectedly received safety_identifier")
	}
}

func TestResponseRequiresEndUserIdentifier(t *testing.T) {
	response := &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"This request requires an end-user identifier. Provide a non-empty safety_identifier (or user) field."}`))}
	if !responseRequiresEndUserIdentifier(response) {
		t.Fatal("missing end-user identifier response was not recognized")
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || !strings.Contains(string(body), "safety_identifier") {
		t.Fatalf("response body was not restored after inspection: %q (%v)", body, err)
	}

	ordinary := &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{"error":{"message":"invalid model"}}`))}
	if responseRequiresEndUserIdentifier(ordinary) {
		t.Fatal("ordinary 400 response was incorrectly recognized")
	}
}
