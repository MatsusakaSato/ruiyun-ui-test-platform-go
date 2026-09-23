// Command evaldiff 是 P0 差分验证工装的「评估前置层」探针。
//
// 覆盖三个**不依赖 LLM**的纯函数模块：
//
//	core/format_check.py  ↔ internal/formatcheck
//	core/safety_scan.py   ↔ internal/safetyscan
//	core/case_intent.py   ↔ internal/intent
//
// 这三层是 evaluator 打分的事实来源，且全部是确定性函数 ——
// 因此在没有 stub OpenAI server 的情况下也能做**完整**差分。
//
// 用法: evaldiff <inputsJSON>
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"ruiyun-ui-test-platform-go/internal/formatcheck"
	"ruiyun-ui-test-platform-go/internal/intent"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/safetyscan"
)

type caseIn struct {
	Labels        map[string]any     `json:"labels"`
	ArtifactKinds []string           `json:"artifact_kinds"`
	Requirements  []string           `json:"requirements"`
	Haystack      string             `json:"haystack"`
	Weights       map[string]float64 `json:"weights"`
	Kinds         []string           `json:"kinds"`
	FinalAnswer   string             `json:"final_answer"`
	ArtifactTexts [][]string         `json:"artifact_texts"`
	ToolCalls     []struct {
		Name      string `json:"name"`
		Arguments any    `json:"arguments"`
	} `json:"tool_calls"`
	Prompt string `json:"prompt"`
}

type payload struct {
	Cases map[string]caseIn `json:"cases"`
	Order []string          `json:"order"`
}

func hitToMap(h *safetyscan.Hit) map[string]any {
	return map[string]any{
		"rule":       h.Rule,
		"label":      h.Label,
		"source":     h.Source,
		"snippet":    h.Snippet,
		"exempt":     h.Exempt,
		"executable": h.Executable,
	}
}

func hitsToMaps(hs []*safetyscan.Hit) []any {
	out := []any{}
	for _, h := range hs {
		out = append(out, hitToMap(h))
	}
	return out
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: evaldiff <inputsJSON>")
		os.Exit(2)
	}
	b, err := os.ReadFile(os.Args[1])
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取失败: %v\n", err)
		os.Exit(1)
	}
	var p payload
	if err := json.Unmarshal(b, &p); err != nil {
		fmt.Fprintf(os.Stderr, "解析失败: %v\n", err)
		os.Exit(1)
	}

	out := map[string]any{
		"formatcheck": map[string]any{},
		"safetyscan":  map[string]any{},
		"intent":      map[string]any{},
		"errors":      map[string]string{},
	}
	fcOut := out["formatcheck"].(map[string]any)
	ssOut := out["safetyscan"].(map[string]any)
	inOut := out["intent"].(map[string]any)
	errOut := out["errors"].(map[string]string)

	for _, cid := range p.Order {
		c, ok := p.Cases[cid]
		if !ok {
			continue
		}

		// ---- formatcheck ----
		func() {
			defer func() {
				if r := recover(); r != nil {
					errOut["formatcheck:"+cid] = fmt.Sprint(r)
				}
			}()
			reqs := c.Requirements
			if reqs == nil {
				reqs = []string{}
			}
			kinds := c.ArtifactKinds
			if kinds == nil {
				kinds = []string{}
			}
			fcOut[cid] = formatcheck.Evaluate(c.Labels, kinds, reqs, c.Haystack, c.Weights, c.Kinds)
		}()

		// ---- safetyscan ----
		func() {
			defer func() {
				if r := recover(); r != nil {
					errOut["safetyscan:"+cid] = fmt.Sprint(r)
				}
			}()
			pairs := make([][2]string, 0, len(c.ArtifactTexts))
			for _, kv := range c.ArtifactTexts {
				if len(kv) >= 2 {
					pairs = append(pairs, [2]string{kv[0], kv[1]})
				}
			}
			tcs := make([]models.ToolCall, 0, len(c.ToolCalls))
			for _, t := range c.ToolCalls {
				tcs = append(tcs, models.ToolCall{Name: t.Name, Arguments: t.Arguments})
			}
			sources := safetyscan.BuildSourcesOrdered(c.FinalAnswer, pairs, tcs)

			// 有序输出：顺序本身有语义（决定 hits 顺序与截断），必须比
			srcList := []any{}
			for _, src := range sources {
				srcList = append(srcList, []any{src.Label, src.Text})
			}

			hits := safetyscan.ScanOrdered(sources)
			ssOut[cid] = map[string]any{
				"sources":     srcList,
				"hits":        hitsToMaps(hits),
				"redlines":    hitsToMaps(safetyscan.Redlines(hits)),
				"exempt":      hitsToMaps(safetyscan.ExemptHits(hits)),
				"has_redline": len(safetyscan.Redlines(hits)) > 0,
			}
		}()

		// ---- intent ----
		func() {
			defer func() {
				if r := recover(); r != nil {
					errOut["intent:"+cid] = fmt.Sprint(r)
				}
			}()
			kindSet, kindWhy := intent.InferTargetKinds(c.Prompt)
			kinds := make([]string, 0, len(kindSet))
			for k := range kindSet {
				kinds = append(kinds, k)
			}
			sort.Strings(kinds)
			scene, sceneWhy := intent.InferScene(c.Prompt)
			inOut[cid] = map[string]any{
				"kinds":     kinds,
				"kind_why":  nonNil(kindWhy),
				"scene":     scene,
				"scene_why": nonNil(sceneWhy),
			}
		}()
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "输出失败: %v\n", err)
		os.Exit(1)
	}
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
