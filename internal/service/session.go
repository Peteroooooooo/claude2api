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
	"claude2api/internal/utils"
)

const rootMessageUUID = "00000000-0000-4000-8000-000000000000"

type sessionLock struct {
	contextMutex
	refs int
}

var sessionLockMu sync.Mutex
var sessionLocks = map[string]*sessionLock{}
var newAPIClient = NewClaudeAI

func lockSession(key string, try bool) (func(), bool) {
	return lockSessionContext(context.Background(), key, try)
}

func lockSessionContext(ctx context.Context, key string, try bool) (func(), bool) {
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
		if entry.LockContext(ctx) != nil {
			releaseRef()
			return nil, false
		}
	}
	return func() { entry.Unlock(); releaseRef() }, true
}

func sessionKey(prompt Prompt) string {
	// Policy（endpoint + system + tools）参与会话键，让同一个 session_id 下的不同
	// 请求流各自绑定独立的网页会话：Claude Code 主代理与子代理（system/tools 不同）
	// 常交替复用同一个 session_id，若共用一条绑定就会互相覆盖 ParentUUID 和历史，
	// 导致双方都无法续聊、每轮都重建。用 Policy 的哈希区分，互不干扰。
	sum := sha256.Sum256([]byte(prompt.Policy))
	b, _ := json.Marshal([]string{prompt.Scope, prompt.SessionID, hex.EncodeToString(sum[:8])})
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

// sessionAnchor 是网页会话里一个“可接续的节点”：某一轮成功完成的回复。
// Upto 是这条回复在客户端历史中的下标；Prefix/Hash 分别是该回复之前、
// 以及包含该回复在内的历史滚动哈希。只要客户端发来的历史在 Upto 处与某个
// 锚点哈希一致，就能把新消息挂在这条回复下面续接——不一定是最新那条，
// 客户端编辑或回退了后面的消息时就从更早的节点分叉（claude.ai 会话本身是树）。
type sessionAnchor struct {
	Upto   int    `json:"upto"`
	Prefix string `json:"prefix"`
	Hash   string `json:"hash"`
	UUID   string `json:"uuid"`
}

const maxSessionAnchors = 64

func chainHash(previous, key string) string {
	keySum := sha256.Sum256([]byte(key))
	sum := sha256.Sum256([]byte(previous + hex.EncodeToString(keySum[:])))
	return hex.EncodeToString(sum[:])
}

// historyChain 返回每个前缀的滚动哈希：chain[i] 覆盖 History[0..i]。
func historyChain(history []HistoryMessage) []string {
	chain := make([]string, len(history))
	previous := ""
	for i, m := range history {
		previous = chainHash(previous, m.Key)
		chain[i] = previous
	}
	return chain
}

func loadAnchors(session repository.ChatSession) []sessionAnchor {
	var anchors []sessionAnchor
	if len(session.Anchors) > 0 && json.Unmarshal(session.Anchors, &anchors) == nil && len(anchors) > 0 {
		return anchors
	}
	// 兼容升级前的会话：由最近一轮的 History + ReplyKey 推出唯一锚点。
	if session.ParentUUID == "" || session.ParentUUID == rootMessageUUID || session.ReplyKey == "" {
		return nil
	}
	var history []HistoryMessage
	if json.Unmarshal(session.History, &history) != nil {
		return nil
	}
	prefix := ""
	if chain := historyChain(history); len(chain) > 0 {
		prefix = chain[len(chain)-1]
	}
	return []sessionAnchor{{Upto: len(history), Prefix: prefix, Hash: chainHash(prefix, session.ReplyKey), UUID: session.ParentUUID}}
}

func appendAnchor(session *repository.ChatSession, prompt Prompt, replyKey, messageUUID string) {
	anchors := loadAnchors(*session)
	prefix := ""
	if chain := historyChain(prompt.History); len(chain) > 0 {
		prefix = chain[len(chain)-1]
	}
	next := sessionAnchor{Upto: len(prompt.History), Prefix: prefix, Hash: chainHash(prefix, replyKey), UUID: messageUUID}
	kept := anchors[:0]
	for _, a := range anchors {
		if a.Hash != next.Hash {
			kept = append(kept, a)
		}
	}
	kept = append(kept, next)
	if len(kept) > maxSessionAnchors {
		kept = kept[len(kept)-maxSessionAnchors:]
	}
	session.Anchors, _ = json.Marshal(kept)
}

// continuationStart 在已知锚点里找与本次历史一致、位置最靠后的那个。
// 返回新消息起始下标、要挂接的父消息 UUID；无法续接时返回原因。
func continuationStart(session repository.ChatSession, prompt Prompt, model string) (int, string, string) {
	switch {
	case session.ConversationID == "":
		return 0, "", "no_session"
	case session.Model != model:
		return 0, "", "model_changed"
	case session.Policy != prompt.Policy:
		return 0, "", "policy_changed"
	}
	anchors := loadAnchors(session)
	if len(anchors) == 0 {
		return 0, "", "no_anchor"
	}
	chain := historyChain(prompt.History)
	tailOK := func(start int) bool {
		if start >= len(prompt.History) {
			return false
		}
		for _, m := range prompt.History[start:] {
			if m.Role != "user" && m.Role != "tool" {
				return false
			}
		}
		return true
	}
	best := -1
	for i, a := range anchors {
		if a.UUID == "" || a.Upto >= len(prompt.History) || chain[a.Upto] != a.Hash || prompt.History[a.Upto].Role != "assistant" || !tailOK(a.Upto+1) {
			continue
		}
		if best < 0 || a.Upto > anchors[best].Upto {
			best = i
		}
	}
	if best >= 0 {
		return anchors[best].Upto + 1, anchors[best].UUID, ""
	}
	// 给出未命中原因，方便从日志定位重建的根因：取“回复之前的历史仍一致”且位置
	// 最靠后的锚点，看是卡在回复本身、没有新输入，还是尾部夹了 assistant。
	closest := -1
	shorter := false
	for i, a := range anchors {
		if a.Upto >= len(prompt.History) {
			shorter = true
			continue
		}
		if (a.Upto == 0 && a.Prefix == "") || (a.Upto > 0 && chain[a.Upto-1] == a.Prefix) {
			if closest < 0 || a.Upto > anchors[closest].Upto {
				closest = i
			}
		}
	}
	if closest < 0 {
		if shorter {
			return 0, "", "history_shorter"
		}
		return 0, "", "history_diverged"
	}
	a := anchors[closest]
	switch {
	case prompt.History[a.Upto].Role != "assistant" || chain[a.Upto] != a.Hash:
		return 0, "", "reply_mismatch"
	case a.Upto+1 >= len(prompt.History):
		return 0, "", "no_new_input"
	default:
		return 0, "", "assistant_in_tail"
	}
}

func historyTokens(history []HistoryMessage) int {
	total := 0
	for _, m := range history {
		total += utils.EstimateTokens(m.Text) + utils.ImageTokens(m.Images)
	}
	return total
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

func pickSessionAccount(model, reqModel string, excluded map[string]bool) *repository.Account {
	accounts := repository.LoadAccounts()
	usable := make([]repository.Account, 0, len(accounts))
	for _, account := range accounts {
		if !AccountUsable(&account) || excluded[account.Email] || !AccountSupportsModel(&account, reqModel) {
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
	res := CompletionResult{SessionID: prompt.SessionID, UpstreamModel: model, ThinkingMode: mode, Effort: prompt.Effort}
	fail := func(code int, err error) (CompletionResult, error) {
		if prompt.Context != nil && prompt.Context.Err() != nil {
			code, err = 499, prompt.Context.Err()
		}
		if code < 400 {
			code = 502
		}
		res.StatusCode = code
		return res, &CompletionError{StatusCode: code, Err: err}
	}
	unlock, ok := lockSessionContext(prompt.Context, key, false)
	if !ok {
		return fail(499, prompt.Context.Err())
	}
	defer unlock()
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
	// 闲置不再主动作废续聊：网页会话仍在就继续用；若已被清理，上游会返回
	// conversation_not_found，由下方 conversation_missing 分支自动重建。
	start, anchorUUID, miss := continuationStart(session, prompt, reqModel)
	reuse := miss == ""
	forked := false
	if reuse {
		if anchors := loadAnchors(session); anchors[len(anchors)-1].UUID != anchorUUID {
			forked = true
		}
	} else {
		res.ContinuationMiss = miss
	}
	targetReady := reuse
	if found && previous.Status == "failed" && previous.ConversationID == session.ConversationID && session.ParentUUID == rootMessageUUID && session.Model == reqModel && session.Policy == prompt.Policy {
		targetReady = true
		res.Action = "new"
	}
	think := mode == "extended"
	excluded := map[string]bool{}
	account := AccountByEmail(session.Account)
	if account != nil && !AccountSupportsModel(account, reqModel) {
		account = nil
	}
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
			account = pickSessionAccount(model, reqModel, excluded)
		}
		if account == nil {
			return fail(429, fmt.Errorf("没有可使用模型 %s 的账号（账号失效、额度冷却中或套餐不支持）", reqModel))
		}
		res.Account = account.Email
		if session.Account != account.Email {
			reuse, targetReady = false, false
		}
		lease := clientFor(account, s.Proxy)
		if err = lease.LockContext(prompt.Context); err != nil {
			return fail(499, err)
		}
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
			// 锚点只属于旧网页会话，换了会话必须清空，否则会挂到不存在的父消息上。
			session.Anchors = nil
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
			if forked {
				res.Action = "fork"
			}
		}
		outgoing := prompt
		if reuse {
			outgoing = incrementalPrompt(prompt, start)
			session.ParentUUID = anchorUUID
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
		code, usage, sendErr := client.SendMessage(account.Email, session.ConversationID, model, outgoing, attachments, files, func(text string) {
			output.WriteString(text)
			if onText != nil {
				onText(text)
			}
		})
		lease.Unlock()
		if usage != nil && reuse {
			// 续聊只上传新消息；上游仍会读取此前的历史，单独记为 cache_read。
			usage.CacheReadTokens = historyTokens(prompt.History[:start])
		}
		res.Usage = usage
		res.MessageUUID = turn.MessageUUID
		if sendErr != nil && prompt.Context != nil && prompt.Context.Err() != nil {
			turn.Status, turn.Response, turn.Error, turn.StatusCode = "interrupted", output.String(), prompt.Context.Err().Error(), 499
			session.ParentUUID = ""
			if err = repository.CommitChatTurn(&session, &turn); err != nil {
				return fail(500, err)
			}
			return fail(499, prompt.Context.Err())
		}
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
				appendAnchor(&session, prompt, session.ReplyKey, turn.MessageUUID)
			}
			turn.Status, turn.Response, turn.StatusCode = "completed", output.String(), 200
			if err = repository.CommitChatTurn(&session, &turn); err != nil {
				return fail(500, err)
			}
			res.StatusCode = 200
			slog.Info("[API] 网页会话完成", "action", res.Action, "email", res.Account, "conversation", res.ConversationID, "parent", res.ParentUUID, "message", res.MessageUUID, "input_bytes", res.InputBytes, "switch_reason", res.SwitchReason, "continuation_miss", res.ContinuationMiss)
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
		// 上游不接受从较早节点分叉（父消息无效等 4xx）时，退回完整重建，不让请求失败。
		if upstream != nil && upstream.Kind == "other" && forked && reuse && code >= 400 && code < 500 {
			reuse, targetReady, forked = false, false, false
			res.ContinuationMiss = "fork_rejected"
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
