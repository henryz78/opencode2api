package gateway

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opencode2api/internal/identity"
	wire "opencode2api/internal/protocol"
)

func TestEnsureProviderEndUserIdentifier(t *testing.T) {
	request := httptest.NewRequest(http.MethodPost, "http://gateway.test/v1/responses", nil)
	request.Header.Set("Authorization", "Bearer local-server-key")

	responses := map[string]any{"model": "free-model"}
	ensureProviderEndUserIdentifier(request, wire.Responses, responses)
	want := identity.StableID("safety", "opencode2api:local-server-key")
	if responses["safety_identifier"] != want {
		t.Fatalf("safety_identifier = %#v, want %q", responses["safety_identifier"], want)
	}

	chat := map[string]any{"model": "free-model"}
	ensureProviderEndUserIdentifier(request, wire.Chat, chat)
	if chat["user"] != want {
		t.Fatalf("user = %#v, want %q", chat["user"], want)
	}

	provided := map[string]any{"user": "client-user"}
	ensureProviderEndUserIdentifier(request, wire.Responses, provided)
	if provided["user"] != "client-user" || provided["safety_identifier"] != nil {
		t.Fatalf("provided identifier was changed: %#v", provided)
	}
}

func TestResponseRequiresEndUserIdentifier(t *testing.T) {
	response := &http.Response{
		StatusCode: http.StatusBadRequest,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"This request requires an end-user identifier. Provide a non-empty safety_identifier (or user) field."}}`)),
	}
	if !responseRequiresEndUserIdentifier(response) {
		t.Fatal("missing identifier response was not recognized")
	}
	body, err := io.ReadAll(response.Body)
	if err != nil || !strings.Contains(string(body), "safety_identifier") {
		t.Fatalf("response body was not restored: %q (%v)", body, err)
	}
}
