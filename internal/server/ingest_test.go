package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"ruiyun-ui-test-platform-go/internal/config"
	"ruiyun-ui-test-platform-go/internal/testcasedb"
)

// 用临时 HOME 把附件库收敛到测试沙箱，避免污染本机 uploads。
func withTempUploads(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	uploads := config.UploadsDir()
	if err := os.MkdirAll(uploads, 0755); err != nil {
		t.Fatal(err)
	}
	return uploads
}

func TestIngestExcelAttachment_LocalCopy(t *testing.T) {
	uploads := withTempUploads(t)

	src := filepath.Join(t.TempDir(), "本地素材.pdf")
	if err := os.WriteFile(src, []byte("本地文件内容"), 0644); err != nil {
		t.Fatal(err)
	}
	got := ingestExcelAttachment(src)
	if got == "" {
		t.Fatal("本地存在文件应被复制进附件库，得到空串")
	}
	if got == src {
		t.Fatal("应返回附件库内的新路径，而非原始路径")
	}
	if !strings.HasPrefix(got, uploads) {
		t.Fatalf("返回路径应在附件库内，实际 %s（库=%s）", got, uploads)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("附件库内文件读不到：%v", err)
	}
	if string(data) != "本地文件内容" {
		t.Fatalf("复制内容不一致：%q", data)
	}
}

func TestIngestExcelAttachment_LocalMissingKeptRaw(t *testing.T) {
	withTempUploads(t)
	missing := filepath.Join(t.TempDir(), "已删除.pdf")
	got := ingestExcelAttachment(missing)
	if got != missing {
		t.Fatalf("本地不存在的路径应原样保留，期望 %q，实际 %q", missing, got)
	}
}

func TestIngestExcelAttachment_RelativePathKeptRaw(t *testing.T) {
	withTempUploads(t)
	rel := "相对路径/素材.pdf"
	got := ingestExcelAttachment(rel)
	if got != rel {
		t.Fatalf("相对路径应原样保留，期望 %q，实际 %q", rel, got)
	}
}

func TestIngestExcelAttachment_HTTPDownload(t *testing.T) {
	uploads := withTempUploads(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="远程.xlsx"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("远程文件内容"))
	}))
	defer srv.Close()

	got := ingestExcelAttachment(srv.URL)
	if got == "" {
		t.Fatal("可下载的 http 链接应被拉进附件库，得到空串")
	}
	if !strings.HasPrefix(got, uploads) {
		t.Fatalf("返回路径应在附件库内，实际 %s（库=%s）", got, uploads)
	}
	if filepath.Base(got) != "远程.xlsx" {
		t.Fatalf("文件名应取自 Content-Disposition，实际 %q", filepath.Base(got))
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("附件库内文件读不到：%v", err)
	}
	if string(data) != "远程文件内容" {
		t.Fatalf("下载内容不一致：%q", data)
	}
}

func TestIngestExcelAttachment_HTTP404KeptRaw(t *testing.T) {
	withTempUploads(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	got := ingestExcelAttachment(srv.URL)
	if got != srv.URL {
		t.Fatalf("下载失败的链接应原样保留，期望 %q，实际 %q", srv.URL, got)
	}
}

func TestIngestItemAttachments_CountsAndTypes(t *testing.T) {
	uploads := withTempUploads(t)

	// 一个真实本地文件 + 一个缺失路径 + 一个 http 链接（用本地 server 模拟）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("x"))
	}))
	defer srv.Close()

	real := filepath.Join(t.TempDir(), "a.pdf")
	_ = os.WriteFile(real, []byte("a"), 0644)
	missing := filepath.Join(t.TempDir(), "gone.pdf")

	item := map[string]any{
		"attachments": []string{real, missing, srv.URL, "   "}, // 末尾空串应被丢弃
	}
	ing, raw := ingestItemAttachments(item)
	if ing != 2 {
		t.Fatalf("成功落库应为 2（真实文件 + http），实际 %d", ing)
	}
	if raw != 1 {
		t.Fatalf("保留原样应为 1（缺失路径），实际 %d", raw)
	}
	kept, ok := item["attachments"].([]string)
	if !ok {
		t.Fatalf("结果应写回 []string，实际 %T", item["attachments"])
	}
	if len(kept) != 3 {
		t.Fatalf("保留条目数应为 3（2 落库 + 1 原样），实际 %d: %v", len(kept), kept)
	}
	// 落库的两个都应在附件库内；原样的那个不在
	inLib := 0
	for _, p := range kept {
		if strings.HasPrefix(p, uploads) {
			inLib++
		}
	}
	if inLib != 2 {
		t.Fatalf("应有 2 条在附件库内，实际 %d: %v", inLib, kept)
	}
}

func TestIngestItemAttachments_AllEmptyDeletesField(t *testing.T) {
	withTempUploads(t)
	item := map[string]any{"attachments": []string{"", "   ", ""}}
	ing, raw := ingestItemAttachments(item)
	if ing != 0 || raw != 0 {
		t.Fatalf("全空应无落库也无保留，实际 ing=%d raw=%d", ing, raw)
	}
	if _, ok := item["attachments"]; ok {
		t.Fatalf("全部为空时应删掉 attachments 字段，实际保留：%v", item["attachments"])
	}
}

// TestPresetImportIngestsAttachmentsE2E 端到端：用真实 Excel（含「本地绝对路径」和
// 「http 链接」两行）走完整导入链路，验证写进数据库的 attachments 已是附件库内路径。
func TestPresetImportIngestsAttachmentsE2E(t *testing.T) {
	uploads := withTempUploads(t)

	// 1. 一个真实存在的本地文件
	local := filepath.Join(t.TempDir(), "本地素材.pdf")
	if err := os.WriteFile(local, []byte("本地内容"), 0644); err != nil {
		t.Fatal(err)
	}
	// 2. 一个能下载的 http 链接
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="远程.xlsx"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("远程内容"))
	}))
	defer srv.Close()

	// 3. 构造 Excel：提问, 附件
	book := excelize.NewFile()
	defer book.Close()
	sheet := book.GetSheetName(0)
	header := []string{"提问", "附件"}
	for c, v := range header {
		cell, _ := excelize.CoordinatesToCellName(c+1, 1)
		if err := book.SetCellValue(sheet, cell, v); err != nil {
			t.Fatal(err)
		}
	}
	rows := [][]string{
		{"本地附件用例", local},
		{"远程附件用例", srv.URL},
	}
	for r, row := range rows {
		for c, v := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
			if err := book.SetCellValue(sheet, cell, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	buf, err := book.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"name":        "ingest.xlsx",
		"data_base64": base64.StdEncoding.EncodeToString(buf.Bytes()),
	})

	// 4. 走真实 handler（非 parse_only）
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec, req)

	var resp struct {
		OK                  bool     `json:"ok"`
		Added               int      `json:"added"`
		Ingested            int      `json:"attachments_ingested"`
		KeptRaw             int      `json:"attachments_kept_raw"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || !resp.OK {
		t.Fatalf("导入失败 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if resp.Added != 2 {
		t.Errorf("应导入 2 行，实际 %d", resp.Added)
	}
	if resp.Ingested != 2 {
		t.Errorf("应落库 2 个附件，实际 %d", resp.Ingested)
	}
	if resp.KeptRaw != 0 {
		t.Errorf("不应有保留原样的附件，实际 %d", resp.KeptRaw)
	}

	// 5. 直接读库验证 attachments 已是附件库内路径
	db, err := testcasedb.GetConnection("")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows2, err := db.Query("SELECT prompt, attachments FROM testcases ORDER BY seq")
	if err != nil {
		t.Fatal(err)
	}
	defer rows2.Close()
	seen := map[string][]string{}
	for rows2.Next() {
		var prompt, attsJSON string
		if err := rows2.Scan(&prompt, &attsJSON); err != nil {
			t.Fatal(err)
		}
		var atts []string
		_ = json.Unmarshal([]byte(attsJSON), &atts)
		seen[prompt] = atts
	}
	for _, want := range []string{"本地附件用例", "远程附件用例"} {
		atts, ok := seen[want]
		if !ok {
			t.Fatalf("库里缺用例 %q", want)
		}
		if len(atts) != 1 {
			t.Fatalf("用例 %q 应有 1 个附件，实际 %v", want, atts)
		}
		if !strings.HasPrefix(atts[0], uploads) {
			t.Fatalf("用例 %q 的附件应在附件库内，实际 %q（库=%s）", want, atts[0], uploads)
		}
		if got := filepath.Base(atts[0]); want == "远程附件用例" && got != "远程.xlsx" {
			t.Errorf("远程附件文件名应取自 Content-Disposition，实际 %q", got)
		}
	}
}
