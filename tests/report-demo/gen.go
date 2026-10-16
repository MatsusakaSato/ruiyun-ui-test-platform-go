//go:build ignore

// 生成多轮报告的样例 HTML，用于人工核对展示效果。
// 运行：GOCACHE=$PWD/.gocache go run tests/report-demo/gen.go
package main

import (
	"fmt"
	"path/filepath"

	"ruiyun-ui-test-platform-go/internal/metrics"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/report"
)

func main() {
	single := &models.CaseResult{
		CaseID: "CASE-001", Name: "单轮-记忆读取",
		Prompt: "请读取你的记忆文件并告诉我记录条数", Prompts: []string{"请读取你的记忆文件并告诉我记录条数"},
		TurnCount: 1, SessionID: "sess_single",
		Trace: &models.ExecutionTrace{
			FinalAnswer:  "共 42 条记录",
			ToolCalls:    []*models.ToolCall{{Name: "read_file"}},
			ThinkingSteps: []*models.ThinkingStep{{}},
			Turns: []*models.TurnTrace{{Index: 0, Prompt: "请读取你的记忆文件并告诉我记录条数",
				Answer: "共 42 条记录", ElapsedS: 6.2,
				ToolCalls: []*models.ToolCall{{Name: "read_file"}}, ThinkingSteps: []*models.ThinkingStep{{}}}},
		},
		AbortedAtTurn: -1,
	}

	multi := &models.CaseResult{
		CaseID: "CASE-002", Name: "多轮-先说后改",
		Prompt: "写一首关于秋天的诗", Prompts: []string{"写一首关于秋天的诗", "再改短一点", "翻译成英文"},
		TurnCount: 3, SessionID: "sess_multi",
		Trace: &models.ExecutionTrace{
			FinalAnswer:   "Autumn wind...",
			ToolCalls:     []*models.ToolCall{{Name: "write"}, {Name: "read"}},
			ThinkingSteps: []*models.ThinkingStep{{}, {}},
			Turns: []*models.TurnTrace{
				{Index: 0, Prompt: "写一首关于秋天的诗", Answer: "秋风起兮……", ElapsedS: 8.4,
					ToolCalls: []*models.ToolCall{{Name: "write"}}, ThinkingSteps: []*models.ThinkingStep{{}}},
				{Index: 1, Prompt: "再改短一点", Answer: "秋风起……", ElapsedS: 4.1,
					ToolCalls: []*models.ToolCall{{Name: "read"}}, ThinkingSteps: []*models.ThinkingStep{{}}},
				{Index: 2, Prompt: "翻译成英文", Answer: "", ElapsedS: 3.0,
					Incomplete: true, IncompleteWhy: "等待会话静默超时，该轮未收到完整答复",
					SystemErrors: []string{"[181001] model call failed, reason: 积分已用完，余额为 0"}},
			},
		},
		TurnFindings: map[int][]*models.Finding{
			2: {
				{Rule: "TURN_INCOMPLETE", SessionID: "sess_multi", Detail: "第 3 轮未收到答复"},
				{Rule: "TURN_MODEL_ERROR", SessionID: "sess_multi", Detail: "该轮模型调用报错", Evidence: "积分已用完，余额为 0"},
			},
		},
		TurnElapsedS:  map[int]float64{0: 8.4, 1: 4.1, 2: 3.0},
		AbortedAtTurn: -1,
	}

	m := metrics.BuildMetrics([]*models.CaseResult{single, multi}, map[string]any{}, nil, nil)
	out := filepath.Join("tests", "report-demo", "demo.html")
	if _, err := report.RenderReport(m, out, "0.1.0", "com.ruiyun.demo", "演示", 25.7); err != nil {
		panic(err)
	}
	fmt.Println("已生成", out)
}
