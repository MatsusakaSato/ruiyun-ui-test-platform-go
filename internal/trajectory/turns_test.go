package trajectory

import (
	"testing"

	"ruiyun-ui-test-platform-go/internal/models"
)

func multiTurnCase() *models.CaseResult {
	turns := []*models.TurnTrace{
		{Index: 0, Prompt: "第一轮提问", Answer: "第一轮答复", ElapsedS: 8.34, ToolCalls: []*models.ToolCall{{Name: "a"}}},
		{Index: 1, Prompt: "第二轮提问", Answer: "第二轮答复", ElapsedS: 13.92, ToolCalls: []*models.ToolCall{{Name: "b"}, {Name: "c"}}, ThinkingSteps: []*models.ThinkingStep{{Index: 0, Content: "想"}}},
	}
	return &models.CaseResult{
		CaseID: "CASE-001", Name: "多轮",
		Prompt: "第一轮提问", Prompts: []string{"第一轮提问", "第二轮提问"},
		TurnCount: 2, AbortedAtTurn: -1, SessionID: "sess_x",
		Trace: &models.ExecutionTrace{
			SessionID: "sess_x", Mode: "plan", FinalAnswer: "第二轮答复",
			Turns: turns,
		},
		TurnFindings: map[int][]*models.Finding{
			1: {{Rule: "TURN_MODEL_ERROR", SessionID: "sess_x", Detail: "model call failed"}},
		},
	}
}

// TestBuildCaseDetailCarriesTurns 归档必须带上每一轮，
// 否则前端与报告只能看到第 1 轮（表现为「多轮用例只展示了一轮」）。
func TestBuildCaseDetailCarriesTurns(t *testing.T) {
	d := BuildCaseDetail(multiTurnCase(), map[string][]*models.Finding{}, map[string]any{})

	prompts, _ := d["prompts"].([]string)
	if len(prompts) != 2 || prompts[1] != "第二轮提问" {
		t.Errorf("prompts 应含两轮，实际 %#v", d["prompts"])
	}
	if d["turn_count"] != 2 {
		t.Errorf("turn_count 应为 2，实际 %v", d["turn_count"])
	}
	if d["aborted_at_turn"] != -1 {
		t.Errorf("aborted_at_turn 应为 -1，实际 %v", d["aborted_at_turn"])
	}

	turns, _ := d["turns"].([]map[string]any)
	if len(turns) != 2 {
		t.Fatalf("turns 应为 2 轮，实际 %d", len(turns))
	}
	// 第 1 轮
	if turns[0]["prompt"] != "第一轮提问" || turns[0]["answer_chars"] != len([]rune("第一轮答复")) {
		t.Errorf("第 1 轮字段不对：%#v", turns[0])
	}
	if turns[0]["tool_calls"] != 1 {
		t.Errorf("第 1 轮工具数应为 1，实际 %v", turns[0]["tool_calls"])
	}
	// 第 2 轮
	if turns[1]["prompt"] != "第二轮提问" {
		t.Errorf("第 2 轮提问应为「第二轮提问」，实际 %v", turns[1]["prompt"])
	}
	if turns[1]["tool_calls"] != 2 {
		t.Errorf("第 2 轮工具数应为 2，实际 %v", turns[1]["tool_calls"])
	}
	if turns[1]["thinking_steps"] != 1 {
		t.Errorf("第 2 轮思考步应为 1，实际 %v", turns[1]["thinking_steps"])
	}
	// 轮级 findings 要跟着该轮走
	fs, _ := turns[1]["findings"].([]map[string]any)
	if len(fs) != 1 || fs[0]["rule"] != "TURN_MODEL_ERROR" {
		t.Errorf("第 2 轮应带 1 条轮级 finding，实际 %#v", turns[1]["findings"])
	}
	// 耗时保留一位小数
	if turns[1]["elapsed_s"] != 13.9 {
		t.Errorf("第 2 轮耗时应为 13.9，实际 %v", turns[1]["elapsed_s"])
	}
}

// TestBuildCaseDetailSingleTurnStillHasTurnsField 单轮：turns 是空切片而非 null
func TestBuildCaseDetailSingleTurnStillHasTurnsField(t *testing.T) {
	cr := &models.CaseResult{
		CaseID: "C1", Prompt: "单轮提问", Prompts: []string{"单轮提问"},
		TurnCount: 1, AbortedAtTurn: -1, SessionID: "s",
		Trace: &models.ExecutionTrace{SessionID: "s", FinalAnswer: "答复"},
	}
	d := BuildCaseDetail(cr, map[string][]*models.Finding{}, map[string]any{})

	prompts, _ := d["prompts"].([]string)
	if len(prompts) != 1 {
		t.Errorf("单轮 prompts 应为长度 1，实际 %#v", d["prompts"])
	}
	// nil 会被序列化成 null，前端 `?? []` 与 null 处理不等价
	if d["turns"] == nil {
		t.Error("turns 不应为 nil（要序列化成 []）")
	}
	turns, _ := d["turns"].([]map[string]any)
	if turns == nil {
		t.Fatal("turns 应为空切片")
	}
	if len(turns) != 0 {
		t.Errorf("单轮不应有轮次行，实际 %d", len(turns))
	}
}

// TestBuildCaseDetailNoTraceStillCarriesTurnMeta 没有 trace 时，
// 多轮元信息也不能丢（否则界面会显示成单轮）。
func TestBuildCaseDetailNoTraceStillCarriesTurnMeta(t *testing.T) {
	cr := &models.CaseResult{
		CaseID: "C1", Prompt: "第一轮", Prompts: []string{"第一轮", "第二轮"},
		TurnCount: 2, AbortedAtTurn: 1, SessionID: "s",
	}
	d := BuildCaseDetail(cr, map[string][]*models.Finding{}, map[string]any{})

	prompts, _ := d["prompts"].([]string)
	if len(prompts) != 2 {
		t.Errorf("无 trace 时 prompts 仍应含两轮，实际 %#v", d["prompts"])
	}
	if d["turn_count"] != 2 {
		t.Errorf("无 trace 时 turn_count 应为 2，实际 %v", d["turn_count"])
	}
	if d["aborted_at_turn"] != 1 {
		t.Errorf("无 trace 时 aborted_at_turn 应为 1，实际 %v", d["aborted_at_turn"])
	}
	if d["turns"] == nil {
		t.Error("turns 不应为 nil")
	}
}

// TestBuildRoundDetailKeepsTurns 整轮归档里每个用例都带轮次
func TestBuildRoundDetailKeepsTurns(t *testing.T) {
	detail := BuildRoundDetail(
		[]*models.CaseResult{multiTurnCase()},
		map[string]any{}, nil, map[string]float64{}, map[string]any{})

	cases, _ := detail["cases"].([]map[string]any)
	if len(cases) != 1 {
		t.Fatalf("应有 1 个用例，实际 %d", len(cases))
	}
	turns, _ := cases[0]["turns"].([]map[string]any)
	if len(turns) != 2 {
		t.Errorf("整轮归档里应保留 2 轮，实际 %d", len(turns))
	}
}
