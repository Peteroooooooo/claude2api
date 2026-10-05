package adapter

import (
	"encoding/json"
	"fmt"
	"strings"

	"claude2api/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func messageKey(message Message) string {
	if len(message.Images) == 0 {
		message.Images = []string{}
	}
	content := strings.TrimSpace(message.Content)
	calls := make([]map[string]any, 0, len(message.ToolCalls))
	for _, call := range message.ToolCalls {
		calls = append(calls, map[string]any{"name": call.Name, "arguments": decodeArgs(call.Arguments)})
	}
	if len(calls) > 0 && message.Role == "assistant" {
		content = ""
	}
	return string(mustJSON(map[string]any{"role": message.Role, "content": content, "calls": calls, "call_id": message.ToolCallID, "images": message.Images}))
}

func prepareHistory(prompt *service.Prompt, messages []Message, tools []map[string]any, parallel bool, choice json.RawMessage) error {
	var systems []string
	imageOffset := 0
	for _, original := range messages {
		message := original
		if message.Role == "system" || message.Role == "developer" {
			if text := sanitizeSystemPrompt(message.Content); text != "" {
				systems = append(systems, text)
			}
			continue
		}
		end := imageOffset + len(message.Images)
		if end > len(prompt.Images) {
			return fmt.Errorf("图片与消息历史不匹配")
		}
		message.Images = prompt.Images[imageOffset:end]
		imageOffset = end
		bridge := ""
		if len(message.ToolCalls) > 0 {
			bridge = "Client IDs for tool calls in your previous response: " + string(mustJSON(message.ToolCalls))
		}
		entry := service.HistoryMessage{Role: message.Role, Key: messageKey(message), Text: renderMessages([]Message{message}, parallel), Images: message.Images, ToolIDs: bridge}
		// Responses represents parallel calls as adjacent assistant items.
		if message.Role == "assistant" && len(prompt.History) > 0 && prompt.History[len(prompt.History)-1].Role == "assistant" {
			previous := &prompt.History[len(prompt.History)-1]
			var value map[string]any
			_ = json.Unmarshal([]byte(previous.Key), &value)
			var current map[string]any
			_ = json.Unmarshal([]byte(entry.Key), &current)
			oldCalls, _ := value["calls"].([]any)
			newCalls, _ := current["calls"].([]any)
			value["calls"] = append(oldCalls, newCalls...)
			oldContent, _ := value["content"].(string)
			newContent, _ := current["content"].(string)
			value["content"] = strings.TrimSpace(oldContent + "\n\n" + newContent)
			if len(oldCalls)+len(newCalls) > 0 {
				value["content"] = ""
			}
			previous.Key = string(mustJSON(value))
			previous.Text += "\n\n" + entry.Text
			previous.ToolIDs += "\n" + entry.ToolIDs
			continue
		}
		prompt.History = append(prompt.History, entry)
	}
	// Include complete tool schemas so changes cannot silently reuse old instructions.
	prompt.Policy = string(mustJSON(map[string]any{"system": systems, "tools": withoutCacheMarkers(tools), "parallel": parallel}))
	prompt.Continuation = "Continue this existing conversation with only your next assistant response."
	if len(tools) > 0 {
		format := taggedToolPromptSingle
		if parallel {
			format = taggedToolPromptParallel
		}
		prompt.Continuation = format + toolChoiceInstruction(choice)
	}
	return nil
}

func withoutCacheMarkers(value any) any {
	switch v := value.(type) {
	case []map[string]any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = withoutCacheMarkers(x)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = withoutCacheMarkers(x)
		}
		return out
	case map[string]any:
		out := map[string]any{}
		for key, x := range v {
			if key != "cache_control" {
				out[key] = withoutCacheMarkers(x)
			}
		}
		return out
	default:
		return value
	}
}

func configureSession(c *gin.Context, prompt *service.Prompt, endpoint string, hasTools bool) {
	var body struct {
		Metadata       map[string]json.RawMessage `json:"metadata"`
		SessionID      string                     `json:"session_id"`
		PromptCacheKey string                     `json:"prompt_cache_key"`
	}
	_ = json.Unmarshal(prompt.RawRequest, &body)
	id := strings.TrimSpace(c.GetHeader("X-Claude2API-Session-ID"))
	if id == "" {
		id = strings.TrimSpace(c.GetHeader("X-Session-ID"))
	}
	if id == "" {
		id = body.SessionID
	}
	if id == "" {
		_ = json.Unmarshal(body.Metadata["session_id"], &id)
	}
	identity := ""
	if id == "" {
		var userID string
		_ = json.Unmarshal(body.Metadata["user_id"], &userID)
		var user struct {
			SessionID   string `json:"session_id"`
			DeviceID    string `json:"device_id"`
			AccountUUID string `json:"account_uuid"`
		}
		if json.Unmarshal([]byte(userID), &user) == nil && user.SessionID != "" {
			id = user.SessionID
			identity = user.DeviceID + ":" + user.AccountUUID
		} else if _, session, found := strings.Cut(userID, "_session_"); found && session != "" {
			id = session
			identity = strings.Split(userID, "_session_")[0]
		}
	}
	if id == "" {
		id = body.PromptCacheKey
	}
	if id == "" {
		id = uuid.NewString()
	}
	prompt.SessionID = id
	prompt.Scope = c.GetString("api_scope") + ":" + identity
	prompt.RequestID = c.GetHeader("Idempotency-Key")
	if prompt.RequestID == "" {
		prompt.RequestID = c.GetHeader("X-Idempotency-Key")
	}
	prompt.Context = c.Request.Context()
	turnID := shortID()
	prompt.TurnID = &turnID
	prompt.Policy = endpoint + "\n" + prompt.Policy
	prompt.ReplyKey = func(raw string) string {
		message := Message{Role: "assistant", Content: raw}
		if hasTools {
			parsed := ParseTaggedOutputTolerant(raw)
			for _, call := range parsed.ToolCalls {
				message.ToolCalls = append(message.ToolCalls, ToolCall{Name: call.Name, Arguments: argsJSON(call.Arguments)})
			}
			if endpoint == "messages" {
				message.Content = strings.TrimSpace(strings.Join([]string{parsed.Thinking, parsed.FinalAnswer}, "\n\n"))
			} else {
				message.Content = formatOpenAITaggedAnswer(parsed)
			}
		} else {
			filter := outputFilter{stop: prompt.Stop, maxBytes: prompt.MaxTokens * 4}
			message.Content = filter.push(raw, true)
		}
		if endpoint == "responses" {
			message.Content = stripThinkingTags(message.Content)
		}
		return messageKey(message)
	}
	c.Header("X-Claude2API-Session-ID", id)
}

func stableToolID(prompt service.Prompt, index int, anthropic bool) string {
	prefix := "call_"
	if anthropic {
		prefix = "toolu_"
	}
	if prompt.TurnID != nil {
		return fmt.Sprintf("%s%s_%d", prefix, *prompt.TurnID, index)
	}
	return prefix + shortID()
}
