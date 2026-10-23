package handler

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRewriteGeminiModelDetailResponse(t *testing.T) {
	for _, tc := range []struct {
		name, body, requested, want string
	}{
		{"mapped model", `{"name":"models/gemini-2.5-pro","displayName":"Pro"}`, "gemini-public", `{"name":"models/gemini-public","displayName":"Pro"}`},
		{"already qualified request", `{"name":"models/gemini-upstream"}`, "models/gemini-public", `{"name":"models/gemini-public"}`},
		{"error response", `{"error":{"message":"not found"}}`, "gemini-public", `{"error":{"message":"not found"}}`},
		{"missing name metadata", `{"displayName":"model"}`, "gemini-public", `{"displayName":"model"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.JSONEq(t, tc.want, string(rewriteGeminiModelDetailResponse([]byte(tc.body), tc.requested)))
		})
	}
	malformed := []byte(`{"name":`)
	require.Equal(t, malformed, rewriteGeminiModelDetailResponse(malformed, "gemini-public"))
}
