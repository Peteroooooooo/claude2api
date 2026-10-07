package adapter

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestSSEHeartbeatDuringSilentGenerationAndCleanup(t *testing.T) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.Request = httptest.NewRequest("POST", "/v1/messages", nil).WithContext(ctx)
	s := newSSEWithInterval(c, 5*time.Millisecond)
	defer s.finish()
	deadline := time.Now().Add(time.Second)
	for {
		s.mu.Lock()
		heartbeat := strings.Contains(w.Body.String(), ": keep-alive\n\n")
		s.mu.Unlock()
		if heartbeat {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("silent stream did not send a heartbeat")
		}
		time.Sleep(time.Millisecond)
	}
	s.data(map[string]string{"text": "OK"})
	cancel()
	select {
	case <-s.finished:
	case <-time.After(time.Second):
		t.Fatal("heartbeat survived request cancellation")
	}
	s.finish()
	if !strings.Contains(w.Body.String(), `data: {"text":"OK"}`) {
		t.Fatal("heartbeat damaged the payload")
	}
}
