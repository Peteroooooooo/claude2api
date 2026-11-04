package service

import (
	"context"
	"encoding/json"
	"slices"
	"strings"

	"claude2api/internal/repository"
)

// UserInfo 是账号信息。
type UserInfo struct {
	Email            string
	OrgUUID          string
	SubscriptionPlan string
	Models           []repository.AccountModel
}

type ClaudeAccount struct {
	EmailAddress string `json:"email_address"`
	Memberships  []struct {
		Organization struct {
			UUID             string   `json:"uuid"`
			SubscriptionPlan string   `json:"analytics_subscription_plan"`
			Capabilities     []string `json:"capabilities"`
			Models           []struct {
				ID           string   `json:"model"`
				Name         string   `json:"name"`
				Inactive     bool     `json:"inactive"`
				PaprikaModes []string `json:"paprika_modes"`
				HardLimit    int      `json:"hard_limit"`
			} `json:"claude_ai_bootstrap_models_config"`
		} `json:"organization"`
	} `json:"memberships"`
}

func (account ClaudeAccount) UserInfo() *UserInfo {
	info := &UserInfo{Email: account.EmailAddress, Models: []repository.AccountModel{}}
	if len(account.Memberships) == 0 {
		return info
	}
	// 同一登录可能同时属于 API Console 组织（capabilities 为 api、套餐 api_*）
	// 和 Claude.ai 聊天组织（capabilities 含 chat）。Console 组织同样带模型目录，
	// 但网页对话和镜像访问它会 403，所以优先选聊天组织，其次选非 api_ 套餐。
	organization := account.Memberships[0].Organization
	chosen := false
	for _, membership := range account.Memberships {
		if slices.Contains(membership.Organization.Capabilities, "chat") {
			organization, chosen = membership.Organization, true
			break
		}
	}
	for _, membership := range account.Memberships {
		if chosen {
			break
		}
		if !strings.HasPrefix(membership.Organization.SubscriptionPlan, "api_") {
			organization, chosen = membership.Organization, true
		}
	}
	info.OrgUUID = organization.UUID
	info.SubscriptionPlan = organization.SubscriptionPlan
	for _, model := range organization.Models {
		if !model.Inactive {
			info.Models = append(info.Models, repository.AccountModel{ID: model.ID, Name: model.Name, Thinking: slices.Contains(model.PaprikaModes, "extended"), ContextWindow: model.HardLimit})
		}
	}
	return info
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
	// ContinuationMiss 说明为什么没能续聊（例如 reply_mismatch、history_diverged），
	// 便于从日志判断重建原因。
	ContinuationMiss string
	UpstreamModel    string
	ThinkingMode   string
	Effort         string
	Usage          *repository.UsageRecord
}

type CompletionError struct {
	StatusCode int
	Err        error
}

func (e *CompletionError) Error() string { return e.Err.Error() }
func (e *CompletionError) Unwrap() error { return e.Err }
