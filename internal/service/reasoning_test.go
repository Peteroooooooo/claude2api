package service

import (
	"strings"
	"testing"
)

func TestReasoningModePrecedenceAndRetryIdentity(t *testing.T) {
	for _, tc := range []struct{ model, mode, effort, want string }{
		{"claude-sonnet-5-5", "extended", "max", "extended"},
		{"claude-sonnet-5-5-thinking", "off", "low", "off"},
		{"claude-sonnet-5-5-thinking", "", "", "extended"},
		{"claude-sonnet-5-5", "", "max", "extended"},
		{"claude-sonnet-5-5", "", "", "off"},
	} {
		model, mode := resolveReasoning(tc.model, Prompt{ThinkingMode: tc.mode, Effort: tc.effort})
		if model != "claude-sonnet-5-5" || mode != tc.want {
			t.Fatalf("%+v resolved %s %s", tc, model, mode)
		}
	}
	p := sessionPrompt(history("user", "same message"))
	p.ThinkingMode, p.Effort = "extended", "max"
	key := turnKey("session", "claude-sonnet-5-5", p)
	p.Effort = "low"
	if key == turnKey("session", "claude-sonnet-5-5", p) {
		t.Fatal("different effort replays old output")
	}
	p.Effort, p.ThinkingMode = "max", "off"
	if key == turnKey("session", "claude-sonnet-5-5", p) {
		t.Fatal("different thinking replays old output")
	}
}

func TestReasoningChangesPreserveNativeConversation(t *testing.T) {
	w := setupSessionTest(t)
	p := sessionPrompt(history("user", "first"))
	p.ThinkingMode, p.Effort = "extended", "max"
	first := completeTest(t, p)
	p = sessionPrompt(history("user", "first"), history("assistant", "SAVED"), history("user", "next"))
	p.ThinkingMode, p.Effort = "off", "low"
	second := completeTest(t, p)
	if second.Action != "continue" || second.ConversationID != first.ConversationID || second.ParentUUID != first.MessageUUID {
		t.Fatalf("mode change broke continuation: %+v %+v", first, second)
	}
	if len(w.calls) != 2 || w.calls[0].Model != "claude-sonnet-5-5" || w.calls[0].ThinkingMode != "extended" || w.calls[0].Effort != "max" || w.calls[1].ThinkingMode != "off" || w.calls[1].Effort != "low" {
		t.Fatalf("native parameters lost: %+v", w.calls)
	}
	if second.ThinkingMode != "off" || second.Effort != "low" || second.UpstreamModel != "claude-sonnet-5-5" {
		t.Fatalf("resolved log settings wrong: %+v", second)
	}
	third := completeTest(t, p)
	if third.Action != "replay" || len(w.calls) != 2 {
		t.Fatalf("duplicate sent twice: %+v", third)
	}
}

func TestNativeThinkingSummaryAndOmittedDisplay(t *testing.T) {
	sse := `data: {"type":"content_block_start","content_block":{"type":"thinking"}}

data: {"type":"content_block_delta","delta":{"type":"thinking_summary_delta","summary":{"summary":"Checking the calculation."}}}

data: {"type":"content_block_stop"}

data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"89"}}

data: {"type":"message_stop"}

`
	for _, show := range []bool{true, false} {
		var out strings.Builder
		if err := parseCompletionSSEWithThinking(strings.NewReader(sse), func(s string) { out.WriteString(s) }, show); err != nil {
			t.Fatal(err)
		}
		if !strings.HasSuffix(out.String(), "89") || strings.Contains(out.String(), "Checking") != show {
			t.Fatalf("show=%v result=%s", show, out.String())
		}
	}
}

func TestNativeThinkingOutputHonorsRequestedMode(t *testing.T) {
	stream := sseReply("reply-id", "")
	stream = strings.Replace(stream, "data: {\"type\":\"message_stop\"}",
		`data: {"type":"content_block_start","content_block":{"type":"thinking"}}

data: {"type":"content_block_delta","delta":{"type":"thinking_summary_delta","summary":{"summary":"Progress update."}}}

data: {"type":"content_block_stop"}

data: {"type":"content_block_delta","delta":{"type":"text_delta","text":"ANSWER"}}

data: {"type":"message_stop"}`, 1)
	for _, tc := range []struct {
		name, mode, display string
		show                bool
	}{
		{"disabled with max effort", "off", "", false},
		{"disabled with updates", "off", "updates", false},
		{"enabled with max effort", "extended", "updates", true},
		{"enabled but omitted", "extended", "omitted", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			web := &fakeWeb{parents: map[string]string{"test-conversation": rootMessageUUID}, onComplete: func(nativeCall, int) (int, string) { return 200, stream }}
			client := &ClaudeAI{orgUUID: "test-org", client: &fakeHTTP{web: web, account: "test-account"}, headers: map[string]string{}}
			var out strings.Builder
			status, err := client.SendMessage("test-conversation", "claude-sonnet-5-5", Prompt{Text: "question", ThinkingMode: tc.mode, ThinkingDisplay: tc.display, Effort: "max"}, nil, nil, func(text string) { out.WriteString(text) })
			if err != nil || status != 200 {
				t.Fatalf("send failed: status=%d error=%v", status, err)
			}
			if !strings.HasSuffix(out.String(), "ANSWER") || strings.Contains(out.String(), "Progress update.") != tc.show || strings.Contains(out.String(), "<think>") != tc.show {
				t.Fatalf("mode=%s display=%s output=%q", tc.mode, tc.display, out.String())
			}
			if len(web.calls) != 1 || web.calls[0].ThinkingMode != tc.mode || web.calls[0].Effort != "max" {
				t.Fatalf("request settings changed: %+v", web.calls)
			}
		})
	}
}
