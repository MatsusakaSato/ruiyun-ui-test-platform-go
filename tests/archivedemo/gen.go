//go:build ignore

// 用真实会话目录重建归档，验证多轮信息是否完整带出。
// 用法：go run tests/archivedemo/gen.go <session_dir> [第二轮提问]
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"ruiyun-ui-test-platform-go/internal/logparser"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/trajectory"
)

func main() {
	sessDir := os.Args[1]
	tr, err := logparser.ParseSession(sessDir)
	if err != nil {
		fmt.Println("解析失败:", err)
		os.Exit(1)
	}
	prompts := []string{}
	for _, t := range tr.Turns {
		prompts = append(prompts, t.Prompt)
	}
	cr := &models.CaseResult{
		CaseID: "CASE-001", Name: "用例 1",
		SessionID: tr.SessionID, Trace: tr,
		Prompts: prompts, TurnCount: len(prompts),
		AbortedAtTurn: -1,
	}
	if len(prompts) > 0 {
		cr.Prompt = prompts[0]
	}
	detail := trajectory.BuildCaseDetail(cr, map[string][]*models.Finding{}, map[string]any{})
	out := map[string]any{
		"prompt":          detail["prompt"],
		"prompts":         detail["prompts"],
		"turn_count":      detail["turn_count"],
		"aborted_at_turn": detail["aborted_at_turn"],
	}
	turns, _ := detail["turns"].([]map[string]any)
	rows := []map[string]any{}
	for _, t := range turns {
		rows = append(rows, map[string]any{
			"index":       t["index"],
			"prompt":      t["prompt"],
			"answer_len":  t["answer_chars"],
			"tool_calls":  t["tool_calls"],
			"elapsed_s":   t["elapsed_s"],
			"incomplete":  t["incomplete"],
		})
	}
	out["turns"] = rows
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
}
