package formatcheck

import (
	"encoding/json"
	"testing"
)

// TestNormTextStripsPythonWhitespace 钉住「Python `\s` 是 Unicode 感知的」这一修复。
//
// 原实现直接写 Go 的 `\s`（只认 \t \n \f \r 空格），
// 中文语料里极常见的 U+3000 全角空格 / U+00A0 NBSP / U+2003 EM SPACE
// Python 会归一化掉、Go 不会 → 要求项覆盖率静默算错。
// 真实语料实测出现 105 处这类字符。
func TestNormTextStripsPythonWhitespace(t *testing.T) {
	// 每一种都应在 _norm 后被去掉
	for _, sp := range []string{
		"\u3000", "\u00a0", "\u2003", "\u000b", "\u2028",
		"\u0085", "\u001c", "\u1680", "\u202f", "\u2000",
	} {
		got := normText("封面" + sp + "目录")
		if got != "封面目录" {
			t.Errorf("normText 未去掉 U+%04X：got %q want %q",
				[]rune(sp)[0], got, "封面目录")
		}
	}
	// 普通空格与大小写仍要处理
	if got := normText(" A B "); got != "ab" {
		t.Errorf("normText(%q) = %q，want %q", " A B ", got, "ab")
	}
}

// TestRequirementHitAcrossUnicodeSpace 复刻真实差分场景 A-0000：
// 要求项与正文都含 Unicode 空格，命中数从 Go 的 1/5 变成 Python 的 3/5。
func TestRequirementHitAcrossUnicodeSpace(t *testing.T) {
	for _, sp := range []string{"\u3000", "\u00a0", "\u2003", "\u202f"} {
		reqs := []string{
			"包含封面与目录",
			"正文不少于 800 字",
			"使用宋体",
			"包含" + sp + "页眉",
			"参考" + sp + "文献" + sp + "列表",
		}
		hay := "封面\n目录\n正文\n页眉\n参考文献列表\n"
		rate, detail := RequirementCoverage(reqs, hay)
		if rate == nil {
			t.Fatalf("U+%04X: rate 不应为 nil", []rune(sp)[0])
		}
		// 「正文不少于 800 字」「使用宋体」本来就不在正文里 → 3/5。
		// 要点是**含 Unicode 空格的那三条必须全部命中**；
		// 修复前 Go 只命中 1/5（0.2），因为 Go 的 \s 不认这些字符。
		if *rate != 0.6 {
			t.Errorf("U+%04X: coverage_rate = %v，want 0.6\n  detail=%v",
				[]rune(sp)[0], *rate, detail)
		}
		hit := detail["hit"].([]string)
		for _, want := range []string{"包含" + sp + "页眉", "参考" + sp + "文献" + sp + "列表"} {
			found := false
			for _, h := range hit {
				if h == want {
					found = true
				}
			}
			if !found {
				t.Errorf("U+%04X: %q 未命中（Python 归一化后能命中）\n  hit=%v",
					[]rune(sp)[0], want, hit)
			}
		}
	}
}

// TestRequirementCoverageNonNilLists 钉住 `hit`/`missing` 必须是 `[]` 而非 `null`。
//
// Python 的列表推导天然给 []，Go 的 `var s []string` 给 nil → JSON `null`。
// 前端按数组消费，null 会炸。真实差分里这是 11 处 formatcheck 差异的来源。
func TestRequirementCoverageNonNilLists(t *testing.T) {
	// 全部命中：missing 必须是非 nil 空切片
	rate, detail := RequirementCoverage([]string{"封面"}, "封面")
	if rate == nil || *rate != 1.0 {
		t.Fatalf("rate = %v, want 1.0", rate)
	}
	assertJSONHasEmptyArray(t, detail, "missing")

	// 全部未命中：hit 必须是非 nil 空切片
	_, detail = RequirementCoverage([]string{"封面"}, "完全无关")
	assertJSONHasEmptyArray(t, detail, "hit")
}

// TestTypeConsistencySortedAndNonNil 钉住 expected/produced/hit 三个列表
// **必须排序**且非 nil。
//
// 原实现直接 `for k := range kinds`（Go map）→ 输出顺序**每次运行都不同**。
func TestTypeConsistencySortedAndNonNil(t *testing.T) {
	// ⚠️ labels.targets 用的是**词表标签**（word/ppt/html/excel/pdf/network），
	// 不是产物类型名（docx/pptx/html/excel）。见 core/artifacts.py:TARGET_KINDS。
	labels := map[string]any{"targets": []any{"word", "ppt", "html", "excel"}}
	contract := BuildContract(labels, nil)

	// kinds = {docx, pptx, html, excel}；实际产出 {html, excel} → 2/4
	rate, detail := TypeConsistency(contract, []string{"excel", "html"})
	if rate == nil {
		t.Fatal("rate 不应为 nil")
	}
	if *rate != 0.5 {
		t.Errorf("rate = %v, want 0.5", *rate)
	}

	// 连续跑 20 次，输出必须完全一致（否则就是 map 随机序复活）
	first := mustJSON(detail)
	for i := 0; i < 20; i++ {
		if got := mustJSON(detail); got != first {
			t.Fatalf("第 %d 次输出不同，说明又用 map 遍历决定顺序了：\n  1st = %s\n  nth = %s", i, first, got)
		}
	}

	// 三个列表都必须有序
	for _, key := range []string{"expected", "produced", "hit"} {
		list := detail[key].([]string)
		for i := 1; i < len(list); i++ {
			if list[i-1] > list[i] {
				t.Errorf("%s 未排序: %v", key, list)
			}
		}
	}
	// 一个都没命中的类型集合也要给 []（kinds={docx}，产出为空 → hit=[]）
	_, d2 := TypeConsistency(BuildContract(map[string]any{"targets": []any{"word"}}, nil), []string{})
	assertJSONHasEmptyArray(t, d2, "hit")

	// kinds 为空时走「不适用」分支：只有 reason，没有 expected/produced/hit
	_, d3 := TypeConsistency(BuildContract(map[string]any{"targets": []any{"docx"}}, nil), []string{"docx"})
	if _, ok := d3["reason"]; !ok {
		t.Errorf("targets 用了产物类型名（非法标签）时应返回 reason 分支，got %v", d3)
	}
	if _, ok := d3["hit"]; ok {
		t.Errorf("reason 分支不该有 hit 键，got %v", d3)
	}
}

// TestBuildContractStripsPythonWhitespace 覆盖 build_contract 的 targets 归一化。
//
// Python: `[str(t).strip().lower() for t in (labels.get("targets") or []) if str(t).strip()]`
// 两个要点：① `str.strip()` 会去掉 U+001C–U+001F（Go TrimSpace 不会）；
// ② 原实现漏了 `[]string` 分支的 strip。
func TestBuildContractStripsPythonWhitespace(t *testing.T) {
	// ① 文件分隔符 U+001C 必须被 strip 掉（真实差分 S-文件分隔U+001C）
	reqs := []string{"\x1c目录"}
	rate, detail := RequirementCoverage(reqs, "目录")
	if rate == nil || *rate != 1.0 {
		t.Errorf("U+001C 未被 strip：rate=%v detail=%v", rate, detail)
	}

	// ② []any 与 []string 两个分支都要 strip + lower
	for name, targets := range map[string]any{
		"[]any":    []any{"  DOCX  ", "\u3000PPTX\u3000", ""},
		"[]string": []string{"  DOCX  ", "\u3000PPTX\u3000", ""},
	} {
		c := BuildContract(map[string]any{"targets": targets}, nil)
		got := c["targets"].([]string)
		want := []string{"docx", "pptx"}
		if len(got) != len(want) {
			t.Errorf("%s: targets = %v，want %v（空串应被过滤、空白应被去掉）", name, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("%s: targets[%d] = %q，want %q", name, i, got[i], want[i])
			}
		}
	}
}

// TestEvaluateShape 固化 Evaluate 的对外字段形状（前端依赖）。
func TestEvaluateShape(t *testing.T) {
	out := Evaluate(
		map[string]any{"targets": []any{"docx"}, "attachment": true},
		[]string{"docx"},
		[]string{"包含封面"},
		"封面",
		map[string]float64{"type": 0.5, "coverage": 0.5},
		nil,
	)
	for _, k := range []string{"score", "basis", "na_reason", "note",
		"type_rate", "coverage_rate", "attachment", "detail"} {
		if _, ok := out[k]; !ok {
			t.Errorf("Evaluate 输出缺字段 %q", k)
		}
	}
	if out["basis"] != "objective" {
		t.Errorf("basis = %v, want objective", out["basis"])
	}
	if out["attachment"] != true {
		t.Errorf("attachment = %v, want true", out["attachment"])
	}
	detail := out["detail"].(map[string]any)
	for _, k := range []string{"type_consistency", "requirement_coverage"} {
		if _, ok := detail[k]; !ok {
			t.Errorf("detail 缺字段 %q", k)
		}
	}

	// 不适用时 score 必须是 nil（JSON 里是 null），na_reason 非空。
	// 注意 Evaluate 内部用 *float64，nil 指针装进 any 后 `!= nil` 为真，
	// 所以这里判 JSON 形态 —— 前端拿到的就是 JSON。
	na := Evaluate(map[string]any{}, []string{}, nil, "", nil, nil)
	if got := mustJSON(na["score"]); got != "null" {
		t.Errorf("不适用时 score 序列化应为 null，got %s", got)
	}
	if na["na_reason"] == "" {
		t.Error("不适用时 na_reason 不应为空")
	}
}

// TestCombineWeights 覆盖 combine 的加权与去重语义。
func TestCombineWeights(t *testing.T) {
	r1, r2 := 1.0, 0.0
	// 默认等权 → 0.5 比例 → 中间分
	score, note := Combine(&r1, &r2, 0.5, 0.5)
	if score == nil {
		t.Fatal("score 不应为 nil")
	}
	if note == "" {
		t.Error("note 不应为空")
	}
	// 两者都缺 → nil + 原因
	if s, n := Combine(nil, nil, 0.5, 0.5); s != nil || n == "" {
		t.Errorf("两者都缺应返回 (nil, 原因)，got (%v, %q)", s, n)
	}
	// 只有 type 时，权重应被归一化（等价于全权重）
	only, _ := Combine(&r1, nil, 0.3, 0.7)
	full, _ := Combine(&r1, &r1, 0.3, 0.7)
	if only == nil || full == nil || *only != *full {
		t.Errorf("单边加权未归一化：only=%v full=%v", only, full)
	}
}

// ---------------- 小工具 ----------------

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}

// assertJSONHasEmptyArray 断言 detail[key] 序列化成 `[]` 而不是 `null`
func assertJSONHasEmptyArray(t *testing.T, detail map[string]any, key string) {
	t.Helper()
	v, ok := detail[key]
	if !ok {
		t.Fatalf("detail 缺字段 %q", key)
	}
	list, ok := v.([]string)
	if !ok {
		t.Fatalf("detail[%q] 类型是 %T，want []string", key, v)
	}
	if list == nil {
		t.Errorf("detail[%q] 是 nil，JSON 会输出 null；Python 给 []", key)
	}
	if len(list) != 0 {
		t.Errorf("detail[%q] = %v，want 空", key, list)
	}
	if got := mustJSON(v); got != "[]" {
		t.Errorf("detail[%q] 序列化为 %s，want []", key, got)
	}
}
