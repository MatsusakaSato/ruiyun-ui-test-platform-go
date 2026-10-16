package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestArchiveReportSkipsStaleOnRenderFailure 渲染失败时不能把上一轮的旧报告
// 拷进本轮归档目录：那会让读者看到另一轮的数据（真实事故：归档报告只显示
// 最后一轮，实际读到的是几十分钟前的旧档案）。
func TestArchiveReportSkipsStaleOnRenderFailure(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "ruiyun_report.html")
	if err := os.WriteFile(src, []byte("<html>上一轮的旧报告</html>"), 0644); err != nil {
		t.Fatal(err)
	}
	roundDir := filepath.Join(dir, "run_1")
	if err := os.MkdirAll(roundDir, 0755); err != nil {
		t.Fatal(err)
	}
	// 模拟「同 run_id 重跑」：归档目录里已经有上一次留下的报告
	stale := filepath.Join(roundDir, "report.html")
	if err := os.WriteFile(stale, []byte("<html>残留的旧报告</html>"), 0644); err != nil {
		t.Fatal(err)
	}

	var logs []string
	archiveReport(false, src, roundDir, func(m string) { logs = append(logs, m) })

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("渲染失败时应清掉残留的报告文件，实际 err=%v", err)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "报告渲染失败") {
		t.Errorf("应记录明确的失败原因，实际日志：%q", joined)
	}
}

// TestArchiveReportCopiesFreshReport 渲染成功时才写入本轮报告
func TestArchiveReportCopiesFreshReport(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "ruiyun_report.html")
	want := "<html>本轮的正式报告</html>"
	if err := os.WriteFile(src, []byte(want), 0644); err != nil {
		t.Fatal(err)
	}
	roundDir := filepath.Join(dir, "run_2")
	if err := os.MkdirAll(roundDir, 0755); err != nil { // 真实链路里归档目录由 Stage 3 先建好
		t.Fatal(err)
	}

	archiveReport(true, src, roundDir, func(string) {})

	got, err := os.ReadFile(filepath.Join(roundDir, "report.html"))
	if err != nil {
		t.Fatalf("归档里应有本轮报告：%v", err)
	}
	if string(got) != want {
		t.Errorf("归档报告内容应为本轮报告，实际 %q", string(got))
	}
}
