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
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"

	"ruiyun-ui-test-platform-go/internal/config"
	"ruiyun-ui-test-platform-go/internal/testcasedb"
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

// 用例规范化统一入口 ingestItemAttachmentsEx：附件逐个落库校验，
// 只有真实存在的文件留下，其余剔除并回报。
func TestIngestAttachmentsForCaseDropsInvalid(t *testing.T) {
	uploads := withTempUploads(t)

	exist := filepath.Join(uploads, "真实附件.pdf")
	if err := os.WriteFile(exist, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	sub := filepath.Join(dir, "子目录")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "已删除.docx")

	// 不存在的路径与目录都要剔除，只剩真实文件；空白项静默丢弃不计入回报
	item := map[string]any{
		"name":        "第 1 行用例",
		"attachments": []any{exist, missing, "   ", sub},
	}
	ing, dropped, failed := ingestItemAttachmentsEx(item, 1)
	if ing != 1 {
		t.Errorf("只有真实存在的文件应落库，实际 ingested=%d", ing)
	}
	if dropped != 2 {
		t.Errorf("期望剔除 2 个（缺失文件 + 目录），实际 %d: %v", dropped, failed)
	}
	if len(failed) != 2 {
		t.Fatalf("期望回报 2 条，实际 %v", failed)
	}
	wantValues := map[string]bool{missing: true, sub: true}
	for _, f := range failed {
		val, _ := f["value"].(string)
		if !wantValues[val] {
			t.Errorf("不应回报 %q（空白项应静默丢弃），实际回报 %v", val, failed)
		}
		if f["row"] != 1 || f["name"] != "第 1 行用例" || f["reason"] == "" {
			t.Errorf("回报应含行号/用例名/非空原因，实际 %+v", f)
		}
	}
	kept, ok := item["attachments"].([]string)
	if !ok {
		t.Fatalf("结果应写回 []string，实际 %T", item["attachments"])
	}
	if len(kept) != 1 || kept[0] != exist {
		t.Errorf("期望只保留 %s，实际 %v", exist, kept)
	}
}

// 已在附件库内的路径原样保留：不重复复制，返回值与输入完全一致。
func TestIngestAttachmentsForCaseKeepsUploadsPath(t *testing.T) {
	uploads := withTempUploads(t)

	inLib := filepath.Join(uploads, "已入库的课件.pptx")
	if err := os.WriteFile(inLib, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	item := map[string]any{"attachments": []string{inLib}}
	ing, dropped, failed := ingestItemAttachmentsEx(item, 1)
	if ing != 1 || dropped != 0 || len(failed) != 0 {
		t.Fatalf("库内文件应原样保留，实际 ing=%d dropped=%d failed=%v", ing, dropped, failed)
	}
	kept, _ := item["attachments"].([]string)
	if len(kept) != 1 || kept[0] != inLib {
		t.Fatalf("库内路径不应被复制成新副本，实际 %v（原 %s）", kept, inLib)
	}
	entries, err := os.ReadDir(uploads)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != filepath.Base(inLib) {
		t.Errorf("不应在附件库里新建副本，实际目录内容 %v", entries)
	}
}

// 难度是标签：手填新增的接口响应里要回传 labels.difficulty；调用方传的 id/name 一律被忽略。
func TestPresetCasesPostReturnsDifficultyLabel(t *testing.T) {
	withTempUploads(t)

	body, _ := json.Marshal(map[string]any{
		"action": "add",
		"item": map[string]any{
			"id":     "MY-OWN-001",
			"name":   "我自己起的名字",
			"prompt": "帮我写一份教学反思",
			"labels": map[string]any{"scene": "备课", "difficulty": "难"},
		},
	})
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-cases", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec, req)

	var resp struct {
		OK   bool `json:"ok"`
		Case struct {
			ID     string         `json:"id"`
			Name   string         `json:"name"`
			Labels map[string]any `json:"labels"`
		} `json:"case"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if !resp.OK {
		t.Fatalf("新增应成功 status=%d body=%s", rec.Code, rec.Body.String())
	}
	if resp.Case.ID != "CASE-001" {
		t.Errorf("编号应服务端生成为 CASE-001，实际 %q", resp.Case.ID)
	}
	if resp.Case.Name != "备课-001" {
		t.Errorf("名称应服务端生成为 备课-001，实际 %q", resp.Case.Name)
	}
	if resp.Case.Labels["difficulty"] != "难" {
		t.Errorf("响应里应回传 labels.difficulty=难，实际 %v", resp.Case.Labels)
	}
	if resp.Case.Labels["scene"] != "备课" {
		t.Errorf("响应里应回传 labels.scene=备课，实际 %v", resp.Case.Labels)
	}
}

// GET /api/preset-cases 的难度筛选：查询参数是 difficulties（多选逗号分隔），
// 响应里以 difficulties 返回可选难度列表（取代原来的 targets）。
func TestPresetCasesGetFiltersByDifficulty(t *testing.T) {
	withTempUploads(t)

	seed := func(prompt, scene, difficulty string, attachment bool) {
		t.Helper()
		labels := map[string]any{"scene": scene}
		if difficulty != "" {
			labels["difficulty"] = difficulty
		}
		if attachment {
			labels["attachment"] = true
		}
		if ok, msg, _ := testcasedb.AddPresetCase(map[string]any{
			"prompt": prompt,
			"labels": labels,
		}, ""); !ok {
			t.Fatalf("准备用例 %q 失败: %s", prompt, msg)
		}
	}
	seed("备课易题", "备课", "易", false)
	seed("备课难题", "备课", "难", false)
	seed("评测易题带附件", "评测", "易", true)
	seed("没有难度的题", "备课", "", false)

	get := func(query string) (map[string]any, []string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/preset-cases"+query, nil)
		rec := httptest.NewRecorder()
		NewServer(false, "").Handler().ServeHTTP(rec, req)

		var raw map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var prompts []string
		for _, c := range raw["cases"].([]any) {
			prompts = append(prompts, c.(map[string]any)["prompt"].(string))
		}
		return raw, prompts
	}

	// 1. 多选难度取并集
	raw, prompts := get("?difficulties=易,难")
	if len(prompts) != 3 {
		t.Errorf("difficulties=易,难 应命中 3 条，实际 %v", prompts)
	}
	if _, has := raw["targets"]; has {
		t.Errorf("响应里不应再有 targets 字段，实际 %v", raw["targets"])
	}
	// 选项列表来自库内实际值（升序），空难度不出现
	diffs, ok := raw["difficulties"].([]any)
	if !ok {
		t.Fatalf("difficulties 应是数组，实际 %T", raw["difficulties"])
	}
	if got := strings.Join(toStringList(diffs), ","); got != "易,难" {
		t.Errorf("difficulties 选项应来自库内实际值（易,难，不含空难度），实际 %q", got)
	}

	// 2. 与场景组合（同一难度下按场景收窄）
	if _, prompts := get("?difficulties=易&scene=评测"); len(prompts) != 1 || prompts[0] != "评测易题带附件" {
		t.Errorf("difficulties=易&scene=评测 应只剩 1 条，实际 %v", prompts)
	}

	// 3. 与附件筛选组合
	if _, prompts := get("?difficulties=易&attachment=yes"); len(prompts) != 1 || prompts[0] != "评测易题带附件" {
		t.Errorf("difficulties=易&attachment=yes 应只剩 1 条，实际 %v", prompts)
	}

	// 4. 空难度的行既不进选项、也不被任何一档命中
	if _, prompts := get("?difficulties="); len(prompts) != 4 {
		t.Errorf("未指定难度时不过滤，应返回全部 4 条，实际 %v", prompts)
	}
}

// 编辑用例走 HTTP：改写后编号不变、名称按新场景重算、标签跟着变；缺 id 直接 400，不会被当成新增。
func TestPresetCasesPostUpdatesCase(t *testing.T) {
	withTempUploads(t)

	post := func(body map[string]any) (int, map[string]any) {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-cases", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		NewServer(false, "").Handler().ServeHTTP(rec, req)
		var out map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
		}
		return rec.Code, out
	}

	_, add := post(map[string]any{
		"action": "add",
		"prompt": "帮我写一份教学反思",
		"labels": map[string]any{"scene": "备课", "difficulty": "易", "tags": []string{"回归"}},
	})
	if add["ok"] != true {
		t.Fatalf("新增应成功: %v", add)
	}
	added, _ := add["case"].(map[string]any)
	id, _ := added["id"].(string)
	if id == "" {
		t.Fatalf("新增应回传编号: %v", add)
	}
	// 新增路径也要把自定义标签原样回传
	if al, _ := added["labels"].(map[string]any); al != nil {
		tags, _ := al["tags"].([]any)
		if len(tags) != 1 || tags[0] != "回归" {
			t.Errorf("新增应回传 labels.tags=[回归]，实际 %v", added["labels"])
		}
	} else {
		t.Errorf("新增用例应带 labels，实际 %v", added)
	}

	// 1. 正常编辑（平铺提交，id 在顶层）
	code, up := post(map[string]any{
		"action": "update",
		"id":     id,
		"prompt": "帮我写一份教学反思（改）",
		"labels": map[string]any{"scene": "评测", "difficulty": "难", "tags": []string{"冒烟", "回归"}},
	})
	if code != http.StatusOK || up["ok"] != true {
		t.Fatalf("编辑应成功 status=%d body=%v", code, up)
	}
	uc, _ := up["case"].(map[string]any)
	if uc["id"] != id {
		t.Errorf("编号不应变：%v -> %v", id, uc["id"])
	}
	if uc["name"] != "评测-001" {
		t.Errorf("名称应按新场景与原序号重算为 评测-001，实际 %v", uc["name"])
	}
	if ul, _ := uc["labels"].(map[string]any); ul != nil {
		tags, _ := ul["tags"].([]any)
		if len(tags) != 2 || tags[0] != "冒烟" || tags[1] != "回归" {
			t.Errorf("编辑应回传改写后的标签 [冒烟 回归]，实际 %v", uc["labels"])
		}
		if ul["scene"] != "评测" || ul["difficulty"] != "难" {
			t.Errorf("编辑应回传新的场景与难度，实际 %v", uc["labels"])
		}
	} else {
		t.Errorf("编辑应回传 labels，实际 %v", uc)
	}

	// 2. 落库核对：GET 回来的是改写后的内容，且仍只有一条（没有多出一条新用例）
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/preset-cases", nil)
	rec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec, req)
	var list struct {
		Total int `json:"total"`
		Cases []struct {
			ID     string         `json:"id"`
			Prompt string         `json:"prompt"`
			Labels map[string]any `json:"labels"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if list.Total != 1 || len(list.Cases) != 1 {
		t.Fatalf("编辑不应新增用例，期望库里 1 条，实际 total=%d cases=%d", list.Total, len(list.Cases))
	}
	if list.Cases[0].ID != id || list.Cases[0].Prompt != "帮我写一份教学反思（改）" {
		t.Errorf("库里应是改写后的那一条，实际 %+v", list.Cases[0])
	}

	// 3. 缺 id：400 且不能退化成「新增」
	codeNoID, noID := post(map[string]any{"action": "update", "prompt": "缺编号的编辑"})
	if codeNoID != http.StatusBadRequest || noID["ok"] != false {
		t.Errorf("缺 id 应 400，实际 status=%d body=%v", codeNoID, noID)
	}
	req2 := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/preset-cases", nil)
	rec2 := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec2, req2)
	var after struct {
		Total int `json:"total"`
	}
	if err := json.Unmarshal(rec2.Body.Bytes(), &after); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if after.Total != 1 {
		t.Errorf("缺 id 的编辑不应写出新用例，期望仍是 1 条，实际 %d", after.Total)
	}
}

// GET /api/preset-cases 的标签筛选：参数 tags（多选逗号分隔），响应以 tags 返回可选标签列表。
func TestPresetCasesGetFiltersByTags(t *testing.T) {
	withTempUploads(t)

	seed := func(prompt string, tags []string) {
		t.Helper()
		labels := map[string]any{}
		if len(tags) > 0 {
			labels["tags"] = tags
		}
		body, _ := json.Marshal(map[string]any{"action": "add", "prompt": prompt, "labels": labels})
		req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-cases", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		NewServer(false, "").Handler().ServeHTTP(rec, req)
		if !strings.Contains(rec.Body.String(), `"ok":true`) {
			t.Fatalf("准备用例 %q 失败: %s", prompt, rec.Body.String())
		}
	}
	seed("带回归的题", []string{"回归", "P0"})
	seed("带冒烟的题", []string{"冒烟"})
	seed("回归加冒烟", []string{"回归", "冒烟"})
	seed("没标签的题", nil)

	get := func(query string) ([]string, []string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1/api/preset-cases"+query, nil)
		rec := httptest.NewRecorder()
		NewServer(false, "").Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
		var raw map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
			t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
		}
		var prompts []string
		for _, c := range raw["cases"].([]any) {
			prompts = append(prompts, c.(map[string]any)["prompt"].(string))
		}
		tags := toStringList(raw["tags"])
		return prompts, tags
	}

	// 1. 单选标签
	prompts, tags := get("?tags=回归")
	if len(prompts) != 2 {
		t.Errorf("tags=回归 应命中 2 条，实际 %v", prompts)
	}
	// 选项列表来自库内实际值（去重升序），没标签的用例不贡献选项
	if got := strings.Join(tags, ","); got != "P0,冒烟,回归" {
		t.Errorf("tags 选项应来自库内实际值 [P0,冒烟,回归]，实际 %q", got)
	}

	// 2. 多选取交集：两个标签都带的才留下
	prompts, _ = get("?tags=回归,冒烟")
	if len(prompts) != 1 || prompts[0] != "回归加冒烟" {
		t.Errorf("tags=回归,冒烟 取交集应只剩「回归加冒烟」，实际 %v", prompts)
	}

	// 3. 交集里的某个标签没有任何用例同时满足 -> 空集
	prompts, _ = get("?tags=回归,P0,冒烟")
	if len(prompts) != 0 {
		t.Errorf("没有用例同时带 回归+P0+冒烟，应返回空集，实际 %v", prompts)
	}

	// 4. 与关键词组合（关键词仍匹配编号 / 名称 / 提问）
	prompts, _ = get("?tags=回归&keyword=CASE-003")
	if len(prompts) != 1 || prompts[0] != "回归加冒烟" {
		t.Errorf("tags=回归&keyword=CASE-003 应只剩 1 条，实际 %v", prompts)
	}

	// 5. 与附件标记组合：这几条都没挂附件，attachment=yes 应为空
	prompts, _ = get("?tags=回归&attachment=yes")
	if len(prompts) != 0 {
		t.Errorf("tags=回归&attachment=yes 应为空，实际 %v", prompts)
	}

	// 6. 选了不存在的标签：空结果，不是报错
	prompts, _ = get("?tags=不存在的标签")
	if len(prompts) != 0 {
		t.Errorf("选不存在的标签应返回空集，实际 %v", prompts)
	}

	// 7. 不传标签即不过滤
	prompts, _ = get("")
	if len(prompts) != 4 {
		t.Errorf("不传标签应返回全部 4 条，实际 %v", prompts)
	}
}
