package service

import (
	"claude2api/internal/repository"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	fhttp "github.com/bogdanfinn/fhttp"
	tlsclient "github.com/bogdanfinn/tls-client"
)

func TestCompletionStopsWithoutTransportEOF(t *testing.T) {
	r, w := io.Pipe()
	defer r.Close()
	defer w.Close()
	done := make(chan error, 1)
	go func() { done <- parseCompletionSSE(r, nil) }()
	if _, err := io.WriteString(w, sseReply("reply", "OK")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("parser waited for EOF after message_stop")
	}
}

type cancelledHTTP struct {
	tlsclient.HttpClient
	received chan context.Context
}

func (c *cancelledHTTP) Do(req *fhttp.Request) (*fhttp.Response, error) {
	c.received <- req.Context()
	<-req.Context().Done()
	return nil, req.Context().Err()
}

func TestClientCancellationReachesNativeCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := &cancelledHTTP{received: make(chan context.Context, 1)}
	c := &ClaudeAI{orgUUID: "org", client: h, headers: map[string]string{}}
	done := make(chan error, 1)
	go func() {
		_, err := c.SendMessage("conv", "claude-sonnet-5-5", Prompt{Context: ctx, Text: "hello"}, nil, nil, nil)
		done <- err
	}()
	select {
	case received := <-h.received:
		if received != ctx {
			t.Fatal("native request lost the caller context")
		}
	case <-time.After(time.Second):
		t.Fatal("native request was not submitted")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("native request ignored cancellation")
	}
}

func TestCancelledSessionAndAccountWaitersDoNotAcquireLocks(t *testing.T) {
	unlock, _ := lockSession("cancelled-session", false)
	defer unlock()
	lease := &accountClient{ClaudeAI: &ClaudeAI{}}
	lease.Lock()
	defer lease.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	sessionDone, accountDone := make(chan bool, 1), make(chan error, 1)
	go func() {
		release, ok := lockSessionContext(ctx, "cancelled-session", false)
		if ok {
			release()
		}
		sessionDone <- ok
	}()
	go func() {
		err := lease.LockContext(ctx)
		if err == nil {
			lease.Unlock()
		}
		accountDone <- err
	}()
	cancel()
	select {
	case ok := <-sessionDone:
		if ok {
			t.Fatal("cancelled session waiter acquired lock")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled session waiter kept waiting")
	}
	select {
	case err := <-accountDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("account error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled account waiter kept waiting")
	}
}

type cancelCompletionHTTP struct {
	*fakeHTTP
	started chan struct{}
}

func (c *cancelCompletionHTTP) Do(req *fhttp.Request) (*fhttp.Response, error) {
	if strings.HasSuffix(req.URL.Path, "/completion") {
		close(c.started)
		<-req.Context().Done()
		return nil, req.Context().Err()
	}
	return c.fakeHTTP.Do(req)
}

func TestCancelledTurnPersistsInterruptionAndReleasesAccount(t *testing.T) {
	w := setupSessionTest(t)
	started := make(chan struct{})
	newAPIClient = func(key, proxy, identity string, org ...string) *ClaudeAI {
		return &ClaudeAI{orgUUID: org[0], headers: map[string]string{}, modeSet: true,
			client: &cancelCompletionHTTP{fakeHTTP: &fakeHTTP{web: w, account: identity}, started: started}}
	}
	p := sessionPrompt(history("user", "cancel this request"))
	p.ThinkingMode = "off"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	p.Context = ctx
	done := make(chan CompletionResult, 1)
	go func() { res, _ := (Dispatcher{}).Complete("claude-sonnet-5-5", p, nil); done <- res }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("completion did not start")
	}
	cancel()
	var res CompletionResult
	select {
	case res = <-done:
	case <-time.After(time.Second):
		t.Fatal("dispatcher retained cancelled account")
	}
	if res.StatusCode != 499 {
		t.Fatalf("cancelled request status=%d", res.StatusCode)
	}
	turn, found, err := repository.LoadChatTurn(turnKey(sessionKey(p), "claude-sonnet-5-5", p))
	if err != nil || !found || turn.Status != "interrupted" {
		t.Fatalf("turn=%+v found=%v error=%v", turn, found, err)
	}
	lease := apiClients[res.Account]
	if !lease.TryLock() {
		t.Fatal("account remained locked after cancellation")
	}
	lease.Unlock()
	session, err := repository.LoadChatSession(sessionKey(p))
	if err != nil || session.ParentUUID != "" {
		t.Fatalf("cancelled turn remained a continuation parent: %+v error=%v", session, err)
	}
}
