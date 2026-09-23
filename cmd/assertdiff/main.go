// Command assertdiff 是 P0 差分验证工装的「断言层」探针。
//
// 用法: assertdiff <sessionRoot> <rulesJSONPath> <maxIterations>
//
// 遍历 <sessionRoot>/sess_*/ 下的真实会话，用 Go 侧 assertor.RunAssertions
// 逐条产出 Finding，输出规范 JSON 供与 Python 侧 tools/diff/assert_ref.py 比对。
//
// cfg 从外部 JSON 文件读入，保证两侧拿到**完全相同**的 rules 配置
// （避免"因为配置不同所以结果不同"的假阳性）。
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"ruiyun-ui-test-platform-go/internal/assertor"
	"ruiyun-ui-test-platform-go/internal/logparser"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "用法: assertdiff <sessionRoot> <rulesJSONPath> <maxIterations>")
		os.Exit(2)
	}
	root := os.Args[1]
	cfgPath := os.Args[2]
	maxIter, _ := strconv.Atoi(os.Args[3])

	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取 rules 失败: %v\n", err)
		os.Exit(1)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		fmt.Fprintf(os.Stderr, "解析 rules 失败: %v\n", err)
		os.Exit(1)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "读取目录失败: %v\n", err)
		os.Exit(1)
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "sess_") {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, e.Name(), "session.messages.json")); err != nil {
			continue
		}
		dirs = append(dirs, e.Name())
	}
	sort.Strings(dirs)

	out := map[string]any{}
	byRule := map[string]int{}
	bySeverity := map[string]int{}
	total := 0

	for _, name := range dirs {
		trace, err := logparser.ParseSession(filepath.Join(root, name))
		if err != nil || trace == nil {
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			out[name] = map[string]any{"parse_error": msg}
			continue
		}

		findings := assertor.RunAssertions(trace, cfg, maxIter)
		arr := []map[string]any{}
		for _, f := range findings {
			var stepIndex any
			if f.StepIndex != nil {
				stepIndex = *f.StepIndex
			}
			arr = append(arr, map[string]any{
				"rule":       f.Rule,
				"severity":   f.Severity,
				"session_id": f.SessionID,
				"detail":     f.Detail,
				"tool":       f.Tool,
				"step_index": stepIndex,
				"evidence":   f.Evidence,
			})
			byRule[f.Rule]++
			bySeverity[f.Severity]++
			total++
		}
		out[name] = map[string]any{"count": len(arr), "findings": arr}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	_ = enc.Encode(map[string]any{
		"root":        root,
		"max_iter":    maxIter,
		"total":       total,
		"by_rule":     byRule,
		"by_severity": bySeverity,
		"sessions":    out,
	})
}
