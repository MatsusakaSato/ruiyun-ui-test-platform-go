// Command tracediff 是 P0 差分验证工装的「分析层」探针。
//
// 用法: tracediff <sessionRoot> [--limit N]
//
// 遍历 <sessionRoot>/sess_*/ 下的全部真实会话，用 Go 侧 logparser.ParseSession
// 解析成 ExecutionTrace，输出可比的规范 JSON，供与 Python 侧
// tools/diff/trace_ref.py 逐字段比对。
//
// 设计要点：
//   - 正文/结果只输出「哈希 + 码点长度」——长度相等不代表内容相等，
//     必须用哈希才能证明内容一致；同时避免 dump 出几百 MB 原文。
//   - 码点长度（rune）而非字节长度，正是为了暴露 Python len(str) 与
//     Go len(string) 的语义差异。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"ruiyun-ui-test-platform-go/internal/logparser"
)

func sha16(s string) string {
	if s == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:16]
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "用法: tracediff <sessionRoot> [--limit N]")
		os.Exit(2)
	}
	root := os.Args[1]
	limit := 0
	if len(os.Args) > 2 {
		limit, _ = strconv.Atoi(os.Args[len(os.Args)-1])
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
		// 必须有 messages.json 才算一份可用样本
		if _, err := os.Stat(filepath.Join(root, e.Name(), "session.messages.json")); err != nil {
			continue
		}
		dirs = append(dirs, e.Name())
	}
	sort.Strings(dirs)
	if limit > 0 && len(dirs) > limit {
		dirs = dirs[:limit]
	}

	out := map[string]any{}
	for _, name := range dirs {
		dir := filepath.Join(root, name)
		trace, err := logparser.ParseSession(dir)
		if err != nil || trace == nil {
			msg := ""
			if err != nil {
				msg = err.Error()
			}
			out[name] = map[string]any{"parse_error": msg}
			continue
		}

		calls := []map[string]any{}
		for _, tc := range trace.ToolCalls {
			var dur any
			if tc.DurationMs != nil {
				dur = *tc.DurationMs
			}
			raw := ""
			if tc.RawResult != nil {
				raw = *tc.RawResult
			}
			calls = append(calls, map[string]any{
				"index":              tc.Index,
				"tool_call_id":       tc.ToolCallID,
				"name":               tc.Name,
				"body_from":          tc.BodyFrom,
				"event_type":         tc.EventType,
				"failed":             tc.Failed,
				"fail_reason":        tc.FailReason,
				"truncated":          tc.Truncated,
				"empty_required_arg": tc.EmptyRequiredArg,
				"duration_ms":        dur,
				"body_len":           utf8.RuneCountInString(tc.Body),
				"body_sha":           sha16(tc.Body),
				"result_len":         utf8.RuneCountInString(raw),
				"result_sha":         sha16(raw),
				"sig_sha":            sha16(tc.Signature()),
				"result_is_nil":      tc.RawResult == nil,
				"result_obj_nil":     tc.ResultObj == nil,
			})
		}

		out[name] = map[string]any{
			"session_id":               trace.SessionID,
			"title":                    trace.Title,
			"status":                   trace.Status,
			"mode":                     trace.Mode,
			"created_at":               trace.CreatedAt,
			"updated_at":               trace.UpdatedAt,
			"user_prompt":              trace.UserPrompt,
			"final_answer":             trace.FinalAnswer,
			"reasoning_chars":          trace.ReasoningChars,
			"is_streaming":             trace.IsStreaming,
			"completed_at":             trace.CompletedAt,
			"raw_message_count":        trace.RawMessageCount,
			"source_file":              trace.SourceFile,
			"error":                    trace.Error,
			"user_at":                  trace.UserAt,
			"assistant_at":             trace.AssistantAt,
			"first_response_s":         trace.FirstResponseS,
			"generation_s":             trace.GenerationS,
			"session_s":                trace.SessionS,
			"turn_count":               trace.TurnCount,
			"tool_names":               trace.ToolNames(),
			"orphan_ids":               trace.OrphanIDs(),
			"associated_tool_call_ids": trace.AssociatedToolCallIDs,
			"thinking_step_count":      len(trace.ThinkingSteps),
			"message_span_count":       len(trace.MessageSpans),
			"tool_calls":               calls,
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", " ")
	if err := enc.Encode(map[string]any{
		"root":     root,
		"sessions": out,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "编码失败: %v\n", err)
		os.Exit(1)
	}
}
