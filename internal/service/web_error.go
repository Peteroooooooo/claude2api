package service

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"claude2api/internal/utils"
)

type webError struct {
	Status  int
	Kind    string
	Message string
	RetryAt time.Time
}

func (e *webError) Error() string { return fmt.Sprintf("Claude.ai HTTP %d: %s", e.Status, e.Message) }

func webResponseError(status int, body []byte, retryAfter string) *webError {
	message := string(body)
	var data map[string]any
	_ = json.Unmarshal(body, &data)
	if nested, ok := data["error"].(map[string]any); ok {
		data = nested
	}
	kind, _ := data["type"].(string)
	if text, ok := data["message"].(string); ok && text != "" {
		message = text
	}
	lower := strings.ToLower(kind + " " + message)
	e := &webError{Status: status, Kind: "other", Message: utils.Truncate(message, 500)}
	switch {
	case status == 401 || strings.Contains(lower, "account_session_invalid") || strings.Contains(lower, "authentication_error"):
		e.Kind = "auth"
	case strings.Contains(lower, "exceeded_limit") || strings.Contains(lower, "quota") || strings.Contains(lower, "usage_limit") || strings.Contains(lower, "usage limit") || strings.Contains(lower, "message_limit") || strings.Contains(lower, "message limit") || strings.Contains(lower, "weekly") || strings.Contains(lower, "reached your") || strings.Contains(lower, "limit for"):
		e.Kind, e.Status = "quota", 429
	case status == 429 || strings.Contains(lower, "rate_limit") || strings.Contains(lower, "rate limit"):
		e.Kind, e.Status = "rate", 429
	case status == 404 || strings.Contains(lower, "conversation_not_found"):
		e.Kind = "conversation"
	}
	if seconds, err := strconv.Atoi(retryAfter); err == nil && seconds > 0 {
		e.RetryAt = time.Now().Add(time.Duration(seconds) * time.Second)
	} else if at, err := http.ParseTime(retryAfter); err == nil {
		e.RetryAt = at
	}
	for _, key := range []string{"resets_at", "resetsAt", "reset_at", "resetAt"} {
		switch value := data[key].(type) {
		case float64:
			if value > 1e12 {
				value /= 1000
			}
			e.RetryAt = time.Unix(int64(value), 0)
		case string:
			if at, err := time.Parse(time.RFC3339, value); err == nil {
				e.RetryAt = at
			}
		}
	}
	return e
}
