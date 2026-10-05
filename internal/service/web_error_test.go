package service

import (
	"testing"
	"time"
)

const nativeExceededLimitResponse = `{"type":"exceeded_limit","resetsAt":1791210600,"remaining":null,"perModelLimit":false,"representativeClaim":"five_hour","overageDisabledReason":"overage_not_provisioned","overageInUse":false,"windows":{"5h":{"status":"exceeded_limit","resets_at":1791210600,"utilization":1.14,"surpassed_threshold":1.0},"7d":{"status":"within_limit","resets_at":1791619200,"utilization":0.1}},"resolved":{"status":"exceeded","limit":{"kind":"session","group":"session","percent":100,"severity":"critical","resets_at":1791210600}}}`

func TestWebResponseErrorQuotaAndRateLimits(t *testing.T) {
	for _, tc := range []struct {
		name, body, kind string
		reset            int64
	}{
		{"native five hour quota", nativeExceededLimitResponse, "quota", 1791210600},
		{"nested native quota", `{"error":` + nativeExceededLimitResponse + `}`, "quota", 1791210600},
		{"temporary rate limit", `{"error":{"type":"rate_limit_error","message":"temporarily rate limited"}}`, "rate", 0},
		{"unspecified 429", `{"message":"too many requests"}`, "rate", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := webResponseError(429, []byte(tc.body), "")
			if err.Kind != tc.kind || err.Status != 429 {
				t.Fatalf("classified as %s / %d, want %s / 429", err.Kind, err.Status, tc.kind)
			}
			if tc.reset != 0 && !err.RetryAt.Equal(time.Unix(tc.reset, 0)) {
				t.Fatalf("reset time %s, want %s", err.RetryAt, time.Unix(tc.reset, 0))
			}
		})
	}
}
