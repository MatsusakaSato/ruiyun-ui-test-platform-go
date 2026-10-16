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

func TestResolveAttachment_LocalCopy(t *testing.T) {
	uploads := withTempUploads(t)

	src := filepath.Join(t.TempDir(), "本地素材.pdf")
	if err := os.WriteFile(src, []byte("本地文件内容"), 0644); err != nil {
		t.Fatal(err)
	}
	got, reason := resolveAttachment(src)
	if got == "" {
		t.Fatalf("本地存在文件应被复制进附件库，得到空串（reason=%q）", reason)
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

func TestResolveAttachment_LocalMissing(t *testing.T) {
	withTempUploads(t)
	missing := filepath.Join(t.TempDir(), "已删除.pdf")
	got, reason := resolveAttachment(missing)
	if got != "" || reason == "" {
		t.Fatalf("本地不存在的路径应判为无效并给出原因，实际 path=%q reason=%q", got, reason)
	}
}

func TestResolveAttachment_RelativePath(t *testing.T) {
	withTempUploads(t)
	rel := "相对路径/素材.pdf"
	got, reason := resolveAttachment(rel)
	if got != "" || reason == "" {
		t.Fatalf("相对路径应判为无效并给出原因，实际 path=%q reason=%q", got, reason)
	}
}

func TestResolveAttachment_HTTPDownload(t *testing.T) {
	uploads := withTempUploads(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="远程.xlsx"`)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("远程文件内容"))
	}))
	defer srv.Close()

	got, reason := resolveAttachment(srv.URL)
	if got == "" {
		t.Fatalf("可下载的 http 链接应被拉进附件库，得到空串（reason=%q）", reason)
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

func TestResolveAttachment_HTTP404(t *testing.T) {
	withTempUploads(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer srv.Close()

	got, reason := resolveAttachment(srv.URL)
	if got != "" {
		t.Fatalf("下载失败的链接应判为无效，实际 path=%q", got)
	}
	if !strings.Contains(reason, "404") {
		t.Errorf("原因里应带上 HTTP 状态，实际 %q", reason)
	}
}

func TestIngestItemAttachments_DropsInvalid(t *testing.T) {
	uploads := withTempUploads(t)

	// 一个真实本地文件 + 一个缺失路径 + 一个可下载的 http 链接 + 一个空串
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("x"))
	}))
	defer srv.Close()

	real := filepath.Join(t.TempDir(), "a.pdf")
	_ = os.WriteFile(real, []byte("a"), 0644)
	missing := filepath.Join(t.TempDir(), "gone.pdf")

	item := map[string]any{
		"name":        "第 3 行用例",
		"attachments": []string{real, missing, srv.URL, "   "}, // 末尾空串应被丢弃
	}
	ing, dropped, failed := ingestItemAttachmentsEx(item, 3)
	if ing != 2 {
		t.Fatalf("成功落库应为 2（真实文件 + http），实际 %d", ing)
	}
	if dropped != 1 {
		t.Fatalf("应剔除 1 个无效附件（缺失路径），实际 %d", dropped)
	}
	if len(failed) != 1 || failed[0]["value"] != missing {
		t.Fatalf("应回报缺失路径，实际 %v", failed)
	}
	if failed[0]["row"] != 3 {
		t.Errorf("回报应带上行号 3，实际 %v", failed[0]["row"])
	}

	kept, ok := item["attachments"].([]string)
	if !ok {
		t.Fatalf("结果应写回 []string，实际 %T", item["attachments"])
	}
	if len(kept) != 2 {
		t.Fatalf("保留条目数应为 2（仅落库的），实际 %d: %v", len(kept), kept)
	}
	for _, p := range kept {
		if !strings.HasPrefix(p, uploads) {
			t.Fatalf("保留下来的都应在附件库内，实际 %v", kept)
		}
	}
}

// 附件全部无效时应剔除字段并把「需要附件」标记回落，避免留下挂空引用的脏数据。
func TestIngestItemAttachments_AllInvalidClearsLabel(t *testing.T) {
	withTempUploads(t)
	item := map[string]any{
		"attachments": []string{"素材/不存在.docx"},
		"labels":      map[string]any{"attachment": true},
	}
	ing, dropped, failed := ingestItemAttachmentsEx(item, 1)
	if ing != 0 || dropped != 1 || len(failed) != 1 {
		t.Fatalf("无效附件应全部剔除，实际 ing=%d dropped=%d failed=%v", ing, dropped, failed)
	}
	if _, ok := item["attachments"]; ok {
		t.Fatalf("全部无效时应删掉 attachments 字段，实际保留：%v", item["attachments"])
	}
	labels, _ := item["labels"].(map[string]any)
	if labels == nil || labels["attachment"] != false {
		t.Fatalf("附件清空后标记应回落为 false，实际 %v", item["labels"])
	}
}

func TestIngestItemAttachments_AllEmptyDeletesField(t *testing.T) {
	withTempUploads(t)
	item := map[string]any{"attachments": []string{"", "   ", ""}}
	ing, dropped, failed := ingestItemAttachmentsEx(item, 1)
	if ing != 0 || dropped != 0 || len(failed) != 0 {
		t.Fatalf("全空应无落库也无剔除，实际 ing=%d dropped=%d failed=%v", ing, dropped, failed)
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
		OK       bool `json:"ok"`
		Added    int  `json:"added"`
		Ingested int  `json:"attachments_ingested"`
		Dropped  int  `json:"attachments_dropped"`
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
	if resp.Dropped != 0 {
		t.Errorf("不应有被剔除的附件，实际 %d", resp.Dropped)
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
		// prompt 列存 JSON 数组，按第 1 轮建索引
		var turns []string
		_ = json.Unmarshal([]byte(prompt), &turns)
		key := ""
		if len(turns) > 0 {
			key = turns[0]
		}
		seen[key] = atts
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

// 无效链接/无效路径走完整导入链路：用例照常入库，无效附件被剔除并回报，
// 且「需要附件」标记随之回落 —— 不给运行时留「附件不存在」的坑。
func TestPresetImportDropsInvalidAttachments(t *testing.T) {
	withTempUploads(t)

	book := excelize.NewFile()
	defer book.Close()
	sheet := book.GetSheetName(0)
	for c, v := range []string{"提问", "附件"} {
		cell, _ := excelize.CoordinatesToCellName(c+1, 1)
		if err := book.SetCellValue(sheet, cell, v); err != nil {
			t.Fatal(err)
		}
	}
	rows := [][]string{
		{"带坏链接的用例", "http://127.0.0.1:1/not-exist.pdf"}, // 端口 1，必然连不上
		{"带相对路径的用例", "素材/不存在.docx"},
	}
	for r, row := range rows {
		for c, v := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
			if err := book.SetCellValue(sheet, cell, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	buf, _ := book.WriteToBuffer()
	body, _ := json.Marshal(map[string]any{
		"name":        "bad-att.xlsx",
		"data_base64": base64.StdEncoding.EncodeToString(buf.Bytes()),
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec, req)

	var resp struct {
		OK      bool `json:"ok"`
		Added   int  `json:"added"`
		Dropped int  `json:"attachments_dropped"`
		Invalid []struct {
			Row    int    `json:"row"`
			Value  string `json:"value"`
			Reason string `json:"reason"`
		} `json:"invalid_attachments"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || !resp.OK {
		t.Fatalf("用例应照常入库 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if resp.Added != 2 {
		t.Errorf("应导入 2 条用例，实际 %d", resp.Added)
	}
	if resp.Dropped != 2 || len(resp.Invalid) != 2 {
		t.Fatalf("应剔除并回报 2 个无效附件，实际 dropped=%d invalid=%v", resp.Dropped, resp.Invalid)
	}
	for _, iv := range resp.Invalid {
		if iv.Row < 1 || iv.Value == "" || iv.Reason == "" {
			t.Errorf("回报应含行号/原值/原因，实际 %+v", iv)
		}
	}

	// 库里两条用例都不应再挂附件
	db, err := testcasedb.GetConnection("")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rowsDB, err := db.Query("SELECT prompt, attachment, attachments FROM testcases")
	if err != nil {
		t.Fatal(err)
	}
	defer rowsDB.Close()
	n := 0
	for rowsDB.Next() {
		var prompt, attsJSON string
		var att int
		if err := rowsDB.Scan(&prompt, &att, &attsJSON); err != nil {
			t.Fatal(err)
		}
		n++
		if att != 0 {
			t.Errorf("用例 %q 的「需要附件」标记应为 0，实际 %d", prompt, att)
		}
		var atts []string
		if attsJSON != "" {
			_ = json.Unmarshal([]byte(attsJSON), &atts)
		}
		if len(atts) != 0 {
			t.Errorf("用例 %q 不应再挂附件，实际 %v", prompt, atts)
		}
	}
	if n != 2 {
		t.Errorf("库中应有 2 条用例，实际 %d", n)
	}
}

// 不符合模板的文件在预览阶段就被判定为无效，并在导入时被拒绝（不再让用户手动改列）。
func TestPresetImportRejectsNonTemplate(t *testing.T) {
	book := excelize.NewFile()
	defer book.Close()
	sheet := book.GetSheetName(0)
	// 只有数据行、没有表头行（长句含标点，不会被当表头）
	book.SetCellValue(sheet, "A1", "把这段很长的内容写成一篇完整的教学总结，越详细越好。")
	if err := book.SetCellValue(sheet, "A2", "再写一份班会主持稿，注意语气要自然。"); err != nil {
		t.Fatal(err)
	}
	buf, _ := book.WriteToBuffer()
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	// 1. 预览：valid_template=false 且带错误说明
	previewBody, _ := json.Marshal(map[string]any{
		"name": "no-header.xlsx", "data_base64": encoded, "parse_only": true,
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-import", bytes.NewReader(previewBody))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec, req)

	var preview struct {
		OK            bool   `json:"ok"`
		ValidTemplate bool   `json:"valid_template"`
		TemplateErr   string `json:"template_error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if !preview.OK {
		t.Fatalf("预览请求本身应成功，body=%s", rec.Body.String())
	}
	if preview.ValidTemplate {
		t.Fatalf("无表头文件不应判为符合模板，body=%s", rec.Body.String())
	}
	if !strings.Contains(preview.TemplateErr, "模板") {
		t.Errorf("应给出模板相关错误说明，实际 %q", preview.TemplateErr)
	}

	// 2. 强行导入：应被拒绝
	importBody, _ := json.Marshal(map[string]any{
		"name": "no-header.xlsx", "data_base64": encoded,
	})
	req2 := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-import", bytes.NewReader(importBody))
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec2, req2)

	var imported struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &imported); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec2.Body.String())
	}
	if rec2.Code != http.StatusBadRequest || imported.OK {
		t.Fatalf("不符合模板的文件应被拒绝，status=%d body=%s", rec2.Code, rec2.Body.String())
	}
	if imported.Message == "" {
		t.Errorf("拒绝时应给出原因")
	}
}

// 有表头但认不出提问列的文件同样不算符合模板（不能靠「猜最长的一列」放行）。
func TestPresetImportRejectsTemplateWithoutPromptColumn(t *testing.T) {
	book := excelize.NewFile()
	defer book.Close()
	sheet := book.GetSheetName(0)
	book.SetCellValue(sheet, "A1", "姓名")
	book.SetCellValue(sheet, "B1", "年龄")
	book.SetCellValue(sheet, "A2", "张三")
	if err := book.SetCellValue(sheet, "B2", "18"); err != nil {
		t.Fatal(err)
	}
	buf, _ := book.WriteToBuffer()
	body, _ := json.Marshal(map[string]any{
		"name":        "people.xlsx",
		"data_base64": base64.StdEncoding.EncodeToString(buf.Bytes()),
		"parse_only":  true,
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec, req)

	var resp struct {
		OK            bool   `json:"ok"`
		ValidTemplate bool   `json:"valid_template"`
		TemplateErr   string `json:"template_error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if !resp.OK || resp.ValidTemplate {
		t.Fatalf("无提问列的文件不应判为符合模板，body=%s", rec.Body.String())
	}
	if !strings.Contains(resp.TemplateErr, "Query内容") && !strings.Contains(resp.TemplateErr, "提问") {
		t.Errorf("错误说明应指出缺少提问列，实际 %q", resp.TemplateErr)
	}
}

// 平台自带的 resource/template.xlsx 必须能通过模板校验，并识别出 Query内容 列 ——
// 用户拿到的模板与被校验的结构必须一致。
func TestPresetImportAcceptsShippedTemplate(t *testing.T) {
	tmpl := filepath.Join("..", "..", "resource", "template.xlsx")
	data, err := os.ReadFile(tmpl)
	if err != nil {
		t.Skipf("跳过：读不到随包模板 %s（%v）", tmpl, err)
	}
	body, _ := json.Marshal(map[string]any{
		"name":        "template.xlsx",
		"data_base64": base64.StdEncoding.EncodeToString(data),
		"parse_only":  true,
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec, req)

	var resp struct {
		OK            bool     `json:"ok"`
		ValidTemplate bool     `json:"valid_template"`
		PromptCol     *int     `json:"prompt_col"`
		AttachCol     *int     `json:"attachment_col"`
		DiffCol       *int     `json:"difficulty_col"`
		Total         int      `json:"total"`
		Header        []string `json:"header"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if !resp.OK || !resp.ValidTemplate {
		t.Fatalf("随包模板应通过校验，body=%s", rec.Body.String())
	}
	if resp.Total != 1 {
		t.Errorf("模板示例行应解析为 1 条，实际 %d", resp.Total)
	}
	if resp.PromptCol == nil || *resp.PromptCol >= len(resp.Header) || resp.Header[*resp.PromptCol] != "Query内容" {
		t.Errorf("应把 Query内容 识别为提问列，header=%v prompt_col=%v", resp.Header, resp.PromptCol)
	}
	if resp.AttachCol == nil || *resp.AttachCol >= len(resp.Header) || resp.Header[*resp.AttachCol] != "附件" {
		t.Errorf("应识别出 附件 列，header=%v attachment_col=%v", resp.Header, resp.AttachCol)
	}
	if resp.DiffCol == nil || *resp.DiffCol >= len(resp.Header) || resp.Header[*resp.DiffCol] != "难度" {
		t.Errorf("应识别出 难度 列，header=%v difficulty_col=%v", resp.Header, resp.DiffCol)
	}
}

// 难度是真实标签：导入时从「难度」列读出来，落库后出现在 labels.difficulty。
func TestPresetImportReadsDifficultyColumn(t *testing.T) {
	withTempUploads(t)

	book := excelize.NewFile()
	defer book.Close()
	sheet := book.GetSheetName(0)
	for c, v := range []string{"场景", "难度", "Query内容"} {
		cell, _ := excelize.CoordinatesToCellName(c+1, 1)
		if err := book.SetCellValue(sheet, cell, v); err != nil {
			t.Fatal(err)
		}
	}
	for r, row := range [][]string{
		{"备课", " 中 ", "请帮我重构教学过程"}, // 带空白，应被 TrimSpace
		{"备课", "难", "请帮我写一份试卷讲评"},
	} {
		for c, v := range row {
			cell, _ := excelize.CoordinatesToCellName(c+1, r+2)
			if err := book.SetCellValue(sheet, cell, v); err != nil {
				t.Fatal(err)
			}
		}
	}
	buf, _ := book.WriteToBuffer()
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	// 1. 预览：difficulty_col 指向「难度」列，预览 item 里带上该行的难度
	previewBody, _ := json.Marshal(map[string]any{
		"name": "difficulty.xlsx", "data_base64": encoded, "parse_only": true,
	})
	preq := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-import", bytes.NewReader(previewBody))
	preq.Header.Set("Content-Type", "application/json")
	prec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(prec, preq)

	var preview struct {
		OK      bool     `json:"ok"`
		DiffCol *int     `json:"difficulty_col"`
		Header  []string `json:"header"`
		Items   []struct {
			Difficulty string `json:"difficulty"`
		} `json:"items"`
	}
	if err := json.Unmarshal(prec.Body.Bytes(), &preview); err != nil {
		t.Fatalf("decode preview: %v; body=%s", err, prec.Body.String())
	}
	if !preview.OK || preview.DiffCol == nil || preview.Header[*preview.DiffCol] != "难度" {
		t.Fatalf("预览应识别出 难度 列，body=%s", prec.Body.String())
	}
	if len(preview.Items) != 2 || preview.Items[0].Difficulty != "中" || preview.Items[1].Difficulty != "难" {
		t.Fatalf("预览 item 应带上难度，实际 %+v", preview.Items)
	}

	// 2. 真导入：难度落库并由 labels.difficulty 回传
	body, _ := json.Marshal(map[string]any{
		"name":        "difficulty.xlsx",
		"data_base64": encoded,
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec, req)

	var resp struct {
		OK    bool `json:"ok"`
		Added int  `json:"added"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || !resp.OK {
		t.Fatalf("导入失败 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if resp.Added != 2 {
		t.Fatalf("应导入 2 行，实际 %d（body=%s）", resp.Added, rec.Body.String())
	}

	cases, err := testcasedb.GetPresetCases("")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, c := range cases {
		labels, _ := c["labels"].(map[string]any)
		// prompt 自多轮改造起是数组，这里按第 1 轮建索引
		turns, _ := c["prompt"].([]string)
		if len(turns) == 0 {
			continue
		}
		got[turns[0]] = labels["difficulty"]
	}
	if got["请帮我重构教学过程"] != "中" {
		t.Errorf("难度列的值应落到 labels.difficulty 且已 TrimSpace，实际 %v", got["请帮我重构教学过程"])
	}
	if got["请帮我写一份试卷讲评"] != "难" {
		t.Errorf("难度列的值应落到 labels.difficulty，实际 %v", got["请帮我写一份试卷讲评"])
	}
}

// 表里写着「编号」「用例名称」也不该影响编号与名称：两者一律由服务端生成。
func TestPresetImportIgnoresNumberAndNameColumns(t *testing.T) {
	withTempUploads(t)

	book := excelize.NewFile()
	defer book.Close()
	sheet := book.GetSheetName(0)
	for c, v := range []string{"编号", "用例名称", "Query内容"} {
		cell, _ := excelize.CoordinatesToCellName(c+1, 1)
		if err := book.SetCellValue(sheet, cell, v); err != nil {
			t.Fatal(err)
		}
	}
	for c, v := range []string{"99", "我自己的名字", "写一份班会主持稿"} {
		cell, _ := excelize.CoordinatesToCellName(c+1, 2)
		if err := book.SetCellValue(sheet, cell, v); err != nil {
			t.Fatal(err)
		}
	}
	buf, _ := book.WriteToBuffer()
	body, _ := json.Marshal(map[string]any{
		"name":        "numbered.xlsx",
		"data_base64": base64.StdEncoding.EncodeToString(buf.Bytes()),
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec, req)

	var resp struct {
		OK    bool `json:"ok"`
		Added int  `json:"added"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK || !resp.OK || resp.Added != 1 {
		t.Fatalf("应导入 1 行 status=%d body=%s", rec.Code, rec.Body.String())
	}

	cases, err := testcasedb.GetPresetCases("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 1 {
		t.Fatalf("库中应有 1 条用例，实际 %d", len(cases))
	}
	c := cases[0]
	if c["id"] != "CASE-001" {
		t.Errorf("编号应由服务端生成 CASE-001，不被「编号」列带偏，实际 %v", c["id"])
	}
	if c["name"] != "导入-001" {
		t.Errorf("名称应由服务端生成 导入-001，不被「用例名称」列带入，实际 %v", c["name"])
	}
}
