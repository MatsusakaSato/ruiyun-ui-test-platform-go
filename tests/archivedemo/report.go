//go:build ignore

// 用真实会话目录渲染一份报告，核对多轮展示效果。
// 用法：go run tests/archivedemo/report.go <session_dir> <out.html>
package main

import (
	"fmt"
	"os"

	"ruiyun-ui-test-platform-go/internal/logparser"
	"ruiyun-ui-test-platform-go/internal/metrics"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/report"
)

func main() {
	sessDir, out := os.Args[1], os.Args[2]
	tr, err := logparser.ParseSession(sessDir)
	if err != nil {
		fmt.Println("解析失败:", err)
		os.Exit(1)
	}
	prompts := []string{}
	for _, t := range tr.Turns {
		prompts = append(prompts, t.Prompt)
	}
	cr := &models.CaseResult{
		CaseID: "CASE-001", Name: "用例 1",
		SessionID: tr.SessionID, Trace: tr,
		Prompts: prompts, TurnCount: len(prompts), AbortedAtTurn: -1,
		TurnFindings: map[int][]*models.Finding{},
		TurnElapsedS: map[int]float64{},
	}
	if len(prompts) > 0 {
		cr.Prompt = prompts[0]
	}
	m := metrics.BuildMetrics([]*models.CaseResult{cr}, map[string]any{}, nil, nil)
	if _, err := report.RenderReport(m, out, "0.1.0", "com.ruiyun", "多轮展示核对", 30.0); err != nil {
		panic(err)
	}
	fmt.Println("已生成", out)
}
