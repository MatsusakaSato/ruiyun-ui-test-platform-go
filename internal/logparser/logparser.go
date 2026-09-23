package logparser

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"ruiyun-ui-test-platform-go/internal/models"
)

var wrapperKeys = []string{"event_type", "tool_name", "tool_call_id"}

// UnwrapResult 解包工具返回形态，返回 (文本形态, 结构化形态)
func UnwrapResult(raw any) (*string, any) {
	if raw == nil {
		return nil, nil
	}

	if m, ok := raw.(map[string]any); ok {
		if inner, hasInner := m["result"]; hasInner && inner != nil {
			isWrapper := false
			for _, k := range wrapperKeys {
				if _, exists := m[k]; exists {
					isWrapper = true
					break
				}
			}
			if isWrapper {
				return UnwrapResult(inner)
			}
		}
		b, err := json.Marshal(m)
		if err == nil {
			s := string(b)
			return &s, m
		}
		s := fmt.Sprintf("%v", m)
		return &s, m
	}

	switch v := raw.(type) {
	case int, int64, float64, bool:
		s := fmt.Sprintf("%v", v)
		return &s, v
	case string:
		stripped := strings.TrimSpace(v)
		if strings.HasPrefix(stripped, "{") || strings.HasPrefix(stripped, "[") {
			var obj any
			if err := json.Unmarshal([]byte(stripped), &obj); err == nil {
				return &v, obj
			}
			if pyObj, err := ParsePyLiteral(stripped); err == nil {
				return &v, pyObj
			}
		}
		return &v, nil
	default:
		s := fmt.Sprintf("%v", raw)
		return &s, raw
	}
}

var bodyFields = []string{
	"file_content", "content", "text", "markdown", "markdown_content",
	"body", "data", "answer", "result_text",
}

// ExtractBody 从工具返回中取出正文与来源说明
func ExtractBody(text *string, obj any) (string, string) {
	if m, ok := obj.(map[string]any); ok {
		for _, k := range bodyFields {
			if v, exists := m[k]; exists {
				if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
					return s, k
				}
			}
		}
		for _, k := range bodyFields {
			if v, exists := m[k]; exists {
				if arr, ok := v.([]any); ok && len(arr) > 0 {
					b, _ := json.Marshal(arr)
					return string(b), fmt.Sprintf("%s[]", k)
				}
			}
		}
	}
	if text != nil {
		return *text, "raw"
	}
	return "", "raw"
}

// DetectEmptyRequiredArgs 检测必填参数为空被放行的情况
func DetectEmptyRequiredArgs(name string, arguments any) []string {
	// 必须是空切片而非 nil：nil 会被 encoding/json 序列化成 null，
	// 而 Python 原版返回 []。前端对二者不等价（真实差分：1037 次调用全部不一致）。
	empty := []string{}
	m, ok := arguments.(map[string]any)
	if !ok {
		return empty
	}

	requiredMap := map[string][]string{
		"mcp_write_workspace_file": {"relative_path", "content"},
		"convert_markdown_to_docx": {"md_file_name", "markdown_content"},
		"read_memory":              {"path"},
		"mcp_fetch_webpage":        {"url"},
		"mcp_paid_search":          {"query"},
		"todo_complete":            {"idx"},
	}

	reqList, exists := requiredMap[name]
	if !exists {
		for _, k := range []string{"content", "query", "url", "path"} {
			if _, hasKey := m[k]; hasKey {
				reqList = append(reqList, k)
			}
		}
	}

	for _, k := range reqList {
		if val, hasKey := m[k]; hasKey {
			if val == nil {
				empty = append(empty, k)
			} else if s, ok := val.(string); ok && strings.TrimSpace(s) == "" {
				empty = append(empty, k)
			}
		}
	}
	return empty
}

// pyStr 复刻 Python 的 `d.get(key, "")` / `d.get(key) or ""` 语义：
// 键缺失或值为 None 一律返回空串。
//
// 绝不能用 fmt.Sprintf("%v", m[key]) 代替 —— 那样会产出字面量字符串 "<nil>"。
// 真实事故：session.meta.json 用的是 snake_case（session_id / created_at /
// updated_at），早期实现读的是 id / createdAt / updatedAt，于是 219/219 个会话的
// session_id、created_at、updated_at 全部变成字符串 "<nil>"。
func pyStr(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// round2 复刻 Python 的 round(x, 2)。
// Python 侧所有派生耗时都做了 2 位小数取整，Go 侧不做就会全量不一致。
func round2(x float64) float64 {
	return math.Round(x*100) / 100
}

func parseTime(tsStr string) (time.Time, bool) {
	if tsStr == "" {
		return time.Time{}, false
	}
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05.999999",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, f := range formats {
		if t, err := time.ParseInLocation(f, tsStr, time.Local); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// ParseSession 解析一个会话目录并构建规范化的 ExecutionTrace
func ParseSession(sessionDir string) (*models.ExecutionTrace, error) {
	metaFile := filepath.Join(sessionDir, "session.meta.json")
	msgFile := filepath.Join(sessionDir, "session.messages.json")

	metaData, err := os.ReadFile(metaFile)
	if err != nil {
		return nil, fmt.Errorf("读取 session.meta.json 失败: %w", err)
	}
	var meta map[string]any
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return nil, fmt.Errorf("解析 session.meta.json 失败: %w", err)
	}

	msgData, err := os.ReadFile(msgFile)
	if err != nil {
		return nil, fmt.Errorf("读取 session.messages.json 失败: %w", err)
	}
	var msgRoot map[string]any
	if err := json.Unmarshal(msgData, &msgRoot); err != nil {
		return nil, fmt.Errorf("解析 session.messages.json 失败: %w", err)
	}

	// 键名必须与 session.meta.json 实际的 snake_case 字段一致
	sessionID := pyStr(meta, "session_id")
	if sessionID == "" {
		// Python: meta.get("session_id") or session_dir.name
		sessionID = filepath.Base(sessionDir)
	}
	title := pyStr(meta, "title")
	status := pyStr(meta, "status")
	mode := pyStr(meta, "mode")
	createdAt := pyStr(meta, "created_at")
	updatedAt := pyStr(meta, "updated_at")

	rawMessages, _ := msgRoot["messages"].([]any)

	trace := &models.ExecutionTrace{
		SessionID:             sessionID,
		Title:                 title,
		Status:                status,
		Mode:                  mode,
		CreatedAt:             createdAt,
		UpdatedAt:             updatedAt,
		RawMessageCount:       len(rawMessages),
		SourceFile:            sessionDir,
		FirstResponseS:        -1,
		GenerationS:           -1,
		SessionS:              -1,
		ToolCalls:             []*models.ToolCall{},
		ThinkingSteps:         []*models.ThinkingStep{},
		MessageSpans:          []map[string]any{},
		AssociatedToolCallIDs: []string{},
	}

	var firstUserPrompt string
	var currentTurnPrompt string // 多轮会话：每条 user 消息都会刷新触发源
	var lastAssistantAnswer string
	var userAt, assistantAt, completedAt string
	var isStreaming bool
	stepIndex := 0

	for _, rawMsg := range rawMessages {
		msg, ok := rawMsg.(map[string]any)
		if !ok {
			continue
		}
		role := pyStr(msg, "role")
		msgTs := pyStr(msg, "timestamp")

		if role == "user" {
			// 多轮会话：每条 user 消息都刷新触发源（Python: current_turn_prompt）
			currentTurnPrompt = pyStr(msg, "content")
			trace.TurnCount++
			if firstUserPrompt == "" {
				firstUserPrompt = currentTurnPrompt
				userAt = msgTs
			}
		} else if role == "assistant" {
			// Python 累加的是整条消息的 reasoningContent，
			// 不是 thinking 步骤的 content 之和（二者在现有语料里恰好相等，
			// 但来源不同，一旦日志多出「有 reasoning 无 thinking 步骤」就露馅）。
			trace.ReasoningChars += utf8.RuneCountInString(pyStr(msg, "reasoningContent"))

			if assistantAt == "" && msgTs != "" {
				assistantAt = msgTs
			}
			msgDoneAt := pyStr(msg, "completedAt")
			if msgDoneAt != "" {
				completedAt = msgDoneAt
			}
			if s, ok := msg["isStreaming"].(bool); ok && s {
				isStreaming = true
			}

			// 检查 associatedToolCallIds
			if assoc, ok := msg["associatedToolCallIds"].([]any); ok {
				for _, a := range assoc {
					trace.AssociatedToolCallIDs = append(trace.AssociatedToolCallIDs, fmt.Sprintf("%v", a))
				}
			}

			// 最终答复：Python 取最后一条 content 非 None 的 assistant 消息，
			// 空串也算（只要不是 null）—— 不能跳过空串。
			if ans, exists := msg["content"]; exists && ans != nil {
				lastAssistantAnswer = pyStr(msg, "content")
			}

			// 遍历 timelineSteps
			// Python 的 step_index 是「先取当前值，循环末尾再 +1」，首个步骤 index=0。
			// 早期实现先 ++ 再取，导致全部 index/hint 偏移 +1（真实差分 1037/1037）。
			spanFirst := stepIndex
			timeline, _ := msg["timelineSteps"].([]any)
			for _, item := range timeline {
				step, ok := item.(map[string]any)
				if !ok {
					continue
				}
				stepType := pyStr(step, "type")

				if stepType == "thinking" {
					trace.ThinkingSteps = append(trace.ThinkingSteps, &models.ThinkingStep{
						Index:     stepIndex,
						Content:   pyStr(step, "content"),
						MsgAt:     msgTs,
						MsgDoneAt: msgDoneAt,
					})
				} else if stepType == "tool_call" {
					toolName := pyStr(step, "name")
					arguments := step["arguments"]
					rawRes, resObj := UnwrapResult(step["result"])
					body, bodyFrom := ExtractBody(rawRes, resObj)

					// 结果外壳里的元信息。
					// Python: meta_res = raw_res if isinstance(raw_res, dict) else {}
					resMeta, _ := step["result"].(map[string]any)
					tcSessionID := pyStr(resMeta, "session_id")
					if tcSessionID == "" {
						tcSessionID = sessionID
					}
					reqID := pyStr(resMeta, "request_id")
					if reqID == "" {
						reqID = pyStr(msg, "request_id")
					}

					trace.ToolCalls = append(trace.ToolCalls, &models.ToolCall{
						Index:            stepIndex,
						ToolCallID:       pyStr(step, "toolCallId"),
						Name:             toolName,
						Arguments:        arguments,
						RawResult:        rawRes,
						ResultObj:        resObj,
						Body:             body,
						BodyFrom:         bodyFrom,
						EventType:        pyStr(resMeta, "event_type"),
						SessionID:        tcSessionID,
						RequestID:        reqID,
						TurnPrompt:       currentTurnPrompt,
						MsgAt:            msgTs,
						MsgDoneAt:        msgDoneAt,
						EmptyRequiredArg: DetectEmptyRequiredArgs(toolName, arguments),
					})
				}
				stepIndex++
			}

			// 只有本条消息确有步骤时才记录区间（Python: if span_last >= span_first）
			if spanLast := stepIndex - 1; spanLast >= spanFirst {
				trace.MessageSpans = append(trace.MessageSpans, map[string]any{
					"at":         msgTs,
					"done_at":    msgDoneAt,
					"first_step": spanFirst,
					"last_step":  spanLast,
				})
			}
		}
	}

	trace.UserPrompt = firstUserPrompt
	trace.FinalAnswer = lastAssistantAnswer
	trace.UserAt = userAt
	trace.AssistantAt = assistantAt
	trace.CompletedAt = completedAt
	trace.IsStreaming = isStreaming

	// 派生耗时（秒）：Python 侧全部 round(x, 2)，且不做非负兜底。
	uT, uOK := parseTime(userAt)
	aT, aOK := parseTime(assistantAt)
	dT, dOK := parseTime(completedAt)
	crT, crOK := parseTime(createdAt)
	upT, upOK := parseTime(updatedAt)

	if uOK && aOK {
		trace.FirstResponseS = round2(aT.Sub(uT).Seconds())
	}
	if aOK && dOK {
		trace.GenerationS = round2(dT.Sub(aT).Seconds())
	} else if uOK && dOK {
		// Python 的 elif 兜底：无 assistant 时间锚时退化为 user -> done
		trace.GenerationS = round2(dT.Sub(uT).Seconds())
	}
	if crOK && upOK {
		trace.SessionS = round2(upT.Sub(crT).Seconds())
	}

	return trace, nil
}
