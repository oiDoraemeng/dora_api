package clienterror

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRewrite(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
	}{
		{"openai", `{"error":{"type":"upstream_error","message":"provider custom"}}`, `{"error":{"type":"upstream_error","message":"` + Message + `"}}`},
		{"responses", `{"type":"response.failed","response":{"error":{"code":"server_error","message":"provider custom"}}}`, `{"type":"response.failed","response":{"error":{"code":"server_error","message":"` + Message + `"}}}`},
		{"google", `{"error":{"code":503,"message":"provider custom","status":"UNAVAILABLE"}}`, `{"error":{"code":503,"message":"` + Message + `","status":"UNAVAILABLE"}}`},
		{"top level", `{"type":"error","message":"provider custom"}`, `{"type":"error","message":"` + Message + `"}`},
		{"string error", `{"error":"provider custom"}`, `{"error":"` + Message + `"}`},
		{"missing message", `{"error":{"type":"upstream_error"}}`, `{"error":{"type":"upstream_error","message":"` + Message + `"}}`},
		{"success", `{"type":"response.completed","response":{"status":"completed"},"message":"keep"}`, `{"type":"response.completed","response":{"status":"completed"},"message":"keep"}`},
		{"null error", `{"error":null,"message":"keep"}`, `{"error":null,"message":"keep"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.JSONEq(t, tc.want, string(Rewrite([]byte(tc.input))))
		})
	}
}

func TestRewriteSSE(t *testing.T) {
	input := "event: response.failed\r\ndata: {\r\ndata: \"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"provider custom\"}}}\r\n\r\n"
	want := "event: response.failed\r\ndata: {\"type\":\"response.failed\",\"response\":{\"error\":{\"message\":\"" + Message + "\"}}}\r\n\r\n"
	require.Equal(t, want, string(RewriteSSE([]byte(input))))
	require.Equal(t, "data: [DONE]\n\n", string(RewriteSSE([]byte("data: [DONE]\n\n"))))
}
