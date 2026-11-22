package service

import (
	"math"
	"time"
)

const (
	openAISchedulerStatsGrace    = 3 * time.Minute
	openAISchedulerStatsHalfLife = 5 * time.Minute
	openAISchedulerTTFTMaxAge    = 30 * time.Minute
	openAISchedulerNeutralError  = 0.1
	openAISchedulerRecoveryEvery = 20
	openAISchedulerRecoveryIdle  = 3 * time.Minute
)

func openAIStatsConfidence(updatedAt int64, now time.Time) float64 {
	if updatedAt <= 0 {
		return 0
	}
	age := now.Sub(time.Unix(0, updatedAt))
	if age <= openAISchedulerStatsGrace {
		return 1
	}
	return math.Exp2(-float64(age-openAISchedulerStatsGrace) / float64(openAISchedulerStatsHalfLife))
}

func openAIAgedErrorRate(rate float64, updatedAt int64, now time.Time) float64 {
	if updatedAt <= 0 {
		return rate
	}
	return openAISchedulerNeutralError + (rate-openAISchedulerNeutralError)*openAIStatsConfidence(updatedAt, now)
}

// Scheduling ages old observations toward neutral confidence. Sticky escape
// continues to use the raw observations until a new result proves recovery.
func (s *openAIAccountRuntimeStats) schedulingSnapshot(accountID int64, now time.Time) (errorRate, ttft float64, hasTTFT bool, staleWeight float64) {
	errorRate, ttft, hasTTFT = s.snapshot(accountID)
	if s == nil {
		return
	}
	value, ok := s.accounts.Load(accountID)
	if !ok {
		return
	}
	stat := value.(*openAIAccountRuntimeStat)
	errorRate = openAIAgedErrorRate(errorRate, stat.resultAt.Load(), now)
	updatedAt := stat.ttftAt.Load()
	if hasTTFT && (updatedAt <= 0 || now.Sub(time.Unix(0, updatedAt)) >= openAISchedulerTTFTMaxAge) {
		return errorRate, 0, false, 1
	}
	return errorRate, ttft, hasTTFT, 1 - openAIStatsConfidence(updatedAt, now)
}

func (s *openAIAccountRuntimeStats) markSelected(accountID int64, now time.Time) {
	stat := s.loadOrCreate(accountID)
	stamp := now.UnixNano()
	for previous := stat.selectedAt.Load(); stamp > previous; previous = stat.selectedAt.Load() {
		if stat.selectedAt.CompareAndSwap(previous, stamp) {
			return
		}
	}
}

func (s *openAIAccountRuntimeStats) lastActivity(account *Account) int64 {
	stamp := account.CreatedAt.UnixNano()
	if account.CreatedAt.IsZero() {
		stamp = 0
	}
	if account.LastUsedAt != nil && account.LastUsedAt.UnixNano() > stamp {
		stamp = account.LastUsedAt.UnixNano()
	}
	if value, ok := s.accounts.Load(account.ID); ok {
		stat := value.(*openAIAccountRuntimeStat)
		for _, at := range []int64{stat.resultAt.Load(), stat.selectedAt.Load(), stat.recoveryAt.Load()} {
			if at > stamp {
				stamp = at
			}
		}
		if stat.recoveryInFlight.Load() {
			return time.Now().UnixNano()
		}
	}
	return stamp
}

// Only real, movable text requests can refresh an idle API-key account. No
// timer, synthetic request, extra DB/Redis round trip, or Compact probe is used.
func (s *defaultOpenAIAccountScheduler) promoteOpenAIRecoveryCandidate(req OpenAIAccountScheduleRequest, order []openAIAccountCandidateScore, preferred int) []openAIAccountCandidateScore {
	if s == nil || s.stats == nil || req.Platform != PlatformOpenAI || req.RequestedModel == "" ||
		req.RequireCompact || req.RequiredImageCapability != "" || req.StickyWeighted ||
		(req.RequiredCapability != "" && req.RequiredCapability != OpenAIEndpointCapabilityChatCompletions && req.RequiredCapability != OpenAIEndpointCapabilityResponses) ||
		req.DisableStickyEscape || req.PreviousResponseID != "" || req.GuardianParentAccountID > 0 ||
		req.selectionNonce == 0 || req.selectionNonce%openAISchedulerRecoveryEvery != 0 || preferred <= 0 || preferred >= len(order) {
		return order
	}
	now := time.Now()
	cutoff := now.Add(-openAISchedulerRecoveryIdle).UnixNano()
	selected, oldest := -1, cutoff
	bestPriority := order[0].account.Priority
	for _, c := range order[:preferred] {
		if c.account.Priority < bestPriority {
			bestPriority = c.account.Priority
		}
	}
	for i := preferred; i < len(order); i++ {
		c := order[i]
		if c.account.Platform != PlatformOpenAI || c.account.Type != AccountTypeAPIKey ||
			c.account.Priority != bestPriority || c.account.ID == req.escapedStickyID ||
			!c.loadKnown || c.loadInfo.CurrentConcurrency != 0 || c.loadInfo.WaitingCount != 0 {
			continue
		}
		activity := s.stats.lastActivity(c.account)
		if activity <= oldest {
			selected, oldest = i, activity
		}
	}
	if selected >= 0 {
		candidate := order[selected]
		candidate.recovery = true
		copy(order[1:selected+1], order[:selected])
		order[0] = candidate
	}
	return order
}

func (s *openAIAccountRuntimeStats) beginRecovery(accountID int64, now time.Time) (func(), bool) {
	stat := s.loadOrCreate(accountID)
	if !stat.recoveryInFlight.CompareAndSwap(false, true) {
		return nil, false
	}
	release := func() { stat.recoveryInFlight.Store(false) }
	cutoff := now.Add(-openAISchedulerRecoveryIdle).UnixNano()
	if stat.recoveryAt.Load() > cutoff || stat.selectedAt.Load() > cutoff || stat.resultAt.Load() > cutoff {
		release()
		return nil, false
	}
	stat.recoveryAt.Store(now.UnixNano())
	return release, true
}
