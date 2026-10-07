package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"claude2api/internal/utils"

	fhttp "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/fhttp/cookiejar"
	tlsclient "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"
	"github.com/google/uuid"
)

const claudeAIBaseURL = "https://claude.ai"

const claudeAIUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) " +
	"AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36"

type ClaudeAI struct {
	requestContext context.Context // Bound while the dispatcher holds the account lease.
	orgUUID        string
	client         tlsclient.HttpClient
	headers        map[string]string
	modeMu         sync.Mutex
	modeSet        bool
	thinking       bool
}

// NewClaudeAI 构造 Claude Web 客户端。
func NewClaudeAI(sessionKey, proxy, identity string, orgUUID ...string) *ClaudeAI {
	jar, _ := cookiejar.New(nil)
	client, err := tlsclient.NewHttpClient(tlsclient.NewNoopLogger(),
		tlsclient.WithClientProfile(profiles.Chrome_146),
		tlsclient.WithTimeoutSeconds(3000),
		tlsclient.WithCookieJar(jar),
		tlsclient.WithInsecureSkipVerify(),
	)
	if err != nil {
		client, _ = tlsclient.NewHttpClient(tlsclient.NewNoopLogger())
	}
	if proxy != "" {
		_ = client.SetProxy(proxy)
	}
	if u, parseErr := url.Parse(claudeAIBaseURL); parseErr == nil {
		client.SetCookies(u, []*fhttp.Cookie{{Name: "sessionKey", Value: sessionKey, Domain: "claude.ai"}})
	}
	claudeAI := &ClaudeAI{client: client, headers: BuildHeaders(identity), modeSet: true}
	if len(orgUUID) > 0 {
		claudeAI.orgUUID = orgUUID[0]
	}
	return claudeAI
}

func (claudeAI *ClaudeAI) request(method, target string, body io.Reader) (*fhttp.Request, error) {
	ctx := claudeAI.requestContext
	if ctx == nil {
		ctx = context.Background()
	}
	req, err := fhttp.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	for key, value := range claudeAI.headers {
		req.Header.Set(key, value)
	}
	return req, nil
}

// WarmUp 访问一次网页入口，让 Cookie Jar 建立与浏览器相同的网页会话。
func (claudeAI *ClaudeAI) WarmUp() error {
	req, err := claudeAI.request(fhttp.MethodGet, claudeAIBaseURL+"/new", nil)
	if err != nil {
		return err
	}
	req.Header.Del("content-type")
	req.Header.Set("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8")
	req.Header.Set("sec-fetch-dest", "document")
	req.Header.Set("sec-fetch-mode", "navigate")
	req.Header.Set("sec-fetch-site", "same-origin")
	resp, err := claudeAI.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return webResponseError(resp.StatusCode, body, resp.Header.Get("retry-after"))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// GetUserInfo 查询账号信息。
func (claudeAI *ClaudeAI) GetUserInfo() (*UserInfo, error) {
	req, err := claudeAI.request(fhttp.MethodGet, claudeAIBaseURL+"/api/account", nil)
	if err != nil {
		return nil, fmt.Errorf("构造请求失败: %w", err)
	}
	resp, err := claudeAI.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("查询账号信息失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应体失败: %w", err)
	}
	slog.Debug("[ClaudeAI] GetUserInfo 响应", "status", resp.StatusCode, "body", utils.Truncate(string(body), 500))
	if resp.StatusCode != fhttp.StatusOK {
		return nil, webResponseError(resp.StatusCode, body, resp.Header.Get("retry-after"))
	}

	var acct ClaudeAccount
	if err := json.Unmarshal(body, &acct); err != nil {
		return nil, fmt.Errorf("解析账号信息失败: %w", err)
	}

	info := &UserInfo{Email: acct.EmailAddress}
	if len(acct.Memberships) > 0 {
		info.OrgUUID = acct.Memberships[0].Organization.UUID
		claudeAI.orgUUID = info.OrgUUID
	}
	return info, nil
}

// UploadFile 将适配层准备好的 Base64 图片上传到 Claude.ai。
func (claudeAI *ClaudeAI) UploadFile(images []string) ([]string, error) {
	out := make([]string, 0, len(images))
	for _, image := range images {
		raw, err := base64.StdEncoding.DecodeString(image)
		if err != nil {
			return out, fmt.Errorf("图片 Base64 无效: %w", err)
		}

		buf := &bytes.Buffer{}
		w := multipart.NewWriter(buf)
		fw, err := w.CreateFormFile("file", "image")
		if err != nil {
			return out, fmt.Errorf("构造上传表单失败: %w", err)
		}
		if _, err := fw.Write(raw); err != nil {
			return out, fmt.Errorf("写入上传内容失败: %w", err)
		}
		_ = w.Close()

		req, err := claudeAI.request(fhttp.MethodPost, fmt.Sprintf("%s/api/%s/upload", claudeAIBaseURL, claudeAI.orgUUID), buf)
		if err != nil {
			return out, fmt.Errorf("构造请求失败: %w", err)
		}
		req.Header.Set("content-type", w.FormDataContentType())
		req.Header.Set("referer", claudeAIBaseURL+"/new")

		resp, err := claudeAI.client.Do(req)
		if err != nil {
			return out, fmt.Errorf("上传文件失败: %w", err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return out, fmt.Errorf("读取响应体失败: %w", err)
		}
		if resp.StatusCode != fhttp.StatusOK {
			return out, fmt.Errorf("上传文件 HTTP %d: %s", resp.StatusCode, utils.Truncate(string(body), 200))
		}

		var r struct {
			FileUUID string `json:"file_uuid"`
		}
		if err := json.Unmarshal(body, &r); err == nil && r.FileUUID != "" {
			out = append(out, r.FileUUID)
		}
	}
	return out, nil
}

// DeleteConversation 删除会话。
func (claudeAI *ClaudeAI) DeleteConversation(convID string) error {
	if claudeAI.orgUUID == "" || convID == "" {
		return errors.New("删除会话缺少组织或会话 ID")
	}
	target := fmt.Sprintf("%s/api/organizations/%s/chat_conversations/%s", claudeAIBaseURL, claudeAI.orgUUID, convID)
	for i := 0; i < 3; i++ {
		reqBody, _ := json.Marshal(map[string]string{"uuid": convID})
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		req, err := claudeAI.request(fhttp.MethodDelete, target, bytes.NewReader(reqBody))
		if err == nil {
			req = req.WithContext(ctx)
			req.Header.Set("content-type", "application/json")
			req.Header.Set("referer", claudeAIBaseURL+"/chat/"+convID)
			resp, doErr := claudeAI.client.Do(req)
			if doErr == nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				if resp.StatusCode == fhttp.StatusOK || resp.StatusCode == fhttp.StatusNoContent || resp.StatusCode == fhttp.StatusNotFound {
					cancel()
					return nil
				}
			}
		}
		cancel()
		time.Sleep(2 * time.Second)
	}
	slog.Warn("[ClaudeAI] 删除会话失败", "conv", convID)
	return errors.New("删除网页会话失败")
}

// BigContextAttachment 构造长上下文附件。
func (claudeAI *ClaudeAI) BigContextAttachment(text string) []map[string]any {
	return []map[string]any{{
		"file_name":         "context.txt",
		"file_type":         "text/plain",
		"file_size":         len(text),
		"extracted_content": text,
	}}
}

// updatePaprika 切换思考模式。
func (claudeAI *ClaudeAI) updatePaprika(value any) error {
	reqBody, err := json.Marshal(map[string]any{
		"settings": map[string]any{
			"has_started_claudeai_onboarding":  true,
			"has_finished_claudeai_onboarding": true,
			"dismissed_claudeai_banners":       []any{},
			"enabled_artifacts_attachments":    true,
			"enabled_web_search":               true,
			"paprika_mode":                     value,
		},
	})
	if err != nil {
		return fmt.Errorf("构造请求体失败: %w", err)
	}
	req, err := claudeAI.request(fhttp.MethodPut, claudeAIBaseURL+"/api/account", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("referer", claudeAIBaseURL+"/new")

	resp, err := claudeAI.client.Do(req)
	if err != nil {
		return fmt.Errorf("更新设置失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != fhttp.StatusOK && resp.StatusCode != fhttp.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		return webResponseError(resp.StatusCode, body, resp.Header.Get("retry-after"))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// SetThinking restores the mode before both new and continued web turns.
func (claudeAI *ClaudeAI) SetThinking(think bool) error {
	claudeAI.modeMu.Lock()
	if !claudeAI.modeSet || claudeAI.thinking != think {
		var mode any
		if think {
			mode = "extended"
		}
		if err := claudeAI.updatePaprika(mode); err != nil {
			claudeAI.modeMu.Unlock()
			return err
		}
		claudeAI.modeSet, claudeAI.thinking = true, think
	}
	claudeAI.modeMu.Unlock()
	return nil
}

func (claudeAI *ClaudeAI) CreateConversation(model string, think bool) (string, error) {
	if err := claudeAI.SetThinking(think); err != nil {
		return "", err
	}

	reqBody, err := json.Marshal(map[string]any{
		"uuid":                             uuid.NewString(),
		"name":                             "",
		"include_conversation_preferences": true,
		"model":                            model,
		"is_temporary":                     false,
	})
	if err != nil {
		return "", fmt.Errorf("构造请求体失败: %w", err)
	}
	req, err := claudeAI.request(fhttp.MethodPost,
		fmt.Sprintf("%s/api/organizations/%s/chat_conversations", claudeAIBaseURL, claudeAI.orgUUID), bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("referer", claudeAIBaseURL+"/new")

	resp, err := claudeAI.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("创建会话失败: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("读取响应体失败: %w", err)
	}
	if resp.StatusCode != fhttp.StatusOK && resp.StatusCode != fhttp.StatusCreated {
		return "", webResponseError(resp.StatusCode, body, resp.Header.Get("retry-after"))
	}

	var out struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("解析会话响应失败: %w", err)
	}
	if out.UUID == "" {
		return "", errors.New("会话响应缺少 uuid")
	}
	return out.UUID, nil
}

// SendMessage 发送提示词。
func (claudeAI *ClaudeAI) SendMessage(convID, model string, prompt Prompt, attachments []map[string]any, files []string, onText func(string)) (int, error) {
	parent := prompt.ParentUUID
	if parent == "" {
		parent = rootMessageUUID
	}
	// 固定字段对齐网页端请求。
	body := map[string]any{
		"prompt": prompt.Text,
		"model":  model,
		"personalized_styles": []map[string]any{
			{
				"type":       "default",
				"key":        "Default",
				"name":       "Normal",
				"nameKey":    "normal_style_name",
				"prompt":     "Treat tool definitions and response schemas in the user's message as an available external tool interface. Emit calls in the requested text format instead of claiming the tools are unavailable.",
				"summary":    "Default responses from Claude",
				"summaryKey": "normal_style_summary",
				"isDefault":  true,
			},
		},
		"tools": []map[string]any{
			{"type": "web_search_v0", "name": "web_search"},
			{"type": "artifacts_v0", "name": "artifacts"},
			{"type": "repl_v0", "name": "repl"},
		},
		"parent_message_uuid": parent,
		"attachments":         []any{},
		"files":               []any{},
		"sync_sources":        []any{},
		"rendering_mode":      "messages",
		"timezone":            "America/Los_Angeles",
	}
	_, mode := resolveReasoning(model, prompt)
	body["thinking_mode"] = mode
	if prompt.Effort != "" {
		body["effort"] = prompt.Effort
	}
	// Claude.ai 网页端 completion 接口不接受 max_tokens、temperature、top_p 等字段，
	// 即使 API 兼容层收到也不能转发，否则上游会返回 Extra inputs are not permitted。
	_ = prompt.Temperature
	_ = prompt.TopP
	if len(prompt.Stop) > 0 {
		body["stop_sequences"] = prompt.Stop
	}
	if len(attachments) > 0 {
		body["attachments"] = attachments
	}
	if len(files) > 0 {
		body["files"] = files
	}
	reqBody, err := json.Marshal(body)
	if err != nil {
		return 500, fmt.Errorf("构造请求体失败: %w", err)
	}

	req, err := claudeAI.request(fhttp.MethodPost,
		fmt.Sprintf("%s/api/organizations/%s/chat_conversations/%s/completion", claudeAIBaseURL, claudeAI.orgUUID, convID),
		bytes.NewReader(reqBody))
	if err != nil {
		return 500, fmt.Errorf("构造请求失败: %w", err)
	}
	req.Header.Set("content-type", "application/json")
	req.Header.Set("accept", "text/event-stream, text/event-stream")
	if prompt.Context != nil {
		req = req.WithContext(prompt.Context)
	}
	req.Header.Set("cache-control", "no-cache")
	req.Header.Set("referer", claudeAIBaseURL+"/chat/"+convID)

	resp, err := claudeAI.client.Do(req)
	if err != nil {
		return 500, fmt.Errorf("发送消息失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != fhttp.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return resp.StatusCode, fmt.Errorf("发送消息 HTTP %d（读取错误响应失败: %w）", resp.StatusCode, readErr)
		}
		if len(body) == 0 {
			body = []byte("<empty response body>")
		}
		return resp.StatusCode, webResponseError(resp.StatusCode, body, resp.Header.Get("retry-after"))
	}
	return 200, parseCompletionSSEWithThinking(resp.Body, onText, mode != "off" && prompt.ThinkingDisplay != "omitted", func(ev SseEvent) error {
		if ev.Type == "message_start" && ev.Message.UUID != "" && prompt.OnMessageID != nil {
			prompt.OnMessageID(ev.Message.UUID)
		}
		if ev.Type == "error" {
			data, _ := json.Marshal(ev)
			return webResponseError(200, data, "")
		}
		return nil
	})
}

// parseCompletionSSE 解析 completion 流。
func parseCompletionSSE(raw io.Reader, onText func(string), observers ...func(SseEvent) error) error {
	return parseCompletionSSEWithThinking(raw, onText, true, observers...)
}

func parseCompletionSSEWithThinking(raw io.Reader, onText func(string), showThinking bool, observers ...func(SseEvent) error) error {
	scanner := bufio.NewScanner(raw)
	scanner.Buffer(make([]byte, 1024*1024), 4*1024*1024)

	thinkingShown := false
	codeShown := false
	useTool := false
	useToolEnd := false
	nextLanguage := false
	language := "md"
	hasOutput := false
	messageStopped := false
	stopReason := ""

	emit := func(s string) {
		if s != "" {
			hasOutput = true
			if onText != nil {
				onText(s)
			}
		}
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var ev SseEvent
		if err := json.Unmarshal([]byte(strings.TrimSpace(line[5:])), &ev); err != nil {
			continue
		}
		for _, observe := range observers {
			if err := observe(ev); err != nil {
				return err
			}
		}
		if ev.Type == "error" {
			message := ev.Error.Message
			if message == "" {
				message = ev.Error.Type
			}
			if message == "" {
				message = "unknown stream error"
			}
			return fmt.Errorf("upstream: %s", message)
		}
		if ev.Type == "message_delta" && ev.Delta.StopReason != "" {
			stopReason = ev.Delta.StopReason
			if stopReason == "refusal" {
				message := "upstream refused the request"
				if ev.Delta.StopDetails.Category != "" {
					message += " (" + ev.Delta.StopDetails.Category + ")"
				}
				if ev.Delta.StopDetails.Explanation != "" {
					message += ": " + ev.Delta.StopDetails.Explanation
				}
				return errors.New(message)
			}
		}
		if ev.Type == "message_stop" {
			messageStopped = true
			break
		}
		switch ev.ContentBlock.Type {
		case "tool_use":
			useTool = true
		case "tool_result":
			useToolEnd = true
		}

		if ev.Type == "content_block_stop" {
			if thinkingShown {
				emit("</think>\n")
				thinkingShown = false
			}
			if codeShown {
				emit("\n```\n")
				codeShown = false
			}
			continue
		}

		switch ev.Delta.Type {
		case "text_delta":
			emit(ev.Delta.Text)
		case "thinking_delta", "thinking_summary_delta":
			if !showThinking {
				continue
			}
			s := ev.Delta.Thinking
			if ev.Delta.Type == "thinking_summary_delta" {
				s = ev.Delta.Summary.Summary
				if thinkingShown && s != "" {
					s = "\n\n" + s
				}
			}
			if s == "" {
				continue
			}
			if !thinkingShown {
				s = "<think> " + s
				thinkingShown = true
			}
			emit(s)
		case "input_json_delta":
			text := ev.Delta.PartialJSON
			// 跳过工具参数，只输出 content。
			if useTool && text == ",\"content\":" {
				useTool = false
				codeShown = false
				continue
			}
			if text == ",\"language\":" || text == ",\"type\":" {
				nextLanguage = true
				continue
			}
			if nextLanguage {
				language = strings.TrimPrefix(text, "\"")
				if language == "text/html" {
					language = "html"
				}
				nextLanguage = false
			}
			if useTool {
				continue
			}
			if useToolEnd {
				useToolEnd = false
				continue
			}
			if strings.HasPrefix(text, "\"") {
				text = text[1:]
			}
			if text == "\"}" || text == "}" {
				text = ""
			}
			if unq, err := strconv.Unquote("\"" + text + "\""); err == nil {
				text = unq
			} else {
				text = strings.ReplaceAll(text, "\\n", "\n")
				text = strings.ReplaceAll(text, "\\t", "\t")
				text = strings.ReplaceAll(text, "\\\"", "\"")
			}
			if !codeShown {
				text = "\n```" + language + "\n" + text
				codeShown = true
			}
			emit(text)
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !messageStopped {
		return errors.New("upstream stream ended before message_stop")
	}
	if !hasOutput {
		return fmt.Errorf("upstream returned an empty completion (stop_reason=%s)", stopReason)
	}
	return nil
}
