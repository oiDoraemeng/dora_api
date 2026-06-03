package service

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"gopkg.in/yaml.v3"
)

type TokenBillingMultipliers struct {
	Input  float64
	Output float64
	Cache  float64
}

type tokenBillingOverridesFile struct {
	Users []tokenBillingUserOverride `yaml:"users"`
}

type tokenBillingUserOverride struct {
	UserID int64    `yaml:"user_id"`
	Input  *float64 `yaml:"input"`
	Output *float64 `yaml:"output"`
	Cache  *float64 `yaml:"cache"`
}

type tokenBillingMultiplierOverride struct {
	Input  *float64
	Output *float64
	Cache  *float64
}

type tokenBillingOverrideResolver struct {
	byUserID map[int64]tokenBillingMultiplierOverride
}

func loadTokenBillingOverrideResolver(cfg *config.Config) (*tokenBillingOverrideResolver, error) {
	resolver := &tokenBillingOverrideResolver{byUserID: make(map[int64]tokenBillingMultiplierOverride)}

	data, err := os.ReadFile(resolveTokenBillingOverridesFile(cfg))
	if err != nil {
		if os.IsNotExist(err) {
			return resolver, nil
		}
		return nil, fmt.Errorf("read token billing overrides: %w", err)
	}

	var payload tokenBillingOverridesFile
	if err := yaml.Unmarshal(data, &payload); err != nil {
		return nil, fmt.Errorf("parse token billing overrides: %w", err)
	}

	for idx, item := range payload.Users {
		if item.UserID <= 0 {
			return nil, fmt.Errorf("token billing override users[%d].user_id must be positive", idx)
		}
		override := tokenBillingMultiplierOverride{Input: item.Input, Output: item.Output, Cache: item.Cache}
		if err := validateTokenBillingMultiplierOverride(override); err != nil {
			return nil, fmt.Errorf("token billing override users[%d]: %w", idx, err)
		}
		resolver.byUserID[item.UserID] = override
	}

	return resolver, nil
}

func resolveTokenBillingOverridesFile(cfg *config.Config) string {
	dataDir := "./data"
	if cfg != nil {
		if configured := strings.TrimSpace(cfg.Pricing.DataDir); configured != "" {
			dataDir = configured
		}
	}
	return filepath.Join(dataDir, "token_billing_overrides.yaml")
}

func (r *tokenBillingOverrideResolver) Resolve(userID int64) (tokenBillingMultiplierOverride, bool) {
	if r == nil || userID <= 0 {
		return tokenBillingMultiplierOverride{}, false
	}
	multipliers, ok := r.byUserID[userID]
	return multipliers, ok
}

func validateTokenBillingMultiplierOverride(m tokenBillingMultiplierOverride) error {
	if m.Input == nil && m.Output == nil && m.Cache == nil {
		return fmt.Errorf("at least one multiplier must be set")
	}
	if m.Input != nil && *m.Input <= 0 {
		return fmt.Errorf("input multiplier must be positive")
	}
	if m.Output != nil && *m.Output <= 0 {
		return fmt.Errorf("output multiplier must be positive")
	}
	if m.Cache != nil && *m.Cache <= 0 {
		return fmt.Errorf("cache multiplier must be positive")
	}
	return nil
}
