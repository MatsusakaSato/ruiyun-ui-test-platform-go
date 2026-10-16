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

// TestTurnRowsCarryOwnSteps 每一轮必须带**自己**的步骤（思考 + 工具，按时间线合并），
// 并且带答复原文。只给会话级平铺 steps 时，前端/报告拿不到轮次归属，
// 多轮用例会表现成「所有内容只剩最后一轮」（用户报告的问题）。
func TestTurnRowsCarryOwnSteps(t *testing.T) {
	tr := &models.ExecutionTrace{
		SessionID: "sess_t", FinalAnswer: "第二轮答复",
		// 真实轨迹里平铺字段与轮内切片是一份数据（logparser 同时写入），
		// fixture 也照此构造，避免测出「归档里出现两个事实」的假象。
		ToolCalls: []*models.ToolCall{
			{Index: 0, Name: "read"}, {Index: 1, Name: "write"}, {Index: 2, Name: "search"},
		},
		ThinkingSteps: []*models.ThinkingStep{{Index: 0, Content: "第一轮的思考"}},
		Turns: []*models.TurnTrace{
			{Index: 0, Prompt: "第一轮提问", Answer: "第一轮答复",
				ToolCalls:     []*models.ToolCall{{Index: 0, Name: "read"}, {Index: 1, Name: "write"}},
				ThinkingSteps: []*models.ThinkingStep{{Index: 0, Content: "第一轮的思考"}}},
			{Index: 1, Prompt: "第二轮提问", Answer: "第二轮答复",
				ToolCalls: []*models.ToolCall{{Index: 2, Name: "search"}}},
		},
	}
	cr := &models.CaseResult{
		CaseID: "C1", Prompt: "第一轮提问", Prompts: []string{"第一轮提问", "第二轮提问"},
		TurnCount: 2, AbortedAtTurn: -1, SessionID: "sess_t", Trace: tr,
	}
	d := BuildCaseDetail(cr, map[string][]*models.Finding{}, map[string]any{})

	turns, _ := d["turns"].([]map[string]any)
	if len(turns) != 2 {
		t.Fatalf("turns 应为 2 轮，实际 %d", len(turns))
	}

	// 第 1 轮：读、思考、写 —— 同 index 时工具排在思考前（稳定排序）
	s0, _ := turns[0]["steps"].([]map[string]any)
	got0 := stepNames(s0)
	want0 := []string{"read", "(思考)", "write"}
	if !eqStrs(got0, want0) {
		t.Errorf("第 1 轮步骤应为 %v，实际 %v", want0, got0)
	}

	// 第 2 轮只带自己的步骤，不能把第 1 轮的调用混进来
	s1, _ := turns[1]["steps"].([]map[string]any)
	got1 := stepNames(s1)
	want1 := []string{"search"}
	if !eqStrs(got1, want1) {
		t.Errorf("第 2 轮步骤应为 %v，实际 %v", want1, got1)
	}

	// 答复原文要带出来，前端才能按轮展示「输出内容」
	if turns[0]["answer"] != "第一轮答复" || turns[1]["answer"] != "第二轮答复" {
		t.Errorf("每轮应带答复原文，实际 %#v / %#v", turns[0]["answer"], turns[1]["answer"])
	}
	if turns[1]["answer_chars"] != len([]rune("第二轮答复")) {
		t.Errorf("第 2 轮答复字数不对：%v", turns[1]["answer_chars"])
	}

	// 会话级 steps 仍是全部步骤（4 个），老前端与单轮展示不受影响
	all, _ := d["steps"].([]map[string]any)
	if len(all) != 4 {
		t.Errorf("会话级 steps 应保留全部 4 个步骤，实际 %d", len(all))
	}
}

func stepNames(steps []map[string]any) []string {
	out := []string{}
	for _, s := range steps {
		if s["type"] == "thinking" {
			out = append(out, "(思考)")
			continue
		}
		out = append(out, nilSafeStr(s["name"]))
	}
	return out
}

func eqStrs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
