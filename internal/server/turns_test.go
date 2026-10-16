package server

import (
	"testing"
)

// TestNormalizeCasesMultiTurn 运行接口的用例归一化：轮次数组、上限、空轮、单轮到多轮
func TestNormalizeCasesMultiTurn(t *testing.T) {
	raw := []any{
		// 多轮（数组）
		map[string]any{"id": "C1", "prompt": []any{"第一轮", "第二轮", "第三轮"}},
		// 单轮（长度 1 的数组）
		map[string]any{"id": "C2", "prompt": []any{"单轮提问"}},
		// 空轮要被丢掉，只剩有效轮
		map[string]any{"id": "C3", "prompt": []any{"有效", "", "   ", "也有效"}},
		// 裸字符串（兼容路径）按单轮
		map[string]any{"id": "C4", "prompt": "裸字符串提问"},
		// 全是空轮 -> 整条丢掉
		map[string]any{"id": "C5", "prompt": []any{"", "  "}},
	}
	got := normalizeCases(raw)

	if len(got) != 4 {
		t.Fatalf("应保留 4 条（全空轮的丢掉），实际 %d", len(got))
	}

	// C1 三轮
	c1 := got[0]
	turns, _ := c1["prompt"].([]string)
	if len(turns) != 3 || turns[0] != "第一轮" || turns[2] != "第三轮" {
		t.Errorf("C1 应为 3 轮且顺序正确，实际 %#v", c1["prompt"])
	}
	if c1["turn_count"] != 3 {
		t.Errorf("C1 turn_count 应为 3，实际 %v", c1["turn_count"])
	}

	// C2 单轮仍是数组，长度为 1
	c2turns, _ := got[1]["prompt"].([]string)
	if len(c2turns) != 1 || c2turns[0] != "单轮提问" {
		t.Errorf("C2 应为长度 1 的数组，实际 %#v", got[1]["prompt"])
	}

	// C3 空轮被剔除
	c3turns, _ := got[2]["prompt"].([]string)
	if len(c3turns) != 2 || c3turns[0] != "有效" || c3turns[1] != "也有效" {
		t.Errorf("C3 应只剩 2 个有效轮，实际 %#v", c3turns)
	}

	// C4 裸字符串按单轮
	c4turns, _ := got[3]["prompt"].([]string)
	if len(c4turns) != 1 || c4turns[0] != "裸字符串提问" {
		t.Errorf("C4 裸字符串应按单轮处理，实际 %#v", c4turns)
	}
}

// TestNormalizeCasesTurnLimit 超过 8 轮被截断
func TestNormalizeCasesTurnLimit(t *testing.T) {
	many := make([]any, 0, 12)
	for i := 0; i < 12; i++ {
		many = append(many, "轮次")
	}
	got := normalizeCases([]any{map[string]any{"id": "C1", "prompt": many}})
	if len(got) != 1 {
		t.Fatalf("应保留 1 条，实际 %d", len(got))
	}
	turns, _ := got[0]["prompt"].([]string)
	if len(turns) != 8 {
		t.Errorf("应截断到 8 轮，实际 %d", len(turns))
	}
	if got[0]["turn_count"] != 8 {
		t.Errorf("turn_count 应为 8，实际 %v", got[0]["turn_count"])
	}
}

// TestNormalizeCasesStringElement 数组里的裸字符串用例（内部调用方）仍按单轮
func TestNormalizeCasesStringElement(t *testing.T) {
	got := normalizeCases([]any{"直接一个字符串"})
	if len(got) != 1 {
		t.Fatalf("应保留 1 条，实际 %d", len(got))
	}
	turns, _ := got[0]["prompt"].([]string)
	if len(turns) != 1 || turns[0] != "直接一个字符串" {
		t.Errorf("应为长度 1 的数组，实际 %#v", got[0]["prompt"])
	}
}
