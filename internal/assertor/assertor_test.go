package assertor

import (
	"encoding/json"
	"strings"
	"testing"

	"ruiyun-ui-test-platform-go/internal/models"
)

func TestAssertorToolFailed(t *testing.T) {
	trace := &models.ExecutionTrace{SessionID: "test_sess"}
	cfg := map[string]any{
		"error_markers": []string{
			"[ERROR]",
			"[error]",
			"failed to",
			"traceback",
			"timed out",
			"timeout",
			"connection aborted",
			"max retries exceeded",
			"tool_call_failed",
			"exception",
		},
	}

	// 1. 测试 read_skill_file 正文中包含 exception，明确 success: true 时绝不误报
	rawJSON := `{
		"success": true,
		"skill_ref": "skill_ref_3a2ce0a39ae3",
		"skill_name": "local-doc-edit",
		"relative_path": "SKILL.md",
		"file_content": "The host recognizes same-request, unpublished Word drafts and classifies matching-version continuation as low risk. This exception ends with the request and does not cover existing user files..."
	}`
	var resObj map[string]any
	_ = json.Unmarshal([]byte(rawJSON), &resObj)

	tc := &models.ToolCall{
		Index:      1,
		ToolCallID: "call_1",
		Name:       "read_skill_file",
		RawResult:  &rawJSON,
		ResultObj:  resObj,
	}
	findings := CheckToolFailed(tc, trace, cfg)
	if len(findings) != 0 {
		t.Fatalf("显式 success: true 的结果不应报警: %v", findings)
	}

	// 2. 测试未带 success 字段的对象，正文字段中的 exception 同样不应误报
	rawJSON2 := `{
		"skill_ref": "skill_ref_123",
		"relative_path": "SKILL.md",
		"file_content": "With one minor exception, the document is complete."
	}`
	var resObj2 map[string]any
	_ = json.Unmarshal([]byte(rawJSON2), &resObj2)
	tc2 := &models.ToolCall{
		Index:      2,
		ToolCallID: "call_2",
		Name:       "read_file",
		RawResult:  &rawJSON2,
		ResultObj:  resObj2,
	}
	findings2 := CheckToolFailed(tc2, trace, cfg)
	if len(findings2) != 0 {
		t.Fatalf("正文字段包含普通 exception 不应误报: %v", findings2)
	}

	// 3. 测试文本中真实的程序异常抛出（带冒号或异常上下文），能正常报警
	rawErr := "Unhandled Exception: database connection failed\n  at db.connect()"
	tc3 := &models.ToolCall{
		Index:      3,
		ToolCallID: "call_3",
		Name:       "query_db",
		RawResult:  &rawErr,
		ResultObj:  nil,
	}
	findings3 := CheckToolFailed(tc3, trace, cfg)
	if len(findings3) != 1 || findings3[0].Rule != "TOOL_CALL_FAILED" {
		t.Fatalf("真实异常未能正确报警: %v", findings3)
	}

	// 4. 测试字典中显式 success=false 或包含 error 字段能够正确报警
	rawJSON4 := `{"success": false, "error": "File not found: document.docx"}`
	var resObj4 map[string]any
	_ = json.Unmarshal([]byte(rawJSON4), &resObj4)
	tc4 := &models.ToolCall{
		Index:      4,
		ToolCallID: "call_4",
		Name:       "read_file",
		RawResult:  &rawJSON4,
		ResultObj:  resObj4,
	}
	findings4 := CheckToolFailed(tc4, trace, cfg)
	if len(findings4) != 1 || findings4[0].Rule != "TOOL_CALL_FAILED" {
		t.Fatalf("显式 error 字典未能正确报警: %v", findings4)
	}
	if !strings.Contains(findings4[0].Detail, "success=false") {
		t.Errorf("期望详情包含 success=false，实际: %s", findings4[0].Detail)
	}

	// 5. 空结果触发 TOOL_RESULT_MISSING
	emptyStr := ""
	tc5 := &models.ToolCall{
		Index:      5,
		ToolCallID: "call_5",
		Name:       "some_tool",
		RawResult:  &emptyStr,
		ResultObj:  nil,
	}
	findings5 := CheckToolFailed(tc5, trace, cfg)
	if len(findings5) != 1 || findings5[0].Rule != "TOOL_RESULT_MISSING" {
		t.Fatalf("空结果未能正确触发 TOOL_RESULT_MISSING: %v", findings5)
	}
}
