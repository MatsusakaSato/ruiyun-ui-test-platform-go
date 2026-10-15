package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/xuri/excelize/v2"

	"ruiyun-ui-test-platform-go/internal/config"
)

func TestPresetImportReadsExcelAttachmentPath(t *testing.T) {
	attachmentPath := filepath.Join(t.TempDir(), "附件.pdf")
	if err := os.WriteFile(attachmentPath, []byte("test attachment"), 0644); err != nil {
		t.Fatal(err)
	}

	book := excelize.NewFile()
	defer book.Close()
	sheet := book.GetSheetName(0)
	for col, value := range []string{"提问", "附件"} {
		cell, err := excelize.CoordinatesToCellName(col+1, 1)
		if err != nil {
			t.Fatal(err)
		}
		if err := book.SetCellValue(sheet, cell, value); err != nil {
			t.Fatal(err)
		}
	}
	for col, value := range []string{"读取这个文件", attachmentPath} {
		cell, err := excelize.CoordinatesToCellName(col+1, 2)
		if err != nil {
			t.Fatal(err)
		}
		if err := book.SetCellValue(sheet, cell, value); err != nil {
			t.Fatal(err)
		}
	}
	workbook, err := book.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]any{
		"name":        "cases.xlsx",
		"data_base64": base64.StdEncoding.EncodeToString(workbook.Bytes()),
		"parse_only":  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec, req)

	var response struct {
		OK    bool `json:"ok"`
		Items []struct {
			Attachments []string `json:"attachments"`
		} `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v; body: %s", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || !response.OK {
		t.Fatalf("import failed: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if len(response.Items) != 1 {
		t.Fatalf("got %d imported items, want 1; response: %s", len(response.Items), rec.Body.String())
	}
	if len(response.Items[0].Attachments) != 1 || response.Items[0].Attachments[0] != attachmentPath {
		t.Fatalf("attachments = %#v, want [%q]", response.Items[0].Attachments, attachmentPath)
	}
}

func TestTemplateDownload(t *testing.T) {
	root := t.TempDir()
	templateDir := filepath.Join(root, "resource")
	if err := os.MkdirAll(templateDir, 0755); err != nil {
		t.Fatal(err)
	}
	want := []byte("xlsx template")
	if err := os.WriteFile(filepath.Join(templateDir, "template.xlsx"), want, 0644); err != nil {
		t.Fatal(err)
	}

	previousRoot := config.RootDir
	config.RootDir = root
	t.Cleanup(func() { config.RootDir = previousRoot })

	req := httptest.NewRequest(http.MethodGet, "/resource/template.xlsx", nil)
	rec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec, req)

	res := rec.Result()
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", res.StatusCode, http.StatusOK)
	}
	if got := res.Header.Get("Content-Type"); got != "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" {
		t.Errorf("Content-Type = %q", got)
	}
	if got := res.Header.Get("Content-Disposition"); got != `attachment; filename="template.xlsx"` {
		t.Errorf("Content-Disposition = %q", got)
	}
	if string(body) != string(want) {
		t.Errorf("body = %q, want %q", body, want)
	}
}

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
