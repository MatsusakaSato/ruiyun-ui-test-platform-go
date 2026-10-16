package pipeline

import (
	"testing"

	"ruiyun-ui-test-platform-go/internal/testcasedb"
)

// TestNormalizeCaseTurns 轮次归一化：数组/字符串/空轮/超限
func TestNormalizeCaseTurns(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want []string
	}{
		{"字符串数组", []string{"a", "b"}, []string{"a", "b"}},
		{"any 数组", []any{"a", "b"}, []string{"a", "b"}},
		{"单元素数组", []string{"只有一个"}, []string{"只有一个"}},
		{"字符串按单轮", "单个提问", []string{"单个提问"}},
		{"去掉空轮与空白", []string{" a ", "", "  ", "b"}, []string{"a", "b"}},
		{"全空返回空", []string{"", "  "}, []string{}},
		{"缺字段返回空", nil, []string{}},
		{"非字符串元素", []any{1, "b"}, []string{"1", "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := normalizeCaseTurns(map[string]any{"prompt": c.in})
			if len(got) != len(c.want) {
				t.Fatalf("轮数应为 %d，实际 %d（%#v）", len(c.want), len(got), got)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Errorf("第 %d 轮应为 %q，实际 %q", i, c.want[i], got[i])
				}
			}
		})
	}
}

// TestNormalizeCaseTurnsTruncates 超过上限的轮次被截断
func TestNormalizeCaseTurnsTruncates(t *testing.T) {
	many := make([]string, 0, 20)
	for i := 0; i < 20; i++ {
		many = append(many, "轮")
	}
	got := normalizeCaseTurns(map[string]any{"prompt": many})
	if len(got) != testcasedb.MaxTurns {
		t.Errorf("应截断到 %d 轮，实际 %d", testcasedb.MaxTurns, len(got))
	}
}
