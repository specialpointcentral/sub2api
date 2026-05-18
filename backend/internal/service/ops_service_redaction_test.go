package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsSensitiveKey_TokenBudgetKeysNotRedacted(t *testing.T) {
	t.Parallel()

	for _, key := range []string{
		"max_tokens",
		"max_output_tokens",
		"max_input_tokens",
		"max_completion_tokens",
		"max_tokens_to_sample",
		"budget_tokens",
		"prompt_tokens",
		"completion_tokens",
		"input_tokens",
		"output_tokens",
		"total_tokens",
		"token_count",
	} {
		if isSensitiveKey(key) {
			t.Fatalf("expected key %q to NOT be treated as sensitive", key)
		}
	}

	for _, key := range []string{
		"authorization",
		"Authorization",
		"access_token",
		"refresh_token",
		"id_token",
		"session_token",
		"token",
		"client_secret",
		"private_key",
		"signature",
	} {
		if !isSensitiveKey(key) {
			t.Fatalf("expected key %q to be treated as sensitive", key)
		}
	}
}

func TestSanitizeAndTrimJSONPayload_PreservesTokenBudgetFields(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"model":"claude-3","max_tokens":123,"thinking":{"type":"enabled","budget_tokens":456},"access_token":"abc","messages":[{"role":"user","content":"hi"}]}`)
	out, _, _ := sanitizeAndTrimJSONPayload(raw, 10*1024)
	if out == "" {
		t.Fatalf("expected non-empty sanitized output")
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("unmarshal sanitized output: %v", err)
	}

	if got, ok := decoded["max_tokens"].(float64); !ok || got != 123 {
		t.Fatalf("expected max_tokens=123, got %#v", decoded["max_tokens"])
	}

	thinking, ok := decoded["thinking"].(map[string]any)
	if !ok || thinking == nil {
		t.Fatalf("expected thinking object to be preserved, got %#v", decoded["thinking"])
	}
	if got, ok := thinking["budget_tokens"].(float64); !ok || got != 456 {
		t.Fatalf("expected thinking.budget_tokens=456, got %#v", thinking["budget_tokens"])
	}

	if got := decoded["access_token"]; got != "[REDACTED]" {
		t.Fatalf("expected access_token to be redacted, got %#v", got)
	}
}

func TestBuildOpsRequestBodyPreview_RedactsCredentialsAndKeepsDebugFields(t *testing.T) {
	t.Parallel()

	body := []byte(`{"model":"gpt-5.3","max_tokens":64,"access_token":"secret-token","messages":[{"role":"user","content":"debug this failure"}]}`)

	preview, truncated, bytesLen := BuildOpsRequestBodyPreview(body)

	if preview == "" {
		t.Fatal("expected request body preview")
	}
	if truncated {
		t.Fatal("small request body should not be marked truncated")
	}
	if bytesLen == nil || *bytesLen != len(body) {
		t.Fatalf("expected original byte size %d, got %#v", len(body), bytesLen)
	}
	if !strings.Contains(preview, `"model":"gpt-5.3"`) {
		t.Fatalf("expected model in preview, got %s", preview)
	}
	if !strings.Contains(preview, `"max_tokens":64`) {
		t.Fatalf("expected token budget in preview, got %s", preview)
	}
	if strings.Contains(preview, "secret-token") {
		t.Fatalf("expected credentials redacted, got %s", preview)
	}
	if !strings.Contains(preview, `"[REDACTED]"`) {
		t.Fatalf("expected redaction marker, got %s", preview)
	}
}

func TestBuildOpsRequestBodyPreview_TruncatesOversizedSingleMessageContent(t *testing.T) {
	t.Parallel()

	largeContent := strings.Repeat("debug-context-", opsMaxStoredRequestBodyPreviewBytes)
	bodyMap := map[string]any{
		"model":  "gpt-5.5",
		"stream": true,
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": largeContent,
			},
		},
	}
	raw, err := json.Marshal(bodyMap)
	if err != nil {
		t.Fatalf("marshal test body: %v", err)
	}

	preview, truncated, bytesLen := BuildOpsRequestBodyPreview(raw)

	if !truncated {
		t.Fatal("expected oversized request body preview to be truncated")
	}
	if bytesLen == nil || *bytesLen != len(raw) {
		t.Fatalf("expected original byte size %d, got %#v", len(raw), bytesLen)
	}
	if len(preview) > opsMaxStoredRequestBodyPreviewBytes {
		t.Fatalf("preview should fit storage cap: got %d bytes", len(preview))
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(preview), &decoded); err != nil {
		t.Fatalf("preview should remain valid JSON: %v\n%s", err, preview)
	}
	msgs, ok := decoded["messages"].([]any)
	if !ok || len(msgs) != 1 {
		t.Fatalf("expected one truncated message to remain, got %#v", decoded["messages"])
	}
	msg, ok := msgs[0].(map[string]any)
	if !ok {
		t.Fatalf("expected message object, got %#v", msgs[0])
	}
	if got := msg["role"]; got != "user" {
		t.Fatalf("expected role to be preserved, got %#v", got)
	}
	content, ok := msg["content"].(string)
	if !ok {
		t.Fatalf("expected string content prefix to be preserved, got %#v", msg["content"])
	}
	if !strings.HasPrefix(content, "debug-context-debug-context-") {
		t.Fatalf("expected content prefix to be preserved, got %.80q", content)
	}
	if !strings.Contains(content, "truncated") {
		t.Fatalf("expected truncation marker in content, got %.120q", content)
	}
}

func TestBuildOpsRequestBodyPreview_TruncatesOversizedResponsesInputContent(t *testing.T) {
	t.Parallel()

	largeContent := strings.Repeat("responses-debug-", opsMaxStoredRequestBodyPreviewBytes)
	bodyMap := map[string]any{
		"model":  "gpt-5.5",
		"stream": true,
		"input": []any{
			map[string]any{
				"role": "user",
				"content": []any{
					map[string]any{
						"type": "input_text",
						"text": largeContent,
					},
				},
			},
		},
	}
	raw, err := json.Marshal(bodyMap)
	if err != nil {
		t.Fatalf("marshal test body: %v", err)
	}

	preview, truncated, _ := BuildOpsRequestBodyPreview(raw)

	if !truncated {
		t.Fatal("expected oversized request body preview to be truncated")
	}
	if len(preview) > opsMaxStoredRequestBodyPreviewBytes {
		t.Fatalf("preview should fit storage cap: got %d bytes", len(preview))
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(preview), &decoded); err != nil {
		t.Fatalf("preview should remain valid JSON: %v\n%s", err, preview)
	}
	items, ok := decoded["input"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("expected one truncated input item to remain, got %#v", decoded["input"])
	}
	item, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("expected input object, got %#v", items[0])
	}
	blocks, ok := item["content"].([]any)
	if !ok || len(blocks) != 1 {
		t.Fatalf("expected content block to remain, got %#v", item["content"])
	}
	block, ok := blocks[0].(map[string]any)
	if !ok {
		t.Fatalf("expected content block object, got %#v", blocks[0])
	}
	text, ok := block["text"].(string)
	if !ok {
		t.Fatalf("expected text prefix to be preserved, got %#v", block["text"])
	}
	if !strings.HasPrefix(text, "responses-debug-responses-debug-") {
		t.Fatalf("expected input text prefix to be preserved, got %.80q", text)
	}
	if !strings.Contains(text, "truncated") {
		t.Fatalf("expected truncation marker in text, got %.120q", text)
	}
}

func TestShrinkToEssentials_IncludesThinking(t *testing.T) {
	t.Parallel()

	root := map[string]any{
		"model":      "claude-3",
		"max_tokens": 100,
		"thinking": map[string]any{
			"type":          "enabled",
			"budget_tokens": 200,
		},
		"messages": []any{
			map[string]any{"role": "user", "content": "first"},
			map[string]any{"role": "user", "content": "last"},
		},
	}

	out := shrinkToEssentials(root)
	if _, ok := out["thinking"]; !ok {
		t.Fatalf("expected thinking to be included in essentials: %#v", out)
	}
}
