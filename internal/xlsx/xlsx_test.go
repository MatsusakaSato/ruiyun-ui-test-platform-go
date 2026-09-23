package xlsx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTableAndDetect(t *testing.T) {
	// 查找生产测试文件
	path := "/Users/amano/WorkSpace/工作台测试集1000.xlsx"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Skip("跳过：本地测试文件不存在")
		return
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取测试文件失败: %v", err)
	}

	table, err := ReadTable(filepath.Base(path), data, 0)
	if err != nil {
		t.Fatalf("解析表格失败: %v", err)
	}

	rows, ok := table["rows"].([][]string)
	if !ok {
		t.Fatalf("rows 类型错误")
	}

	t.Logf("成功读取 rows 数量: %d", len(rows))
	if len(rows) != 1001 {
		t.Errorf("期望 1001 行，实际 %d 行", len(rows))
	}

	meta := DetectColumns(rows)
	promptCol := meta["prompt_col"].(int)
	headIdx, _ := meta["head_idx"].(*int)

	t.Logf("识别到 prompt_col: %d, head_idx: %v", promptCol, headIdx)

	var sceneCol, targetCol, nameCol, attachmentCol, skillCol *int
	if v, ok := meta["scene_col"].(*int); ok {
		sceneCol = v
	}
	if v, ok := meta["target_col"].(*int); ok {
		targetCol = v
	}
	if v, ok := meta["name_col"].(*int); ok {
		nameCol = v
	}
	if v, ok := meta["attachment_col"].(*int); ok {
		attachmentCol = v
	}
	if v, ok := meta["skill_col"].(*int); ok {
		skillCol = v
	}

	res := RowsToItems(rows, headIdx, promptCol, sceneCol, targetCol, nameCol, attachmentCol, skillCol)
	items, ok := res["items"].([]map[string]any)
	if !ok {
		t.Fatalf("items 类型错误")
	}
	skipped := res["skipped"].(int)

	t.Logf("转换用例 items: %d, skipped: %d", len(items), skipped)
	if len(items) != 1000 {
		t.Errorf("期望 1000 个用例，实际 %d 个", len(items))
	}

	// 检查首条用例
	first := items[0]
	t.Logf("First case: id=%v, name=%v, scene=%v, targets=%v",
		first["id"], first["name"], first["scene"], first["targets"])
}
