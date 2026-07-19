package service

import "strings"

func openAIResponseModelMatchesReplacementTarget(actualModel, targetModel string) bool {
	actual := strings.TrimSpace(actualModel)
	target := strings.TrimSpace(targetModel)
	if actual == "" || target == "" {
		return false
	}
	if actual == target {
		return true
	}
	if !strings.HasPrefix(actual, target+"-") {
		return false
	}
	return isOpenAIResponseModelDateSuffix(actual[len(target)+1:])
}

func isOpenAIResponseModelDateSuffix(suffix string) bool {
	switch len(suffix) {
	case 8:
		return openAIResponseModelSuffixDigits(suffix)
	case 10:
		return openAIResponseModelSuffixDigits(suffix[0:4]) &&
			suffix[4] == '-' &&
			openAIResponseModelSuffixDigits(suffix[5:7]) &&
			suffix[7] == '-' &&
			openAIResponseModelSuffixDigits(suffix[8:10])
	default:
		return false
	}
}

func openAIResponseModelSuffixDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
