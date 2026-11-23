package apicompat

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesToolHistoryPreservesCompleteAndRemoteHistory(t *testing.T) {
	for _, tc := range []struct {
		name  string
		input string
		prior bool
	}{
		{"complete", `[{"type":"function_call","name":"lookup","call_id":"call_1","arguments":"{}","extra":9007199254740993},{"type":"function_call_output","call_id":"call_1","output":""}]`, false},
		{"stateful call", `[{"type":"function_call","name":"lookup","call_id":"call_1","arguments":"{}"}]`, true},
		{"stateful output", `[{"type":"function_call_output","call_id":"call_1","output":"real result"}]`, true},
		{"reference", `[{"type":"item_reference","id":"fc_remote"},{"type":"function_call_output","call_id":"remote","output":"real result"}]`, false},
		{"external input", `[{"type":"function_call_output","name":"delegation","namespace":"codex_app","output":"task"}]`, false},
		{"native search", `[{"type":"tool_search_call","call_id":"search","arguments":{"query":"docs"}}]`, false},
		{"mixed search output", `[{"type":"tool_search_call","call_id":"search","arguments":{"query":"docs"}},{"type":"function_call_output","call_id":"search","output":"docs"}]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, changed, err := RepairResponsesToolHistory(json.RawMessage(tc.input), tc.prior)
			require.NoError(t, err)
			require.False(t, changed)
			require.Equal(t, tc.input, string(got))
		})
	}
}

func TestResponsesToolHistoryRepairsInterruptedParallelCallsWithoutFakeResults(t *testing.T) {
	input := json.RawMessage(`[
	 {"type":"message","role":"user","content":"continue","metadata":{"n":9007199254740993}},
	 {"type":"function_call","name":"lookup","call_id":"answered","arguments":"{}"},
	 {"type":"function_call","name":"write_file","call_id":"fc_1","arguments":"{\"path\":\"file\"}"},
	 {"type":"function_call_output","call_id":"answered","output":"actual result"}
	]`)
	untouched, changed, err := NormalizeResponsesToolHistory(input, false)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, input, untouched)
	got, changed, err := RepairResponsesToolHistory(input, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "function_call", gjson.GetBytes(got, "1.type").String())
	require.Equal(t, "assistant", gjson.GetBytes(got, "2.role").String())
	require.Contains(t, gjson.GetBytes(got, "2.content.0.text").String(), "Incomplete tool call")
	require.Contains(t, gjson.GetBytes(got, "2.content.0.text").String(), "write_file")
	require.Equal(t, "actual result", gjson.GetBytes(got, "3.output").String())
	require.Equal(t, "9007199254740993", gjson.GetBytes(got, "0.metadata.n").Raw)
	require.NotContains(t, string(got), `"call_id":"fc_1"`)
	again, changed, err := RepairResponsesToolHistory(got, false)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, got, again)
}

func TestResponsesToolHistoryRestoresIdentifiableNamesAndIDs(t *testing.T) {
	input := json.RawMessage(`[
	 {"type":"function_call","id":"fc_item","call_id":"call_real","arguments":{"n":9007199254740993}},
	 {"type":"function_call_output","call_id":"fc_item","name":"lookup","output":"actual result"},
	 {"type":"function_call","id":"fc_legacy","name":"read","arguments":"{}"},
	 {"type":"function_call_output","call_id":"fc_legacy","output":"contents"}
	]`)
	got, changed, err := NormalizeResponsesToolHistory(input, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "lookup", gjson.GetBytes(got, "0.name").String())
	require.Equal(t, `{"n":9007199254740993}`, gjson.GetBytes(got, "0.arguments").String())
	require.Equal(t, "call_real", gjson.GetBytes(got, "1.call_id").String())
	require.Equal(t, "actual result", gjson.GetBytes(got, "1.output").String())
	require.Equal(t, "fc_legacy", gjson.GetBytes(got, "2.call_id").String())
}

func TestResponsesToolHistoryKeepsUnidentifiableResultsVisible(t *testing.T) {
	input := json.RawMessage(`[
	 {"type":"function_call","call_id":"unknown","arguments":"{}"},
	 {"type":"function_call_output","call_id":"unknown","output":"do not lose this result"},
	 {"type":"function_call_output","output":"external result without a name"}
	]`)
	got, changed, err := RepairResponsesToolHistory(input, false)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "assistant", gjson.GetBytes(got, "0.role").String())
	require.Equal(t, "user", gjson.GetBytes(got, "1.role").String())
	require.Contains(t, gjson.GetBytes(got, "1.content.0.text").String(), "do not lose this result")
	require.Contains(t, gjson.GetBytes(got, "2.content.0.text").String(), "external result without a name")
	require.NotContains(t, string(got), `"type":"function_call"`)
}

func TestResponsesToAnthropicAcceptsObjectArguments(t *testing.T) {
	for _, arguments := range []string{`{"n":9007199254740993,"nested":{"flag":true}}`, `"{\"n\":9007199254740993,\"nested\":{\"flag\":true}}"`} {
		req := &ResponsesRequest{Model: "claude-opus-5-5", Input: json.RawMessage(`[
		 {"type":"function_call","call_id":"call_1","name":"lookup","arguments":` + arguments + `},
		 {"type":"function_call_output","call_id":"call_1","output":"actual result"}
		]`)}
		out, err := ResponsesToAnthropicRequest(req)
		require.NoError(t, err)
		require.Len(t, out.Messages, 2)
		require.Equal(t, "9007199254740993", gjson.GetBytes(out.Messages[0].Content, "0.input.n").Raw)
		require.True(t, gjson.GetBytes(out.Messages[0].Content, "0.input.nested.flag").Bool())
		require.Equal(t, "lookup", gjson.GetBytes(out.Messages[0].Content, "0.name").String())
		require.Equal(t, "actual result", gjson.GetBytes(out.Messages[1].Content, "0.content").String())
	}
	var item ResponsesInputItem
	require.Error(t, json.Unmarshal([]byte(`{"type":"function_call","arguments":42}`), &item))
}
