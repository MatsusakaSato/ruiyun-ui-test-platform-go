package testcasedb

import (
	"path/filepath"
	"testing"
)

func createTempDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "testcases.db")
	_, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	return dbPath
}

func TestCaseDedupeKey(t *testing.T) {
	// 1. 同 prompt + 同 scene + 同 targets（不同顺序） + 同 attachment
	k1 := CaseDedupeKey(" 写一首诗 ", " 创作 ", []string{"word", "ppt"}, false)
	k2 := CaseDedupeKey("写一首诗", "创作", []string{"ppt", "word"}, false)
	if k1 != k2 {
		t.Errorf("expected k1 == k2, got k1=%q, k2=%q", k1, k2)
	}

	// 2. attachment 差异
	k3 := CaseDedupeKey("写一首诗", "创作", []string{"word", "ppt"}, true)
	if k1 == k3 {
		t.Errorf("expected k1 != k3 when attachment differs")
	}

	// 3. scene 差异
	k4 := CaseDedupeKey("写一首诗", "教育", []string{"word", "ppt"}, false)
	if k1 == k4 {
		t.Errorf("expected k1 != k4 when scene differs")
	}

	// 4. targets 差异
	k5 := CaseDedupeKey("写一首诗", "创作", []string{"word"}, false)
	if k1 == k5 {
		t.Errorf("expected k1 != k5 when targets differ")
	}

	// 5. prompt 差异
	k6 := CaseDedupeKey("写一首散文", "创作", []string{"word", "ppt"}, false)
	if k1 == k6 {
		t.Errorf("expected k1 != k6 when prompt differs")
	}
}

func TestAddPresetCase_Deduplication(t *testing.T) {
	dbPath := createTempDB(t)

	// 1. 成功添加第一条用例
	item1 := map[string]any{
		"prompt": "查询2024年GDP数据",
		"labels": map[string]any{
			"scene":   "数据统计",
			"targets": []string{"excel", "pdf"},
		},
	}
	ok, msg, c1 := AddPresetCase(item1, dbPath)
	if !ok || c1 == nil {
		t.Fatalf("AddPresetCase failed: %s", msg)
	}
	if c1["id"] != "CASE-001" {
		t.Errorf("expected id CASE-001, got %v", c1["id"])
	}

	// 验证 labels
	labels, ok := c1["labels"].(map[string]any)
	if !ok || labels["scene"] != "数据统计" {
		t.Errorf("expected scene 数据统计, got %v", labels)
	}

	// 2. 添加提问和标签完全一致的用例 -> 应自动去重失败
	itemDup := map[string]any{
		"prompt": " 查询2024年GDP数据 ",
		"labels": map[string]any{
			"scene":   "数据统计",
			"targets": []string{"pdf", "excel"}, // 顺序颠倒
		},
	}
	okDup, msgDup, cDup := AddPresetCase(itemDup, dbPath)
	if okDup {
		t.Errorf("expected duplicate addition to fail, but succeeded: %v", cDup)
	}
	if cDup != nil {
		t.Errorf("expected nil case on duplicate error")
	}
	t.Logf("Duplicate rejection message: %s", msgDup)

	// 验证总数仍为 1
	count, err := CountCases(dbPath)
	if err != nil || count != 1 {
		t.Fatalf("expected count 1, got %d (err: %v)", count, err)
	}

	// 3. 相同 prompt 但不同标签（scene 不同） -> 允许添加
	itemDiffScene := map[string]any{
		"prompt": "查询2024年GDP数据",
		"labels": map[string]any{
			"scene":   "经济分析",
			"targets": []string{"excel", "pdf"},
		},
	}
	okDiff, msgDiff, cDiff := AddPresetCase(itemDiffScene, dbPath)
	if !okDiff || cDiff == nil {
		t.Fatalf("AddPresetCase with diff scene failed: %s", msgDiff)
	}
	if cDiff["id"] != "CASE-002" {
		t.Errorf("expected id CASE-002, got %v", cDiff["id"])
	}

	// 4. 相同 prompt 但不同 targets -> 允许添加
	itemDiffTargets := map[string]any{
		"prompt": "查询2024年GDP数据",
		"labels": map[string]any{
			"scene":   "数据统计",
			"targets": []string{"excel"},
		},
	}
	okDiffTg, msgDiffTg, cDiffTg := AddPresetCase(itemDiffTargets, dbPath)
	if !okDiffTg || cDiffTg == nil {
		t.Fatalf("AddPresetCase with diff targets failed: %s", msgDiffTg)
	}
	if cDiffTg["id"] != "CASE-003" {
		t.Errorf("expected id CASE-003, got %v", cDiffTg["id"])
	}

	// 5. 相同 prompt 但 attachment 不同 -> 允许添加
	itemDiffAtt := map[string]any{
		"prompt": "查询2024年GDP数据",
		"labels": map[string]any{
			"scene":      "数据统计",
			"targets":    []string{"excel", "pdf"},
			"attachment": true,
		},
	}
	okDiffAtt, msgDiffAtt, cDiffAtt := AddPresetCase(itemDiffAtt, dbPath)
	if !okDiffAtt || cDiffAtt == nil {
		t.Fatalf("AddPresetCase with diff attachment failed: %s", msgDiffAtt)
	}
	if cDiffAtt["id"] != "CASE-004" {
		t.Errorf("expected id CASE-004, got %v", cDiffAtt["id"])
	}

	total, _ := CountCases(dbPath)
	if total != 4 {
		t.Errorf("expected total 4, got %d", total)
	}
}

func TestAddPresetCases_BatchDeduplication(t *testing.T) {
	dbPath := createTempDB(t)

	// 先添加一条既有用例
	_, _, _ = AddPresetCase(map[string]any{
		"prompt": "既有用例1",
		"scene":  "默认场景",
	}, dbPath)

	// 批量导入列表：含与既有用例重复、批次内自身重复、以及新用例
	items := []map[string]any{
		{
			"prompt": "既有用例1", // 与既有用例完全相同 -> 应跳过
			"scene":  "默认场景",
		},
		{
			"prompt": "新用例A",
			"scene":  "场景A",
			"targets": []string{"word"},
		},
		{
			"prompt": "新用例A", // 批次内重复 -> 应跳过
			"scene":  "场景A",
			"targets": []string{"word"},
		},
		{
			"prompt": "新用例B",
			"scene":  "场景B",
			"targets": []string{"ppt"},
		},
	}

	ok, msg, added := AddPresetCases(items, false, dbPath)
	if !ok {
		t.Fatalf("AddPresetCases failed: %s", msg)
	}
	if added != 2 {
		t.Errorf("expected 2 added, got %d", added)
	}
	t.Logf("Batch import result: %s", msg)

	total, _ := CountCases(dbPath)
	if total != 3 {
		t.Errorf("expected total 3, got %d", total)
	}

	// 测试清空覆盖导入：即使 replace=true，批次内部重复仍应去重
	replaceItems := []map[string]any{
		{"prompt": "覆盖用例1", "scene": "S1"},
		{"prompt": "覆盖用例1", "scene": "S1"}, // 批次内重复
		{"prompt": "覆盖用例2", "scene": "S2"},
	}
	okRep, msgRep, addedRep := AddPresetCases(replaceItems, true, dbPath)
	if !okRep {
		t.Fatalf("AddPresetCases replace failed: %s", msgRep)
	}
	if addedRep != 2 {
		t.Errorf("expected 2 added on replace, got %d", addedRep)
	}
	t.Logf("Replace import result: %s", msgRep)

	totalRep, _ := CountCases(dbPath)
	if totalRep != 2 {
		t.Errorf("expected total 2 on replace, got %d", totalRep)
	}
}
