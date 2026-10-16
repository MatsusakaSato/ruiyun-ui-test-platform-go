package logparser

import (
	"os"
	"path/filepath"
	"testing"
)

// TestTurnSplitInvariants 用真实会话日志校验按轮切分的不变式。
//
// 切分依据是「user→assistant 消息对」，不是「最近一条 user」：
// 真实语料里存在 user 无对应 assistant 的情况（模型调用失败 / 连续两条 user）。
// 本测试在 SESSION_ROOT 可用时跑真语料，不可用时自动跳过（CI 友好）。
func TestTurnSplitInvariants(t *testing.T) {
	root := os.Getenv("SESSION_ROOT")
	if root == "" {
		root = filepath.Join(os.Getenv("HOME"), ".srtclaw", "workspace", "session")
	}
	ents, err := os.ReadDir(root)
	if err != nil {
		t.Skipf("会话日志目录不可用：%s", root)
	}

	nSess, nMulti := 0, 0
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		d := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(d, "session.messages.json")); err != nil {
			continue
		}
		tr, err := ParseSession(d)
		if err != nil || tr == nil {
			continue
		}
		nSess++
		if len(tr.Turns) >= 2 {
			nMulti++
		}

		// 单轮会话也必须有一条轮记录
		if tr.TurnCount > 0 && len(tr.Turns) != tr.TurnCount {
			t.Errorf("%s: len(Turns)=%d != TurnCount=%d", e.Name(), len(tr.Turns), tr.TurnCount)
		}
		// 索引连续且与下标一致
		for i, tn := range tr.Turns {
			if tn.Index != i {
				t.Errorf("%s: turn[%d].Index=%d 不连续", e.Name(), i, tn.Index)
			}
		}
		// 每个工具的 TurnIndex 必须合法
		for _, tc := range tr.ToolCalls {
			if tc.TurnIndex < 0 || tc.TurnIndex >= len(tr.Turns) {
				t.Errorf("%s: 工具 %s 的 TurnIndex=%d 越界（共 %d 轮）",
					e.Name(), tc.Name, tc.TurnIndex, len(tr.Turns))
			}
		}
		// 轮内工具调用合计必须等于平铺总数（不漏不重）
		var sum int
		for _, tn := range tr.Turns {
			sum += len(tn.ToolCalls)
		}
		if sum != len(tr.ToolCalls) {
			t.Errorf("%s: 轮内工具调用合计 %d != 平铺 %d", e.Name(), sum, len(tr.ToolCalls))
		}
		// 轮内思考步合计必须等于平铺总数
		var sumT int
		for _, tn := range tr.Turns {
			sumT += len(tn.ThinkingSteps)
		}
		if sumT != len(tr.ThinkingSteps) {
			t.Errorf("%s: 轮内思考步合计 %d != 平铺 %d", e.Name(), sumT, len(tr.ThinkingSteps))
		}
	}
	if nSess == 0 {
		t.Skip("没有可用的会话日志")
	}
	t.Logf("校验会话 %d 个（多轮 %d 个）", nSess, nMulti)
}

// TestTurnSplitUnanswered 校验「提问未被答复」的真实边界用例。
// 语料：一条 user 后跟 system 模型调用失败，assistant 完全没回。
// 期望：该轮仍存在（否则轮号会与实际提问顺序错位），标记 incomplete，并带上 system 错误。
func TestTurnSplitUnanswered(t *testing.T) {
	root := os.Getenv("SESSION_ROOT")
	if root == "" {
		root = filepath.Join(os.Getenv("HOME"), ".srtclaw", "workspace", "session")
	}

	cases := []struct {
		name     string
		dir      string
		wantTurn int
		wantLast bool // 最后一轮是否应标记未完成
	}{
		{"连续两条 user", "sess_1a142b80a10_a24a51", 3, false},
		{"末轮模型失败", "sess_1a09ef3918f_92f0f4", 2, true},
		{"全部轮失败", "sess_1a0ad4e74ab_707b2f", 2, true},
	}
	ran := 0
	for _, c := range cases {
		d := filepath.Join(root, c.dir)
		if _, err := os.Stat(filepath.Join(d, "session.messages.json")); err != nil {
			continue // 真实语料不含该会话时跳过
		}
		ran++
		tr, err := ParseSession(d)
		if err != nil || tr == nil {
			t.Fatalf("%s: 解析失败 %v", c.name, err)
		}
		if len(tr.Turns) != c.wantTurn {
			t.Errorf("%s: 轮数=%d，期望 %d", c.name, len(tr.Turns), c.wantTurn)
		}
		last := tr.Turns[len(tr.Turns)-1]
		if last.Incomplete != c.wantLast {
			t.Errorf("%s: 末轮 incomplete=%v，期望 %v", c.name, last.Incomplete, c.wantLast)
		}
		if last.Incomplete && last.IncompleteWhy == "" {
			t.Errorf("%s: 末轮标记未完成但没写原因", c.name)
		}
	}
	if ran == 0 {
		t.Skip("真实语料中不含边界会话")
	}
}
