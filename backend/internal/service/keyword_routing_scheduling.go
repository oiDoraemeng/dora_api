package service

import (
	"context"
	"errors"
)

func (s *GatewayService) SelectAccountWithLoadAwareness(ctx context.Context, groupID *int64, sessionHash, model string, excluded map[int64]struct{}, metadataUserID string, userID int64) (*AccountSelectionResult, error) {
	if keywordRoutingRequestFromContext(ctx) == nil {
		return s.selectAccountWithLoadAwarenessWithoutKeyword(ctx, groupID, sessionHash, model, excluded, metadataUserID, userID)
	}
	if _, nested := ctx.Value(keywordRoutingPhaseKey{}).(keywordRoutingPhase); nested {
		return s.selectAccountWithLoadAwarenessWithoutKeyword(ctx, groupID, sessionHash, model, excluded, metadataUserID, userID)
	}
	group, effectiveGroupID, err := s.checkClaudeCodeRestriction(ctx, groupID)
	if err != nil {
		return nil, err
	}
	ctx = s.withGroupContext(ctx, group)
	platform, forced, err := s.resolvePlatform(ctx, effectiveGroupID, group, model)
	if err != nil {
		return nil, err
	}
	var accounts []Account
	var mixed bool
	if s.schedulerSnapshot != nil {
		accounts, mixed, err = s.schedulerSnapshot.ListSchedulableAccounts(ctx, effectiveGroupID, platform, forced)
	} else {
		accounts, mixed, err = s.listSchedulableAccounts(ctx, effectiveGroupID, platform, forced)
	}
	if err != nil {
		return nil, err
	}
	ctx = withKeywordRoutingSnapshot(ctx, effectiveGroupID, platform, forced, mixed, accounts)
	active, hit, err := keywordRoutingDecision(ctx, s.settingService, accounts)
	if err != nil {
		return nil, err
	}
	if !active {
		return s.selectAccountWithLoadAwarenessWithoutKeyword(ctx, effectiveGroupID, sessionHash, model, excluded, metadataUserID, userID)
	}
	// Explicit Gemini cachedContent is bound to an upstream account and cannot
	// be reconstructed on another account. Its existing exclusion set pins it.
	if geminiCachedContentBoundFromContext(ctx) {
		return s.selectAccountWithLoadAwarenessWithoutKeyword(ctx, effectiveGroupID, sessionHash, model, excluded, metadataUserID, userID)
	}
	if keywordRoutingToolContinuation(ctx) && sessionHash != "" && s.cache != nil {
		if id, err := s.cache.GetSessionAccountID(ctx, derefGroupID(effectiveGroupID), sessionHash); err == nil && id > 0 {
			for i := range accounts {
				if accounts[i].ID == id {
					hit = accounts[i].KeywordRoutingEnabled()
					break
				}
			}
		}
	}
	selectPool := func(enabled bool) (*AccountSelectionResult, error) {
		phaseCtx := context.WithValue(ctx, keywordRoutingPhaseKey{}, keywordRoutingPhase{enabled: enabled})
		poolExcluded := keywordRoutingExclusions(accounts, excluded, enabled)
		for {
			selection, err := s.selectAccountWithLoadAwarenessWithoutKeyword(phaseCtx, effectiveGroupID, sessionHash, model, poolExcluded, metadataUserID, userID)
			if err == nil && selection != nil && selection.Account != nil && !keywordRoutingAllows(phaseCtx, selection.Account) {
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				if _, alreadyExcluded := poolExcluded[selection.Account.ID]; alreadyExcluded {
					return nil, ErrNoAvailableAccounts
				}
				poolExcluded[selection.Account.ID] = struct{}{}
				continue
			}
			return attachSelectionProfitGate(phaseCtx, selection), err
		}
	}
	if hit {
		selection, err := selectPool(true)
		if !keywordRoutingPoolExhausted(selection, err) {
			return selection, err
		}
	}
	return selectPool(false)
}
func keywordRoutingPoolExhausted(selection *AccountSelectionResult, err error) bool {
	return (err == nil && (selection == nil || selection.Account == nil)) || errors.Is(err, ErrNoAvailableAccounts) || errors.Is(err, ErrNoAvailableCompactAccounts)
}

func (s *OpenAIGatewayService) selectAccountWithScheduler(ctx context.Context, groupID *int64, previousResponseID, sessionHash, model string, excluded map[int64]struct{}, transport OpenAIUpstreamTransport, capability OpenAIEndpointCapability, imageCapability OpenAIImagesCapability, compact bool, platform string, previousCanMove, upstreamCost bool) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
	selectOriginal := func(callCtx context.Context, callExcluded map[int64]struct{}) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		return s.selectAccountWithSchedulerWithoutKeyword(callCtx, groupID, previousResponseID, sessionHash, model, callExcluded, transport, capability, imageCapability, compact, platform, previousCanMove, upstreamCost)
	}
	if keywordRoutingRequestFromContext(ctx) == nil || imageCapability != "" {
		return selectOriginal(ctx, excluded)
	}
	var accounts []Account
	var err error
	if s.schedulerSnapshot != nil {
		accounts, _, err = s.schedulerSnapshot.ListSchedulableAccounts(ctx, groupID, NormalizeOpenAICompatiblePlatform(platform), false)
	} else {
		accounts, err = s.listSchedulableAccounts(ctx, groupID, platform)
	}
	if err != nil {
		return nil, OpenAIAccountScheduleDecision{}, err
	}
	ctx = withKeywordRoutingSnapshot(ctx, groupID, NormalizeOpenAICompatiblePlatform(platform), false, false, accounts)
	active, hit, err := keywordRoutingDecision(ctx, s.settingService, accounts)
	if err != nil {
		return nil, OpenAIAccountScheduleDecision{}, err
	}
	if !active {
		return selectOriginal(ctx, excluded)
	}
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, groupID)
	ctx = s.withOpenAIProfitControlGate(ctx, groupID)
	if keywordRoutingToolContinuation(ctx) && sessionHash != "" && s.cache != nil {
		if id, err := s.getStickySessionAccountID(ctx, groupID, sessionHash); err == nil && id > 0 {
			for i := range accounts {
				if accounts[i].ID == id {
					hit = accounts[i].KeywordRoutingEnabled()
					break
				}
			}
		}
	}
	// A server-side continuation that cannot be reconstructed must keep its
	// actual owner. This exception never relaxes group/capability/privacy gates.
	if previousResponseID != "" && !previousCanMove {
		selection, found, err := s.selectLegacyAccountByPreviousResponse(ctx, groupID, previousResponseID, sessionHash, model, excluded, transport, capability, imageCapability, compact, NormalizeOpenAICompatiblePlatform(platform))
		if err != nil {
			return nil, OpenAIAccountScheduleDecision{}, err
		}
		if found {
			return selection, OpenAIAccountScheduleDecision{Layer: openAIAccountScheduleLayerPreviousResponse, StickyPreviousHit: true, SelectedAccountID: selection.Account.ID, SelectedAccountType: selection.Account.Type}, nil
		}
	}
	selectPool := func(enabled bool) (*AccountSelectionResult, OpenAIAccountScheduleDecision, error) {
		phaseCtx := context.WithValue(ctx, keywordRoutingPhaseKey{}, keywordRoutingPhase{enabled: enabled})
		poolExcluded := keywordRoutingExclusions(accounts, excluded, enabled)
		for {
			selection, decision, err := selectOriginal(phaseCtx, poolExcluded)
			if err == nil && selection != nil && selection.Account != nil && !keywordRoutingAllows(phaseCtx, selection.Account) {
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				if _, alreadyExcluded := poolExcluded[selection.Account.ID]; alreadyExcluded {
					return nil, decision, ErrNoAvailableAccounts
				}
				poolExcluded[selection.Account.ID] = struct{}{}
				continue
			}
			return attachSelectionProfitGate(phaseCtx, selection), decision, err
		}
	}
	if hit {
		selection, decision, err := selectPool(true)
		if !keywordRoutingPoolExhausted(selection, err) {
			return selection, decision, err
		}
	}
	return selectPool(false)
}
