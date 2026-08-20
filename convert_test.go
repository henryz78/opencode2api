package main

import "testing"

func TestConvertChatToResponsesPreservesSafetyIdentifier(t *testing.T) {
	output, err := convertRequest(ProtocolChat, ProtocolResponses, map[string]any{
		"model":             "muse-spark-1.2-contributor-free",
		"safety_identifier": "playground-admin",
		"messages":          []any{map[string]any{"role": "user", "content": "Hi"}},
	})
	if err != nil {
		t.Fatalf("convertRequest returned error: %v", err)
	}
	if got := output["safety_identifier"]; got != "playground-admin" {
		t.Fatalf("safety_identifier = %#v, want playground-admin", got)
	}
}

func TestConvertChatToResponsesPreservesLegacyUser(t *testing.T) {
	output, err := convertRequest(ProtocolChat, ProtocolResponses, map[string]any{
		"model":    "muse-spark-1.2-contributor-free",
		"user":     "playground-admin",
		"messages": []any{map[string]any{"role": "user", "content": "Hi"}},
	})
	if err != nil {
		t.Fatalf("convertRequest returned error: %v", err)
	}
	if got := output["user"]; got != "playground-admin" {
		t.Fatalf("user = %#v, want playground-admin", got)
	}
}

func TestConvertResponsesToChatMapsSafetyIdentifierToUser(t *testing.T) {
	output, err := convertRequest(ProtocolResponses, ProtocolChat, map[string]any{
		"model":             "chat-model",
		"safety_identifier": "playground-admin",
		"input":             "Hi",
	})
	if err != nil {
		t.Fatalf("convertRequest returned error: %v", err)
	}
	if got := output["user"]; got != "playground-admin" {
		t.Fatalf("user = %#v, want playground-admin", got)
	}
}
