// Command dbdiff 是 P0 差分验证工装的 DB 层探针。
//
// 用法: dbdiff <dbPath>
//
// 对同一个 SQLite 用例库跑一组固定的查询电池，输出规范 JSON 供与
// Python 侧 tools/diff/db_ref.py 比对。
//
// 安全约定：本工具**只做只读查询**。调用方应传入生产库的**副本**。
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"ruiyun-ui-test-platform-go/internal/testcasedb"
)

type battery struct {
	Name    string
	Keyword string
	Scene   string
	Targets []string
	Attach  string
	IDs     []string
	Limit   int
	Offset  int
	Order   string
}

// 覆盖分页边界是重点：limit 的钳制规则（下限 1、上限 500）在两侧实现里
// 写法不同，必须用真实库逐条验证。
var batteries = []battery{
	{Name: "default"},
	{Name: "keyword", Keyword: "备课"},
	{Name: "keyword_none", Keyword: "zzz-不存在-zzz"},
	{Name: "scene", Scene: "备课"},
	{Name: "targets_word", Targets: []string{"word"}},
	{Name: "targets_multi", Targets: []string{"word", "ppt"}},
	{Name: "attach_yes", Attach: "yes"},
	{Name: "attach_no", Attach: "no"},
	{Name: "ids", IDs: []string{"CASE-001", "CASE-002"}},
	{Name: "limit_0", Limit: 0},
	{Name: "limit_neg", Limit: -5},
	{Name: "limit_1", Limit: 1},
	{Name: "limit_50", Limit: 50},
	{Name: "limit_500", Limit: 500},
	{Name: "limit_501_OVER", Limit: 501},
	{Name: "limit_1000_OVER", Limit: 1000},
	{Name: "offset_neg", Offset: -3, Limit: 5},
	{Name: "asc_limit5", Order: "asc", Limit: 5},
	{Name: "desc_limit5", Order: "desc", Limit: 5},
	{Name: "paging", Limit: 7, Offset: 7},
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: dbdiff <dbPath>")
		os.Exit(2)
	}
	dbPath := os.Args[1]

	out := map[string]any{}

	if n, err := testcasedb.CountCases(dbPath); err == nil {
		out["count"] = n
	} else {
		out["count_error"] = err.Error()
	}

	if all, err := testcasedb.GetPresetCases(dbPath); err == nil {
		out["get_preset_cases"] = all
	} else {
		out["get_preset_cases_error"] = err.Error()
	}

	queries := map[string]any{}
	for _, b := range batteries {
		res, err := testcasedb.QueryPresetCases(
			b.Keyword, b.Scene, b.Targets, b.Attach, b.IDs,
			b.Limit, b.Offset, b.Order, dbPath,
		)
		if err != nil {
			queries[b.Name] = map[string]any{"error": err.Error()}
			continue
		}
		queries[b.Name] = res
	}
	out["queries"] = queries

	if idx, err := testcasedb.GetPresetLabelsIndex(dbPath); err == nil {
		out["labels_index"] = idx
	} else {
		out["labels_index_error"] = err.Error()
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "编码失败: %v\n", err)
		os.Exit(1)
	}
}
