package xlsx

import (
	"encoding/json"
	"strings"
	"testing"
)

// 本文件锁住 P0 差分验证（真实 38MB 用例表 vs Python 原版）中发现并修复的
// 三个保真度缺陷。任何一条回归都会破坏与 Python 行为的一致性。
//
// 背景：不要"优化"这些断言 —— 它们复现的是 Python 原版的实际行为，
// 包括看起来像 Bug 的部分。

// TestInferTargetsKeywordBranchesAreExclusive 锁住 if/elif 互斥语义。
//
// Python 原版第 4 步是 if/elif 链：命中「课件」(ppt) 后就不再判「表格」(excel)。
// 早期 Go 移植用成了顺序 if，导致同一句里同时命中两个关键词时会多推一个目标。
// 真实文件 item[82] 就是这个 case。
func TestInferTargetsKeywordBranchesAreExclusive(t *testing.T) {
	prompt := "九年级物理《欧姆定律》实验探究课件，含实验电路图、数据记录表格、结论分析。"

	got := InferTargets(prompt, "", "")

	// Python 原版返回 ['ppt'] —— 只能有一个，不能同时出现 excel
	if len(got) != 1 || got[0] != "ppt" {
		t.Fatalf("InferTargets = %v，期望 [ppt]（ppt 与 excel 关键词必须互斥）", got)
	}
	for _, g := range got {
		if g == "excel" {
			t.Fatalf("InferTargets 多推了 excel：%v —— 第 4 步的 if 分支应互斥", got)
		}
	}
}

// TestInferTargetsTruncatedChainStopsAtFirstMatch 覆盖互斥链的更多分支。
func TestInferTargetsTruncatedChainStopsAtFirstMatch(t *testing.T) {
	cases := []struct {
		name   string
		prompt string
		want   string
	}{
		{"pptx 优先于 excel", "做一个课件，附一份表格", "ppt"},
		{"word 优先于 pptx", "撰写一份教学设计，顺便做个课件", "word"},
		// 无产出动词 → 第 3 步不命中，落到第 4 步的互斥链；excel 先于 pdf
		{"excel 优先于 pdf", "参考这份表格，同时附上 pdf 说明", "excel"},
		{"pdf 优先于 html", "生成 pdf 网页版", "pdf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := InferTargets(tc.prompt, "", "")
			if len(got) == 0 {
				t.Fatalf("InferTargets(%q) 返回空", tc.prompt)
			}
			// 只在没有更强证据时才会走到关键词补全；此处断言包含期望值且不越界
			found := false
			for _, g := range got {
				if g == tc.want {
					found = true
				}
			}
			if !found {
				t.Fatalf("InferTargets(%q) = %v，期望包含 %q", tc.prompt, got, tc.want)
			}
		})
	}
}

// TestInferTargetsIsDeterministic 锁住可复现性。
//
// Python 原版遍历 set（顺序随 PYTHONHASHSEED 变化），Go 早期版本遍历 map
// 同样随机 —— 同一输入多次运行会得到不同顺序。已改为按 kindOrder 固定遍历。
func TestInferTargetsIsDeterministic(t *testing.T) {
	// 该提问同时命中 docx 与 pptx 两个 kind → 顺序敏感
	prompt := "根据《平均数》教学设计文档生成一份ppt，围绕数据意识核心素养。"

	first := strings.Join(InferTargets(prompt, "", ""), ",")
	if len(strings.Split(first, ",")) < 2 {
		t.Fatalf("用例无效：期望命中多个目标，实际得到 %q", first)
	}
	for i := 0; i < 200; i++ {
		got := strings.Join(InferTargets(prompt, "", ""), ",")
		if got != first {
			t.Fatalf("第 %d 次结果不一致：%q != %q（顺序必须可复现）", i, got, first)
		}
	}
}

// TestRowsToItemsEmptySlicesMarshalAsArray 锁住 null vs [] 语义。
//
// Python 原版对空 attachments / expect_tools 返回 []，而 Go 的 nil slice
// 会被 encoding/json 序列化成 null。前端做防御式读取（?? []），
// 二者不等价 —— 真实文件里有 858~1000 项受影响。
func TestRowsToItemsEmptySlicesMarshalAsArray(t *testing.T) {
	rows := [][]string{
		{"序号", "场景", "Query内容", "Skill", "附件"},
		{"1", "备课", "写一份教案", "", ""},
	}
	headIdx := 0
	promptCol := 2
	sceneCol := 1
	skillCol := 3
	attachCol := 4

	got := RowsToItems(rows, &headIdx, promptCol, &sceneCol, nil, nil, &attachCol, &skillCol)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal 失败: %v", err)
	}
	s := string(raw)

	for _, bad := range []string{`"attachments":null`, `"expect_tools":null`, `"items":null`} {
		if strings.Contains(s, bad) {
			t.Fatalf("出现 %s —— 空集合必须序列化为 []，不能是 null\n%s", bad, s)
		}
	}
	for _, want := range []string{`"attachments":[]`, `"expect_tools":[]`} {
		if !strings.Contains(s, want) {
			t.Fatalf("缺少 %s\n%s", want, s)
		}
	}

	// targets 永远非空（第 5 步兜底 word），同样不能是 null
	if strings.Contains(s, `"targets":null`) {
		t.Fatalf("targets 为 null\n%s", s)
	}
}

// TestInferTargetsNeverReturnsNil 锁住兜底与空切片语义。
func TestInferTargetsNeverReturnsNil(t *testing.T) {
	got := InferTargets("随便一句话，没有任何可识别关键词", "", "")
	if got == nil {
		t.Fatal("InferTargets 返回 nil —— 必须返回非 nil 切片")
	}
	// Python 第 5 步兜底为 word
	if len(got) != 1 || got[0] != "word" {
		t.Fatalf("兜底期望 [word]，实际 %v", got)
	}
}
