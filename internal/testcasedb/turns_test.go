package testcasedb

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestMultiTurnRoundTrip 多轮用例能写入并按轮读回
func TestMultiTurnRoundTrip(t *testing.T) {
	dbPath := createTempDB(t)
	turns := []string{"第一轮提问", "第二轮追问", "第三轮再追问"}

	ok, msg, c := AddPresetCase(map[string]any{
		"prompt": turns,
		"labels": map[string]any{"scene": "多轮", "difficulty": "中"},
	}, dbPath)
	if !ok {
		t.Fatalf("多轮用例写入失败: %s", msg)
	}
	if c["turn_count"] != 3 {
		t.Errorf("turn_count 应为 3，实际 %v", c["turn_count"])
	}
	got, _ := c["prompt"].([]string)
	if len(got) != 3 || got[0] != "第一轮提问" || got[2] != "第三轮再追问" {
		t.Errorf("轮次应原样读回，实际 %#v", got)
	}

	// 从库里重新读一遍，确认落库形态就是 JSON 数组
	list, err := GetPresetCases(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("期望 1 条，实际 %d", len(list))
	}
	back, _ := list[0]["prompt"].([]string)
	if len(back) != 3 {
		t.Errorf("库里读回应为 3 轮，实际 %#v", back)
	}
}

// TestMultiTurnOrderMattersForDedupe 判重含轮序：["A","B"] 与 ["B","A"] 是两条用例
func TestMultiTurnOrderMattersForDedupe(t *testing.T) {
	dbPath := createTempDB(t)

	if ok, msg, _ := AddPresetCase(map[string]any{
		"prompt": []string{"A问", "B问"},
	}, dbPath); !ok {
		t.Fatalf("第一条写入失败: %s", msg)
	}
	// 顺序不同 -> 不应判重
	if ok, msg, _ := AddPresetCase(map[string]any{
		"prompt": []string{"B问", "A问"},
	}, dbPath); !ok {
		t.Fatalf("轮序不同的用例不应被判重: %s", msg)
	}
	// 完全一致 -> 判重
	if ok, msg, _ := AddPresetCase(map[string]any{
		"prompt": []string{"A问", "B问"},
	}, dbPath); ok {
		t.Errorf("完全一致的多轮用例应被判重，实际通过了（msg=%s）", msg)
	}
	if total, _ := CountCases(dbPath); total != 2 {
		t.Errorf("期望库中 2 条，实际 %d", total)
	}
}

// TestMultiTurnWhitespaceDeduped 轮内空白差异仍应判重
func TestMultiTurnWhitespaceDeduped(t *testing.T) {
	dbPath := createTempDB(t)
	if ok, _, _ := AddPresetCase(map[string]any{"prompt": []string{"写 一首诗", "再改短"}}, dbPath); !ok {
		t.Fatal("第一条写入失败")
	}
	if ok, msg, _ := AddPresetCase(map[string]any{"prompt": []string{"写一首诗", "再改短"}}, dbPath); ok {
		t.Errorf("轮内仅空白差异应判重，实际未被判重（msg=%s）", msg)
	}
}

// TestTurnLimit 超过 8 轮被截断并在提示里说明
func TestTurnLimit(t *testing.T) {
	dbPath := createTempDB(t)
	turns := make([]string, 0, 10)
	for i := 0; i < 10; i++ {
		turns = append(turns, "第"+string(rune('一'+i))+"轮")
	}
	ok, msg, c := AddPresetCase(map[string]any{"prompt": turns}, dbPath)
	if !ok {
		t.Fatalf("写入失败: %s", msg)
	}
	if c["turn_count"] != MaxTurns {
		t.Errorf("轮数应被截断为 %d，实际 %v", MaxTurns, c["turn_count"])
	}
	if !strings.Contains(msg, "超出") {
		t.Errorf("截断应在提示里说明，实际 %q", msg)
	}
}

// TestPromptMustBeArray 非数组的 prompt 一律拒绝（用户确认：只接受数组）
func TestPromptMustBeArray(t *testing.T) {
	dbPath := createTempDB(t)
	for _, bad := range []any{"裸字符串", 123, map[string]any{"a": 1}} {
		ok, msg, _ := AddPresetCase(map[string]any{"prompt": bad}, dbPath)
		if ok {
			t.Errorf("prompt=%#v 应被拒绝", bad)
		}
		if msg == "" {
			t.Errorf("prompt=%#v 被拒绝时应给出原因", bad)
		}
	}
}

// TestEmptyTurnsRejected 空数组与全空白轮次视为「提问为必填」
func TestEmptyTurnsRejected(t *testing.T) {
	dbPath := createTempDB(t)
	for _, bad := range []any{[]string{}, []string{"  ", ""}, []any{}} {
		ok, msg, _ := AddPresetCase(map[string]any{"prompt": bad}, dbPath)
		if ok {
			t.Errorf("prompt=%#v 应被拒绝", bad)
		}
		if msg != "提问为必填" {
			t.Errorf("prompt=%#v 的提示应为「提问为必填」，实际 %q", bad, msg)
		}
	}
}

// TestEncodeParseTurns 存储形态与解析互逆
func TestEncodeParseTurns(t *testing.T) {
	turns := []string{"a", "b c", "d\"e"}
	enc := EncodeTurns(turns)
	if !json.Valid([]byte(enc)) {
		t.Fatalf("EncodeTurns 应产出合法 JSON，实际 %q", enc)
	}
	back := parseTurns(enc)
	if len(back) != len(turns) {
		t.Fatalf("往返后轮数应一致，实际 %#v", back)
	}
	for i := range turns {
		if back[i] != turns[i] {
			t.Errorf("第 %d 轮应往返一致：%q -> %q", i, turns[i], back[i])
		}
	}
	if got := parseTurns("裸字符串"); len(got) != 0 {
		t.Errorf("非 JSON 应解析为空轮次，实际 %#v", got)
	}
	if got := parseTurns(""); len(got) != 0 {
		t.Errorf("空串应解析为空轮次，实际 %#v", got)
	}
}
