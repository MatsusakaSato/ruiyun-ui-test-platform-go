package report

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ruiyun-ui-test-platform-go/internal/metrics"
	"ruiyun-ui-test-platform-go/internal/models"
)

// renderWith 用给定用例跑一遍完整指标 + 报告渲染，返回 HTML
func renderWith(t *testing.T, caseResults []*models.CaseResult) string {
	t.Helper()
	m := metrics.BuildMetrics(caseResults, map[string]any{}, nil, nil)
	out := filepath.Join(t.TempDir(), "r.html")
	if _, err := RenderReport(m, out, "1.0", "com.x", "test", 1.0); err != nil {
		t.Fatalf("渲染报告失败: %v", err)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func multiTurnCase() *models.CaseResult {
	tr := &models.ExecutionTrace{
		FinalAnswer: "总答复",
		Turns: []*models.TurnTrace{
			{Index: 0, Prompt: "第一轮提问", Answer: "第一轮答复", ElapsedS: 3.5,
				ToolCalls: []*models.ToolCall{{Name: "read"}}, ThinkingSteps: []*models.ThinkingStep{{}}},
			{Index: 1, Prompt: "第二轮提问", Answer: "", ElapsedS: 2.0, Incomplete: true,
				IncompleteWhy: "未等到答复", SystemErrors: []string{"model call failed: 余额为 0"}},
		},
	}
	return &models.CaseResult{
		CaseID:    "CASE-001",
		Name:      "多轮用例",
		Prompt:    "第一轮提问",
		Prompts:   []string{"第一轮提问", "第二轮提问"},
		TurnCount: 2,
		SessionID: "sess_x",
		Trace:     tr,
		TurnFindings: map[int][]*models.Finding{
			1: {{Rule: "TURN_INCOMPLETE", SessionID: "sess_x", Detail: "该轮未收到答复"}},
		},
		TurnElapsedS:  map[int]float64{0: 3.5, 1: 2.0},
		AbortedAtTurn: -1,
	}
}

// TestReportMultiTurnExpands 多轮用例渲染逐轮明细，且轮级发现落在对应轮次里
func TestReportMultiTurnExpands(t *testing.T) {
	html := renderWith(t, []*models.CaseResult{multiTurnCase()})

	for _, want := range []string{
		"逐轮明细（共 2 轮）",
		"第 1 轮", "第 2 轮",
		"第一轮提问", "第二轮提问",
		"未答复",          // 第 2 轮标记未答复
		"TURN_INCOMPLETE", // 轮级发现
		"model call failed", // 该轮的模型报错
	} {
		if !strings.Contains(html, want) {
			t.Errorf("多轮报告应包含 %q", want)
		}
	}
}

// TestReportSingleTurnUnchanged 单轮用例不出现逐轮明细，报告与改造前一致
func TestReportSingleTurnUnchanged(t *testing.T) {
	cr := &models.CaseResult{
		CaseID: "CASE-002", Name: "单轮用例",
		Prompt: "单轮提问", Prompts: []string{"单轮提问"}, TurnCount: 1,
		SessionID: "sess_y",
		Trace: &models.ExecutionTrace{
			FinalAnswer: "答复内容",
			Turns: []*models.TurnTrace{
				{Index: 0, Prompt: "单轮提问", Answer: "答复内容", ElapsedS: 1.0},
			},
		},
		AbortedAtTurn: -1,
	}
	html := renderWith(t, []*models.CaseResult{cr})

	if strings.Contains(html, "逐轮明细") {
		t.Error("单轮用例不应渲染逐轮明细")
	}
	if !strings.Contains(html, "单轮提问") {
		t.Error("单轮用例的提问仍应正常展示")
	}
	if strings.Contains(html, "第 1 轮") {
		t.Error("单轮用例不应出现「第 N 轮」字样")
	}
}

// TestReportMultiTurnNoTurnsFallsBack 标了多轮但轨迹缺失时不炸，退回用例级展示
func TestReportMultiTurnNoTurnsFallsBack(t *testing.T) {
	cr := &models.CaseResult{
		CaseID: "CASE-003", Name: "轨迹缺失",
		Prompt: "第一轮", Prompts: []string{"第一轮", "第二轮"}, TurnCount: 2,
		SessionID: "sess_z", Trace: nil, AbortedAtTurn: -1,
	}
	html := renderWith(t, []*models.CaseResult{cr})
	// turn_count>1 会进逐轮区，但 turns 为空 -> 区块内没有轮次行
	if !strings.Contains(html, "逐轮明细") {
		t.Error("多轮用例应出现逐轮明细标题")
	}
	if strings.Contains(html, "第 1 轮") {
		t.Error("轨迹缺失时不应虚拟出轮次行")
	}
}

// TestReportAbortedTurnsExplained 提交 3 轮但只跑完 2 轮时，
// 报告要说清楚「提交几轮、实际跑几轮、在第几轮中止」，
// 不能让读者以为漏了内容。
func TestReportAbortedTurnsExplained(t *testing.T) {
	cr := &models.CaseResult{
		CaseID: "CASE-004", Name: "中途中止",
		Prompt: "第一轮", Prompts: []string{"第一轮", "第二轮", "第三轮"},
		TurnCount: 3, SessionID: "sess_abort",
		AbortedAtTurn: 2,
		Trace: &models.ExecutionTrace{
			Turns: []*models.TurnTrace{
				{Index: 0, Prompt: "第一轮", Answer: "a0", ElapsedS: 1},
				{Index: 1, Prompt: "第二轮", Answer: "a1", ElapsedS: 1},
			},
		},
	}
	html := renderWith(t, []*models.CaseResult{cr})

	if !strings.Contains(html, "实际执行 2 轮") {
		t.Error("标题应说明实际只执行了 2 轮")
	}
	if !strings.Contains(html, "未跑完") {
		t.Error("应有一段说明该用例未跑完")
	}
	if !strings.Contains(html, "第 3 轮中止") {
		t.Error("应说明在第 3 轮中止（aborted_at_turn=2 是 0-based）")
	}
	// 只渲染实际存在的两轮
	if strings.Contains(html, "提问：第三轮") {
		t.Error("未执行的第三轮不应渲染成轮次块")
	}
}
