package server

import (
	"os"
	"path/filepath"
	"testing"
)

// 附件入库前的路径校验：只有真实存在的文件才留下，其余剔除并回报。
func TestDropMissingAttachments(t *testing.T) {
	dir := t.TempDir()
	exist := filepath.Join(dir, "真实附件.pdf")
	if err := os.WriteFile(exist, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "子目录")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "已删除.docx")

	// 1. 不存在的路径与目录都要剔除，只剩真实文件；空白项静默丢弃不计入回报
	item := map[string]any{"attachments": []any{exist, missing, "   ", sub}}
	skipped := dropMissingAttachments(item)
	if len(skipped) != 2 {
		t.Errorf("期望剔除 2 个（缺失文件 + 目录），实际 %v", skipped)
	}
	kept, _ := item["attachments"].([]any)
	if len(kept) != 1 || kept[0] != exist {
		t.Errorf("期望只保留 %s，实际 %v", exist, item["attachments"])
	}

	// 2. 全部有效时不动字段、也不回报跳过
	itemOK := map[string]any{"attachments": []any{exist}}
	if got := dropMissingAttachments(itemOK); got != nil {
		t.Errorf("全部有效时不应回报跳过，实际 %v", got)
	}
	if got, _ := itemOK["attachments"].([]any); len(got) != 1 {
		t.Errorf("全部有效时字段不应被改写，实际 %v", itemOK["attachments"])
	}

	// 3. []string 形式同样支持
	itemStr := map[string]any{"attachments": []string{exist, missing}}
	if got := dropMissingAttachments(itemStr); len(got) != 1 || got[0] != missing {
		t.Errorf("[]string 形式应剔除 %s，实际 %v", missing, got)
	}

	// 4. 没有该字段、或类型不是切片时原样放过，不 panic
	if got := dropMissingAttachments(map[string]any{"prompt": "没有附件"}); got != nil {
		t.Errorf("没有 attachments 字段应返回 nil，实际 %v", got)
	}
	if got := dropMissingAttachments(map[string]any{"attachments": "a.pdf"}); got != nil {
		t.Errorf("字段不是切片应返回 nil，实际 %v", got)
	}
}
