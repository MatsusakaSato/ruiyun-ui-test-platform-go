package evaluator

import (
	"testing"

	"ruiyun-ui-test-platform-go/internal/artifacts"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/rubric"
)

// stubJudge 记录每次调用收到的「提问 + 答复」，按维度名回一个固定分。
// 用它验证逐轮评分确实把每一轮的提问/答复分别送进了模型。
type stubCall struct{ prompt, answer string }

func stubJudge(calls *[]stubCall) JudgeCaseFunc {
	return func(caseItem map[string]interface{}, trace *models.ExecutionTrace,
		arts *artifacts.ArtifactSet, labels map[string]interface{},
		dims []rubric.Dimension, objective map[string]interface{}, cfg map[string]interface{},
		facts map[string]interface{}) JudgeResult {
		p := ""
		if caseItem != nil {
			p = toStr(caseItem["prompt"])
		}
		a := ""
		if trace != nil {
			a = trace.FinalAnswer
		}
		if calls != nil {
			*calls = append(*calls, stubCall{prompt: p, answer: a})
		}
		dimsOut := map[string]interface{}{}
		for _, d := range dims {
			dimsOut[d.Key] = map[string]interface{}{"score": 4, "reason": "ok"}
		}
		return JudgeResult{Dims: dimsOut, Requirements: []string{"r"}, Usage: map[string]interface{}{}}
	}
}

func toStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func multiTurnTrace() *models.ExecutionTrace {
	return &models.ExecutionTrace{
		FinalAnswer: "总答复",
		Turns: []*models.TurnTrace{
			{Index: 0, Prompt: "第一轮提问", Answer: "第一轮答复", ElapsedS: 3.5},
			{Index: 1, Prompt: "第二轮提问", Answer: "第二轮答复", ElapsedS: 2.0},
			{Index: 2, Prompt: "第三轮提问", Answer: "", ElapsedS: 1.0,
				Incomplete: true, SystemErrors: []string{"model call failed"}},
		},
	}
}

// TestJudgeTurnsPerTurnPrompts 逐轮评分把每轮的提问与答复分别送去打分
func TestJudgeTurnsPerTurnPrompts(t *testing.T) {
	var calls []stubCall
	dims := []rubric.Dimension{{Key: "d1", Label: "维度一"}}
	trace := multiTurnTrace()
	rows := judgeTurns(
		map[string]interface{}{"prompt": "第一轮提问", "case_id": "C1"},
		trace, nil, map[string]interface{}{}, dims, map[string]interface{}{},
		map[string]interface{}{}, nil, stubJudge(&calls))

	if len(calls) != 3 {
		t.Fatalf("应逐轮调用模型 3 次，实际 %d 次", len(calls))
	}
	want := []stubCall{
		{prompt: "第一轮提问", answer: "第一轮答复"},
		{prompt: "第二轮提问", answer: "第二轮答复"},
		{prompt: "第三轮提问", answer: ""},
	}
	for i, w := range want {
		if calls[i] != w {
			t.Errorf("第 %d 次调用应为 %+v，实际 %+v", i+1, w, calls[i])
		}
	}
	if len(rows) != 3 {
		t.Fatalf("应产出 3 行轮级分，实际 %d", len(rows))
	}
	for i, r := range rows {
		if r["index"] != i {
			t.Errorf("第 %d 行的 index 应为 %d，实际 %v", i, i, r["index"])
		}
		if r["prompt"] != want[i].prompt {
			t.Errorf("第 %d 行提问应为 %q，实际 %v", i, want[i].prompt, r["prompt"])
		}
	}
}

// TestJudgeTurnsMarksIncomplete 未答复的轮被标出来，并带上模型报错
func TestJudgeTurnsMarksIncomplete(t *testing.T) {
	dims := []rubric.Dimension{{Key: "d1", Label: "维度一"}}
	rows := judgeTurns(
		map[string]interface{}{"prompt": "第一轮提问"},
		multiTurnTrace(), nil, map[string]interface{}{}, dims, map[string]interface{}{},
		map[string]interface{}{}, nil, stubJudge(nil))

	last := rows[2]
	if last["incomplete"] != true {
		t.Errorf("第三轮应标记未答复，实际 %v", last["incomplete"])
	}
	errs, _ := last["system_errors"].([]string)
	if len(errs) != 1 || errs[0] != "model call failed" {
		t.Errorf("第三轮应带上模型报错，实际 %v", last["system_errors"])
	}
	if rows[0]["incomplete"] != false {
		t.Errorf("第一轮不应标记未答复，实际 %v", rows[0]["incomplete"])
	}
}

// TestJudgeTurnsScoresOrdered 轮级分数按 dims 声明顺序输出（map 遍历顺序随机）
func TestJudgeTurnsScoresOrdered(t *testing.T) {
	dims := []rubric.Dimension{{Key: "z", Label: "Z"}, {Key: "a", Label: "A"}, {Key: "m", Label: "M"}}
	rows := judgeTurns(
		map[string]interface{}{"prompt": "p"}, multiTurnTrace(), nil,
		map[string]interface{}{}, dims, map[string]interface{}{}, map[string]interface{}{}, nil, stubJudge(nil))

	sc, _ := rows[0]["scores"].([]map[string]interface{})
	if len(sc) != 3 {
		t.Fatalf("应有 3 个维度，实际 %d", len(sc))
	}
	for i, wantKey := range []string{"z", "a", "m"} {
		if sc[i]["key"] != wantKey {
			t.Errorf("第 %d 个维度应为 %q，实际 %v（顺序必须稳定）", i, wantKey, sc[i]["key"])
		}
	}
}

// TestJudgeTurnsSingleTurnReturnsRows 单轮轨迹也走同一函数（由调用方决定要不要用）
func TestJudgeTurnsSingleTurnReturnsRows(t *testing.T) {
	trace := &models.ExecutionTrace{Turns: []*models.TurnTrace{{Index: 0, Prompt: "p", Answer: "a"}}}
	rows := judgeTurns(map[string]interface{}{"prompt": "p"}, trace, nil,
		map[string]interface{}{}, []rubric.Dimension{{Key: "d", Label: "D"}},
		map[string]interface{}{}, map[string]interface{}{}, nil, stubJudge(nil))
	if len(rows) != 1 {
		t.Errorf("单轮应产出 1 行，实际 %d", len(rows))
	}
}

// TestJudgeTurnsNoTrace 轨迹缺失或没有轮次时返回 nil，不炸
func TestJudgeTurnsNoTrace(t *testing.T) {
	dims := []rubric.Dimension{{Key: "d", Label: "D"}}
	if got := judgeTurns(map[string]interface{}{}, nil, nil, map[string]interface{}{},
		dims, map[string]interface{}{}, map[string]interface{}{}, nil, stubJudge(nil)); got != nil {
		t.Errorf("轨迹为 nil 时应返回 nil，实际 %v", got)
	}
	if got := judgeTurns(map[string]interface{}{}, &models.ExecutionTrace{}, nil,
		map[string]interface{}{}, dims, map[string]interface{}{}, map[string]interface{}{}, nil, stubJudge(nil)); got != nil {
		t.Errorf("没有轮次时应返回 nil，实际 %v", got)
	}
}

// TestNonNilTurnRows turns 恒为数组，前端与报告端不必判 null
func TestNonNilTurnRows(t *testing.T) {
	if got := nonNilTurnRows(nil); got == nil || len(got) != 0 {
		t.Errorf("nil 应转成空数组，实际 %#v", got)
	}
}
