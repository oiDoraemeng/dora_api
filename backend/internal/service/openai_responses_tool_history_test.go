package service

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestResponsesIngressRepairsToolHistoryAndKeepsRequestFields(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		body := []byte(`{"model":"gpt-6-luna","stream":false,"metadata":{"n":9007199254740993},"input":[{"type":"function_call","name":"lookup","call_id":"fc_1","arguments":"{}"},{"role":"user","content":"continue"}]}`)
		if legacy {
			body = []byte(`{"model":"gpt-6-luna","stream":false,"metadata":{"n":9007199254740993},"messages":[{"role":"assistant","tool_calls":[{"id":"fc_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"user","content":"continue"}]}`)
		}
		got, changed, err := normalizeOpenAIResponsesLegacyIngress(body)
		require.NoError(t, err)
		if !legacy {
			require.False(t, changed)
			require.Equal(t, body, got)
			continue
		}
		require.True(t, changed)
		require.Equal(t, "assistant", gjson.GetBytes(got, "input.0.role").String())
		require.Contains(t, gjson.GetBytes(got, "input.0.content.0.text").String(), "Incomplete tool call")
		require.Equal(t, "9007199254740993", gjson.GetBytes(got, "metadata.n").Raw)
		require.False(t, gjson.GetBytes(got, "messages").Exists())
	}
}

func TestChatGatewayRepairsToolHistoryBeforeSendingResponses(t *testing.T) {
	for _, history := range []string{`"messages":[{"role":"assistant","tool_calls":[{"id":"fc_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"user","content":"continue"}]`} {
		body := []byte(`{"model":"gpt-5.5","stream":false,` + history + `}`)
		upstream := &httpUpstreamRecorder{responses: []*http.Response{
			newOpenAIRejectedFieldTestResponse(http.StatusOK, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_ok\",\"model\":\"gpt-5.5\",\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"),
		}}
		result, err := newOpenAIRejectedFieldTestService(upstream).ForwardAsChatCompletions(
			context.Background(), newOpenAIRejectedFieldTestContext(body), newOpenAIRejectedFieldTestAccount(), body, "", "",
		)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, upstream.bodies, 1)
		require.NotContains(t, string(upstream.bodies[0]), `"type":"function_call"`)
		require.Contains(t, gjson.GetBytes(upstream.bodies[0], "input.0.content.0.text").String(), "Incomplete tool call")
	}
}

func TestResponsesGatewayRepairsOnlyExplicitToolHistoryRejections(t *testing.T) {
	for _, tc := range []struct {
		status int
		error  string
	}{
		{400, `{"error":{"message":"No tool output found for function call fc_1."}}`},
		{502, `{"error":{"message":"A tool call is missing its matching output. Resend the complete tool-call and tool-output context."}}`},
		{400, `{"error":{"message":"Missing required parameter: 'input[0].name'."}}`},
	} {
		body := []byte(`{"model":"gpt-5.5","stream":false,"input":[{"type":"function_call","call_id":"fc_1","arguments":"{}"},{"role":"user","content":"continue"}]}`)
		upstream := &httpUpstreamRecorder{responses: []*http.Response{
			newOpenAIRejectedFieldTestResponse(tc.status, tc.error),
			newOpenAIRejectedFieldTestResponse(http.StatusOK, `{"id":"resp_ok","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`),
		}}
		result, err := newOpenAIRejectedFieldTestService(upstream).Forward(
			context.Background(), newOpenAIRejectedFieldTestContext(body), newOpenAIRejectedFieldTestAccount(), body,
		)
		require.NoError(t, err)
		require.NotNil(t, result)
		require.Len(t, upstream.bodies, 2)
		require.Equal(t, "function_call", gjson.GetBytes(upstream.bodies[0], "input.0.type").String())
		require.Equal(t, "assistant", gjson.GetBytes(upstream.bodies[1], "input.0.role").String())
		require.Contains(t, gjson.GetBytes(upstream.bodies[1], "input.0.content.0.text").String(), "Incomplete tool call")
	}
}

func TestResponsesToolHistoryRetryPreservesStatefulAndUnrelatedErrors(t *testing.T) {
	for _, tc := range []struct{ body, rejection string }{
		{`{"input":[{"type":"function_call","call_id":"fc_1","name":"lookup","arguments":"{}"}]}`, `{"error":{"message":"Rate limit exceeded"}}`},
		{`{"previous_response_id":"resp_remote","input":[{"type":"function_call_output","call_id":"remote","output":"real result"}]}`, `{"error":{"message":"No tool output found for function call remote."}}`},
		{`{"input":[{"type":"item_reference","id":"fc_remote"},{"type":"function_call_output","call_id":"remote","output":"real result"}]}`, `{"error":{"message":"No tool output found for function call remote."}}`},
	} {
		_, _, changed, err := normalizeOpenAIResponsesRejectedFieldRetryBody(400, []byte(tc.body), []byte(tc.rejection))
		require.NoError(t, err)
		require.False(t, changed)
	}
}
