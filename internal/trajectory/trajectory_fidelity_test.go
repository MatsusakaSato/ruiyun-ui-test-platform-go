package trajectory

import (
	"encoding/json"
	"math"
	"testing"
)

// TestPyRoundIsBankersRounding 锁住 Python round() 的 ties-to-even 语义。
//
// 这不是「风格问题」：真实语料里 219 个用例的 error_rate 恰好出现了 31.25，
// Python 给 31.2、math.Round 给 31.3，指标层差分就是这么炸出来的。
func TestPyRoundIsBankersRounding(t *testing.T) {
	cases := []struct {
		in   float64
		nd   int
		want float64
	}{
		{31.25, 1, 31.2}, // 恰好半值 → 取偶（2 是偶数，不进位）
		{31.35, 1, 31.4}, // 注意 31.35 的二进制精确值略大于 31.35 → 进位
		{0.5, 0, 0.0},    // 半值取偶
		{1.5, 0, 2.0},
		{2.5, 0, 2.0},
		{-2.5, 0, -2.0}, // 负数同样是 ties-to-even
		{-31.25, 1, -31.2},
		{2.675, 2, 2.67}, // 经典二进制表示陷阱
		{0.125, 2, 0.12},
		{0.135, 2, 0.14}, // 0.135 的精确值略大于 0.135
		{100.0, 1, 100.0},
		{0.0, 1, 0.0},
	}
	for _, c := range cases {
		if got := PyRound(c.in, c.nd); got != c.want {
			t.Errorf("PyRound(%v, %d) = %v，期望 %v", c.in, c.nd, got, c.want)
		}
	}
	// 特殊值必须原样返回，不能变成 NaN 或 panic
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if got := PyRound(v, 1); !math.IsNaN(got) && !math.IsInf(got, 0) {
			t.Errorf("PyRound(%v, 1) = %v，期望原样返回特殊值", v, got)
		}
	}
}

// TestRoundToOneDecimalUsesPyRound 确认对外入口没有绕过银行家舍入。
func TestRoundToOneDecimalUsesPyRound(t *testing.T) {
	if got := RoundToOneDecimal(31.25); got != 31.2 {
		t.Fatalf("RoundToOneDecimal(31.25) = %v，期望 31.2（Python round）", got)
	}
}

// TestNilSafeStrNeverProducesLiteralNil 复刻 Python 的 `str(x or "")`。
//
// fmt.Sprintf("%v", nil) 会得到字面量字符串 "<nil>" —— logparser 的 session_id
// 就是这么在 219/219 个会话上全错的，这里是同一类缺陷的兜底。
func TestNilSafeStrNeverProducesLiteralNil(t *testing.T) {
	if got := nilSafeStr(nil); got != "" {
		t.Errorf("nilSafeStr(nil) = %q，期望空串", got)
	}
	if got := nilSafeStr("x"); got != "x" {
		t.Errorf("nilSafeStr(\"x\") = %q", got)
	}
	if got := nilSafeStr(7); got != "7" {
		t.Errorf("nilSafeStr(7) = %q", got)
	}
}

// TestNonNilSlicesSerializesAsArray 锁住 nil → []。
// 前端用 `?? []` 与 `=== false` 之外还靠 `!= null` 判断，null 与 [] 不等价。
func TestNonNilSlicesSerializesAsArray(t *testing.T) {
	if got := nonNilSlices(nil); got == nil || len(got) != 0 {
		t.Errorf("nonNilSlices(nil) = %#v，期望非 nil 空切片", got)
	}
	var s []string
	if got := nonNilSlices(s); got == nil {
		t.Error("nonNilSlices(空 nil 切片) 仍为 nil")
	}
	if got := nonNilSlices([]string{"a"}); len(got) != 1 || got[0] != "a" {
		t.Errorf("nonNilSlices 破坏了原值: %#v", got)
	}
}

func TestAppendUniqueSkipsEmptyAndDupes(t *testing.T) {
	var l []string
	l = appendUnique(l, "")
	if len(l) != 0 {
		t.Error("appendUnique 不应收空串")
	}
	l = appendUnique(l, "SKILL.md")
	l = appendUnique(l, "SKILL.md")
	l = appendUnique(l, "other.md")
	if len(l) != 2 || l[0] != "SKILL.md" || l[1] != "other.md" {
		t.Fatalf("appendUnique 结果错误: %#v", l)
	}
}

// TestSkillEntryShapeMatchesPython 锁住逐用例 skill 的字段契约。
// Python 是 {"name","files","steps"} —— 多输出 calls/sessions 会让前端拿到不存在的契约。
func TestSkillEntryShapeMatchesPython(t *testing.T) {
	b, err := json.Marshal(SkillEntry{Name: "xlsx", Files: []string{"SKILL.md"}, Steps: []int{0, 3}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"name":"xlsx","files":["SKILL.md"],"steps":[0,3]}`
	if string(b) != want {
		t.Fatalf("SkillEntry 序列化 = %s，期望 %s", b, want)
	}
	// 反向确认：Python 侧**没有** calls/sessions，多输出即契约偏离
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"calls", "sessions"} {
		if _, bad := m[k]; bad {
			t.Errorf("SkillEntry 不应有 %q 字段（Python 逐用例 skill 是 name/files/steps）", k)
		}
	}
}

// TestUnknownSkillConstant 锁住回退名，前端可能按它做展示分流。
func TestUnknownSkillConstant(t *testing.T) {
	if UnknownSkill != "(未识别技能)" {
		t.Fatalf("UnknownSkill = %q", UnknownSkill)
	}
}

// TestConfirmModeLabelDefaultsToAutoConfirm 复刻
// Python `CONFIRM_MODE_LABEL.get(mode, mode or "自动确认")`。
func TestConfirmModeLabelAlwaysNonEmpty(t *testing.T) {
	// mode 为空时，Python 回退到 "自动确认"，绝不能是空串
	if got := confirmLabel(""); got != "自动确认" {
		t.Errorf("confirmLabel(\"\") = %q，期望 自动确认", got)
	}
	if got := confirmLabel("keyword"); got != "关键词命中授权按钮" {
		t.Errorf("confirmLabel(keyword) = %q", got)
	}
	if got := confirmLabel("未登记的mode"); got != "未登记的mode" {
		t.Errorf("confirmLabel 未登记 mode = %q，期望原样返回", got)
	}
}

// TestIsoToEpochOptHandlesLocalAndZ 覆盖 Python fromisoformat 的两种输入。
func TestIsoToEpochOptHandlesLocalAndZ(t *testing.T) {
	if _, ok := isoToEpochOpt(""); ok {
		t.Error("空串应返回 ok=false（Python 返回 None）")
	}
	if _, ok := isoToEpochOpt("不是时间"); ok {
		t.Error("无法解析应返回 ok=false")
	}
	v, ok := isoToEpochOpt("2026-09-23T10:00:00")
	if !ok || v <= 0 {
		t.Errorf("本地无时区串解析失败: %v %v", v, ok)
	}
	if v2, ok2 := isoToEpochOpt("2026-09-23T10:00:00+08:00"); !ok2 || v2 <= 0 {
		t.Errorf("带时区串解析失败: %v %v", v2, ok2)
	}
}

// TestToIntOKRejectsNonNumeric 防止 message_spans 的 null 区间被当成 0。
func TestToIntOKRejectsNonNumeric(t *testing.T) {
	if _, ok := toIntOK(nil); ok {
		t.Error("nil 不应被当作 0")
	}
	if _, ok := toIntOK("3"); ok {
		t.Error("字符串不应被当作整数")
	}
	if v, ok := toIntOK(float64(3)); !ok || v != 3 {
		t.Errorf("float64(3) → %v %v", v, ok)
	}
}
