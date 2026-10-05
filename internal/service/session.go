package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"claude2api/internal/config"
	"claude2api/internal/repository"
)

const rootMessageUUID = "00000000-0000-4000-8000-000000000000"

type sessionLock struct {
	sync.Mutex
	refs int
}

var sessionLockMu sync.Mutex
var sessionLocks = map[string]*sessionLock{}
var newAPIClient = NewClaudeAI

func lockSession(key string, try bool) (func(), bool) {
	sessionLockMu.Lock()
	entry := sessionLocks[key]
	if entry == nil {
		entry = &sessionLock{}
		sessionLocks[key] = entry
	}
	entry.refs++
	sessionLockMu.Unlock()
	releaseRef := func() {
		sessionLockMu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(sessionLocks, key)
		}
		sessionLockMu.Unlock()
	}
	if try {
		if !entry.TryLock() {
			releaseRef()
			return nil, false
		}
	} else {
		entry.Lock()
	}
	return func() { entry.Unlock(); releaseRef() }, true
}

func sessionKey(prompt Prompt) string {
	b, _ := json.Marshal([]string{prompt.Scope, prompt.SessionID})
	return string(b)
}

// The persisted fingerprint identifies relay retries, including after restart.
func turnKey(key, model string, prompt Prompt) string {
	keys := make([]string, len(prompt.History))
	for i, m := range prompt.History {
		keys[i] = m.Key
	}
	data, _ := json.Marshal(struct {
		Session, Model, Policy, Continuation, RequestID string
		History                                         []string
		MaxTokens                                       int
		Stop                                            []string
		Temperature, TopP                               *float64
		ThinkingMode, ThinkingDisplay, Effort           string
	}{key, model, prompt.Policy, prompt.Continuation, prompt.RequestID, keys, prompt.MaxTokens, prompt.Stop, prompt.Temperature, prompt.TopP, prompt.ThinkingMode, prompt.ThinkingDisplay, prompt.Effort})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func continuationStart(session repository.ChatSession, prompt Prompt, model string) (int, bool) {
	if session.ConversationID == "" || session.ParentUUID == "" || session.ParentUUID == rootMessageUUID || session.Model != model || session.Policy != prompt.Policy {
		return 0, false
	}
	var history []HistoryMessage
	if json.Unmarshal(session.History, &history) != nil || len(prompt.History) <= len(history) {
		return 0, false
	}
	for i, m := range history {
		if m.Key != prompt.History[i].Key {
			return 0, false
		}
	}
	boundary := prompt.History[len(history)]
	if boundary.Role != "assistant" || boundary.Key != session.ReplyKey {
		return 0, false
	}
	start := len(history) + 1
	if start >= len(prompt.History) {
		return 0, false
	}
	for _, m := range prompt.History[start:] {
		if m.Role != "user" && m.Role != "tool" {
			return 0, false
		}
	}
	return start, true
}

func incrementalPrompt(prompt Prompt, start int) Prompt {
	var parts []string
	if start > 0 && prompt.History[start-1].ToolIDs != "" {
		parts = append(parts, prompt.History[start-1].ToolIDs)
	}
	prompt.Images = nil
	for _, m := range prompt.History[start:] {
		if m.Text != "" {
			parts = append(parts, m.Text)
		}
		prompt.Images = append(prompt.Images, m.Images...)
	}
	parts = append(parts, prompt.Continuation)
	prompt.Text = strings.Join(parts, "\n\n")
	return prompt
}

func pickSessionAccount(model string, excluded map[string]bool) *repository.Account {
	accounts := repository.LoadAccounts()
	usable := make([]repository.Account, 0, len(accounts))
	for _, account := range accounts {
		if !AccountUsable(&account) || excluded[account.Email] {
			continue
		}
		if repository.GetAccountCooldown(account.Email, model).Until.After(time.Now()) {
			continue
		}
		usable = append(usable, account)
	}
	apiIndexLock.Lock()
	defer apiIndexLock.Unlock()
	if len(usable) == 0 {
		return nil
	}
	account := usable[apiIndex%len(usable)]
	apiIndex++
	return &account
}

func waitForRateLimit(ctx context.Context, until time.Time, maxSeconds int) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	delay := time.Until(until)
	if delay <= 0 {
		return true
	}
	if maxSeconds <= 0 || delay > time.Duration(maxSeconds)*time.Second {
		return false
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func (dispatcher Dispatcher) Complete(reqModel string, prompt Prompt, onText func(string)) (CompletionResult, error) {
	model, mode := resolveReasoning(reqModel, prompt)
	prompt.ThinkingMode = mode
	s := config.Get()
	if !s.SessionReuse || prompt.SessionID == "" || len(prompt.History) == 0 {
		return dispatcher.completeStateless(reqModel, prompt, onText)
	}
	key := sessionKey(prompt)
	unlock, _ := lockSession(key, false)
	defer unlock()
	res := CompletionResult{SessionID: prompt.SessionID, UpstreamModel: model, ThinkingMode: mode, Effort: prompt.Effort}
	fail := func(code int, err error) (CompletionResult, error) {
		if code < 400 {
			code = 502
		}
		res.StatusCode = code
		return res, &CompletionError{StatusCode: code, Err: err}
	}
	requestKey := turnKey(key, reqModel, prompt)
	if prompt.TurnID != nil {
		*prompt.TurnID = requestKey[:24]
	}
	previous, found, err := repository.LoadChatTurn(requestKey)
	if err != nil {
		return fail(500, fmt.Errorf("读取请求状态失败: %w", err))
	}
	if found && previous.Status == "completed" {
		res.Account, res.ConversationID, res.ParentUUID, res.MessageUUID = previous.Account, previous.ConversationID, previous.ParentUUID, previous.MessageUUID
		res.StatusCode, res.Action, res.InputBytes = 200, "replay", 0
		if onText != nil {
			onText(previous.Response)
		}
		if session, e := repository.LoadChatSession(key); e == nil && session.ConversationID != "" {
			if e = repository.SaveChatSession(&session); e != nil {
				return fail(500, e)
			}
		}
		return res, nil
	}
	if found && (previous.Status == "pending" || previous.Status == "interrupted") {
		res.Account, res.ConversationID, res.Action = previous.Account, previous.ConversationID, "interrupted"
		return fail(409, errors.New("上一轮已提交但未完整完成，已阻止重复提交；请用新的 Idempotency-Key 发起重建请求"))
	}
	session, err := repository.LoadChatSession(key)
	if err != nil {
		return fail(500, err)
	}
	if !session.UpdatedAt.IsZero() && time.Since(session.UpdatedAt) > time.Duration(s.SessionIdleSeconds)*time.Second {
		session.ParentUUID = ""
	}
	start, reuse := continuationStart(session, prompt, reqModel)
	targetReady := reuse
	if found && previous.Status == "failed" && previous.ConversationID == session.ConversationID && session.ParentUUID == rootMessageUUID && session.Model == reqModel && session.Policy == prompt.Policy {
		targetReady = true
		res.Action = "new"
	}
	think := mode == "extended"
	excluded := map[string]bool{}
	account := AccountByEmail(session.Account)
	if account != nil {
		cooldown := repository.GetAccountCooldown(account.Email, model)
		if cooldown.Until.After(time.Now()) {
			if cooldown.Reason == "rate" {
				if !waitForRateLimit(prompt.Context, cooldown.Until, s.RateLimitWaitSeconds) {
					return fail(429, errors.New("绑定账号暂时限速，请稍后重试"))
				}
			} else {
				excluded[account.Email] = true
				account = nil
				res.SwitchReason = "quota"
			}
		}
	}
	if session.Account != "" && account == nil && res.SwitchReason == "" {
		res.SwitchReason = "account_unavailable"
	}
	maxAttempts := len(repository.LoadAccounts()) + min(s.RetryCount, 8) + 2
	rateRetried := false
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if account == nil {
			account = pickSessionAccount(model, excluded)
		}
		if account == nil {
			return fail(429, errors.New("号池没有可用账号（账号失效或额度冷却中）"))
		}
		res.Account = account.Email
		if session.Account != account.Email {
			reuse, targetReady = false, false
		}
		lease := clientFor(account, s.Proxy)
		lease.Lock()
		client := lease.ClaudeAI
		if !lease.ready {
			err = client.WarmUp()
			if err == nil {
				lease.ready = true
			}
		}
		if err == nil && client.orgUUID == "" {
			var info *UserInfo
			info, err = client.GetUserInfo()
			if err == nil && info.OrgUUID != "" {
				repository.UpdateAccount(account.Email, func(a *repository.Account) { a.OrgUUID = info.OrgUUID })
			}
		}
		if err == nil {
			err = client.SetThinking(think)
		}
		if err != nil {
			lease.Unlock()
			var upstream *webError
			if errors.As(err, &upstream) && (upstream.Kind == "auth" || upstream.Kind == "quota") {
				if e := coolAccount(account, model, upstream); e != nil {
					return fail(500, e)
				}
				excluded[account.Email] = true
				account = nil
				reuse, targetReady = false, false
				res.SwitchReason = upstream.Kind
				err = nil
				continue
			}
			return fail(502, err)
		}
		if targetReady && session.OrgUUID != client.orgUUID {
			reuse, targetReady = false, false
		}
		if !targetReady {
			var convID string
			convID, err = client.CreateConversation(model, think)
			if err != nil {
				lease.Unlock()
				var upstream *webError
				if errors.As(err, &upstream) && (upstream.Kind == "auth" || upstream.Kind == "quota") {
					if e := coolAccount(account, model, upstream); e != nil {
						return fail(500, e)
					}
					excluded[account.Email] = true
					account = nil
					res.SwitchReason = upstream.Kind
					err = nil
					continue
				}
				return fail(502, err)
			}
			session.Account, session.OrgUUID, session.ConversationID, session.ParentUUID = account.Email, client.orgUUID, convID, rootMessageUUID
			session.Model, session.Policy = reqModel, prompt.Policy
			if err = repository.SaveChatConversation(repository.ChatConversation{ID: convID, SessionKey: key, Account: account.Email, OrgUUID: client.orgUUID}); err != nil {
				lease.Unlock()
				return fail(500, err)
			}
			if err = repository.SaveChatSession(&session); err != nil {
				lease.Unlock()
				return fail(500, err)
			}
			targetReady = true
			res.Action = "new"
			if len(session.History) > 0 {
				res.Action = "rebuild"
			}
			if res.SwitchReason != "" {
				res.Action = "switch"
			}
		} else if reuse {
			res.Action = "continue"
		}
		outgoing := prompt
		if reuse {
			outgoing = incrementalPrompt(prompt, start)
		}
		outgoing.ParentUUID = session.ParentUUID
		res.ConversationID, res.ParentUUID, res.InputBytes = session.ConversationID, session.ParentUUID, len(outgoing.Text)
		var files []string
		if len(outgoing.Images) > 0 {
			files, err = client.UploadFile(outgoing.Images)
		}
		if err != nil {
			lease.Unlock()
			return fail(400, err)
		}
		var attachments []map[string]any
		if !outgoing.ForceInline && len(outgoing.Text) > s.MaxChatHistoryLength {
			attachments = client.BigContextAttachment(outgoing.Text)
			outgoing.Text = "Continue the conversation using the new input in context.txt. Return only the next assistant response in its specified format."
		}
		turn := repository.ChatTurn{Key: requestKey, SessionKey: key, Status: "pending", Account: account.Email, OrgUUID: client.orgUUID, ConversationID: session.ConversationID, ParentUUID: session.ParentUUID, InputBytes: res.InputBytes}
		if err = repository.SaveChatSession(&session); err == nil {
			err = repository.SaveChatTurn(&turn)
		}
		if err != nil {
			lease.Unlock()
			return fail(500, err)
		}
		var persistErr error
		outgoing.OnMessageID = func(id string) { turn.MessageUUID = id; persistErr = repository.SaveChatTurn(&turn) }
		var output strings.Builder
		code, sendErr := client.SendMessage(session.ConversationID, model, outgoing, attachments, files, func(text string) {
			output.WriteString(text)
			if onText != nil {
				onText(text)
			}
		})
		lease.Unlock()
		res.MessageUUID = turn.MessageUUID
		var upstream *webError
		if errors.As(sendErr, &upstream) {
			code = upstream.Status
		}
		if sendErr == nil && persistErr != nil {
			sendErr = fmt.Errorf("保存消息编号失败: %w", persistErr)
		}
		if sendErr == nil && turn.MessageUUID == "" {
			sendErr = errors.New("上游完成但缺少真实消息 UUID，无法续聊")
		}
		if sendErr == nil {
			session.Model, session.Policy, session.ParentUUID, session.LastTurn = reqModel, prompt.Policy, turn.MessageUUID, requestKey
			session.History, _ = json.Marshal(prompt.History)
			if prompt.ReplyKey != nil {
				session.ReplyKey = prompt.ReplyKey(output.String())
			}
			turn.Status, turn.Response, turn.StatusCode = "completed", output.String(), 200
			if err = repository.CommitChatTurn(&session, &turn); err != nil {
				return fail(500, err)
			}
			res.StatusCode = 200
			slog.Info("[API] 网页会话完成", "action", res.Action, "email", res.Account, "conversation", res.ConversationID, "parent", res.ParentUUID, "message", res.MessageUUID, "input_bytes", res.InputBytes, "switch_reason", res.SwitchReason)
			return res, nil
		}
		turn.Response, turn.Error, turn.StatusCode = output.String(), sendErr.Error(), code
		turn.Status = "failed"
		if output.Len() > 0 || code == 200 || (code >= 500 && upstream == nil) {
			turn.Status = "interrupted"
			session.ParentUUID = ""
		}
		if err = repository.CommitChatTurn(&session, &turn); err != nil {
			return fail(500, err)
		}
		if output.Len() > 0 {
			return fail(code, sendErr)
		}
		if upstream != nil && (upstream.Kind == "quota" || upstream.Kind == "auth") {
			if err = coolAccount(account, model, upstream); err != nil {
				return fail(500, err)
			}
			excluded[account.Email] = true
			account = nil
			reuse, targetReady = false, false
			res.SwitchReason = upstream.Kind
			err = nil
			continue
		}
		if upstream != nil && upstream.Kind == "conversation" && reuse {
			reuse, targetReady = false, false
			res.SwitchReason = "conversation_missing"
			err = nil
			continue
		}
		if upstream != nil && upstream.Kind == "rate" {
			until := upstream.RetryAt
			if !until.After(time.Now()) {
				until = time.Now().Add(time.Second)
			}
			if err = repository.SetAccountCooldown(account.Email, model, "rate", until); err != nil {
				return fail(500, err)
			}
			if !rateRetried && waitForRateLimit(prompt.Context, until, s.RateLimitWaitSeconds) {
				rateRetried = true
				err = nil
				continue
			}
		}
		return fail(code, sendErr)
	}
	return fail(429, errors.New("可用账号已尝试完，请等待额度恢复"))
}

func coolAccount(account *repository.Account, model string, err *webError) error {
	if err.Kind == "auth" {
		if config.Get().RemoveInvalidAccount {
			repository.DeleteAccount(account.Email)
		} else {
			repository.UpdateAccount(account.Email, func(a *repository.Account) { a.Status = "expired" })
		}
		return nil
	}
	until := err.RetryAt
	if !until.After(time.Now()) {
		until = time.Now().Add(time.Duration(config.Get().QuotaCooldownSeconds) * time.Second)
	}
	return repository.SetAccountCooldown(account.Email, model, "quota", until)
}

func StartSessionJanitor() {
	go func() {
		timer := time.NewTicker(time.Minute)
		defer timer.Stop()
		for range timer.C {
			if err := cleanIdleSessions(time.Now()); err != nil {
				slog.Warn("[API] 清理闲置会话失败", "err", err)
			}
		}
	}()
}

func cleanIdleSessions(now time.Time) error {
	s := config.Get()
	rows, err := repository.IdleChatSessions(now.Add(-time.Duration(s.SessionIdleSeconds) * time.Second))
	if err != nil {
		return err
	}
	for _, candidate := range rows {
		unlock, ok := lockSession(candidate.Key, true)
		if !ok {
			continue
		}
		err = func() error {
			defer unlock()
			row, e := repository.LoadChatSession(candidate.Key)
			if e != nil {
				return e
			}
			if row.UpdatedAt.After(now.Add(-time.Duration(s.SessionIdleSeconds) * time.Second)) {
				return nil
			}
			if s.ChatDelete {
				conversations, e := repository.SessionConversations(row.Key)
				if e != nil {
					return e
				}
				for _, conv := range conversations {
					account := AccountByEmail(conv.Account)
					if account == nil {
						continue
					}
					lease := clientFor(account, s.Proxy)
					lease.Lock()
					if !lease.ready {
						if warmErr := lease.WarmUp(); warmErr != nil {
							lease.Unlock()
							return warmErr
						}
						lease.ready = true
					}
					deleter := ClaudeAI{orgUUID: conv.OrgUUID, client: lease.client, headers: lease.headers}
					deleteErr := deleter.DeleteConversation(conv.ID)
					lease.Unlock()
					if deleteErr != nil {
						return deleteErr
					}
				}
			}
			return repository.DeleteChatSession(row.Key)
		}()
		if err != nil {
			return err
		}
	}
	return nil
}
