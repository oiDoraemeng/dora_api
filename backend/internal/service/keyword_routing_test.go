package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type keywordSettingsRepo struct {
	SettingRepository
	mu    sync.Mutex
	raw   string
	reads int
	err   error
}

func (r *keywordSettingsRepo) GetValue(context.Context, string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reads++
	return r.raw, r.err
}
func (r *keywordSettingsRepo) Set(_ context.Context, _, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.raw = value
	return r.err
}

type keywordStatsStub struct {
	calls  int
	groups []int64
	words  [][]string
	err    error
}

func (s *keywordStatsStub) Record(_ context.Context, _ time.Time, group int64, words []string) error {
	s.calls++
	s.groups = append(s.groups, group)
	s.words = append(s.words, words)
	return s.err
}
func (s *keywordStatsStub) Today(context.Context, *int64) (*KeywordRoutingTodayStats, error) {
	return nil, s.err
}
func keywordSettings(words ...string) *SettingService {
	raw, _ := json.Marshal(KeywordRoutingConfig{Keywords: words})
	return NewSettingService(&keywordSettingsRepo{raw: string(raw)}, nil)
}

func TestKeywordRoutingMatcherAll(t *testing.T) {
	m := newContentModerationKeywordMatcher([]string{"HE", "she", "hers", "画图", "图"})
	require.ElementsMatch(t, []string{"HE", "she", "hers", "画图", "图"}, m.MatchAll("SHE draws hers 画图画图"))
	_, ok := m.Match("SHE")
	require.True(t, ok)
	require.Empty(t, m.MatchAll("nothing"))
}
func TestKeywordRoutingInput(t *testing.T) {
	cases := []struct {
		name, protocol, body, want string
		tool                       bool
	}{
		{"chat latest", ContentModerationProtocolOpenAIChat, `{"messages":[{"role":"system","content":"画图"},{"role":"user","content":"历史画图"},{"role":"assistant","content":"画图"},{"role":"user","content":[{"type":"text","text":"代码"},{"type":"image_url","image_url":{"url":"画图"}}]}]}`, "代码", false},
		{"chat tool", ContentModerationProtocolOpenAIChat, `{"messages":[{"role":"user","content":"画图"},{"role":"tool","content":"画图"}]}`, "", true},
		{"assistant prefill", ContentModerationProtocolAnthropicMessages, `{"messages":[{"role":"user","content":"代码"},{"role":"assistant","content":"画图"}]}`, "代码", false},
		{"anthropic mixed", ContentModerationProtocolAnthropicMessages, `{"system":"画图","messages":[{"role":"user","content":[{"type":"tool_result","content":"画图"},{"type":"text","text":"代码"}]}]}`, "代码", false},
		{"anthropic tool", ContentModerationProtocolAnthropicMessages, `{"messages":[{"role":"user","content":[{"type":"tool_result","content":"画图"}]}]}`, "", true},
		{"responses string", ContentModerationProtocolOpenAIResponses, `{"instructions":"画图","input":"本轮代码"}`, "本轮代码", false},
		{"responses array", ContentModerationProtocolOpenAIResponses, `{"input":[{"role":"user","content":"画图"},{"role":"assistant","content":"画图"},{"role":"user","content":[{"type":"input_text","text":"本轮"},{"type":"input_image","image_url":"画图"}]}]}`, "本轮", false},
		{"responses tool", ContentModerationProtocolOpenAIResponses, `{"input":[{"type":"function_call_output","output":"画图"}]}`, "", true},
		{"gemini", ContentModerationProtocolGemini, `{"systemInstruction":{"parts":[{"text":"画图"}]},"contents":[{"role":"user","parts":[{"text":"画图"}]},{"role":"user","parts":[{"text":"代码"},{"inlineData":{"data":"画图"}}]}]}`, "代码", false},
		{"gemini tool", ContentModerationProtocolGemini, `{"contents":[{"role":"user","parts":[{"functionResponse":{"response":{"text":"画图"}}}]}]}`, "", true},
		{"systemone", ContentModerationProtocolTypeSafeSystemOne, `{"state":{"text":"画图"},"questions":{"画图":{"instructions":"本轮","criteria":"标准","type":"boolean"}}}`, "本轮\n标准", false},
		{"empty", ContentModerationProtocolOpenAIResponses, `{"input":[]}`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text, tool := extractKeywordRoutingText(tc.protocol, []byte(tc.body))
			require.Equal(t, tc.want, text)
			require.Equal(t, tc.tool, tool)
		})
	}
}
func TestKeywordRoutingLazyMemoAndStatsFailure(t *testing.T) {
	settings := keywordSettings("画图", "图")
	stats := &keywordStatsStub{err: errors.New("redis offline")}
	settings.keywordRoutingStats = stats
	groupID := int64(12)
	ctx := WithKeywordRoutingRequest(context.Background(), ContentModerationProtocolOpenAIChat, []byte(`{"messages":[{"role":"user","content":"画图画图"}]}`), &groupID)
	active, _, err := keywordRoutingDecision(ctx, settings, []Account{{ID: 1}})
	require.NoError(t, err)
	require.False(t, active)
	require.Zero(t, settings.settingRepo.(*keywordSettingsRepo).reads)
	accounts := []Account{{ID: 2, Extra: map[string]any{KeywordRoutingEnabledExtraKey: true}}}
	for i := 0; i < 4; i++ {
		active, hit, err := keywordRoutingDecision(ctx, settings, accounts)
		require.NoError(t, err)
		require.True(t, active)
		require.True(t, hit)
	}
	require.Equal(t, 1, stats.calls)
	require.Equal(t, []int64{groupID}, stats.groups)
	require.ElementsMatch(t, []string{"画图", "图"}, stats.words[0])
	require.Nil(t, keywordRoutingRequestFromContext(ctx).body)
}
func TestKeywordRoutingConfigValidationAndRefresh(t *testing.T) {
	settings := keywordSettings()
	other := NewSettingService(settings.settingRepo, nil)
	_, err := other.GetKeywordRoutingConfig(context.Background())
	require.NoError(t, err)
	config, err := settings.SetKeywordRoutingConfig(context.Background(), KeywordRoutingConfig{Keywords: []string{"  HELLO ", "hello", "", "画图"}})
	require.NoError(t, err)
	require.Equal(t, []string{"HELLO", "画图"}, config.Keywords)
	current, err := settings.GetKeywordRoutingConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, config, current)
	current, err = other.GetKeywordRoutingConfig(context.Background())
	require.NoError(t, err)
	require.Empty(t, current.Keywords)
	cached := *other.keywordRoutingCache.Load()
	cached.expires = time.Now().Add(-time.Second)
	other.keywordRoutingCache.Store(&cached)
	current, err = other.GetKeywordRoutingConfig(context.Background())
	require.NoError(t, err)
	require.Equal(t, config, current)
	matcher := other.keywordRoutingCache.Load().matcher
	cached = *other.keywordRoutingCache.Load()
	cached.expires = time.Now().Add(-time.Second)
	other.keywordRoutingCache.Store(&cached)
	_, err = other.GetKeywordRoutingConfig(context.Background())
	require.NoError(t, err)
	require.Same(t, matcher, other.keywordRoutingCache.Load().matcher, "unchanged config must reuse its compiled matcher")
	_, err = normalizeKeywordRoutingConfig(KeywordRoutingConfig{Keywords: []string{strings.Repeat("词", 201)}})
	require.Error(t, err)
	words := make([]string, 10001)
	for i := range words {
		words[i] = fmt.Sprintf("keyword-%d", i)
	}
	_, err = normalizeKeywordRoutingConfig(KeywordRoutingConfig{Keywords: words})
	require.Error(t, err)
	require.Error(t, ValidateKeywordRoutingExtra(map[string]any{KeywordRoutingEnabledExtraKey: "true"}))
	require.NoError(t, ValidateKeywordRoutingExtra(map[string]any{KeywordRoutingEnabledExtraKey: false}))
}
func TestKeywordRoutingEmptyListAndToolDoNotCount(t *testing.T) {
	accounts := []Account{{ID: 1, Extra: map[string]any{KeywordRoutingEnabledExtraKey: true}}}
	for _, words := range [][]string{nil, {"画图"}} {
		settings := keywordSettings(words...)
		stats := &keywordStatsStub{}
		settings.keywordRoutingStats = stats
		for _, body := range []string{`{"input":[]}`, `{"input":[{"type":"function_call_output","output":"画图"}]}`} {
			ctx := WithKeywordRoutingRequest(context.Background(), ContentModerationProtocolOpenAIResponses, []byte(body), nil)
			active, hit, err := keywordRoutingDecision(ctx, settings, accounts)
			require.NoError(t, err)
			require.True(t, active)
			require.False(t, hit)
		}
		require.Zero(t, stats.calls)
	}
}

type keywordAccountUpdateRepo struct {
	AccountRepository
	account Account
	updates int
}

func (r *keywordAccountUpdateRepo) GetByID(context.Context, int64) (*Account, error) {
	account := r.account
	return &account, nil
}
func (r *keywordAccountUpdateRepo) UpdateExtra(_ context.Context, _ int64, updates map[string]any) error {
	r.updates++
	for key, value := range updates {
		r.account.Extra[key] = value
	}
	return nil
}
func TestKeywordRoutingAccountToggleMergesOnlyKey(t *testing.T) {
	repo := &keywordAccountUpdateRepo{account: Account{ID: 1, Extra: map[string]any{"base_rpm": 12, "quota_used": 100}}}
	svc := &adminServiceImpl{accountRepo: repo}
	for _, enabled := range []bool{true, false} {
		account, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{KeywordRoutingEnabledExtraKey: enabled}})
		require.NoError(t, err)
		require.Equal(t, enabled, account.KeywordRoutingEnabled())
		require.Equal(t, 12, account.Extra["base_rpm"])
		require.Equal(t, 100, account.Extra["quota_used"])
	}
	_, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{Extra: map[string]any{KeywordRoutingEnabledExtraKey: "true"}})
	require.Error(t, err)
	require.Equal(t, 2, repo.updates)
}
