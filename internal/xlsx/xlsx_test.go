package xlsx

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadTableAndDetect(t *testing.T) {
	path, ok := findOracleXLSX()
	if !ok {
		// ⚠️ 这个文件是 xlsx 层最强 oracle 的语料来源。跳过它 = 最强的
		// 差分验证静默失效，所以这里把提示写得足够醒目，并把候选路径全列出来。
		t.Skipf("跳过：找不到 xlsx oracle 语料。请设 RUIYUN_XLSX_TEST 指向该文件，"+
			"或把它放到 %s。已尝试：%v", filepath.Join("testdata", oracleXLSXName), oracleXLSXCandidates())
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

// oracleXLSXName 真实生产用例表（38MB / 1001 行），xlsx 层差分的权威语料。
const oracleXLSXName = "工作台测试集1000.xlsx"

// oracleXLSXCandidates 按优先级返回所有候选路径。
//
// ⚠️ 原先这里硬编码了一个绝对路径 —— 换机器 / 换用户名即静默 skip，
// 而「最强的 oracle 失效」是不会报错的，属于最危险的一类退化。
// 现在支持环境变量覆盖 + 多处回落，跨机器可用。
func oracleXLSXCandidates() []string {
	var out []string
	if v := os.Getenv("RUIYUN_XLSX_TEST"); v != "" {
		out = append(out, v) // 显式指定优先
	}
	out = append(out,
		filepath.Join("testdata", oracleXLSXName),             // 随仓库走（推荐）
		filepath.Join("..", "..", "testdata", oracleXLSXName), // 从包目录回退
		filepath.Join(os.Getenv("HOME"), "WorkSpace", oracleXLSXName),
		"/Users/amano/WorkSpace/"+oracleXLSXName, // 当前开发机
	)
	return out
}

// findOracleXLSX 返回第一个存在的候选路径。
func findOracleXLSX() (string, bool) {
	for _, p := range oracleXLSXCandidates() {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
	}
	return "", false
}
