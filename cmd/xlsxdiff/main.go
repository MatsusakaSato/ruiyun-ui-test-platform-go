// Command xlsxdiff 是 P0 差分验证工装的一部分。
//
// 用法: xlsxdiff <file> [maxRows]
//
// 它读取一个 xlsx/csv，走 Go 侧的 detect_columns + rows_to_items 全链路，
// 把结果以「键排序 + 缩进」的规范 JSON 打到 stdout，供与 Python 侧比对。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"ruiyun-ui-test-platform-go/internal/xlsx"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: xlsxdiff <file> [maxRows]")
		os.Exit(2)
	}
	path := os.Args[1]
	maxRows := 0
	if len(os.Args) > 2 {
		if n, err := strconv.Atoi(os.Args[2]); err == nil {
			maxRows = n
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取失败: %v\n", err)
		os.Exit(1)
	}

	table, err := xlsx.ReadTable(path, data, maxRows)
	if err != nil {
		fmt.Fprintf(os.Stderr, "解析失败: %v\n", err)
		os.Exit(1)
	}

	rowsAny, ok := table["rows"].([][]string)
	if !ok {
		// 某些实现可能返回 []any，做一次兜底转换
		rowsAny = coerceRows(table["rows"])
	}

	meta := xlsx.DetectColumns(rowsAny)

	headIdx := asIntPtr(meta["head_idx"])
	promptCol := asInt(meta["prompt_col"])
	sceneCol := asIntPtr(meta["scene_col"])
	targetCol := asIntPtr(meta["target_col"])
	nameCol := asIntPtr(meta["name_col"])
	attachmentCol := asIntPtr(meta["attachment_col"])
	skillCol := asIntPtr(meta["skill_col"])

	got := xlsx.RowsToItems(rowsAny, headIdx, promptCol,
		sceneCol, targetCol, nameCol, attachmentCol, skillCol)

	// 组装成与 Python detect_table 一致的形状：
	// meta（除 preview 外） ∪ rows_to_items 结果，外加 sheet 名。
	out := map[string]any{
		"sheet":      table["sheet"],
		"header":     meta["header"],
		"head_idx":   meta["head_idx"],
		"prompt_col": meta["prompt_col"],
		"scene_col":  meta["scene_col"],
		"target_col": meta["target_col"],
		"name_col":   meta["name_col"],
		"attach_col": meta["attachment_col"],
		"skill_col":  meta["skill_col"],
		"total_rows": meta["total_rows"],
		"items":      got["items"],
		"skipped":    got["skipped"],
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false) // 与 Python ensure_ascii=False 对齐
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "编码失败: %v\n", err)
		os.Exit(1)
	}
}

func asInt(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return 0
}

func asIntPtr(v any) *int {
	switch t := v.(type) {
	case nil:
		return nil
	case int:
		return &t
	case int64:
		n := int(t)
		return &n
	case float64:
		n := int(t)
		return &n
	case *int:
		return t
	}
	return nil
}

func coerceRows(v any) [][]string {
	raw, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([][]string, 0, len(raw))
	for _, r := range raw {
		row, ok := r.([]any)
		if !ok {
			out = append(out, nil)
			continue
		}
		cells := make([]string, 0, len(row))
		for _, c := range row {
			if c == nil {
				cells = append(cells, "")
				continue
			}
			cells = append(cells, fmt.Sprintf("%v", c))
		}
		out = append(out, cells)
	}
	return out
}
