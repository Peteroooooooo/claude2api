package adapter

import (
	"encoding/json"
	"fmt"

	"claude2api/internal/service"
)

// The three API protocols carry reasoning settings outside the model ID.
func configureReasoning(raw json.RawMessage, prompt *service.Prompt) error {
	var req struct {
		Thinking *struct {
			Type    string `json:"type"`
			Display string `json:"display"`
		} `json:"thinking"`
		OutputConfig struct {
			Effort string `json:"effort"`
		} `json:"output_config"`
		ReasoningEffort string `json:"reasoning_effort"`
		Reasoning       struct {
			Effort string `json:"effort"`
		} `json:"reasoning"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("无效思考参数: %w", err)
	}
	if req.Thinking != nil {
		switch req.Thinking.Type {
		case "enabled", "adaptive":
			prompt.ThinkingMode = "extended"
		case "disabled":
			prompt.ThinkingMode = "off"
		default:
			return fmt.Errorf("不支持的 thinking.type: %q", req.Thinking.Type)
		}
		prompt.ThinkingDisplay = req.Thinking.Display
	}
	prompt.Effort = req.OutputConfig.Effort
	if prompt.Effort == "" {
		prompt.Effort = req.ReasoningEffort
	}
	if prompt.Effort == "" {
		prompt.Effort = req.Reasoning.Effort
	}
	if prompt.Effort == "none" {
		prompt.Effort, prompt.ThinkingMode = "", "off"
	}
	switch prompt.Effort {
	case "", "low", "medium", "high", "xhigh", "max":
	default:
		return fmt.Errorf("不支持的 effort: %q（网页端支持 low、medium、high、xhigh、max）", prompt.Effort)
	}
	return nil
}
