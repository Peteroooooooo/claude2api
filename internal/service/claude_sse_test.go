package service

import (
	"strings"
	"testing"
)

func TestParseCompletionSSE(t *testing.T) {
	cases := []struct {
		name, stream, output, err string
	}{
		{
			name: "normal completion",
			stream: "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi!\"}}\n\n" +
				"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
				"data: {\"type\":\"message_stop\"}\n\n",
			output: "Hi!",
		},
		{
			name: "refusal without text",
			stream: "data: {\"type\":\"message_start\"}\n\n" +
				"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"refusal\",\"stop_details\":{\"category\":\"reasoning_extraction\",\"explanation\":\"Request flagged\"}}}\n\n" +
				"data: {\"type\":\"message_stop\"}\n\n",
			err: "upstream refused the request (reasoning_extraction): Request flagged",
		},
		{
			name: "empty completed stream",
			stream: "data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"}}\n\n" +
				"data: {\"type\":\"message_stop\"}\n\n",
			err: "upstream returned an empty completion",
		},
		{name: "empty response body", err: "upstream stream ended before message_stop"},
		{
			name:   "truncated stream",
			stream: "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi!\"}}\n\n",
			output: "Hi!",
			err:    "upstream stream ended before message_stop",
		},
		{
			name:   "error without message",
			stream: "data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\"}}\n\n",
			err:    "upstream: overloaded_error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var output strings.Builder
			err := parseCompletionSSE(strings.NewReader(tc.stream), func(s string) { output.WriteString(s) })
			if output.String() != tc.output {
				t.Fatalf("output=%q want=%q", output.String(), tc.output)
			}
			if tc.err == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("error=%v want=%q", err, tc.err)
			}
		})
	}
}
