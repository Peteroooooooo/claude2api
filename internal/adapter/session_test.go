package adapter

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"claude2api/internal/service"

	"github.com/gin-gonic/gin"
)

func TestClientSessionMetadataAndHistory(t *testing.T) {
	old := runner
	defer func() { runner = old }()
	var seen []service.Prompt
	runner = testRunner(func(_ string, p service.Prompt, emit func(string)) (service.CompletionResult, error) {
		seen = append(seen, p)
		emit("<final_answer>SAVED</final_answer>")
		return service.CompletionResult{StatusCode: 200}, nil
	})
	bodies := []string{
		`{"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"account-a\",\"session_id\":\"session-a\"}"},"system":"instructions","messages":[{"role":"user","content":"remember"}],"tools":[{"name":"echo","input_schema":{"type":"object"}}]}`,
		`{"metadata":{"user_id":"{\"device_id\":\"device-a\",\"account_uuid\":\"account-a\",\"session_id\":\"session-a\"}"},"system":"instructions","messages":[{"role":"user","content":"remember"},{"role":"assistant","content":[{"type":"text","text":"SAVED"}]},{"role":"user","content":"next"}],"tools":[{"name":"echo","input_schema":{"type":"object"},"cache_control":{"type":"ephemeral"}}]}`,
	}
	for _, body := range bodies {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Set("api_scope", "key:7")
		c.Request = httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
		AnthropicMessages(c)
		if w.Code != 200 {
			t.Fatal(w.Body.String())
		}
		if w.Header().Get("X-Claude2API-Session-ID") != "session-a" {
			t.Fatal("session header not returned")
		}
	}
	if len(seen) != 2 || seen[0].SessionID != "session-a" || seen[0].Scope != seen[1].Scope || seen[0].Policy != seen[1].Policy {
		t.Fatalf("metadata lost or cache markers changed policy: %+v", seen)
	}
	if seen[0].ReplyKey("<final_answer>SAVED</final_answer>") != seen[1].History[1].Key {
		t.Fatalf("client response doesn't match stored reply key: %s vs %s", seen[0].ReplyKey("<final_answer>SAVED</final_answer>"), seen[1].History[1].Key)
	}
}

func TestToolReplyMatchesReturnedHistoryAndKeepsCallID(t *testing.T) {
	tools := []map[string]any{{"name": "echo", "input_schema": map[string]any{"type": "object"}}}
	first, err := buildPrompt([]Message{{Role: "user", Content: "call echo"}}, nil, tools, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil)
	configureSession(c, &first, "messages", true)
	raw := `<tool_calls>[{"name":"echo","arguments":{"text":"HELLO"}}]</tool_calls>`
	id := stableToolID(first, 0, true)
	second, err := buildPrompt([]Message{{Role: "user", Content: "call echo"}, {Role: "assistant", ToolCalls: []ToolCall{{ID: id, Name: "echo", Arguments: `{"text":"HELLO"}`}}}, {Role: "tool", ToolCallID: id, Content: "HELLO"}}, nil, tools, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if first.ReplyKey(raw) != second.History[1].Key {
		t.Fatalf("tool response cannot continue: %s vs %s", first.ReplyKey(raw), second.History[1].Key)
	}
	if !strings.Contains(second.History[1].ToolIDs, id) || !strings.Contains(second.History[2].Text, id) {
		t.Fatal("tool-result correlation lost")
	}
}

func TestHistoryImagesStayOnTheirOriginalTurn(t *testing.T) {
	image := "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	messages := []anthropicMessage{{Role: "user", Content: json.RawMessage(`[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"` + image + `"}}]`)}, {Role: "assistant", Content: json.RawMessage(`"image seen"`)}, {Role: "user", Content: json.RawMessage(`"next"`)}}
	msgs, images := normalizeAnthropicMessages(messages)
	p, err := buildPrompt(msgs, images, nil, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.History) != 3 || len(p.History[0].Images) != 1 || len(p.History[2].Images) != 0 {
		t.Fatalf("historical image moved to current turn: %+v", p.History)
	}
}

func TestClaudeCodeBudgetUpdatesKeepPolicy(t *testing.T) {
	a, err := buildPrompt([]Message{{Role: "system", Content: "Instructions\n<total_tokens>15000000 tokens left</total_tokens>"}, {Role: "user", Content: "hello"}}, nil, nil, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildPrompt([]Message{{Role: "system", Content: "Instructions"}, {Role: "system", Content: "<total_tokens>14999900 tokens left</total_tokens>"}, {Role: "user", Content: "hello"}}, nil, nil, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Policy != b.Policy {
		t.Fatal("runtime token budget changed conversation policy")
	}
	c, err := buildPrompt([]Message{{Role: "system", Content: "Changed instructions"}, {Role: "user", Content: "hello"}}, nil, nil, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a.Policy == c.Policy {
		t.Fatal("real system changes were ignored")
	}
}
