package testcasedb

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTestCaseDB(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ruiyun_test_db_*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "testcases.db")
	_, err = InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB 失败: %v", err)
	}

	// 1. 批量插入
	items := []map[string]any{
		{
			"prompt":  "帮我整理一份财务报表",
			"name":    "财务分析",
			"scene":   "办公",
			"targets": []string{"excel"},
		},
		{
			"prompt":      "设计一份教案并导出PDF",
			"name":        "数学教案",
			"scene":       "教学",
			"targets":     []string{"pdf", "word"},
			"attachments": []string{"math.txt"},
		},
		{
			"prompt":  "网页内容爬取分析",
			"name":    "爬虫任务",
			"scene":   "技术",
			"targets": []string{"html", "network"},
		},
	}
	ok, msg, count := AddPresetCases(items, false, dbPath)
	if !ok || count != 3 {
		t.Fatalf("AddPresetCases 失败: ok=%v, msg=%s, count=%d", ok, msg, count)
	}

	// 2. 单条插入
	ok, msg, single := AddPresetCase(map[string]any{
		"prompt":  "第四个任务",
		"scene":   "教学",
		"targets": []string{"ppt"},
	}, dbPath)
	if !ok || single["id"] != "CASE-004" {
		t.Fatalf("AddPresetCase 失败: ok=%v, msg=%s, case=%v", ok, msg, single)
	}

	// 3. 统计数量
	total, err := CountCases(dbPath)
	if err != nil || total != 4 {
		t.Fatalf("CountCases 失败: %v, total=%d", err, total)
	}

	// 4. 查询与 JSON1 过滤（targets 包含 word）
	res, err := QueryPresetCases("", "", []string{"word"}, "", nil, 10, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases 失败: %v", err)
	}
	cases := res["cases"].([]map[string]any)
	if len(cases) != 1 {
		t.Fatalf("期望查询到 1 条 target=word，实际 %d 条", len(cases))
	}
	if cases[0]["id"] != "CASE-002" {
		t.Fatalf("期望命中 CASE-002，实际命中 %v", cases[0]["id"])
	}

	// 5. 关键词搜索
	resKw, err := QueryPresetCases("爬虫", "", nil, "", nil, 10, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases 失败: %v", err)
	}
	casesKw := resKw["cases"].([]map[string]any)
	if len(casesKw) != 1 || casesKw[0]["name"] != "爬虫任务" {
		t.Fatalf("关键词搜索失败: %v", casesKw)
	}

	// 6. 标签索引
	labelsIdx, err := GetPresetLabelsIndex(dbPath)
	if err != nil {
		t.Fatalf("GetPresetLabelsIndex 失败: %v", err)
	}
	if len(labelsIdx) != 4 {
		t.Fatalf("期望 4 个标签索引项，实际 %d", len(labelsIdx))
	}

	// 7. 删除
	okDel, _, removed := DeletePresetCases([]string{"CASE-001", "CASE-003"}, dbPath)
	if !okDel || removed != 2 {
		t.Fatalf("DeletePresetCases 失败: ok=%v, removed=%d", okDel, removed)
	}
	totalAfter, _ := CountCases(dbPath)
	if totalAfter != 2 {
		t.Fatalf("删除后期望剩余 2 条，实际 %d 条", totalAfter)
	}
}
