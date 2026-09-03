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

func TestConvertChatToResponsesReasoningOmitsSyntheticID(t *testing.T) {
	output, err := convertRequest(ProtocolChat, ProtocolResponses, map[string]any{
		"model": "muse-spark-1.2-contributor-free",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hi, what is 2+2?"},
			map[string]any{
				"role":              "assistant",
				"content":           "2 + 2 = 4",
				"reasoning_content": "[redacted thinking]",
			},
			map[string]any{"role": "user", "content": "add 3"},
		},
	})
	if err != nil {
		t.Fatalf("convertRequest returned error: %v", err)
	}
	inputSlice, ok := output["input"].([]any)
	if !ok {
		t.Fatalf("output input is not a slice: %#v", output["input"])
	}
	for i, rawItem := range inputSlice {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		if item["type"] == "reasoning" {
			if id, exists := item["id"]; exists && id != "" {
				t.Fatalf("input[%d] reasoning item has synthetic id %q, expected no id", i, id)
			}
		}
	}
}

func TestPrepareUpstreamRequestNormalizesChatNullContent(t *testing.T) {
	output, err := prepareUpstreamRequest(ProtocolChat, ProtocolChat, map[string]any{
		"model": "mimo-v2.5-free",
		"messages": []any{
			map[string]any{"role": "user", "content": nil},
			map[string]any{"role": "assistant", "content": nil},
		},
	}, "https://opencode.ai/zen")
	if err != nil {
		t.Fatalf("prepareUpstreamRequest returned error: %v", err)
	}
	messages, ok := output["messages"].([]any)
	if !ok {
		t.Fatalf("messages is not a slice: %#v", output["messages"])
	}
	for i, raw := range messages {
		msg, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("messages[%d] not a map", i)
		}
		if msg["content"] != "" {
			t.Fatalf("messages[%d].content = %#v, want empty string", i, msg["content"])
		}
	}
}

