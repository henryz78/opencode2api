package identity

import (
	"net/http/httptest"
	"testing"
)

func TestDerivedSessionsAreStableAndScopedToCaller(t *testing.T) {
	r := httptest.NewRequest("POST", "/v1/chat/completions", nil)
	body := map[string]any{
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	}
	first := DeriveRequestIDs(r, body, "caller-a")
	other := DeriveRequestIDs(r, body, "caller-b")
	if first.Session == other.Session || first.Project == other.Project {
		t.Fatal("independent callers share a derived upstream identity")
	}
	body["messages"] = append(body["messages"].([]any),
		map[string]any{"role": "assistant", "content": "Hello"},
		map[string]any{"role": "user", "content": "Continue"},
	)
	continued := DeriveRequestIDs(r, body, "caller-a")
	if first.Session != continued.Session || first.Project != continued.Project {
		t.Fatal("continuing a conversation changed its upstream identity")
	}
}
