package service

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type gatewayQualityKey struct {
	accountID int64
	model     string
}

type gatewayQualityStat struct {
	mu              sync.Mutex
	errorRate, ttft float64
	updated         time.Time
}

type gatewayQualityStats struct {
	entries sync.Map
	count   atomic.Int64
}

func gatewayQualityModel(account *Account, model string) string {
	return strings.TrimSpace(account.GetMappedModel(model))
}

func (q *gatewayQualityStats) observe(account *Account, model string, failed *bool, ttft float64, now time.Time) {
	key := gatewayQualityKey{account.ID, gatewayQualityModel(account, model)}
	value, ok := q.entries.Load(key)
	if !ok {
		// Unknown client model names must not create unbounded process state.
		if q.count.Load() >= 8192 {
			return
		}
		var loaded bool
		value, loaded = q.entries.LoadOrStore(key, &gatewayQualityStat{})
		if !loaded {
			q.count.Add(1)
		}
	}
	stat := value.(*gatewayQualityStat)
	stat.mu.Lock()
	defer stat.mu.Unlock()
	if !stat.updated.IsZero() {
		stat.errorRate *= math.Exp(-math.Max(0, now.Sub(stat.updated).Seconds()) / 120)
	}
	if failed != nil {
		sample := 0.0
		if *failed {
			sample = 1
		}
		stat.errorRate = .8*stat.errorRate + .2*sample
	}
	if ttft > 0 {
		if stat.ttft == 0 || now.Sub(stat.updated) > 5*time.Minute {
			stat.ttft = ttft
		} else {
			stat.ttft = .7*stat.ttft + .3*ttft
		}
	}
	stat.updated = now
}

func (q *gatewayQualityStats) factors(account *Account, model string, now time.Time) (success, latency float64) {
	success, latency = 1, .5
	value, ok := q.entries.Load(gatewayQualityKey{account.ID, gatewayQualityModel(account, model)})
	if !ok {
		return
	}
	stat := value.(*gatewayQualityStat)
	stat.mu.Lock()
	errorRate, ttft, updated := stat.errorRate, stat.ttft, stat.updated
	stat.mu.Unlock()
	age := math.Max(0, now.Sub(updated).Seconds())
	success = 1 - errorRate*math.Exp(-age/120)
	if ttft > 0 {
		// Old observations gradually become neutral, allowing recovery through
		// normal traffic without permanently excluding the account/model.
		latency += (1/(1+ttft/5000) - .5) * math.Exp(-age/300)
	}
	return
}

func (s *GatewayService) gatewayQualityScore(item accountWithLoad, model string, now time.Time) float64 {
	success, latency := s.qualityStats.factors(item.account, model, now)
	load := clamp01(float64(item.loadInfo.CurrentConcurrency) / float64(max(1, item.account.Concurrency)))
	return .45*success + .25*latency + .30*(1-load)
}

func (s *GatewayService) gatewayStickyDegraded(account *Account, model string) bool {
	if account == nil || account.Platform != PlatformAnthropic || account.Type != AccountTypeAPIKey {
		return false
	}
	success, latency := s.qualityStats.factors(account, model, time.Now())
	return success < .65 || latency < .15
}

func (s *GatewayService) selectByGatewayQuality(candidates []accountWithLoad, model string, preferOAuth bool) *accountWithLoad {
	for _, item := range candidates {
		if item.account.Platform != PlatformAnthropic || item.account.Type != AccountTypeAPIKey {
			return selectByLRU(filterByMinLoadRate(candidates), preferOAuth)
		}
	}
	now := time.Now()
	best := -1.0
	var selected *accountWithLoad
	for i := range candidates {
		item := &candidates[i]
		score := s.gatewayQualityScore(*item, model, now)
		if score > best+1e-6 {
			best, selected = score, item
		} else if math.Abs(score-best) <= 1e-6 {
			selected = selectByLRU([]accountWithLoad{*selected, *item}, preferOAuth)
		}
	}
	return selected
}

func (s *GatewayService) reportGatewayQuality(ctx context.Context, account *Account, model string, result *ForwardResult, err error, elapsed time.Duration) {
	if account == nil || account.Platform != PlatformAnthropic || account.Type != AccountTypeAPIKey {
		return
	}
	var failed *bool
	ttft := 0.0
	if result != nil && result.FirstTokenMs != nil {
		ttft = float64(*result.FirstTokenMs)
	}
	var failover *UpstreamFailoverError
	if errors.As(err, &failover) && (failover.StatusCode >= 500 || failover.StatusCode == 429) {
		value := true
		failed = &value
	} else if err == nil && result != nil && !result.ClientDisconnect {
		value := false
		failed = &value
	}
	// Client cancellations are not failures. A long wait without any first
	// token still provides a lower bound on latency for subsequent selection.
	if ttft == 0 && elapsed >= 10*time.Second && (failed != nil && *failed || ctx.Err() != nil) {
		ttft = float64(elapsed.Milliseconds())
	}
	if failed != nil || ttft > 0 {
		s.qualityStats.observe(account, model, failed, ttft, time.Now())
	}
}
