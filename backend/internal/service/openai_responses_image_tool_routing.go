package service

import (
	"bytes"
	"context"
	"strconv"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type responsesImageToolRoutingKey struct{}
type responsesImageCatalogRequiredKey struct{}

type responsesImageToolRouting struct {
	models           []string
	requireResponses bool
}

// PrepareOpenAIResponsesImageToolRequest keeps optional hosted image tools out
// of groups that do not permit them, and enables catalog-aware selection for
// groups that do. Dedicated image models retain their normal permission gate.
func PrepareOpenAIResponsesImageToolRequest(ctx context.Context, group *Group, model string, body []byte, requireResponses bool) (context.Context, []byte, error) {
	if isOpenAIImageGenerationModel(model) {
		return ctx, body, nil
	}
	policy, found := responsesImageToolPolicy(body)
	if !found {
		return ctx, body, nil
	}
	if !GroupAllowsImageGeneration(group) {
		updated, _, err := stripNativeResponsesImageTool(body)
		return ctx, updated, err
	}
	policy.requireResponses = requireResponses
	return context.WithValue(ctx, responsesImageToolRoutingKey{}, policy), body, nil
}

// A WebSocket session keeps its selected account. Recheck every turn before
// forwarding, including tools added after the initial account selection.
func prepareResponsesImageToolForAccount(ctx context.Context, group *Group, account *Account, model string, body []byte) ([]byte, bool, error) {
	if account == nil || account.Platform != PlatformOpenAI {
		return body, false, nil
	}
	imageCtx, prepared, err := PrepareOpenAIResponsesImageToolRequest(ctx, group, model, body, true)
	if err != nil {
		return nil, false, err
	}
	updated, removed, err := applyResponsesImageToolAccountFallback(imageCtx, account, prepared)
	return updated, removed || !bytes.Equal(body, prepared), err
}

func responsesImageToolPolicy(body []byte) (responsesImageToolRouting, bool) {
	var policy responsesImageToolRouting
	if !bytes.Contains(body, []byte(`"image_generation"`)) {
		return policy, false
	}
	found := false
	visit := func(tools gjson.Result) {
		tools.ForEach(func(_, tool gjson.Result) bool {
			if tool.Get("type").String() == "image_generation" {
				found = true
				if model := strings.TrimSpace(tool.Get("model").String()); model != "" {
					policy.models = append(policy.models, model)
				}
			}
			return true
		})
	}
	visit(gjson.GetBytes(body, "tools"))
	gjson.GetBytes(body, "input").ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "additional_tools" {
			visit(item.Get("tools"))
		}
		return true
	})
	return policy, found
}

func responsesImageToolRoutingFromContext(ctx context.Context) (responsesImageToolRouting, bool) {
	if ctx == nil {
		return responsesImageToolRouting{}, false
	}
	policy, ok := ctx.Value(responsesImageToolRoutingKey{}).(responsesImageToolRouting)
	return policy, ok
}

func accountSupportsResponsesImageTool(account *Account, policy responsesImageToolRouting) bool {
	if account == nil || account.Platform != PlatformOpenAI || account.isRateLimitActiveForKey(openAIImageGenerationRateLimitKey) ||
		!account.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilityResponses) {
		return false
	}
	// Model aliases are not evidence: only actual mapped upstream image models
	// count. A wildcard or an image alias mapped to a text model cannot qualify.
	imageModels := make(map[string]bool)
	for _, model := range account.GetModelMapping() {
		model = strings.TrimSpace(model)
		if IsGPTImageGenerationModel(model) && !strings.Contains(model, "*") {
			imageModels[model] = true
		}
	}
	if len(imageModels) == 0 {
		return false
	}
	for _, requested := range policy.models {
		if !imageModels[requested] {
			return false
		}
	}
	return true
}

func responsesImageCatalogAllowsAccount(ctx context.Context, account *Account) bool {
	if ctx == nil {
		return true
	}
	required, _ := ctx.Value(responsesImageCatalogRequiredKey{}).(bool)
	if !required {
		return true
	}
	policy, _ := responsesImageToolRoutingFromContext(ctx)
	return accountSupportsResponsesImageTool(account, policy)
}

// applyResponsesImageToolAccountFallback runs before protocol conversion and
// passthrough so they see the same sanitized request and billing intent.
func applyResponsesImageToolAccountFallback(ctx context.Context, account *Account, body []byte) ([]byte, bool, error) {
	policy, enabled := responsesImageToolRoutingFromContext(ctx)
	if !enabled || accountSupportsResponsesImageTool(account, policy) {
		return body, false, nil
	}
	return stripNativeResponsesImageTool(body)
}

// Strip only the hosted tool. image_gen namespaces and client functions are
// independent client-side capabilities and must survive this fallback.
func stripNativeResponsesImageTool(body []byte) ([]byte, bool, error) {
	modified := false
	remove := func(path string) error {
		updated, err := sjson.DeleteBytes(body, path)
		if err == nil {
			body = updated
			modified = true
		}
		return err
	}
	strip := func(path string) error {
		tools := gjson.GetBytes(body, path).Array()
		for i := len(tools) - 1; i >= 0; i-- {
			if tools[i].Get("type").String() == "image_generation" {
				if err := remove(path + "." + strconv.Itoa(i)); err != nil {
					return err
				}
			}
		}
		if len(tools) > 0 && len(gjson.GetBytes(body, path).Array()) == 0 {
			return remove(path)
		}
		return nil
	}
	if err := strip("tools"); err != nil {
		return nil, false, err
	}
	input := gjson.GetBytes(body, "input").Array()
	additionalToolsRemain := false
	for i := len(input) - 1; i >= 0; i-- {
		if input[i].Get("type").String() != "additional_tools" {
			continue
		}
		path := "input." + strconv.Itoa(i)
		if err := strip(path + ".tools"); err != nil {
			return nil, false, err
		}
		if !gjson.GetBytes(body, path+".tools").Exists() {
			if err := remove(path); err != nil {
				return nil, false, err
			}
		} else {
			additionalToolsRemain = true
		}
	}
	choice := gjson.GetBytes(body, "tool_choice")
	if choice.Get("type").String() == "allowed_tools" {
		if err := strip("tool_choice.tools"); err != nil {
			return nil, false, err
		}
	}
	if choice.String() == "image_generation" || choice.Get("type").String() == "image_generation" || choice.Get("tool.type").String() == "image_generation" || choice.Get("function.name").String() == "image_generation" ||
		(choice.Get("type").String() == "allowed_tools" && !gjson.GetBytes(body, "tool_choice.tools").Exists()) ||
		(choice.String() == "required" && !gjson.GetBytes(body, "tools").Exists() && !additionalToolsRemain) {
		if err := remove("tool_choice"); err != nil {
			return nil, false, err
		}
	}
	return body, modified, nil
}

func withResponsesImageCatalogRequirement(ctx context.Context, required bool) context.Context {
	ctx = context.WithValue(ctx, responsesImageCatalogRequiredKey{}, required)
	return context.WithValue(ctx, ctxkey.OpenAIImageGenerationIntent, required)
}
