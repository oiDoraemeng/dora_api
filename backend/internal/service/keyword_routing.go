package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const KeywordRoutingEnabledExtraKey = "keyword_routing_enabled"
const keywordRoutingSettingKey = "keyword_routing_config"

type KeywordRoutingConfig struct {
	Keywords []string `json:"keywords"`
}
type KeywordRoutingCounter struct {
	Checks int64 `json:"checks"`
	Hits   int64 `json:"hits"`
}
type KeywordRoutingGroupStats struct {
	GroupID int64 `json:"group_id"`
	KeywordRoutingCounter
}
type KeywordRoutingKeywordStats struct {
	Keyword string `json:"keyword"`
	Hits    int64  `json:"hits"`
}
type KeywordRoutingTodayStats struct {
	Date     string `json:"date"`
	Timezone string `json:"timezone"`
	KeywordRoutingCounter
	Groups   []KeywordRoutingGroupStats   `json:"groups"`
	Keywords []KeywordRoutingKeywordStats `json:"keywords"`
}
type KeywordRoutingStatsStore interface {
	Record(context.Context, time.Time, int64, []string) error
	Today(context.Context, *int64) (*KeywordRoutingTodayStats, error)
}
type keywordRoutingRuntime struct {
	config  KeywordRoutingConfig
	matcher *contentModerationKeywordMatcher
	expires time.Time
	raw     string
}

func ValidateKeywordRoutingExtra(extra map[string]any) error {
	if value, exists := extra[KeywordRoutingEnabledExtraKey]; exists {
		if _, ok := value.(bool); !ok {
			return infraerrors.BadRequest("INVALID_KEYWORD_ROUTING_ENABLED", "keyword_routing_enabled must be a boolean")
		}
	}
	return nil
}
func (a *Account) KeywordRoutingEnabled() bool {
	if a == nil {
		return false
	}
	enabled, _ := a.Extra[KeywordRoutingEnabledExtraKey].(bool)
	return enabled
}
func normalizeKeywordRoutingConfig(config KeywordRoutingConfig) (KeywordRoutingConfig, error) {
	out := KeywordRoutingConfig{Keywords: make([]string, 0, len(config.Keywords))}
	seen := make(map[string]struct{}, len(config.Keywords))
	for _, word := range config.Keywords {
		word = strings.TrimSpace(word)
		if word == "" {
			continue
		}
		if !utf8.ValidString(word) || utf8.RuneCountInString(word) > 200 {
			return out, infraerrors.BadRequest("INVALID_KEYWORD", "keywords must contain at most 200 characters")
		}
		key := strings.ToLower(word)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out.Keywords = append(out.Keywords, word)
		if len(out.Keywords) > 10000 {
			return out, infraerrors.BadRequest("TOO_MANY_KEYWORDS", "at most 10000 keywords are allowed")
		}
	}
	return out, nil
}
func (s *SettingService) keywordRoutingRuntime(ctx context.Context) (*keywordRoutingRuntime, error) {
	if s == nil || s.settingRepo == nil {
		return &keywordRoutingRuntime{}, nil
	}
	if cached := s.keywordRoutingCache.Load(); cached != nil && time.Now().Before(cached.expires) {
		return cached, nil
	}
	s.keywordRoutingMu.Lock()
	defer s.keywordRoutingMu.Unlock()
	if cached := s.keywordRoutingCache.Load(); cached != nil && time.Now().Before(cached.expires) {
		return cached, nil
	}
	raw, err := s.settingRepo.GetValue(ctx, keywordRoutingSettingKey)
	if err != nil && !errors.Is(err, ErrSettingNotFound) {
		return nil, err
	}
	if cached := s.keywordRoutingCache.Load(); cached != nil && cached.raw == raw {
		refreshed := *cached
		refreshed.expires = time.Now().Add(5 * time.Second)
		s.keywordRoutingCache.Store(&refreshed)
		return &refreshed, nil
	}
	config := KeywordRoutingConfig{Keywords: []string{}}
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &config); err != nil {
			return nil, fmt.Errorf("invalid keyword routing configuration: %w", err)
		}
	}
	config, err = normalizeKeywordRoutingConfig(config)
	if err != nil {
		return nil, err
	}
	runtime := &keywordRoutingRuntime{config: config, matcher: newContentModerationKeywordMatcher(config.Keywords), expires: time.Now().Add(5 * time.Second), raw: raw}
	s.keywordRoutingCache.Store(runtime)
	return runtime, nil
}
func (s *SettingService) GetKeywordRoutingConfig(ctx context.Context) (KeywordRoutingConfig, error) {
	runtime, err := s.keywordRoutingRuntime(ctx)
	if err != nil {
		return KeywordRoutingConfig{}, err
	}
	return KeywordRoutingConfig{Keywords: append([]string{}, runtime.config.Keywords...)}, nil
}
func (s *SettingService) SetKeywordRoutingConfig(ctx context.Context, config KeywordRoutingConfig) (KeywordRoutingConfig, error) {
	if s == nil || s.settingRepo == nil {
		return KeywordRoutingConfig{}, infraerrors.ServiceUnavailable("KEYWORD_ROUTING_UNAVAILABLE", "keyword routing configuration unavailable")
	}
	config, err := normalizeKeywordRoutingConfig(config)
	if err != nil {
		return config, err
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return config, err
	}
	s.keywordRoutingMu.Lock()
	defer s.keywordRoutingMu.Unlock()
	if err := s.settingRepo.Set(ctx, keywordRoutingSettingKey, string(raw)); err != nil {
		return config, err
	}
	s.keywordRoutingCache.Store(&keywordRoutingRuntime{config: config, matcher: newContentModerationKeywordMatcher(config.Keywords), expires: time.Now().Add(5 * time.Second), raw: string(raw)})
	return config, nil
}
func (s *SettingService) GetKeywordRoutingTodayStats(ctx context.Context, groupID *int64) (*KeywordRoutingTodayStats, error) {
	if s == nil || s.keywordRoutingStats == nil {
		return nil, infraerrors.ServiceUnavailable("KEYWORD_STATS_UNAVAILABLE", "keyword routing statistics unavailable")
	}
	return s.keywordRoutingStats.Today(ctx, groupID)
}

type keywordRoutingRequestKey struct{}
type keywordRoutingPhaseKey struct{}
type keywordRoutingSnapshotKey struct{}

// Reuse the snapshot used for the group gate within one selection attempt.
// A retry fetches a new snapshot, so account and membership changes stay visible.
type keywordRoutingSnapshot struct {
	groupID  int64
	hasGroup bool
	platform string
	forced   bool
	mixed    bool
	accounts []Account
}

func withKeywordRoutingSnapshot(ctx context.Context, groupID *int64, platform string, forced, mixed bool, accounts []Account) context.Context {
	return context.WithValue(ctx, keywordRoutingSnapshotKey{}, keywordRoutingSnapshot{groupID: derefGroupID(groupID), hasGroup: groupID != nil, platform: platform, forced: forced, mixed: mixed, accounts: accounts})
}
func keywordRoutingSnapshotFromContext(ctx context.Context, groupID *int64, platform string, forced bool) ([]Account, bool, bool) {
	snapshot, ok := ctx.Value(keywordRoutingSnapshotKey{}).(keywordRoutingSnapshot)
	if !ok || snapshot.groupID != derefGroupID(groupID) || snapshot.hasGroup != (groupID != nil) || snapshot.platform != platform || snapshot.forced != forced {
		return nil, false, false
	}
	return append([]Account(nil), snapshot.accounts...), snapshot.mixed, true
}

type keywordRoutingRequest struct {
	mu               sync.Mutex
	protocol         string
	body             []byte
	groupID          int64
	ready            bool
	matched          bool
	toolContinuation bool
}
type keywordRoutingPhase struct{ enabled bool }

// The body is immutable client input. Matching remains lazy until the scheduler
// observes an enabled account, and is memoized across retries and fallback groups.
func WithKeywordRoutingRequest(ctx context.Context, protocol string, body []byte, groupID *int64) context.Context {
	if ctx.Value(keywordRoutingRequestKey{}) != nil {
		return ctx
	}
	return context.WithValue(ctx, keywordRoutingRequestKey{}, &keywordRoutingRequest{protocol: protocol, body: body, groupID: derefGroupID(groupID)})
}
func keywordRoutingRequestFromContext(ctx context.Context) *keywordRoutingRequest {
	request, _ := ctx.Value(keywordRoutingRequestKey{}).(*keywordRoutingRequest)
	return request
}
func KeywordRoutingPrefersEnabled(ctx context.Context, account *Account) bool {
	r := keywordRoutingRequestFromContext(ctx)
	if r == nil || !account.KeywordRoutingEnabled() {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready && r.matched && !r.toolContinuation
}
func keywordRoutingAllows(ctx context.Context, account *Account) bool {
	phase, ok := ctx.Value(keywordRoutingPhaseKey{}).(keywordRoutingPhase)
	return !ok || account.KeywordRoutingEnabled() == phase.enabled
}
func keywordRoutingPhaseActive(ctx context.Context) bool {
	_, active := ctx.Value(keywordRoutingPhaseKey{}).(keywordRoutingPhase)
	return active
}
func keywordRoutingDecision(ctx context.Context, settings *SettingService, accounts []Account) (bool, bool, error) {
	if _, nested := ctx.Value(keywordRoutingPhaseKey{}).(keywordRoutingPhase); nested {
		return false, false, nil
	}
	r := keywordRoutingRequestFromContext(ctx)
	if r == nil {
		return false, false, nil
	}
	hasEnabled := false
	for i := range accounts {
		if accounts[i].KeywordRoutingEnabled() {
			hasEnabled = true
			break
		}
	}
	if !hasEnabled {
		return false, false, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.ready {
		runtime, err := settings.keywordRoutingRuntime(ctx)
		if err != nil {
			return false, false, err
		}
		text, continuation := extractKeywordRoutingText(r.protocol, r.body)
		r.toolContinuation = continuation
		var words []string
		if text != "" && runtime.matcher != nil {
			words = runtime.matcher.MatchAll(text)
			r.matched = len(words) > 0
			if settings != nil && settings.keywordRoutingStats != nil {
				statsCtx, cancel := context.WithTimeout(ctx, 150*time.Millisecond)
				err := settings.keywordRoutingStats.Record(statsCtx, time.Now(), r.groupID, words)
				cancel()
				if err != nil {
					slog.Warn("keyword routing statistics failed", "error", err)
				}
			}
		}
		r.ready = true
		r.body = nil
	}
	return true, r.matched, nil
}

func keywordRoutingToolContinuation(ctx context.Context) bool {
	r := keywordRoutingRequestFromContext(ctx)
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.toolContinuation
}

// WebSocket admission uses the initial routing phase; later turns retain the
// admitted upstream connection even if the account's switch changes meanwhile.
func WithoutKeywordRoutingPhase(ctx context.Context) context.Context {
	return context.WithValue(ctx, keywordRoutingPhaseKey{}, struct{}{})
}
func keywordRoutingExclusions(accounts []Account, excluded map[int64]struct{}, enabled bool) map[int64]struct{} {
	result := make(map[int64]struct{}, len(excluded)+len(accounts))
	for id := range excluded {
		result[id] = struct{}{}
	}
	for i := range accounts {
		if accounts[i].KeywordRoutingEnabled() != enabled {
			result[accounts[i].ID] = struct{}{}
		}
	}
	return result
}

// MatchAll scans once and returns each matching keyword once, including suffix
// matches. Output links avoid repeatedly walking nonterminal failure chains.
func (m *contentModerationKeywordMatcher) MatchAll(text string) []string {
	if m == nil {
		return nil
	}
	seen := make([]bool, len(m.keywords))
	matched := make([]string, 0)
	state := int32(0)
	for _, label := range []byte(strings.ToLower(text)) {
		for {
			next := m.next(state, label)
			if next != 0 {
				state = next
				break
			}
			if state == 0 {
				break
			}
			state = m.nodes[state].failure
		}
		for output := state; output != 0; output = m.nodes[output].output {
			index := m.nodes[output].terminalKeyword
			if index >= 0 && !seen[index] {
				seen[index] = true
				matched = append(matched, m.keywords[index])
			}
		}
	}
	return matched
}
