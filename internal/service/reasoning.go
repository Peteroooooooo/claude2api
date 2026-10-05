package service

import "strings"

func resolveReasoning(reqModel string, prompt Prompt) (string, string) {
	model := strings.TrimSuffix(reqModel, "-thinking")
	mode := prompt.ThinkingMode
	if mode == "" {
		mode = "off"
		if strings.HasSuffix(reqModel, "-thinking") || prompt.Effort != "" {
			mode = "extended"
		}
	}
	return model, mode
}
