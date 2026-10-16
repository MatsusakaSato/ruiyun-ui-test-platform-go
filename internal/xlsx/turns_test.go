package xlsx

import "testing"

// TestTurnColIndex 轮次列头的识别
func TestTurnColIndex(t *testing.T) {
	yes := map[string]int{
		"prompt_1": 1, "prompt_2": 2, "prompt_8": 8, "Prompt_3": 3, "PROMPT_8": 8,
		"提问": 0, // 普通提问列不是轮次列
		"提问1": 1, "提问_2": 2, "提问2": 2,
		"问题3": 3, "turn1": 1, "turn_4": 4, "轮次5": 5,
		"第1轮": 1, "第 2 轮": 2, "第_3_轮": 3,
		"  prompt_2  ": 2,
	}
	for in, want := range yes {
		if got := turnColIndex(in); got != want {
			t.Errorf("turnColIndex(%q) = %d，期望 %d", in, got, want)
		}
	}

	no := []string{
		"", "场景", "难度", "附件", "prompt", "提问内容", "第x轮",
		"prompt_0", "prompt_9", "prompt_10", // 越界：0 与超过 8
		"prompt1extra", "轮次",
	}
	for _, in := range no {
		if got := turnColIndex(in); got != 0 {
			t.Errorf("turnColIndex(%q) 应为 0，实际 %d", in, got)
		}
	}
}

// TestTurnColReBoundaries 上限与下界的边界
func TestTurnColReBoundaries(t *testing.T) {
	if got := turnColIndex("prompt_1"); got != 1 {
		t.Errorf("下界 prompt_1 应为 1，实际 %d", got)
	}
	if got := turnColIndex("prompt_8"); got != 8 {
		t.Errorf("上界 prompt_8 应为 %d，实际 %d", MaxTurnCols, got)
	}
	if got := turnColIndex("prompt_9"); got != 0 {
		t.Errorf("超过上界应返回 0，实际 %d", got)
	}
	if MaxTurnCols != 8 {
		t.Errorf("MaxTurnCols 应与 testcasedb.MaxTurns 一致为 8，实际 %d", MaxTurnCols)
	}
}

// buildCols 从 DetectColumns 的结果构造 CaseCols（与 handlers.go 的用法一致）
func buildCols(meta map[string]any) CaseCols {
	cols := CaseCols{Prompt: -1}
	if v, ok := meta["prompt_col"].(int); ok {
		cols.Prompt = v
	}
	if v, ok := meta["scene_col"].(*int); ok {
		cols.Scene = v
	}
	if v, ok := meta["difficulty_col"].(*int); ok {
		cols.Difficulty = v
	}
	if v, ok := meta["attachment_col"].(*int); ok {
		cols.Attachment = v
	}
	if tc, ok := meta["turn_cols"].(map[int]int); ok {
		cols.TurnCols = tc
	}
	return cols
}

// TestExcelTurnTemplate_PromptOnly prompt_1..prompt_3 模板（无普通提问列）
func TestExcelTurnTemplate_PromptOnly(t *testing.T) {
	rows := [][]string{
		{"prompt_1", "prompt_2", "prompt_3", "场景"},
		{"第一轮提问", "第二轮提问", "第三轮提问", "多轮"},
		{"单轮也在 prompt_1", "", "", "单轮"},
	}
	meta := DetectColumns(rows)
	if meta["turn_cols"] == nil {
		t.Fatalf("应识别出轮次列，实际 meta=%v", meta)
	}
	headIdx := 0
	items := RowsToItems(rows, &headIdx, buildCols(meta))
	list, _ := items["items"].([]map[string]any)
	if len(list) != 2 {
		t.Fatalf("应产出 2 条，实际 %d（%v）", len(list), items)
	}
	first, _ := list[0]["prompt"].([]string)
	if len(first) != 3 || first[0] != "第一轮提问" || first[2] != "第三轮提问" {
		t.Errorf("第 1 条应为 3 轮，实际 %#v", first)
	}
	second, _ := list[1]["prompt"].([]string)
	if len(second) != 1 || second[0] != "单轮也在 prompt_1" {
		t.Errorf("第 2 条应为 1 轮，实际 %#v", second)
	}
}

// TestExcelTurnTemplate_PromptPlusTurns 「提问」列 + prompt_2..N 模板
func TestExcelTurnTemplate_PromptPlusTurns(t *testing.T) {
	rows := [][]string{
		{"提问", "prompt_2", "prompt_3"},
		{"首轮提问", "次轮提问", "三轮提问"},
	}
	meta := DetectColumns(rows)
	headIdx := 0
	items := RowsToItems(rows, &headIdx, buildCols(meta))
	list, _ := items["items"].([]map[string]any)
	if len(list) != 1 {
		t.Fatalf("应产出 1 条，实际 %d", len(list))
	}
	got, _ := list[0]["prompt"].([]string)
	if len(got) != 3 {
		t.Fatalf("应拼成 3 轮，实际 %#v", got)
	}
	// 普通「提问」列即第 1 轮
	if got[0] != "首轮提问" || got[1] != "次轮提问" || got[2] != "三轮提问" {
		t.Errorf("轮次顺序不对：%#v", got)
	}
}

// TestExcelTurnTemplate_SingleTurnUnchanged 只有普通「提问」列时行为不变
func TestExcelTurnTemplate_SingleTurnUnchanged(t *testing.T) {
	rows := [][]string{
		{"提问", "场景", "难度"},
		{"单轮提问", "备课", "易"},
	}
	meta := DetectColumns(rows)
	headIdx := 0
	items := RowsToItems(rows, &headIdx, buildCols(meta))
	list, _ := items["items"].([]map[string]any)
	if len(list) != 1 {
		t.Fatalf("应产出 1 条，实际 %d", len(list))
	}
	got, _ := list[0]["prompt"].([]string)
	if len(got) != 1 || got[0] != "单轮提问" {
		t.Errorf("单轮模板应产出长度 1 的数组，实际 %#v", got)
	}
}

// TestExcelTurnTemplate_BeyondMax 超过 8 轮的列头按普通列处理（不认得）
func TestExcelTurnTemplate_BeyondMax(t *testing.T) {
	if got := turnColIndex("prompt_9"); got != 0 {
		t.Errorf("prompt_9 超过上限应返回 0，实际 %d", got)
	}
	rows := [][]string{
		{"prompt_1", "prompt_9"},
		{"首轮", "第九列"},
	}
	meta := DetectColumns(rows)
	tc, _ := meta["turn_cols"].(map[int]int)
	if _, ok := tc[9]; ok {
		t.Error("prompt_9 不应被当成轮次列")
	}
	if _, ok := tc[1]; !ok {
		t.Error("prompt_1 应被当成轮次列")
	}
}
