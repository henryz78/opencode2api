package protocol

import "testing"

func TestConvertChatToResponsesDoesNotInventReasoningID(t *testing.T) {
	output, err := ConvertRequest(Chat, Responses, map[string]any{
		"model": "muse-spark-1.2-contributor-free",
		"messages": []any{
			map[string]any{"role": "user", "content": "Hi"},
			map[string]any{
				"role":              "assistant",
				"content":           "2 + 2 = 4",
				"reasoning_content": "[redacted thinking]",
			},
			map[string]any{"role": "user", "content": "add 3"},
		},
	})
	if err != nil {
		t.Fatalf("ConvertRequest returned error: %v", err)
	}
	for i, rawItem := range output["input"].([]any) {
		item, ok := rawItem.(map[string]any)
		if ok && item["type"] == "reasoning" {
			if id, exists := item["id"]; exists && id != "" {
				t.Fatalf("input[%d] reasoning item has synthetic id %q", i, id)
			}
		}
	}
}

func TestPrepareRequestNormalizesChatNullContent(t *testing.T) {
	output, err := PrepareRequest(Chat, Chat, map[string]any{
		"model": "mimo-v2.5-free",
		"messages": []any{
			map[string]any{"role": "user", "content": nil},
			map[string]any{"role": "assistant", "content": nil},
		},
	}, "https://opencode.ai/zen")
	if err != nil {
		t.Fatalf("PrepareRequest returned error: %v", err)
	}
	for i, rawMessage := range output["messages"].([]any) {
		message := rawMessage.(map[string]any)
		if message["content"] != "" {
			t.Fatalf("messages[%d].content = %#v, want empty string", i, message["content"])
		}
	}
}
