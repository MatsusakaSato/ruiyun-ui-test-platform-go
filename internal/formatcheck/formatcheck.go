package formatcheck

import (
	"fmt"
	"regexp"
	"strings"

	"ruiyun-ui-test-platform-go/internal/rubric"
)

var (
	reSplit = regexp.MustCompile(`[，,、；;：:/（）()\[\]【】和与及\s]+`)
	rePunct = regexp.MustCompile(`[\s，,。.、；;：:！!？?“”\"'（）()\[\]【】《》<>—\-_/\\|]+`)
	reLead  = regexp.MustCompile(`^(?:包含|包括|具有|具备|提供|给出|需要|必须|要有|应有|涵盖|涉及|有)`)
)

func normText(s string) string {
	return strings.ToLower(rePunct.ReplaceAllString(s, ""))
}

func kindsFromTargets(targets []string) map[string]bool {
	res := make(map[string]bool)
	targetKindsMap := map[string][]string{
		"word":  {"docx"},
		"ppt":   {"pptx"},
		"html":  {"html"},
		"excel": {"excel"},
		"pdf":   {"pdf"},
	}
	for _, t := range targets {
		if kinds, ok := targetKindsMap[t]; ok {
			for _, k := range kinds {
				res[k] = true
			}
		}
	}
	return res
}

func BuildContract(labels map[string]any, kindsOverride []string) map[string]any {
	var targets []string
	if tg, ok := labels["targets"].([]any); ok {
		for _, t := range tg {
			s := strings.TrimSpace(fmt.Sprintf("%v", t))
			if s != "" {
				targets = append(targets, strings.ToLower(s))
			}
		}
	} else if tg, ok := labels["targets"].([]string); ok {
		for _, s := range tg {
			targets = append(targets, strings.ToLower(s))
		}
	}

	kindsSet := make(map[string]bool)
	if kindsOverride != nil {
		for _, k := range kindsOverride {
			kindsSet[k] = true
		}
	} else {
		kindsSet = kindsFromTargets(targets)
	}

	attachment := false
	if att, ok := labels["attachment"].(bool); ok {
		attachment = att
	}

	return map[string]any{
		"targets":    targets,
		"kinds":      kindsSet,
		"attachment": attachment,
	}
}

func TypeConsistency(contract map[string]any, artifactKinds []string) (*float64, map[string]any) {
	kinds, _ := contract["kinds"].(map[string]bool)
	if len(kinds) == 0 {
		return nil, map[string]any{"reason": "未声明也未从问题原文推断出产物类型"}
	}

	gotSet := make(map[string]bool)
	for _, k := range artifactKinds {
		gotSet[k] = true
	}

	var hit []string
	for k := range kinds {
		if gotSet[k] {
			hit = append(hit, k)
		}
	}

	rate := float64(len(hit)) / float64(len(kinds))

	var expectedList []string
	for k := range kinds {
		expectedList = append(expectedList, k)
	}
	var gotList []string
	for k := range gotSet {
		gotList = append(gotList, k)
	}

	return &rate, map[string]any{
		"expected": expectedList,
		"produced": gotList,
		"hit":      hit,
	}
}

func requirementHit(req string, hayNorm string) bool {
	r := normText(req)
	if r == "" {
		return false
	}
	if strings.Contains(hayNorm, r) {
		return true
	}
	core := reLead.ReplaceAllString(r, "")
	if len([]rune(core)) >= 2 && strings.Contains(hayNorm, core) {
		return true
	}
	toks := reSplit.Split(req, -1)
	var validToks []string
	for _, t := range toks {
		if len([]rune(normText(t))) >= 2 {
			validToks = append(validToks, t)
		}
	}
	if len(validToks) == 0 {
		return false
	}
	hits := 0
	for _, t := range validToks {
		tn := normText(t)
		if strings.Contains(hayNorm, tn) || strings.Contains(hayNorm, normText(reLead.ReplaceAllString(t, ""))) {
			hits++
		}
	}
	return float64(hits)/float64(len(validToks)) >= 0.6
}

func RequirementCoverage(requirements []string, haystack string) (*float64, map[string]any) {
	var reqs []string
	for _, r := range requirements {
		s := strings.TrimSpace(r)
		if s != "" {
			reqs = append(reqs, s)
		}
	}
	if len(reqs) == 0 {
		return nil, map[string]any{"reason": "未抽取到显式格式要求"}
	}

	hay := normText(haystack)
	var hit []string
	var missing []string
	for _, r := range reqs {
		if requirementHit(r, hay) {
			hit = append(hit, r)
		} else {
			missing = append(missing, r)
		}
	}
	rate := float64(len(hit)) / float64(len(reqs))
	return &rate, map[string]any{
		"total":   len(reqs),
		"hit":     hit,
		"missing": missing,
	}
}

func Combine(typeRate, covRate *float64, wType, wCoverage float64) (*int, string) {
	type part struct {
		r float64
		w float64
	}
	var parts []part
	if typeRate != nil {
		parts = append(parts, part{*typeRate, wType})
	}
	if covRate != nil {
		parts = append(parts, part{*covRate, wCoverage})
	}
	if len(parts) == 0 {
		return nil, "无格式契约且未抽取到显式要求，不适用"
	}
	totalW := 0.0
	for _, p := range parts {
		totalW += p.w
	}
	if totalW <= 0 {
		totalW = 1.0
	}
	sumR := 0.0
	for _, p := range parts {
		sumR += p.r * p.w
	}
	combinedRate := sumR / totalW
	score := rubric.RatioToScore(combinedRate)
	return &score, fmt.Sprintf("加权比例 %.0f%%", combinedRate*100)
}

func Evaluate(labels map[string]any, artifactKinds []string, requirements []string, haystack string, weights map[string]float64, kindsOverride []string) map[string]any {
	contract := BuildContract(labels, kindsOverride)
	typeRate, typeDetail := TypeConsistency(contract, artifactKinds)
	covRate, covDetail := RequirementCoverage(requirements, haystack)

	wType := 0.5
	wCov := 0.5
	if weights != nil {
		if wt, ok := weights["type"]; ok {
			wType = wt
		}
		if wc, ok := weights["coverage"]; ok {
			wCov = wc
		}
	}

	score, note := Combine(typeRate, covRate, wType, wCov)
	naReason := ""
	if score == nil {
		naReason = note
	}

	return map[string]any{
		"score":         score,
		"basis":         "objective",
		"na_reason":     naReason,
		"note":          note,
		"type_rate":     typeRate,
		"coverage_rate": covRate,
		"attachment":    contract["attachment"],
		"detail": map[string]any{
			"type_consistency":     typeDetail,
			"requirement_coverage": covDetail,
		},
	}
}
