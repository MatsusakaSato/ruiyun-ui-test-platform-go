package logparser

import (
	"encoding/json"
	"fmt"
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
	var empty []string
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

	sessionID := fmt.Sprintf("%v", meta["id"])
	title := fmt.Sprintf("%v", meta["title"])
	status := fmt.Sprintf("%v", meta["status"])
	mode := fmt.Sprintf("%v", meta["mode"])
	createdAt := fmt.Sprintf("%v", meta["createdAt"])
	updatedAt := fmt.Sprintf("%v", meta["updatedAt"])

	rawMessages, _ := msgRoot["messages"].([]any)

	trace := &models.ExecutionTrace{
		SessionID:             sessionID,
		Title:                 title,
		Status:                status,
		Mode:                  mode,
		CreatedAt:             createdAt,
		UpdatedAt:             updatedAt,
		RawMessageCount:       len(rawMessages),
		SourceFile:            msgFile,
		FirstResponseS:        -1,
		GenerationS:           -1,
		SessionS:              -1,
		ToolCalls:             []*models.ToolCall{},
		ThinkingSteps:         []*models.ThinkingStep{},
		MessageSpans:          []map[string]any{},
		AssociatedToolCallIDs: []string{},
	}

	var firstUserPrompt string
	var lastAssistantAnswer string
	var userAt, assistantAt, completedAt string
	var isStreaming bool
	stepIndex := 0

	for _, rawMsg := range rawMessages {
		msg, ok := rawMsg.(map[string]any)
		if !ok {
			continue
		}
		role := fmt.Sprintf("%v", msg["role"])
		msgTs := fmt.Sprintf("%v", msg["timestamp"])
		if msgTs == "<nil>" {
			msgTs = ""
		}

		if role == "user" {
			trace.TurnCount++
			content := fmt.Sprintf("%v", msg["content"])
			if firstUserPrompt == "" {
				firstUserPrompt = content
				userAt = msgTs
			}
		} else if role == "assistant" {
			if assistantAt == "" {
				assistantAt = msgTs
			}
			msgDoneAt := fmt.Sprintf("%v", msg["completedAt"])
			if msgDoneAt == "<nil>" {
				msgDoneAt = ""
			}
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

			// 最终答复
			if ans, exists := msg["content"]; exists && ans != nil {
				ansStr := fmt.Sprintf("%v", ans)
				if ansStr != "" && ansStr != "<nil>" {
					lastAssistantAnswer = ansStr
				}
			}

			// 遍历 timelineSteps
			firstStep := stepIndex + 1
			timeline, _ := msg["timelineSteps"].([]any)
			for _, item := range timeline {
				step, ok := item.(map[string]any)
				if !ok {
					continue
				}
				stepType := fmt.Sprintf("%v", step["type"])
				stepIndex++

				if stepType == "thinking" {
					c := fmt.Sprintf("%v", step["content"])
					trace.ReasoningChars += utf8.RuneCountInString(c)
					trace.ThinkingSteps = append(trace.ThinkingSteps, &models.ThinkingStep{
						Index:     stepIndex,
						Content:   c,
						MsgAt:     msgTs,
						MsgDoneAt: msgDoneAt,
					})
				} else if stepType == "tool_call" {
					tcID := fmt.Sprintf("%v", step["toolCallId"])
					toolName := fmt.Sprintf("%v", step["name"])
					arguments := step["arguments"]
					rawRes, resObj := UnwrapResult(step["result"])
					body, bodyFrom := ExtractBody(rawRes, resObj)
					emptyArgs := DetectEmptyRequiredArgs(toolName, arguments)

					tc := &models.ToolCall{
						Index:            stepIndex,
						ToolCallID:       tcID,
						Name:             toolName,
						Arguments:        arguments,
						RawResult:        rawRes,
						ResultObj:        resObj,
						Body:             body,
						BodyFrom:         bodyFrom,
						SessionID:        sessionID,
						TurnPrompt:       firstUserPrompt,
						MsgAt:            msgTs,
						MsgDoneAt:        msgDoneAt,
						EmptyRequiredArg: emptyArgs,
					}
					trace.ToolCalls = append(trace.ToolCalls, tc)
				}
			}
			lastStep := stepIndex
			trace.MessageSpans = append(trace.MessageSpans, map[string]any{
				"at":         msgTs,
				"done_at":    msgDoneAt,
				"first_step": firstStep,
				"last_step":  lastStep,
			})
		}
	}

	trace.UserPrompt = firstUserPrompt
	trace.FinalAnswer = lastAssistantAnswer
	trace.UserAt = userAt
	trace.AssistantAt = assistantAt
	trace.CompletedAt = completedAt
	trace.IsStreaming = isStreaming

	// 计算时序
	if uT, ok := parseTime(userAt); ok {
		if aT, ok := parseTime(assistantAt); ok {
			diff := aT.Sub(uT).Seconds()
			if diff >= 0 {
				trace.FirstResponseS = diff
			}
		}
	}
	if aT, ok := parseTime(assistantAt); ok {
		if cT, ok := parseTime(completedAt); ok {
			diff := cT.Sub(aT).Seconds()
			if diff >= 0 {
				trace.GenerationS = diff
			}
		}
	}
	if crT, ok := parseTime(createdAt); ok {
		if upT, ok := parseTime(updatedAt); ok {
			diff := upT.Sub(crT).Seconds()
			if diff >= 0 {
				trace.SessionS = diff
			}
		}
	}

	return trace, nil
}
