package metrics

import (
	"testing"

	"ruiyun-ui-test-platform-go/internal/models"
)

// TestTurnCountVsTraceTurns 提交了 3 轮但只跑完 2 轮时，
// turn_count（提交轮数）与实际轨迹轮数会不一致 —— 记录这个事实，
// 报告层据此决定标题与轮次块数量。
func TestTurnCountVsTraceTurns(t *testing.T) {
	cr := &models.CaseResult{
		CaseID: "C1", Name: "n",
		Prompt: "第一轮", Prompts: []string{"第一轮", "第二轮", "第三轮"},
		TurnCount: 3, SessionID: "s",
		AbortedAtTurn: 2,
		Trace: &models.ExecutionTrace{
			Turns: []*models.TurnTrace{
				{Index: 0, Prompt: "第一轮", Answer: "a0"},
				{Index: 1, Prompt: "第二轮", Answer: "a1"},
			},
		},
	}
	m := BuildMetrics([]*models.CaseResult{cr}, map[string]any{}, nil, nil)
	rows, _ := m["case_rows"].([]map[string]any)
	if len(rows) != 1 {
		t.Fatalf("应有 1 行，实际 %d", len(rows))
	}
	turns, _ := rows[0]["turns"].([]map[string]any)
	t.Logf("turn_count=%v 实际轨迹轮数=%d aborted_at_turn=%v",
		rows[0]["turn_count"], len(turns), rows[0]["aborted_at_turn"])
	if len(turns) != 2 {
		t.Errorf("轨迹应只有 2 轮，实际 %d", len(turns))
	}
	// 中止信息要能传下去，报告才能解释「为什么只有 2 轮」
	if rows[0]["aborted_at_turn"] != 2 {
		t.Errorf("aborted_at_turn 应为 2，实际 %v", rows[0]["aborted_at_turn"])
	}
}
