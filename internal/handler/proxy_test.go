package handler

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claude2api/internal/repository"
)

func TestSyncAccountOrgCookie(t *testing.T) {
	for _, tc := range []struct {
		name, url, previousOrg, org string
		wantCookie, wantSecure      bool
	}{
		{"first visit", "http://10.10.10.30:8787/new", "", "org-current", true, false},
		{"account switch", "http://10.10.10.30:8787/new", "org-previous", "org-current", true, false},
		{"already current", "http://10.10.10.30:8787/new", "org-current", "org-current", false, false},
		{"unknown organization", "http://10.10.10.30:8787/new", "", "", false, false},
		{"HTTPS", "https://example.test/new", "", "org-current", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			if tc.previousOrg != "" {
				req.AddCookie(&http.Cookie{Name: "lastActiveOrg", Value: tc.previousOrg})
			}
			rec := httptest.NewRecorder()
			syncAccountOrgCookie(rec, req, tc.org)
			cookies := rec.Result().Cookies()
			if !tc.wantCookie {
				if len(cookies) != 0 {
					t.Fatal("unexpected Set-Cookie")
				}
				return
			}
			if len(cookies) != 1 {
				t.Fatalf("got %d cookies, want one organization cookie", len(cookies))
			}
			cookie := cookies[0]
			if cookie.Name != "lastActiveOrg" || cookie.Value != tc.org || cookie.Path != "/" || cookie.MaxAge <= 0 {
				t.Fatalf("invalid organization cookie: %+v", cookie)
			}
			if cookie.HttpOnly || cookie.Secure != tc.wantSecure {
				t.Fatal("organization cookie must be readable by the frontend over the request scheme")
			}
		})
	}
}

func TestArtifactTicketDoesNotExposeCredential(t *testing.T) {
	token, err := issueArtifactTicket("admin-secret", "user@example.com")
	if err != nil || strings.Contains(token, "admin-secret") {
		t.Fatalf("invalid ticket: %q %v", token, err)
	}
	credential, email, path, ok := artifactAuth(authPrefix + token + "/artifact")
	if !ok || credential != "admin-secret" || email != "user@example.com" || path != "/artifact" {
		t.Fatalf("ticket lookup failed: %q %q %q %v", credential, email, path, ok)
	}
	if cookies := mergeProxyCookies("claude2api_auth=admin-secret; browser=1", "sessionKey=2"); strings.Contains(cookies, "admin-secret") {
		t.Fatalf("auth cookie leaked upstream: %s", cookies)
	}
}

func TestProxyDirectorForwardsFrontendHeaders(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "http://10.10.10.30:8787/v1/toolbox/shttp/mcp/6f616b42-0ed8-571e-823f-ee4aca6b7ce9", nil)
	forwarded := map[string]string{
		"x-organization-uuid": "org-1", "x-mcp-client-session-id": "sid", "x-mcp-client-name": "ClaudeAI",
		"mcp-protocol-version": "2025-06-18", "mcp-session-id": "mcp-sid", "last-event-id": "7",
		"anthropic-version": "2023-06-01", "anthropic-beta": "ccr-byoc-2025-07-29", "X-Frame-CP": "go",
		"Connect-Protocol-Version": "1", "x-activity-session-id": "act",
	}
	dropped := []string{"authorization", "x-forwarded-for", "x-real-ip"}
	for k, v := range forwarded {
		req.Header.Set(k, v)
	}
	for _, k := range dropped {
		req.Header.Set(k, "browser")
	}
	pc := &proxyCtx{onMain: true, browserCookies: "claude2api_auth=admin-secret", record: &repository.Account{Cookies: map[string]string{"sessionKey": "sk"}}}
	req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, pc))
	proxyDirector(req)
	for k, want := range forwarded {
		if got := req.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	for _, k := range dropped {
		if got := req.Header.Get(k); got != "" {
			t.Errorf("%s leaked upstream: %q", k, got)
		}
	}
	if req.Host != upstreamHost || strings.Contains(req.Header.Get("cookie"), "admin-secret") {
		t.Fatalf("bad upstream request: host=%s cookie=%s", req.Host, req.Header.Get("cookie"))
	}
}

func TestProxyBrowserCompat(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		onMain, wantCompat      bool
	}{
		{"main HTML", "text/html; charset=utf-8", `<html><head><script>window.start()</script></head><body></body></html>`, true, true},
		{"head attributes", "text/html", `<HTML><HEAD lang="en"><script type="module" src="app.js"></script></HEAD><BODY></BODY></HTML>`, true, true},
		{"JSON", "application/json", `{"ok":true}`, true, false},
		{"JavaScript", "application/javascript", `window.start()`, true, false},
		{"artifact HTML", "text/html", `<html><head></head><body></body></html>`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "http://10.10.10.30:8787/new", nil)
			req = req.WithContext(context.WithValue(req.Context(), ctxKey{}, &proxyCtx{
				onMain: tc.onMain, mainOrig: "http://10.10.10.30:8787", ucOrig: "http://uc.example.com",
			}))
			resp := &http.Response{
				Request: req, Header: http.Header{"Content-Type": {tc.contentType}},
				Body: io.NopCloser(strings.NewReader(tc.body)), ContentLength: int64(len(tc.body)),
			}
			if err := proxyModifyResponse(resp); err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			text := string(body)
			if got := strings.Contains(text, browserCompatScript); got != tc.wantCompat {
				t.Fatalf("compat injection = %v, want %v", got, tc.wantCompat)
			}
			if tc.wantCompat && strings.Index(text, browserCompatScript) > strings.Index(text, "window.start()") && strings.Contains(text, "window.start()") {
				t.Fatal("compat script must run before the upstream application")
			}
			if tc.wantCompat && strings.Contains(text, `src="app.js"`) && strings.Index(text, browserCompatScript) > strings.Index(text, `src="app.js"`) {
				t.Fatal("compat script must precede upstream modules")
			}
			if resp.ContentLength != int64(len(body)) {
				t.Fatalf("ContentLength = %d, actual body = %d", resp.ContentLength, len(body))
			}
			if !tc.wantCompat && text != tc.body {
				t.Fatal("unexpected response modification")
			}
		})
	}
}
