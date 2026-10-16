package driver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeSession 写一份最小可用的 session.messages.json
func writeSession(t *testing.T, dir string, msgs []map[string]any) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"messages": msgs})
	if err := os.WriteFile(filepath.Join(dir, "session.messages.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
}

func asst(content string) map[string]any {
	return map[string]any{"role": "assistant", "content": content, "completedAt": "2026-10-16T10:00:00Z"}
}

func user(content string) map[string]any {
	return map[string]any{"role": "user", "content": content}
}

// TestAssistantMessageCount 只数「已完成」的 assistant
func TestAssistantMessageCount(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, []map[string]any{
		user("q1"), asst("a1"),
		user("q2"), asst("a2"),
	})
	if got := assistantMessageCount(dir); got != 2 {
		t.Errorf("应数到 2 条已完成 assistant，实际 %d", got)
	}

	// 流式中的不算完成
	streaming := map[string]any{"role": "assistant", "content": "半截", "isStreaming": true}
	writeSession(t, dir, []map[string]any{user("q1"), asst("a1"), user("q2"), streaming})
	if got := assistantMessageCount(dir); got != 1 {
		t.Errorf("流式中的 assistant 不应计入，实际得到 %d", got)
	}

	// 空内容且没有完成时间的不算
	writeSession(t, dir, []map[string]any{
		user("q1"), asst("a1"),
		user("q2"), map[string]any{"role": "assistant", "content": ""},
	})
	if got := assistantMessageCount(dir); got != 1 {
		t.Errorf("未完成的空 assistant 不应计入，实际得到 %d", got)
	}

	// 文件不存在返回 -1
	if got := assistantMessageCount(filepath.Join(dir, "nope")); got != -1 {
		t.Errorf("文件不存在应返回 -1，实际 %d", got)
	}
}

// TestWaitTurnDoneRequiresNewAssistant 关键回归：
// 上一轮已完成、日志静默时，等待「本轮」不能被旧状态骗过而立刻返回。
//
// 这正是 WaitSettled 在轮间会踩的坑：它的判据是「日志静默 + 末条是
// 已完成 assistant」，而发完第 N 轮时第 N-1 轮刚好满足，于是立刻
// 返回 true，本轮还没生成就被当成结束。
func TestWaitTurnDoneRequiresNewAssistant(t *testing.T) {
	dir := t.TempDir()
	// 会话已有 1 轮且完全静默（模拟「刚发完第 2 轮，但第 2 轮还没开始生成」）
	writeSession(t, dir, []map[string]any{user("q1"), asst("a1")})

	d := &RuiyunUIDriver{}
	// 项目里没有可靠的存活探针可用时 CheckAppAlive 会走真实实现，
	// 这里只验证「不会立刻返回 true」这一点，给一个很短的超时。
	start := time.Now()
	got := d.WaitTurnDone(dir, 1, 1.2, 0.1)
	elapsed := time.Since(start)

	if got {
		t.Error("本轮还没开始生成时不应判为已完成（旧状态骗过了等待）")
	}
	if elapsed < 900*time.Millisecond {
		t.Errorf("应当一直等到超时，实际只用了 %v", elapsed)
	}
}

// TestWaitTurnDoneDetectsCompletion 本轮真的答完后返回 true
func TestWaitTurnDoneDetectsCompletion(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, []map[string]any{user("q1"), asst("a1")})

	d := &RuiyunUIDriver{}
	// 模拟本轮开始生成并很快完成
	go func() {
		time.Sleep(300 * time.Millisecond)
		writeSession(t, dir, []map[string]any{
			user("q1"), asst("a1"),
			user("q2"), map[string]any{"role": "assistant", "content": "答完了", "completedAt": "2026-10-16T10:00:05Z"},
		})
	}()

	if !d.WaitTurnDone(dir, 1, 6.0, 0.3) {
		t.Error("本轮答完后应判为已完成")
	}
}

// TestWaitTurnDoneStartedZeroPrev 发送前取不到条数时退化为只看静默
func TestWaitTurnDoneStartedZeroPrev(t *testing.T) {
	dir := t.TempDir()
	writeSession(t, dir, []map[string]any{user("q1"), asst("a1")})
	d := &RuiyunUIDriver{}
	if !d.WaitTurnDone(dir, -1, 3.0, 0.2) {
		t.Error("prevCount 取不到时应退化为只看静默并返回 true")
	}
}

// TestSendOutcome 发送成败的判据：输入框没被清空就等于没发出去
func TestSendOutcome(t *testing.T) {
	cases := []struct {
		name      string
		landed    string
		cleared   bool
		wantOK    bool
		wantRetry bool
	}{
		{"有内容且已清空 = 成功", "第二轮提问", true, true, false},
		{"有内容但没清空 = 失败，可换方式重试", "第二轮提问", false, false, true},
		{"只有空白也算空输入框 = 成功", "   ", false, true, false},
		{"本来就是空的 = 成功", "", false, true, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, retry := sendOutcome(c.landed, c.cleared)
			if ok != c.wantOK || retry != c.wantRetry {
				t.Errorf("sendOutcome(%q,%v) = (%v,%v)，期望 (%v,%v)",
					c.landed, c.cleared, ok, retry, c.wantOK, c.wantRetry)
			}
		})
	}
}

// TestSendFailureErrorMentionsCause 报错文案要能区分三种成因，
// 否则排查时看不出是「按钮被禁用」还是「压根没找到按钮」。
func TestSendFailureErrorMentionsCause(t *testing.T) {
	cases := []struct {
		name    string
		desc    string
		wantSub string
	}{
		{"按钮可点但仍没生效", "发送", "发送按钮 发送"},
		{"按钮被禁用", "disabled:发送", "不可点"},
		{"找不到按钮", "", "未找到可用的发送按钮"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := sendFailureError("第二轮提问", c.desc, "/tmp/x.json")
			if err == nil {
				t.Fatal("应返回 error")
			}
			if !strings.Contains(err.Error(), c.wantSub) {
				t.Errorf("报错应包含 %q，实际 %q", c.wantSub, err.Error())
			}
			if !strings.Contains(err.Error(), "第二轮提问") {
				t.Errorf("报错应带上残留内容，实际 %q", err.Error())
			}
		})
	}
}

// TestSendFailureErrorTruncatesLongText 超长残留内容被截断，避免刷屏
func TestSendFailureErrorTruncatesLongText(t *testing.T) {
	long := strings.Repeat("很长的提问", 30)
	err := sendFailureError(long, "发送", "/tmp/x.json")
	if err == nil {
		t.Fatal("应返回 error")
	}
	if strings.Contains(err.Error(), long) {
		t.Error("超长内容应被截断")
	}
	if !strings.Contains(err.Error(), "…") && len([]rune(err.Error())) > 200 {
		t.Errorf("报错过长：%d 字", len([]rune(err.Error())))
	}
}
