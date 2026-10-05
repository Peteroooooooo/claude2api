package service

import (
	"context"
	"encoding/json"
)

// UserInfo 是账号信息。
type UserInfo struct {
	Email   string
	OrgUUID string
}

type ClaudeAccount struct {
	EmailAddress string `json:"email_address"`
	Memberships  []struct {
		Organization struct {
			UUID string `json:"uuid"`
		} `json:"organization"`
	} `json:"memberships"`
}

// SseEvent 是 completion SSE 事件。
type SseEvent struct {
	Type    string `json:"type"`
	Message struct {
		UUID string `json:"uuid"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
	} `json:"content_block"`
	Delta struct {
		Type     string `json:"type"`
		Text     string `json:"text"`
		Thinking string `json:"thinking"`
		Summary  struct {
			Summary string `json:"summary"`
		} `json:"summary"`
		PartialJSON string `json:"partial_json"`
		StopReason  string `json:"stop_reason"`
		StopDetails struct {
			Category    string `json:"category"`
			Explanation string `json:"explanation"`
		} `json:"stop_details"`
	} `json:"delta"`
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}

// Prompt 是协议适配层交给上游实现的输入。
type Prompt struct {
	Text            string
	Images          []string
	RawRequest      json.RawMessage
	ForceInline     bool
	MaxTokens       int
	Temperature     *float64
	TopP            *float64
	Stop            []string
	ThinkingMode    string
	ThinkingDisplay string
	Effort          string
	Context         context.Context
	SessionID       string
	Scope           string
	RequestID       string
	History         []HistoryMessage
	Policy          string
	Continuation    string
	ReplyKey        func(string) string
	TurnID          *string
	ParentUUID      string
	OnMessageID     func(string)
}

// HistoryMessage keeps the client transcript separate from the native web turn.
type HistoryMessage struct {
	Role    string   `json:"role"`
	Key     string   `json:"key"`
	Text    string   `json:"text"`
	Images  []string `json:"images,omitempty"`
	ToolIDs string   `json:"tool_ids,omitempty"`
}

type CompletionResult struct {
	Account        string
	StatusCode     int
	SessionID      string
	ConversationID string
	ParentUUID     string
	MessageUUID    string
	Action         string
	InputBytes     int
	SwitchReason   string
	UpstreamModel  string
	ThinkingMode   string
	Effort         string
}

type CompletionError struct {
	StatusCode int
	Err        error
}

func (e *CompletionError) Error() string { return e.Err.Error() }
func (e *CompletionError) Unwrap() error { return e.Err }
