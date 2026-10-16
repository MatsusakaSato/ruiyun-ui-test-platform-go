//go:build ignore

// round.go 用真实会话同时跑「报告」与「归档详情」两条链路，核对多轮是否按时间线完整展示。
//
// 用法：GOCACHE=$PWD/.gocache go run tests/archivedemo/round.go <session_dir> <out.html> <out.json>
// （沙箱里默认 GOCACHE 不可写，必须指到工作区内）
//
// 输出：报告每个轮次的提问/调用链路/答复字数/答复原文，以及归档 cases[0].turns
// 的轮次步骤归属与答复原文。「报告只展示最后一轮」这类问题必须用真实会话核对 ——
// 单元测试的 fixture 不会暴露字段漏写的形态。
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"ruiyun-ui-test-platform-go/internal/logparser"
	"ruiyun-ui-test-platform-go/internal/metrics"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/report"
	"ruiyun-ui-test-platform-go/internal/trajectory"
)

func main() {
	tr, err := logparser.ParseSession(os.Args[1])
	if err != nil {
		fmt.Println("解析失败:", err)
		os.Exit(1)
	}
	prompts := []string{}
	for _, t := range tr.Turns {
		prompts = append(prompts, t.Prompt)
	}
	cr := &models.CaseResult{CaseID: "CASE-001", Name: "用例 1", SessionID: tr.SessionID, Trace: tr,
		Prompts: prompts, TurnCount: len(prompts), AbortedAtTurn: -1,
		TurnFindings: map[int][]*models.Finding{}, TurnElapsedS: map[int]float64{}}
	if len(prompts) > 0 {
		cr.Prompt = prompts[0]
	}
	m := metrics.BuildMetrics([]*models.CaseResult{cr}, map[string]any{}, nil, nil)
	if _, err := report.RenderReport(m, os.Args[2], "0.1.0", "com.ruiyun", "多轮核对", 30.0); err != nil {
		fmt.Println("报告渲染失败:", err)
		os.Exit(1)
	}
	detail := trajectory.BuildRoundDetail([]*models.CaseResult{cr}, m, nil, nil, nil)
	b, _ := json.MarshalIndent(detail, "", "  ")
	_ = os.WriteFile(os.Args[3], b, 0644)

	rows, _ := m["case_rows"].([]map[string]any)
	fmt.Println("== 报告 turn_rows ==")
	for _, t := range rows[0]["turns"].([]map[string]any) {
		fmt.Printf("  轮 %v: tools=%v answer_chars=%v answer=%q\n",
			t["index"], t["tools"], t["answer_chars"], trunc(fmt.Sprint(t["answer"]), 24))
	}
	fmt.Println("== 归档 cases[0].turns ==")
	cases, _ := detail["cases"].([]map[string]any)
	for _, t := range cases[0]["turns"].([]map[string]any) {
		steps, _ := t["steps"].([]map[string]any)
		names := []string{}
		for _, s := range steps {
			if s["type"] == "thinking" {
				names = append(names, "(思考)")
				continue
			}
			names = append(names, fmt.Sprint(s["name"]))
		}
		fmt.Printf("  轮 %v: steps=%d %v answer=%q\n", t["index"], len(steps), names, trunc(fmt.Sprint(t["answer"]), 24))
	}
}

func trunc(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
