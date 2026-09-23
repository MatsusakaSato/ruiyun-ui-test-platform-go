package testcasedb

import (
	"os"
	"path/filepath"
	"testing"
)

// 本文件锁住 P0 差分验证（真实生产库 vs Python 原版）中发现并修复的
// 三处查询语义缺陷。断言的是 Python 原版的实际行为，不要"优化"。

func newTempDB(t *testing.T) string {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "ruiyun_fidelity_*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(tmpDir) })
	dbPath := filepath.Join(tmpDir, "testcases.db")
	if _, err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB 失败: %v", err)
	}
	return dbPath
}

// TestQueryTargetsFilterUsesOrSemantics 锁住多选目标的「或」语义。
//
// Python 原版用单条 `EXISTS (... WHERE value IN (?,?))`，命中任意一个即算匹配。
// 早期 Go 移植给每个目标生成一条 EXISTS 再用 AND 串起来 —— 语义变成「且」，
// 结果集大幅缩小（真实生产库实测：或=863 条，且=7 条）。
func TestQueryTargetsFilterUsesOrSemantics(t *testing.T) {
	dbPath := newTempDB(t)

	items := []map[string]any{
		{"prompt": "只有 word", "name": "A", "scene": "备课", "targets": []string{"word"}},
		{"prompt": "只有 ppt", "name": "B", "scene": "备课", "targets": []string{"ppt"}},
		{"prompt": "同时两个", "name": "C", "scene": "备课", "targets": []string{"word", "ppt"}},
		{"prompt": "只有 excel", "name": "D", "scene": "备课", "targets": []string{"excel"}},
	}
	if ok, msg, _ := AddPresetCases(items, false, dbPath); !ok {
		t.Fatalf("AddPresetCases 失败: %s", msg)
	}

	res, err := QueryPresetCases("", "", []string{"word", "ppt"}, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases 失败: %v", err)
	}
	total := res["total"].(int)

	// 或语义 → A、B、C 三条；且语义只会得到 C 一条
	if total != 3 {
		t.Fatalf("targets=[word,ppt] total=%d，期望 3（或语义：命中任意一个即匹配）", total)
	}
}

// TestQueryTargetsFilterSingleValue 单值目标仍需精确匹配。
func TestQueryTargetsFilterSingleValue(t *testing.T) {
	dbPath := newTempDB(t)

	items := []map[string]any{
		{"prompt": "只有 word", "name": "A", "scene": "备课", "targets": []string{"word"}},
		{"prompt": "只有 ppt", "name": "B", "scene": "备课", "targets": []string{"ppt"}},
		{"prompt": "同时两个", "name": "C", "scene": "备课", "targets": []string{"word", "ppt"}},
	}
	if ok, msg, _ := AddPresetCases(items, false, dbPath); !ok {
		t.Fatalf("AddPresetCases 失败: %s", msg)
	}

	res, err := QueryPresetCases("", "", []string{"word"}, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases 失败: %v", err)
	}
	if got := res["total"].(int); got != 2 {
		t.Fatalf("targets=[word] total=%d，期望 2", got)
	}
}

// TestQueryLimitClampingMatchesPython 锁住 limit 的钳制规则。
//
// Python 原版：max(1, min(int(limit or 50), 500))
//   - limit=0  → 0 是假值 → 取 50
//   - limit<0  → 负数仍是真值，不回落 50，而是被 max(1,·) 钳到 1
//   - limit>500 → 被 min(·,500) 钳到 500
//
// 早期 Go 移植只写了 `if limit <= 0 { limit = 50 }`：既缺 500 上限，
// 又把负数错误地回落到 50。
func TestQueryLimitClampingMatchesPython(t *testing.T) {
	dbPath := newTempDB(t)

	items := make([]map[string]any, 0, 12)
	for i := 0; i < 12; i++ {
		items = append(items, map[string]any{
			"prompt": "用例", "name": "N", "scene": "备课", "targets": []string{"word"},
		})
	}
	if ok, msg, _ := AddPresetCases(items, false, dbPath); !ok {
		t.Fatalf("AddPresetCases 失败: %s", msg)
	}

	cases := []struct {
		in       int
		wantLim  int
		wantRows int
	}{
		{0, 50, 12},     // 0 → 50（0 是假值），但库里只有 12 条
		{-5, 1, 1},      // 负数 → 1，不是 50
		{-1, 1, 1},      // 同上
		{1, 1, 1},       // 正常
		{5, 5, 5},       // 正常
		{500, 500, 12},  // 上限内
		{501, 500, 12},  // 超上限 → 500
		{1000, 500, 12}, // 超上限 → 500
	}

	for _, tc := range cases {
		res, err := QueryPresetCases("", "", nil, "", nil, tc.in, 0, "desc", dbPath)
		if err != nil {
			t.Fatalf("limit=%d 查询失败: %v", tc.in, err)
		}
		gotLim := res["limit"].(int)
		if gotLim != tc.wantLim {
			t.Errorf("limit=%d → 返回 limit=%d，期望 %d", tc.in, gotLim, tc.wantLim)
		}
		gotRows := len(res["cases"].([]map[string]any))
		if gotRows != tc.wantRows {
			t.Errorf("limit=%d → 返回 %d 行，期望 %d", tc.in, gotRows, tc.wantRows)
		}
	}
}

// TestQueryAttachmentFilterOnlyAcceptsYesNo 锁住附件筛选的取值集合。
//
// Python 原版只认字面量 "yes" / "no"，其余值一律忽略筛选（不过滤）。
// 早期 Go 移植额外接受 "1"/"true"/"0"/"false"，会让同一请求在两侧得到不同结果。
func TestQueryAttachmentFilterOnlyAcceptsYesNo(t *testing.T) {
	dbPath := newTempDB(t)

	items := []map[string]any{
		{"prompt": "带附件", "name": "A", "scene": "备课", "targets": []string{"word"},
			"attachments": []string{"a.docx"}},
		{"prompt": "无附件", "name": "B", "scene": "备课", "targets": []string{"word"}},
	}
	if ok, msg, _ := AddPresetCases(items, false, dbPath); !ok {
		t.Fatalf("AddPresetCases 失败: %s", msg)
	}

	cases := []struct {
		att  string
		want int
	}{
		{"yes", 1},  // 只保留带附件
		{"no", 1},   // 只保留无附件
		{"", 2},     // 不筛选
		{"true", 2}, // 非 yes/no → 忽略筛选（不能当 1 处理）
		{"1", 2},    // 同上
		{"TRUE", 2}, // 同上（也不做大小写归一）
		{"nope", 2}, // 未知值 → 忽略
	}

	for _, tc := range cases {
		res, err := QueryPresetCases("", "", nil, tc.att, nil, 50, 0, "desc", dbPath)
		if err != nil {
			t.Fatalf("attachment=%q 查询失败: %v", tc.att, err)
		}
		if got := res["total"].(int); got != tc.want {
			t.Errorf("attachment=%q → total=%d，期望 %d", tc.att, got, tc.want)
		}
	}
}
