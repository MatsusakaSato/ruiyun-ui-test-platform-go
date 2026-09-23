package logparser

import (
	"testing"
)

func TestParsePyLiteral(t *testing.T) {
	// 测试包含单引号、None、False/True 的典型 Python repr
	pyStr := `{'success': False, 'error': '未找到对应文件', 'code': 404, 'details': [1, 'two', None, True]}`
	val, err := ParsePyLiteral(pyStr)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}

	m, ok := val.(map[string]any)
	if !ok {
		t.Fatalf("期望 map[string]any，实际 %T", val)
	}

	if m["success"] != false {
		t.Errorf("期望 success=false，实际 %v", m["success"])
	}
	if m["error"] != "未找到对应文件" {
		t.Errorf("error 字段不符: %v", m["error"])
	}
	if m["code"] != 404 {
		t.Errorf("code 字段不符: %v", m["code"])
	}

	arr, ok := m["details"].([]any)
	if !ok || len(arr) != 4 {
		t.Fatalf("details 字段不符: %v", m["details"])
	}
	if arr[2] != nil || arr[3] != true {
		t.Errorf("None / True 解析不符: %v", arr)
	}
}

func TestUnwrapResult(t *testing.T) {
	// 测试嵌套包装字典
	wrapper := map[string]any{
		"event_type":   "tool_result",
		"tool_name":    "read_file",
		"tool_call_id": "call_123",
		"result": map[string]any{
			"success":      true,
			"file_content": "文档正文内容",
		},
	}
	rawStr, obj := UnwrapResult(wrapper)
	if rawStr == nil || obj == nil {
		t.Fatalf("解包失败")
	}

	body, from := ExtractBody(rawStr, obj)
	if body != "文档正文内容" || from != "file_content" {
		t.Errorf("ExtractBody 结果不符: body=%s, from=%s", body, from)
	}
}
