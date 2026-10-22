package modelresponse

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRewriteProtocolMetadata(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"chat snapshot", `{"model":"grok-4.6-20261001","choices":[{"message":{"content":"grok-4.6"}}],"usage":{"total_tokens":3}}`, `{"model":"grok-4.7","choices":[{"message":{"content":"grok-4.6"}}],"usage":{"total_tokens":3}}`},
		{"responses", `{"type":"response.completed","response":{"model":"provider/canonical","output":[{"type":"image_generation_call","model":"image-model"}]}}`, `{"type":"response.completed","response":{"model":"grok-4.7","output":[{"type":"image_generation_call","model":"image-model"}]}}`},
		{"messages", `{"type":"message_start","message":{"model":"claude-upstream"}}`, `{"type":"message_start","message":{"model":"grok-4.7"}}`},
		{"gemini", `{"modelVersion":"gemini-snapshot","candidates":[{"content":{"parts":[{"text":"upstream"}]}}]}`, `{"modelVersion":"grok-4.7","candidates":[{"content":{"parts":[{"text":"upstream"}]}}]}`},
		{"gemini wrapper", `{"response":{"modelVersion":"gemini-snapshot"}}`, `{"response":{"modelVersion":"grok-4.7"}}`},
		{"gemini array", `[{"modelVersion":"gemini-snapshot"},{"modelVersion":"gemini-snapshot"}]`, `[{"modelVersion":"grok-4.7"},{"modelVersion":"grok-4.7"}]`},
		{"no model", `{"type":"response.output_text.delta","delta":"upstream","arguments":{"model":"tool-model"}}`, `{"type":"response.output_text.delta","delta":"upstream","arguments":{"model":"tool-model"}}`},
		{"error", `{"model":"upstream","error":{"message":"unknown upstream model"}}`, `{"model":"upstream","error":{"message":"unknown upstream model"}}`},
		{"null model", `{"model":null}`, `{"model":null}`},
		{"malformed", `{"model":"upstream"`, `{"model":"upstream"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, string(Rewrite([]byte(tc.input), "grok-4.7")))
			require.Equal(t, tc.input, string(Rewrite([]byte(tc.input), "")))
		})
	}
	require.JSONEq(t, `{"model":"alias\"\\\n"}`, string(Rewrite([]byte(`{"model":"upstream"}`), "alias\"\\\n")))
}

func TestRewriteSSE(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"id: 1\nevent: message\ndata: {\"model\":\"upstream\"}\n\n", "id: 1\nevent: message\ndata: {\"model\":\"client\"}\n\n"},
		{"event: response.completed\r\ndata: {\r\ndata: \"response\":{\"model\":\"upstream\"}}\r\n\r\n", "event: response.completed\r\ndata: {\"response\":{\"model\":\"client\"}}\r\n\r\n"},
		{"data: [DONE]\n\n", "data: [DONE]\n\n"},
		{": ping\n\n", ": ping\n\n"},
		{"data: {\"delta\":\"upstream\"}\n\n", "data: {\"delta\":\"upstream\"}\n\n"},
	} {
		require.Equal(t, tc.want, string(RewriteSSE([]byte(tc.input), "client")))
	}
}
