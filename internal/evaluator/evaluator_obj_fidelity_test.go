package evaluator

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/trajectory"
)

// ---------- _obj_delivery_efficiency ----------
//
// Python: `ev = {"turns": turns, "minutes": round(mins, 1)}`
// 是**银行家舍入**（half-to-even，且对二进制精确值做正确十进制舍入）。
// Go 原先写的是 `math.Round(mins*10)/10` —— 既不是 ties-to-even，
// 又多了一次 `*10` 的二次舍入。实测反例（见 TestObjDeliveryEfficiencyMatchesPythonRounding）。

func TestObjDeliveryEfficiencyMatchesPythonRounding(t *testing.T) {
	// 每个用例：elapsedS 秒 → Python round(elapsedS/60, 1) 的期望值
	cases := []struct {
		elapsedS float64
		wantMin  float64
		why      string
	}{
		{1875.0, 31.2, "31.25 是二进制精确的 .5 tie → ties-to-even 取 31.2（math.Round 会给 31.3）"},
		{15.0, 0.2, "0.25 精确 tie → 取 0.2"},
		{21.0, 0.3, "0.35 ≈ 0.34999… 真实值不足 .35 → 取 0.3（math.Round 走 *10 会得 0.4）"},
		{9.0, 0.1, "0.15 ≈ 0.14999… → 取 0.1"},
		{747.0, 12.4, "12.45 ≈ 12.44999… → 取 12.4"},
		{150.0, 2.5, "2.5 无歧义"},
	}
	for _, c := range cases {
		_, ev := objDeliveryEfficiency(3, c.elapsedS, true)
		got := ev["minutes"].(float64)
		if got != c.wantMin {
			t.Errorf("elapsedS=%v → minutes 期望 %v，实际 %v（%s）", c.elapsedS, c.wantMin, got, c.why)
		}
	}
}

// 交叉校验：Go 的结果必须与项目里已对齐 Python 的 trajectory.PyRound 完全一致。
func TestObjDeliveryEfficiencyUsesPyRound(t *testing.T) {
	for sec := 0.0; sec <= 3000.0; sec += 0.25 {
		mins := sec / 60.0
		_, ev := objDeliveryEfficiency(6, sec, true)
		got := ev["minutes"].(float64)
		want := trajectory.PyRound(mins, 1)
		if got != want {
			t.Fatalf("elapsedS=%v: 实际 %v，PyRound 期望 %v", sec, got, want)
		}
	}
}

func TestObjDeliveryEfficiencyBands(t *testing.T) {
	// Python: turns<=2 或 mins<5 → 5；turns<=3 或 mins<10 → 4；turns<=4 → 3；否则 2/1
	cases := []struct {
		turns      int
		elapsedS   float64
		completion bool
		want       int
	}{
		{1, 0, true, 5}, {2, 0, true, 5}, {9, 299, true, 5},
		{3, 600, true, 4}, {9, 599, true, 4},
		{4, 1200, true, 3},
		{5, 1200, true, 2}, {9, 1200, true, 2},
		{5, 1200, false, 1}, {9, 99999, false, 1},
	}
	for _, c := range cases {
		got, _ := objDeliveryEfficiency(c.turns, c.elapsedS, c.completion)
		if got != c.want {
			t.Errorf("turns=%d elapsed=%v complete=%v → 期望 %d，实际 %d",
				c.turns, c.elapsedS, c.completion, c.want, got)
		}
	}
}

// ---------- _obj_tool_selection ----------

// Python 是 `str(t).strip()` —— strip() 会去掉 U+001C–U+001F，
// 而 Go 的 strings.TrimSpace 不会。见 AGENTS.md §6 陷阱 12。
func TestObjToolSelectionStripsUnicodeSeparators(t *testing.T) {
	// U+001C 是 Python \s / strip() 认、Go unicode.IsSpace 不认的分隔符
	trace := &models.ExecutionTrace{
		ToolCalls: []*models.ToolCall{{Name: "read_file", Index: 0}},
	}
	rate, ev := objToolSelection([]interface{}{"\x1cread_file\x1d"}, trace)
	if rate == nil {
		t.Fatalf("不应返回 nil（期望工具非空：%v）", ev["expected"])
	}
	exp := ev["expected"].([]string)
	if len(exp) != 1 || exp[0] != "read_file" {
		t.Errorf("expected 期望 [read_file]，实际 %q（U+001C/U+001D 未被 strip）", exp)
	}
	if *rate != 1.0 {
		t.Errorf("命中率期望 1.0，实际 %v", *rate)
	}
}

// Python: `[str(t).strip() for t in (expect_tools or []) if str(t).strip()]`
// str(None) == "None" —— 是**非空**字符串，Python 会保留它。
// Go 原先用 `s != "<nil>"` 把 nil 过滤掉了，与 Python 不一致。
func TestObjToolSelectionNoneBecomesLiteralNone(t *testing.T) {
	trace := &models.ExecutionTrace{
		ToolCalls: []*models.ToolCall{{Name: "read_file", Index: 0}},
	}
	rate, ev := objToolSelection([]interface{}{nil, "read_file"}, trace)
	if rate == nil {
		t.Fatal("不应返回 nil")
	}
	exp := ev["expected"].([]string)
	want := []string{"None", "read_file"}
	if len(exp) != len(want) || exp[0] != want[0] || exp[1] != want[1] {
		t.Errorf("expected 期望 %q，实际 %q（Python str(None)==\"None\" 会被保留）", want, exp)
	}
	// 2 个期望工具命中 1 个
	if *rate != 0.5 {
		t.Errorf("命中率期望 0.5，实际 %v", *rate)
	}
}

func TestObjToolSelectionBasics(t *testing.T) {
	trace := &models.ExecutionTrace{ToolCalls: []*models.ToolCall{
		{Name: "b_tool", Index: 0}, {Name: "a_tool", Index: 1}, {Name: "a_tool", Index: 2},
	}}
	// hit 保持 expected 顺序；used 去重并排序
	rate, ev := objToolSelection([]interface{}{"a_tool", "b_tool", "missing"}, trace)
	if rate == nil || math.Abs(*rate-2.0/3.0) > 1e-12 {
		t.Errorf("命中率期望 2/3，实际 %v", rate)
	}
	if got := ev["used"]; !equalStr(got.([]string), []string{"a_tool", "b_tool"}) {
		t.Errorf("used 期望 [a_tool b_tool]，实际 %v", got)
	}
	if got := ev["hit"]; !equalStr(got.([]string), []string{"a_tool", "b_tool"}) {
		t.Errorf("hit 期望 [a_tool b_tool]，实际 %v", got)
	}
}

func TestObjToolSelectionEmptyOrNilTrace(t *testing.T) {
	// 无期望工具 → nil + note
	rate, ev := objToolSelection(nil, &models.ExecutionTrace{})
	if rate != nil {
		t.Errorf("无期望工具时期望 nil，实际 %v", *rate)
	}
	if ev["note"] != "用例未声明期望工具" {
		t.Errorf("note 文案不对：%v", ev["note"])
	}
	// 有期望工具但无会话 → nil + 另一种 note
	rate2, ev2 := objToolSelection([]interface{}{"read_file"}, nil)
	if rate2 != nil {
		t.Errorf("无会话时期望 nil，实际 %v", *rate2)
	}
	if ev2["note"] != "无会话数据" {
		t.Errorf("note 文案不对：%v", ev2["note"])
	}
}

// 关键契约：expected / used / hit 必须序列化成 [] 而不是 null
func TestObjToolSelectionEmptySlicesAreNotNull(t *testing.T) {
	trace := &models.ExecutionTrace{ToolCalls: []*models.ToolCall{{Name: "x", Index: 0}}}
	_, ev := objToolSelection([]interface{}{"nope"}, trace)
	b, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"hit":null`) {
		t.Errorf("hit 不应为 null：%s", b)
	}
}

// ---------- _obj_self_correction ----------

func mkFinding(rule string, step float64) map[string]interface{} {
	return map[string]interface{}{"rule": rule, "step_index": step}
}

func TestObjSelfCorrectionNoFailures(t *testing.T) {
	trace := &models.ExecutionTrace{ToolCalls: []*models.ToolCall{{Name: "a", Index: 0}}}
	rate, ev := objSelfCorrection(trace, nil)
	if rate == nil || *rate != 1.0 {
		t.Errorf("无失败时期望 1.0，实际 %v", rate)
	}
	if ev["failures"] != 0 || ev["recovered"] != 0 {
		t.Errorf("failures/recovered 期望 0/0，实际 %v/%v", ev["failures"], ev["recovered"])
	}
	if ev["note"] != "无工具失败，按 100% 计" {
		t.Errorf("note 文案不对：%v", ev["note"])
	}
}

func TestObjSelfCorrectionRecovery(t *testing.T) {
	// 第 0 步失败，第 1 步同名但参数不同 → 算一次自纠正
	trace := &models.ExecutionTrace{ToolCalls: []*models.ToolCall{
		{Name: "shell", Index: 0, Arguments: map[string]interface{}{"cmd": "a"}},
		{Name: "shell", Index: 1, Arguments: map[string]interface{}{"cmd": "b"}},
	}}
	findings := []interface{}{mkFinding("TOOL_CALL_FAILED", 0)}
	rate, ev := objSelfCorrection(trace, findings)
	if rate == nil || *rate != 1.0 {
		t.Errorf("期望 1.0，实际 %v", rate)
	}
	if ev["failures"] != 1 || ev["recovered"] != 1 {
		t.Errorf("期望 1/1，实际 %v/%v", ev["failures"], ev["recovered"])
	}
}

func TestObjSelfCorrectionSameSignatureIsNotRecovery(t *testing.T) {
	// 同名同参 → 不算自纠正
	trace := &models.ExecutionTrace{ToolCalls: []*models.ToolCall{
		{Name: "shell", Index: 0, Arguments: map[string]interface{}{"cmd": "a"}},
		{Name: "shell", Index: 1, Arguments: map[string]interface{}{"cmd": "a"}},
	}}
	findings := []interface{}{mkFinding("TOOL_CALL_FAILED", 0)}
	rate, ev := objSelfCorrection(trace, findings)
	if rate == nil || *rate != 0.0 {
		t.Errorf("同名同参期望 0.0，实际 %v", rate)
	}
	if ev["recovered"] != 0 {
		t.Errorf("recovered 期望 0，实际 %v", ev["recovered"])
	}
}

func TestObjSelfCorrectionBothFailureRulesCount(t *testing.T) {
	// TOOL_CALL_FAILED 与 TOOL_RESULT_MISSING 的并集才是一条 finding 的 bad 集合
	trace := &models.ExecutionTrace{ToolCalls: []*models.ToolCall{
		{Name: "shell", Index: 0, Arguments: map[string]interface{}{"cmd": "a"}},
		{Name: "other", Index: 1, Arguments: map[string]interface{}{"cmd": "b"}},
	}}
	findings := []interface{}{mkFinding("TOOL_CALL_FAILED", 0), mkFinding("TOOL_RESULT_MISSING", 1)}
	rate, ev := objSelfCorrection(trace, findings)
	if ev["failures"] != 2 {
		t.Errorf("failures 期望 2，实际 %v", ev["failures"])
	}
	if rate == nil || *rate != 0.0 {
		t.Errorf("都无法恢复时期望 0.0，实际 %v", rate)
	}
}

func TestObjSelfCorrectionNilTrace(t *testing.T) {
	rate, ev := objSelfCorrection(nil, []interface{}{mkFinding("TOOL_CALL_FAILED", 0)})
	if rate != nil {
		t.Errorf("trace 为 nil 时期望 nil，实际 %v", *rate)
	}
	if len(ev) != 0 {
		t.Errorf("trace 为 nil 时期望空 map，实际 %v", ev)
	}
}

// ---------- _workspace_root / _scope_note / _coerce_score ----------
// 这三个也是纯函数，一并锁住当前行为，避免以后被误改。

// Python: config.paths.workspace_root，缺省取 config.paths.session_root 的父目录
func TestWorkspaceRoot(t *testing.T) {
	if got := workspaceRoot(map[string]interface{}{
		"paths": map[string]interface{}{"workspace_root": "/tmp/ws"},
	}); got != "/tmp/ws" {
		t.Errorf("期望 /tmp/ws，实际 %q", got)
	}
	// 回落：session_root 的父目录
	if got := workspaceRoot(map[string]interface{}{
		"paths": map[string]interface{}{"session_root": "/a/b/sessions"},
	}); got != "/a/b" {
		t.Errorf("回落期望 /a/b，实际 %q", got)
	}
	// 空配置
	if got := workspaceRoot(nil); got != "" {
		t.Errorf("nil 配置期望空串，实际 %q", got)
	}
	// workspace_root 为纯空白 → 走回落（Python .strip() 后为空）
	if got := workspaceRoot(map[string]interface{}{
		"paths": map[string]interface{}{"workspace_root": "\u3000", "session_root": "/a/b/c"},
	}); got != "/a/b" {
		t.Errorf("全角空格应视作空并回落，实际 %q", got)
	}
}

// Python str(None)/str(True)/str(3.0) 与 Go %v 不同，这是 expect_tools 归一化的前提
func TestPyStrValueMatchesPythonStr(t *testing.T) {
	cases := []struct {
		in   interface{}
		want string
	}{
		{nil, "None"},
		{true, "True"},
		{false, "False"},
		{"read_file", "read_file"},
		// PyJSONDumps 把「整数值的 float」渲染成整数形态 —— 这是**刻意**的，
		// 用于补偿 Go 的 encoding/json 把 JSON 的 3 也解成 float64
		//（Python 那边是 int 3，json.dumps 出来就是 "3"）。见 AGENTS.md §3.4。
		{float64(3), "3"},
		{int(3), "3"},
		{float64(3.5), "3.5"},
	}
	for _, c := range cases {
		if got := pyStrValue(c.in); got != c.want {
			t.Errorf("pyStrValue(%#v) 期望 %q，实际 %q", c.in, c.want, got)
		}
	}
	// 非整数浮点保留小数（Go 的 %v 也给 3.5，但 PyJSONDumps 的分隔/转义也一并正确）
	if got := pyStrValue(3.5); got != "3.5" {
		t.Errorf("pyStrValue(3.5) 期望 %q，实际 %q", "3.5", got)
	}
	// 注意：整数值的 float 走的是「整数形态」—— 这是为 JSON 往返刻意做的补偿，
	// 与 Python 独立的 str(3.0)=="3.0" 不同。原因见边界测试的说明。
	if got := pyStrValue(3.0); got != "3" {
		t.Errorf("pyStrValue(3.0) 期望 %q（JSON 往返补偿形态），实际 %q", "3", got)
	}
}

// ⚠️ 残留边界（已知，未修，属 AGENTS.md §3.4 的同一类）：
// Python 的 json 区分 int 与 float（json.loads("3")→int 3，json.loads("3.0")→float 3.0），
// 而 Go 的 encoding/json **两者都解成 float64**，信息已丢失。
//
//	JSON 字面量   Python exp 元素   Go 侧 pyStrValue
//	3             "3"               "3"     ✅ 靠 PyJSONDumps 的整数形态补偿
//	3.0           "3.0"             "3"     ❌ 无法区分，边界在此
//
// 现实语料中 expect_tools 全是工具名字符串，此路径不可达；显式记录以免
// 以后有人误以为"已经完全对齐"。
func TestPyStrValueJSONIntFloatBoundaryDocumented(t *testing.T) {
	// Go 无法从 JSON 数字还原 Python 的 int/float 之分：两者都是 float64(3)
	var fromJSON interface{} = float64(3)
	if got := pyStrValue(fromJSON); got != "3" {
		t.Errorf("JSON 字面量 3：期望 %q（与 Python int 3 一致），实际 %q", "3", got)
	}
	// 反向说明：Go 无法表达"这是一个真正的 float 3.0"
	t.Log("已知边界：JSON 字面量 3.0 → Python \"3.0\" / Go \"3\"（Go 的 JSON 解码不区分 int/float）")
}

func TestScopeNoteIsDeterministic(t *testing.T) {
	a := scopeNote("备课", "labels")
	b := scopeNote("备课", "labels")
	if a != b {
		t.Errorf("scopeNote 不确定：%q vs %q", a, b)
	}
}

// Python `_coerce_score` 先 `v = float(raw)`：
//   - float() 要求**整个**字符串合法，`"3abc"` 直接 ValueError → None（记为未评）；
//   - `float(True)` == 1.0 → 落进 (1,2,3,4,5) → 返回 1。
//
// Go 原先用 `fmt.Sscanf(v, "%f", &parsed)` 是**前缀**解析（`"3abc"` 也能得 3），
// 且 switch 没有 bool 分支 —— 两处都会让"本该未评"变成"有分"。
func TestCoerceScoreOnlyExactScaleValues(t *testing.T) {
	one, three, five, zero := 1, 3, 5, 0
	cases := []struct {
		raw   interface{}
		scale string
		want  *int
	}{
		// 合法档位
		{float64(3), "1-5", &three},
		{float64(5), "1-5", &five},
		{float64(1), "1-5", &one},
		{"3", "1-5", &three},
		{"3.0", "1-5", &three},
		{float64(3), "0-3-5", &three},
		{float64(0), "0-3-5", &zero},
		{float64(5), "5-0", &five},
		{float64(0), "5-0", &zero},
		// 非法档位 → nil
		{float64(4), "0-3-5", nil},
		{float64(2), "5-0", nil},
		{float64(3.7), "1-5", nil},
		{float64(0), "1-5", nil},
		{nil, "1-5", nil},
		{"abc", "1-5", nil},
		// 🔴 前缀解析陷阱：float("3abc") 抛 ValueError → Python 记未评
		{"3abc", "1-5", nil},
		{"3,5", "1-5", nil},
		{"3分", "1-5", nil},
		{"3.0（满分5）", "1-5", nil},
		// 🔴 bool 陷阱：Python float(True)==1.0 → 返回 1
		{true, "1-5", &one},
		{false, "1-5", nil},   // float(False)==0.0，不在 (1..5)
		{false, "5-0", &zero}, // float(False)==0.0，在 (0,5)
	}
	for _, c := range cases {
		got := coerceScore(c.raw, c.scale)
		if (got == nil) != (c.want == nil) {
			t.Errorf("raw=%#v scale=%s: 期望 %v，实际 %v", c.raw, c.scale, c.want, got)
			continue
		}
		if got != nil && *got != *c.want {
			t.Errorf("raw=%#v scale=%s: 期望 %d，实际 %d", c.raw, c.scale, *c.want, *got)
		}
	}
}

func equalStr(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
