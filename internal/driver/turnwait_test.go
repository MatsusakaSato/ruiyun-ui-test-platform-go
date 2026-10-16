package driver

import (
	"encoding/json"
	"os"
	"path/filepath"
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
