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

// TestReportMultiTurnRepeatsCaseBlock 多轮用例按轮重复整块，
// 每块与单轮用例长得一样（用户要求：不要多轮样式，像单轮那样多展示几次）。
func TestReportMultiTurnRepeatsCaseBlock(t *testing.T) {
	html := renderWith(t, []*models.CaseResult{multiTurnCase()})

	// 2 轮 -> 2 个用例块
	if n := strings.Count(html, `class="case-block"`); n != 2 {
		t.Errorf("多轮 2 轮应渲染 2 个用例块，实际 %d", n)
	}
	// 每轮各有自己的提示词，且都用同一套「提示词」标题
	if n := strings.Count(html, "提示词："); n != 2 {
		t.Errorf("每轮都应有「提示词」，实际 %d 处", n)
	}
	for _, want := range []string{"第一轮提问", "第二轮提问"} {
		if !strings.Contains(html, want) {
			t.Errorf("应展示轮次提问 %q", want)
		}
	}
	// 轮级发现与模型报错留在各自轮里
	for _, want := range []string{"TURN_INCOMPLETE", "model call failed"} {
		if !strings.Contains(html, want) {
			t.Errorf("应包含 %q", want)
		}
	}
	// 不应出现多轮专属的措辞
	for _, bad := range []string{"逐轮明细", "多轮 ·", "第 1 轮", "第 2 轮"} {
		if strings.Contains(html, bad) {
			t.Errorf("不应出现多轮措辞 %q", bad)
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

// TestReportMultiTurnShowsEveryTurnOutput 报告必须按时间线把每一轮的
// 调用链路与**输出内容**都展示出来：只展示最后一轮的链路和输出，
// 读报告的人会以为前面的轮次没执行（用户报告的问题）。
func TestReportMultiTurnShowsEveryTurnOutput(t *testing.T) {
	tr := &models.ExecutionTrace{
		FinalAnswer: "第二轮答复",
		Turns: []*models.TurnTrace{
			{Index: 0, Prompt: "第一轮提问", Answer: "第一轮答复", ElapsedS: 3.0,
				ToolCalls: []*models.ToolCall{{Index: 0, Name: "read_memory"}}},
			{Index: 1, Prompt: "第二轮提问", Answer: "第二轮答复", ElapsedS: 5.0,
				ToolCalls: []*models.ToolCall{{Index: 1, Name: "write_memory"}}},
		},
	}
	cr := &models.CaseResult{
		CaseID: "CASE-001", Name: "多轮用例",
		Prompt: "第一轮提问", Prompts: []string{"第一轮提问", "第二轮提问"},
		TurnCount: 2, SessionID: "sess_out", Trace: tr, AbortedAtTurn: -1,
	}
	html := renderWith(t, []*models.CaseResult{cr})

	parts := strings.Split(html, `<div class="case-block">`)
	if len(parts) != 3 {
		t.Fatalf("2 轮应渲染 2 个用例块，实际 %d", len(parts)-1)
	}
	first, second := parts[1], parts[2]

	// 每块各带自己的调用链路，不能两块都写最后一轮的
	if !strings.Contains(first, "read_memory") || strings.Contains(first, "write_memory") {
		t.Errorf("第 1 轮应只展示本轮调用链路 read_memory")
	}
	if !strings.Contains(second, "write_memory") || strings.Contains(second, "read_memory") {
		t.Errorf("第 2 轮应只展示本轮调用链路 write_memory")
	}

	// 每块各带自己的输出内容
	if !strings.Contains(first, "第一轮答复") {
		t.Error("第 1 轮块里应有第 1 轮的输出内容")
	}
	if !strings.Contains(second, "第二轮答复") {
		t.Error("第 2 轮块里应有第 2 轮的输出内容")
	}
	// 时间线顺序：第 1 轮的输出在前
	if strings.Index(html, "第一轮答复") > strings.Index(html, "第二轮答复") {
		t.Error("两轮输出内容应按时间线顺序排列")
	}
	// 每轮一个「输出内容」小节
	if n := strings.Count(html, "输出内容"); n != 2 {
		t.Errorf("2 轮应有 2 个「输出内容」小节，实际 %d", n)
	}
}

// TestReportTurnWithoutAnswerSaysSo 轮次没答复时要写清楚，不能留空白
func TestReportTurnWithoutAnswerSaysSo(t *testing.T) {
	html := renderWith(t, []*models.CaseResult{multiTurnCase()})
	if !strings.Contains(html, "该轮未收到答复") {
		t.Error("未完成轮应显式说明「该轮未收到答复」")
	}
}

// TestReportSingleTurnShowsOutput 单轮用例同样要展示输出内容（与多轮同形）
func TestReportSingleTurnShowsOutput(t *testing.T) {
	cr := &models.CaseResult{
		CaseID: "CASE-002", Name: "单轮用例",
		Prompt: "单轮提问", Prompts: []string{"单轮提问"}, TurnCount: 1,
		SessionID: "sess_y", AbortedAtTurn: -1,
		Trace: &models.ExecutionTrace{FinalAnswer: "单轮答复正文",
			Turns: []*models.TurnTrace{{Index: 0, Prompt: "单轮提问", Answer: "单轮答复正文"}}},
	}
	html := renderWith(t, []*models.CaseResult{cr})
	if !strings.Contains(html, "单轮答复正文") {
		t.Error("单轮用例报告应展示输出内容")
	}
	if n := strings.Count(html, "输出内容"); n != 1 {
		t.Errorf("单轮应只有 1 个「输出内容」小节，实际 %d", n)
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
	// 轮次数据缺失时退化成 1 个用例块，用 prompt（第 1 轮）兜底，不能渲染成空白
	if n := strings.Count(html, `class="case-block"`); n != 1 {
		t.Errorf("轨迹缺失时应渲染 1 个用例块，实际 %d", n)
	}
	if !strings.Contains(html, "第一轮") {
		t.Error("轨迹缺失时应回落到 prompt 展示")
	}
	if strings.Contains(html, "第 1 轮") {
		t.Error("不应虚拟出轮次标记")
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

	// 只渲染实际执行的两轮（第 3 轮没跑就不该有块）
	if n := strings.Count(html, `class="case-block"`); n != 2 {
		t.Errorf("只执行了 2 轮就应渲染 2 个块，实际 %d", n)
	}
	if strings.Contains(html, "第三轮提问") {
		t.Error("未执行的第三轮不应渲染")
	}
	// 中止信息要如实带出，不能让读者以为只提交了 2 轮
	if !strings.Contains(html, "中止") && !strings.Contains(html, "未发出") {
		t.Error("应说明该用例中途中止")
	}
}
