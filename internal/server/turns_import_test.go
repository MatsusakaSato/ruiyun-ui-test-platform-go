package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xuri/excelize/v2"

	"ruiyun-ui-test-platform-go/internal/testcasedb"
)

// importRows 用真实 handler 导入一份 Excel，返回响应字段
func importRows(t *testing.T, header []string, rows [][]string, parseOnly bool) map[string]any {
	t.Helper()

	book := excelize.NewFile()
	defer book.Close()
	sheet := book.GetSheetName(0)
	for c, v := range header {
		cell, _ := excelize.CoordinatesToCellName(c+1, 1)
		if err := book.SetCellValue(sheet, cell, v); err != nil {
			t.Fatal(err)
		}
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
	payload := map[string]any{
		"name":        "turns.xlsx",
		"data_base64": base64.StdEncoding.EncodeToString(buf.Bytes()),
	}
	if parseOnly {
		payload["parse_only"] = true
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/preset-import", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	NewServer(false, "").Handler().ServeHTTP(rec, req)

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("导入失败 status=%d body=%s", rec.Code, rec.Body.String())
	}
	return resp
}

// turnArrays 从响应里取出每条的 prompt 轮次数组
func turnArrays(t *testing.T, resp map[string]any) [][]string {
	t.Helper()
	raw, _ := resp["items"].([]any)
	out := make([][]string, 0, len(raw))
	for _, it := range raw {
		m, _ := it.(map[string]any)
		if m == nil {
			continue
		}
		switch v := m["prompt"].(type) {
		case []any:
			var ts []string
			for _, x := range v {
				ts = append(ts, toStrAny(x))
			}
			out = append(out, ts)
		case string:
			out = append(out, []string{v})
		}
	}
	return out
}

func toStrAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// TestImportMultiTurnPromptOnly 模板 prompt_1..prompt_3（无普通提问列）
func TestImportMultiTurnPromptOnly(t *testing.T) {
	withTempUploads(t)

	resp := importRows(t,
		[]string{"prompt_1", "prompt_2", "prompt_3", "场景"},
		[][]string{
			{"第一轮提问", "第二轮提问", "第三轮提问", "多轮"},
			{"只有一轮", "", "", "单轮"},
		}, true)

	got := turnArrays(t, resp)
	if len(got) != 2 {
		t.Fatalf("应解析出 2 条，实际 %d（resp=%v）", len(got), resp)
	}
	if len(got[0]) != 3 || got[0][0] != "第一轮提问" || got[0][2] != "第三轮提问" {
		t.Errorf("第 1 条应为 3 轮且顺序正确，实际 %#v", got[0])
	}
	if len(got[1]) != 1 || got[1][0] != "只有一轮" {
		t.Errorf("第 2 条应为 1 轮，实际 %#v", got[1])
	}
}

// TestImportMultiTurnPromptPlusTurns 模板「提问」+ prompt_2..N
func TestImportMultiTurnPromptPlusTurns(t *testing.T) {
	withTempUploads(t)

	resp := importRows(t,
		[]string{"提问", "prompt_2", "prompt_3"},
		[][]string{{"首轮提问", "次轮提问", "三轮提问"}}, true)

	got := turnArrays(t, resp)
	if len(got) != 1 {
		t.Fatalf("应解析出 1 条，实际 %d", len(got))
	}
	if len(got[0]) != 3 {
		t.Fatalf("应拼成 3 轮，实际 %#v", got[0])
	}
	if got[0][0] != "首轮提问" || got[0][1] != "次轮提问" || got[0][2] != "三轮提问" {
		t.Errorf("轮次顺序不对：%#v", got[0])
	}
}

// TestImportMultiTurnThenStore 导入后真正落库，轮次完整保真
func TestImportMultiTurnThenStore(t *testing.T) {
	withTempUploads(t)

	resp := importRows(t,
		[]string{"prompt_1", "prompt_2", "场景"},
		[][]string{{"落库第一轮", "落库第二轮", "备课"}}, false)

	if ok, _ := resp["ok"].(bool); !ok {
		t.Fatalf("导入应成功，实际 %v", resp)
	}
	added, _ := resp["added"].(float64)
	if int(added) != 1 {
		t.Fatalf("应落库 1 条，实际 %v", resp["added"])
	}

	// 从库里读回，确认两轮都在
	list, err := testcasedb.GetPresetCases("")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("库里应有 1 条，实际 %d", len(list))
	}
	turns, _ := list[0]["prompt"].([]string)
	if len(turns) != 2 || turns[0] != "落库第一轮" || turns[1] != "落库第二轮" {
		t.Errorf("落库后轮次应完整保真，实际 %#v", turns)
	}
	t.Logf("落库轮次：%#v，turn_count=%v", turns, list[0]["turn_count"])
}

// TestImportMultiTurnPromptColumnAfterTurns 回归（E2E）：
// 「提问」列排在 prompt_N 之后时，第 1 轮不能被丢掉。
//
// 这条走真实 handler + 真实 xlsx 解析，覆盖 unit 测试之外的表头归一化路径。
func TestImportMultiTurnPromptColumnAfterTurns(t *testing.T) {
	withTempUploads(t)

	resp := importRows(t,
		[]string{"prompt_2", "prompt_3", "提问"},
		[][]string{{"第二轮", "第三轮", "首轮真实提问"}}, true)

	got := turnArrays(t, resp)
	if len(got) != 1 {
		t.Fatalf("应解析出 1 条，实际 %d（resp=%v）", len(got), resp)
	}
	want := []string{"首轮真实提问", "第二轮", "第三轮"}
	if len(got[0]) != 3 {
		t.Fatalf("应为 3 轮，实际 %#v", got[0])
	}
	for i := range want {
		if got[0][i] != want[i] {
			t.Errorf("第 %d 轮应为 %q，实际 %q（全部：%#v）", i+1, want[i], got[0][i], got[0])
		}
	}
}
