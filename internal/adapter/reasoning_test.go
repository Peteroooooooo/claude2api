package adapter

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"claude2api/internal/service"
	"github.com/gin-gonic/gin"
)

func TestReasoningParametersReachAllProtocols(t *testing.T) {
	old := runner
	defer func() { runner = old }()
	cases := []struct {
		name, body string
		handler    gin.HandlerFunc
	}{
		{"messages", `{"model":"claude-sonnet-5-5","thinking":{"type":"adaptive","display":"updates"},"output_config":{"effort":"max"},"messages":[{"role":"user","content":"hello"}]}`, AnthropicMessages},
		{"messages-medium", `{"model":"claude-sonnet-5-5-thinking","thinking":{"type":"disabled"},"output_config":{"effort":"medium"},"messages":[{"role":"user","content":"hello"}]}`, AnthropicMessages},
		{"chat", `{"model":"claude-sonnet-5-5","reasoning_effort":"max","messages":[{"role":"user","content":"hello"}]}`, OpenAIChat},
		{"responses", `{"model":"claude-sonnet-5-5","reasoning":{"effort":"max"},"input":"hello"}`, OpenAIResponses},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			runner = testRunner(func(model string, p service.Prompt, emit func(string)) (service.CompletionResult, error) {
				called = true
				wantModel, wantEffort := "claude-sonnet-5-5", "max"
				if tc.name == "messages-medium" {
					wantModel, wantEffort = "claude-sonnet-5-5-thinking", "medium"
				}
				if model != wantModel || p.Effort != wantEffort {
					t.Fatalf("lost parameters: model=%s effort=%s", model, p.Effort)
				}
				if tc.name == "messages" && (p.ThinkingMode != "extended" || p.ThinkingDisplay != "updates") {
					t.Fatalf("thinking lost: %+v", p)
				}
				if tc.name == "messages-medium" && p.ThinkingMode != "off" {
					t.Fatal("explicit thinking disabled was lost")
				}
				emit("SAVED")
				return service.CompletionResult{StatusCode: 200}, nil
			})
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("POST", "/v1/"+tc.name, strings.NewReader(tc.body))
			tc.handler(c)
			if !called || w.Code != 200 {
				t.Fatalf("status=%d body=%s called=%v", w.Code, w.Body.String(), called)
			}
		})
	}
}

func TestReasoningDoesNotSilentlyIgnoreInvalidValues(t *testing.T) {
	for _, raw := range []string{`{"thinking":{"type":"invalid"}}`, `{"output_config":{"effort":"invalid"}}`, `{"thinking":"enabled"}`} {
		if err := configureReasoning(json.RawMessage(raw), &service.Prompt{}); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	var p service.Prompt
	if err := configureReasoning(json.RawMessage(`{"thinking":{"type":"disabled"},"output_config":{"effort":"low"}}`), &p); err != nil {
		t.Fatal(err)
	}
	if p.ThinkingMode != "off" || p.Effort != "low" {
		t.Fatalf("explicit disabled lost: %+v", p)
	}
}
