package service

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claude2api/internal/config"
	"claude2api/internal/repository"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
)

type nativeCall struct{ Account, Conversation, Parent, Text, Model, ThinkingMode, Effort string }
type fakeWeb struct {
	sync.Mutex
	created, deleted int
	calls            []nativeCall
	parents          map[string]string
	message          int
	tree             bool            // true 时像真实网页端一样允许挂在任意已有消息下（分叉）。
	known            map[string]bool // conv + "/" + messageUUID
	onComplete       func(nativeCall, int) (int, string)
}
type fakeHTTP struct {
	tlsclient.HttpClient
	web     *fakeWeb
	account string
}

func response(code int, text string) *fhttp.Response {
	return &fhttp.Response{StatusCode: code, Header: fhttp.Header{}, Body: io.NopCloser(strings.NewReader(text))}
}
func sseReply(id, text string) string {
	start, _ := json.Marshal(map[string]any{"type": "message_start", "message": map[string]any{"uuid": id}})
	delta, _ := json.Marshal(map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": text}})
	return "data: " + string(start) + "\n\ndata: " + string(delta) + "\n\ndata: {\"type\":\"message_stop\"}\n\n"
}
func (client *fakeHTTP) Do(req *fhttp.Request) (*fhttp.Response, error) {
	w := client.web
	w.Lock()
	defer w.Unlock()
	path := req.URL.Path
	if req.Method == "DELETE" {
		w.deleted++
		return response(204, ""), nil
	}
	if strings.HasSuffix(path, "/chat_conversations") {
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		if body["is_temporary"] != false {
			return response(400, "temporary must be false"), nil
		}
		w.created++
		id := fmt.Sprintf("conv-%d", w.created)
		w.parents[id] = rootMessageUUID
		return response(201, `{"uuid":"`+id+`"}`), nil
	}
	if strings.HasSuffix(path, "/completion") {
		var body map[string]any
		_ = json.NewDecoder(req.Body).Decode(&body)
		if _, exists := body["paprika_mode"]; exists {
			return response(400, "paprika_mode: Extra inputs are not permitted"), nil
		}
		parts := strings.Split(path, "/")
		conv := parts[len(parts)-2]
		call := nativeCall{Account: client.account, Conversation: conv, Parent: body["parent_message_uuid"].(string), Text: body["prompt"].(string)}
		call.Model, _ = body["model"].(string)
		call.ThinkingMode, _ = body["thinking_mode"].(string)
		call.Effort, _ = body["effort"].(string)
		w.calls = append(w.calls, call)
		if call.Parent != w.parents[conv] && !(w.tree && (call.Parent == rootMessageUUID || w.known[conv+"/"+call.Parent])) {
			return response(400, "incorrect parent UUID"), nil
		}
		if w.onComplete != nil {
			if code, text := w.onComplete(call, len(w.calls)); code != 0 {
				return response(code, text), nil
			}
		}
		w.message++
		id := fmt.Sprintf("message-%d", w.message)
		w.parents[conv] = id
		w.known[conv+"/"+id] = true
		return response(200, sseReply(id, "SAVED")), nil
	}
	return response(200, "{}"), nil
}

func setupSessionTest(t *testing.T) *fakeWeb {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("session_reuse: true\nsession_idle_seconds: 60\nquota_cooldown_seconds: 30\nrate_limit_wait_seconds: 2\ndelete_chat: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	config.Load()
	if err := repository.InitDB(); err != nil {
		t.Fatal(err)
	}
	for _, email := range []string{"a@example.test", "b@example.test"} {
		if err := repository.UpsertAccount(&repository.Account{Email: email, OrgUUID: "org-test", Cookies: map[string]string{"sessionKey": "test"}, Status: "active", SubscriptionPlan: "claude_pro", Models: []repository.AccountModel{{ID: "claude-sonnet-5-5", Thinking: true}}}); err != nil {
			t.Fatal(err)
		}
	}
	w := &fakeWeb{parents: map[string]string{}, known: map[string]bool{}}
	oldFactory := newAPIClient
	newAPIClient = func(key, proxy, identity string, org ...string) *ClaudeAI {
		return &ClaudeAI{orgUUID: org[0], client: &fakeHTTP{web: w, account: identity}, headers: map[string]string{}, modeSet: true}
	}
	apiClients = map[string]*accountClient{}
	apiIndex = 0
	t.Cleanup(func() { newAPIClient = oldFactory; apiClients = map[string]*accountClient{}; _ = repository.CloseDB() })
	return w
}

func history(role, text string) HistoryMessage {
	return HistoryMessage{Role: role, Key: role + ":" + text, Text: text}
}
func sessionPrompt(messages ...HistoryMessage) Prompt {
	p := Prompt{SessionID: "chat-a", Scope: "key-a", Policy: "policy", Continuation: "Continue", History: messages, ReplyKey: func(text string) string { return "assistant:" + text }}
	var parts []string
	for _, m := range messages {
		parts = append(parts, m.Text)
	}
	p.Text = strings.Join(parts, "\n")
	return p
}
func completeTest(t *testing.T, p Prompt) CompletionResult {
	t.Helper()
	res, err := (Dispatcher{}).Complete("claude-sonnet-5-5", p, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestSessionContinuationPersistenceAndReplay(t *testing.T) {
	w := setupSessionTest(t)
	p := sessionPrompt(history("user", "SECRET_INITIAL"))
	first := completeTest(t, p)
	// Reopen the DB and drop every HTTP client to simulate a restart between turns.
	if err := repository.InitDB(); err != nil {
		t.Fatal(err)
	}
	apiClients = map[string]*accountClient{}
	p = sessionPrompt(history("user", "SECRET_INITIAL"), history("assistant", "SAVED"), history("user", "NEXT_ONLY"))
	second := completeTest(t, p)
	if first.Account != second.Account || first.ConversationID != second.ConversationID || second.ParentUUID != first.MessageUUID || second.Action != "continue" {
		t.Fatalf("not continued: first=%+v second=%+v", first, second)
	}
	if strings.Contains(w.calls[1].Text, "SECRET_INITIAL") || !strings.Contains(w.calls[1].Text, "NEXT_ONLY") {
		t.Fatalf("history uploaded again: %q", w.calls[1].Text)
	}
	third := completeTest(t, p)
	if third.Action != "replay" || len(w.calls) != 2 || w.created != 1 || w.deleted != 0 {
		t.Fatalf("duplicate submitted or active conversation deleted: %+v calls=%d created=%d deleted=%d", third, len(w.calls), w.created, w.deleted)
	}
	p.History[0] = history("user", "COMPACTED_HISTORY")
	p.Text = "COMPACTED_HISTORY\nNEXT_ONLY"
	fourth := completeTest(t, p)
	if fourth.ConversationID == first.ConversationID || fourth.Action != "rebuild" || fourth.Account != first.Account {
		t.Fatalf("compaction not rebuilt: %+v", fourth)
	}
}

func TestSessionQuotaSwitchReplaysHistory(t *testing.T) {
	w := setupSessionTest(t)
	first := completeTest(t, sessionPrompt(history("user", "HISTORY_REQUIRED")))
	w.onComplete = func(call nativeCall, _ int) (int, string) {
		if call.Account == first.Account {
			return 429, `{"error":{"type":"usage_limit_exceeded","message":"usage limit reached","resetsAt":4102444800}}`
		}
		return 0, ""
	}
	p := sessionPrompt(history("user", "HISTORY_REQUIRED"), history("assistant", "SAVED"), history("user", "CONTINUE_TASK"))
	second := completeTest(t, p)
	if first.Account == second.Account || second.Action != "switch" || second.SwitchReason != "quota" || second.ParentUUID != rootMessageUUID {
		t.Fatalf("quota didn't switch: %+v", second)
	}
	last := w.calls[len(w.calls)-1]
	if !strings.Contains(last.Text, "HISTORY_REQUIRED") || !strings.Contains(last.Text, "CONTINUE_TASK") {
		t.Fatalf("switch lost history: %+v", last)
	}
	if !repository.GetAccountCooldown(first.Account, "claude-sonnet-5-5").Until.After(time.Now()) {
		t.Fatal("quota cooldown not persisted")
	}
	p.History = append(p.History, history("assistant", "SAVED"), history("user", "LAST_ONLY"))
	p.Text += "LAST_ONLY"
	third := completeTest(t, p)
	if third.Account != second.Account || third.ConversationID != second.ConversationID || third.Action != "continue" {
		t.Fatalf("switch did not stick: %+v", third)
	}
}

func TestSessionRateLimitWaitKeepsConversation(t *testing.T) {
	w := setupSessionTest(t)
	w.onComplete = func(_ nativeCall, n int) (int, string) {
		if n == 1 {
			return 429, `{"error":{"type":"rate_limit_error","message":"temporarily rate limited"}}`
		}
		return 0, ""
	}
	res := completeTest(t, sessionPrompt(history("user", "hello")))
	if w.created != 1 || len(w.calls) != 2 || w.calls[0].Account != w.calls[1].Account || w.calls[0].Conversation != w.calls[1].Conversation || res.SwitchReason != "" {
		t.Fatalf("rate limit changed conversation: %+v calls=%+v", res, w.calls)
	}
}

func TestSessionNativeExceededLimitSwitchReplaysHistory(t *testing.T) {
	w := setupSessionTest(t)
	p := sessionPrompt(history("user", "HISTORY_REQUIRED"))
	p.ThinkingMode, p.Effort = "extended", "max"
	first := completeTest(t, p)
	w.onComplete = func(call nativeCall, _ int) (int, string) {
		if call.Account == first.Account {
			return 429, nativeExceededLimitResponse
		}
		return 0, ""
	}
	p = sessionPrompt(history("user", "HISTORY_REQUIRED"), history("assistant", "SAVED"), history("user", "CONTINUE_TASK"))
	p.ThinkingMode, p.Effort = "extended", "max"
	second := completeTest(t, p)
	if second.Account == first.Account || second.ConversationID == first.ConversationID || second.Action != "switch" || second.SwitchReason != "quota" || second.ParentUUID != rootMessageUUID {
		t.Fatalf("native quota didn't switch and rebuild: first=%+v second=%+v", first, second)
	}
	last := w.calls[len(w.calls)-1]
	if !strings.Contains(last.Text, "HISTORY_REQUIRED") || !strings.Contains(last.Text, "CONTINUE_TASK") || last.ThinkingMode != "extended" || last.Effort != "max" {
		t.Fatalf("switch lost history or reasoning settings: %+v", last)
	}
	cooldown := repository.GetAccountCooldown(first.Account, "claude-sonnet-5-5")
	if cooldown.Reason != "quota" || !cooldown.Until.After(time.Now()) {
		t.Fatalf("native quota cooldown not persisted: %+v", cooldown)
	}
	p.History = append(p.History, history("assistant", "SAVED"), history("user", "LAST_ONLY"))
	p.Text += "\nLAST_ONLY"
	third := completeTest(t, p)
	last = w.calls[len(w.calls)-1]
	if third.Account != second.Account || third.ConversationID != second.ConversationID || third.Action != "continue" || strings.Contains(last.Text, "HISTORY_REQUIRED") || !strings.Contains(last.Text, "LAST_ONLY") {
		t.Fatalf("new account did not continue incrementally: %+v call=%+v", third, last)
	}
}

func TestSessionConcurrentRetriesAndIsolation(t *testing.T) {
	w := setupSessionTest(t)
	p := sessionPrompt(history("user", "hello"))
	var wait sync.WaitGroup
	for i := 0; i < 6; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if _, err := (Dispatcher{}).Complete("claude-sonnet-5-5", p, nil); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	if len(w.calls) != 1 {
		t.Fatalf("duplicate concurrent submission: %d", len(w.calls))
	}
	p.Scope = "another-key"
	completeTest(t, p)
	if w.created != 2 {
		t.Fatal("different API scopes shared a conversation")
	}
}

func TestSessionInterruptedStreamDoesNotResubmit(t *testing.T) {
	w := setupSessionTest(t)
	w.onComplete = func(_ nativeCall, _ int) (int, string) {
		return 200, "data: {\"type\":\"message_start\",\"message\":{\"uuid\":\"partial-id\"}}\n\ndata: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"PARTIAL\"}}\n\n"
	}
	p := sessionPrompt(history("user", "hello"))
	if _, err := (Dispatcher{}).Complete("claude-sonnet-5-5", p, nil); err == nil {
		t.Fatal("truncated stream accepted")
	}
	res, err := (Dispatcher{}).Complete("claude-sonnet-5-5", p, nil)
	if err == nil || res.StatusCode != 409 || len(w.calls) != 1 {
		t.Fatalf("interrupted request submitted again: %+v %v", res, err)
	}
}

func TestSessionIdleCleanupSkipsActiveSession(t *testing.T) {
	w := setupSessionTest(t)
	p := sessionPrompt(history("user", "hello"))
	completeTest(t, p)
	unlock, _ := lockSession(sessionKey(p), false)
	if err := cleanIdleSessions(time.Now().Add(2 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w.deleted != 0 {
		t.Fatal("active session deleted")
	}
	unlock()
	if err := cleanIdleSessions(time.Now().Add(2 * time.Minute)); err != nil {
		t.Fatal(err)
	}
	if w.deleted != 1 {
		t.Fatalf("idle session not deleted: %d", w.deleted)
	}
	session, err := repository.LoadChatSession(sessionKey(p))
	if err != nil || session.ConversationID != "" {
		t.Fatal("idle binding retained")
	}
	completeTest(t, p)
	if w.created != 2 {
		t.Fatal("cleaned session did not rebuild")
	}
}

func TestSessionForkContinuesFromEarlierReply(t *testing.T) {
	w := setupSessionTest(t)
	// claude.ai 会话是树：编辑/重新生成时可以把新消息挂在任意已有消息下面。
	w.tree = true
	first := completeTest(t, sessionPrompt(history("user", "Q1")))
	p := sessionPrompt(history("user", "Q1"), history("assistant", "SAVED"), history("user", "Q2"))
	second := completeTest(t, p)
	if second.Action != "continue" {
		t.Fatalf("second turn not continued: %+v", second)
	}
	// 客户端编辑了 Q2：历史在第一条回复之后分叉。应挂到第一条回复下面，只发新的 Q2'。
	p = sessionPrompt(history("user", "Q1"), history("assistant", "SAVED"), history("user", "Q2_EDITED"))
	third := completeTest(t, p)
	last := w.calls[len(w.calls)-1]
	if third.Action != "fork" || third.ConversationID != first.ConversationID || third.ParentUUID != first.MessageUUID || w.created != 1 {
		t.Fatalf("edit did not fork from earlier reply: %+v created=%d", third, w.created)
	}
	if strings.Contains(last.Text, "Q1") || !strings.Contains(last.Text, "Q2_EDITED") {
		t.Fatalf("fork resent history: %q", last.Text)
	}
	// 分叉后的新回复成为最新锚点，后续正常续聊。
	p.History = append(p.History, history("assistant", "SAVED"), history("user", "Q3"))
	fourth := completeTest(t, p)
	if fourth.Action != "continue" || fourth.ParentUUID != third.MessageUUID {
		t.Fatalf("did not continue after fork: %+v", fourth)
	}
}

func TestSessionForkRejectedFallsBackToRebuild(t *testing.T) {
	w := setupSessionTest(t)
	completeTest(t, sessionPrompt(history("user", "Q1")))
	completeTest(t, sessionPrompt(history("user", "Q1"), history("assistant", "SAVED"), history("user", "Q2")))
	// 若上游只接受当前叶子作父节点（fakeWeb 默认如此），分叉会被拒；必须退回完整重建而不是报错。
	res := completeTest(t, sessionPrompt(history("user", "Q1"), history("assistant", "SAVED"), history("user", "Q2_EDITED")))
	last := w.calls[len(w.calls)-1]
	if res.ContinuationMiss != "fork_rejected" || res.Action != "rebuild" || !strings.Contains(last.Text, "Q1") || !strings.Contains(last.Text, "Q2_EDITED") {
		t.Fatalf("rejected fork not rebuilt: %+v text=%q", res, last.Text)
	}
}

func TestSessionPoliciesKeepSeparateConversations(t *testing.T) {
	w := setupSessionTest(t)
	main := sessionPrompt(history("user", "MAIN"))
	sub := sessionPrompt(history("user", "SUB"))
	sub.Policy = "subagent-policy"
	completeTest(t, main)
	completeTest(t, sub)
	main.History = append(main.History, history("assistant", "SAVED"), history("user", "MAIN2"))
	sub.History = append(sub.History, history("assistant", "SAVED"), history("user", "SUB2"))
	a, b := completeTest(t, main), completeTest(t, sub)
	if a.Action != "continue" || b.Action != "continue" || a.ConversationID == b.ConversationID || w.created != 2 {
		t.Fatalf("interleaved policies clobbered each other: main=%+v sub=%+v", a, b)
	}
}

func TestSessionCacheReadAndMissReason(t *testing.T) {
	setupSessionTest(t)
	first := completeTest(t, sessionPrompt(history("user", "Q1")))
	if first.Usage == nil || first.Usage.CacheReadTokens != 0 {
		t.Fatalf("new conversation must not report cache read: %+v", first.Usage)
	}
	p := sessionPrompt(history("user", "Q1"), history("assistant", "SAVED"), history("user", "Q2"))
	res := completeTest(t, p)
	if res.Action != "continue" || res.ConversationID != first.ConversationID {
		t.Fatalf("not continued: %+v", res)
	}
	if res.Usage == nil || res.Usage.CacheReadTokens == 0 {
		t.Fatalf("continued history not counted as cache read: %+v", res.Usage)
	}
	p = sessionPrompt(history("user", "Q1"), history("assistant", "DIFFERENT"), history("user", "Q2"))
	miss := completeTest(t, p)
	if miss.Action != "rebuild" || miss.ContinuationMiss != "reply_mismatch" {
		t.Fatalf("miss reason not reported: %+v", miss)
	}
}

func TestSessionDeletedConversationRebuildsWithHistory(t *testing.T) {
	w := setupSessionTest(t)
	first := completeTest(t, sessionPrompt(history("user", "HISTORY_REQUIRED")))
	w.onComplete = func(call nativeCall, _ int) (int, string) {
		if call.Conversation == first.ConversationID {
			return 404, `{"error":{"message":"conversation_not_found"}}`
		}
		return 0, ""
	}
	p := sessionPrompt(history("user", "HISTORY_REQUIRED"), history("assistant", "SAVED"), history("user", "CONTINUE_TASK"))
	second := completeTest(t, p)
	if second.Account != first.Account || second.ConversationID == first.ConversationID || second.SwitchReason != "conversation_missing" || !strings.Contains(w.calls[len(w.calls)-1].Text, "HISTORY_REQUIRED") {
		t.Fatalf("missing conversation not recovered: %+v", second)
	}
}
