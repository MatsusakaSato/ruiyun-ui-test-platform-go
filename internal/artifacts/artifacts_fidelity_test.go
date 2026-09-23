package artifacts

import (
	"os"
	"path/filepath"
	"testing"

	"ruiyun-ui-test-platform-go/internal/models"
)

// TestDisplayAbsPathRequiresExistingFile 是本轮最重要的回归。
//
// Python 的 core.artifacts.resolve_abs_path 每一级候选都必须 is_file() 通过，
// 解析不到返回**空串**（界面据此显示「本机未找到」）。
// Go 原实现直接返回 full_path，会给出一个**指向空气的路径**，
// 前端「查看产物」按钮点开是空的。
func TestDisplayAbsPathRequiresExistingFile(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "报告.md")
	if err := os.WriteFile(real, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}

	// 1) 真实存在的绝对路径 → 原样返回
	a := &Artifact{RelPath: real}
	if got := DisplayAbsPath(a, ""); got != real {
		t.Errorf("已存在的绝对路径 = %q，期望 %q", got, real)
	}

	// 2) 不存在的绝对路径 → 空串（这正是原实现错的地方）
	ghost := &Artifact{RelPath: filepath.Join(dir, "根本不存在.md")}
	if got := DisplayAbsPath(ghost, ""); got != "" {
		t.Errorf("不存在的路径 = %q，期望空串", got)
	}

	// 3) full_path 指向不存在的文件 → 也不能直接采信
	ghostFull := &Artifact{RelPath: "x.md", FullPath: filepath.Join(dir, "也是空气.md")}
	if got := DisplayAbsPath(ghostFull, ""); got != "" {
		t.Errorf("full_path 不存在时 = %q，期望空串", got)
	}

	// 4) 配置根 / rel_path 能拼出真实文件
	relRoot := &Artifact{RelPath: "报告.md"}
	if got := DisplayAbsPath(relRoot, dir); got != real {
		t.Errorf("配置根拼接 = %q，期望 %q", got, real)
	}

	// 5) 目录不算命中（Python 用 is_file 而非 exists）
	if got := DisplayAbsPath(&Artifact{RelPath: dir}, ""); got != "" {
		t.Errorf("目录 = %q，期望空串（必须是文件）", got)
	}

	// 6) 空 RelPath/FullPath → 空串，不 panic
	if got := DisplayAbsPath(&Artifact{}, ""); got != "" {
		t.Errorf("空产物 = %q，期望空串", got)
	}
	if got := DisplayAbsPath(nil, ""); got != "" {
		t.Errorf("nil 产物 = %q，期望空串", got)
	}
}

// TestDisplayAbsPathSearchByNameIsBoundedAndSuffixAware 覆盖按文件名兜底查找。
//
// 两条关键约束：只找浅层（深度 < 4）、且**要求末两级路径一致** ——
// 工作区里同名文件很常见，只比文件名会定位到另一个文件并在 Finder 里选错。
func TestDisplayAbsPathSearchByNameIsBoundedAndSuffixAware(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "deliverables")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	// 名字相同、目录不同 —— 只有末两级匹配的那个才是对的
	want := filepath.Join(sub, "设计稿.pptx")
	if err := os.WriteFile(want, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "设计稿.pptx"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}

	// rel 是 deliverables/设计稿.pptx → 末两级命中 sub 下那个
	a := &Artifact{RelPath: "deliverables/设计稿.pptx"}
	if got := DisplayAbsPath(a, root); got != want {
		t.Errorf("按名查找 = %q，期望 %q", got, want)
	}

	// 深层目录不应被搜到（深度上限 4）
	deep := filepath.Join(root, "a", "b", "c", "d", "深.md")
	if err := os.MkdirAll(filepath.Dir(deep), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deep, []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := DisplayAbsPath(&Artifact{RelPath: "深.md"}, root); got != "" {
		t.Errorf("深层文件被搜到了 = %q，期望空串（深度上限 4）", got)
	}
}

// TestCallFailedSemantics 复刻 Python _call_failed。
//
// 事故原型：convert_markdown_to_docx 返回 {"success": false, "error": ...} 时，
// 旧实现照样把它记成一件产物 —— 界面上就出现一个指向不存在文件的链接。
func TestCallFailedSemantics(t *testing.T) {
	cases := []struct {
		name string
		obj  map[string]any
		want bool
	}{
		{"空对象", map[string]any{}, false},
		{"无任何标志位", map[string]any{"path": "a.docx"}, false},
		{"success=true", map[string]any{"success": true}, false},
		{"success=false", map[string]any{"success": false}, true},
		{"success=false 且有 error", map[string]any{"success": false, "error": "File not found"}, true},
		{"只有 error（success 缺失）", map[string]any{"error": "boom"}, true},
		{"success=true 且 error 非空 → 不算失败", map[string]any{"success": true, "error": "warn"}, false},
		{"error 为空串 → 不算失败", map[string]any{"error": ""}, false},
		{"error 为 nil → 不算失败", map[string]any{"error": nil}, false},
		{"error 为空列表 → 不算失败", map[string]any{"error": []any{}}, false},
		{"error 为非空列表 → 失败", map[string]any{"error": []any{"x"}}, true},
	}
	for _, c := range cases {
		if got := callFailed(c.obj); got != c.want {
			t.Errorf("%s: callFailed(%v) = %v，期望 %v", c.name, c.obj, got, c.want)
		}
	}
}

// TestExtractArtifactsNoteTexts 锁住 Python 的两种文案与回填条件。
func TestExtractArtifactsNoteTexts(t *testing.T) {
	trace := &models.ExecutionTrace{
		ToolCalls: []*models.ToolCall{
			// 写入调用：登记 written[stem] = body（**不限文件类型**）
			{
				Name:      "write_file",
				Arguments: map[string]any{"relative_path": "报告.md", "content": "# 正文"},
				ResultObj: map[string]any{"success": true, "relative_path": "报告.md"},
			},
			// 转换调用：正文为空 → 应从同名写入回填
			{
				Name:      "convert_markdown_to_docx",
				Arguments: map[string]any{"md_file_name": "报告.md"},
				ResultObj: map[string]any{"success": true, "path": "报告.docx"},
			},
			// 二进制产物：正文拿不到 → 默认文案 + 图像后缀
			{
				Name:      "save_image",
				Arguments: map[string]any{"relative_path": "图.png"},
				ResultObj: map[string]any{"success": true, "save_image_path": "图.png"},
			},
			// 失败调用：既不记产物，也不参与回填
			{
				Name:      "convert_markdown_to_docx",
				Arguments: map[string]any{"md_file_name": "坏的.md"},
				ResultObj: map[string]any{"success": false, "error": "File not found", "path": "坏的.docx"},
			},
		},
	}
	set := ExtractArtifacts(trace, "")
	byPath := map[string]*Artifact{}
	for _, a := range set.Items {
		byPath[a.RelPath] = a
	}

	md, ok := byPath["报告.md"]
	if !ok {
		t.Fatalf("缺少 报告.md，实际产物: %v", set.Paths())
	}
	if md.Note != "" || md.Text != "# 正文" {
		t.Errorf("报告.md note=%q text=%q", md.Note, md.Text)
	}

	docx, ok := byPath["报告.docx"]
	if !ok {
		t.Fatalf("缺少 报告.docx，实际产物: %v", set.Paths())
	}
	if docx.Note != "正文由同名写入调用回填" {
		t.Errorf("回填 note = %q，期望「正文由同名写入调用回填」", docx.Note)
	}
	if docx.Text != "# 正文" {
		t.Errorf("回填 text = %q", docx.Text)
	}

	img, ok := byPath["图.png"]
	if !ok {
		t.Fatalf("缺少 图.png，实际产物: %v", set.Paths())
	}
	if img.Note != "仅确认产物存在，正文不可抽取；图像为二进制产物，正文不可抽取" {
		t.Errorf("图像 note = %q", img.Note)
	}

	if _, bad := byPath["坏的.docx"]; bad {
		t.Error("失败的转换调用不应进产物清单（Python _call_failed）")
	}
}

// TestExtractArtifactsEmptyIsNonNil 空产物集必须是 []，不能是 null。
func TestExtractArtifactsEmptyIsNonNil(t *testing.T) {
	if got := ExtractArtifacts(nil, ""); got.Items == nil {
		t.Error("nil trace 应返回非 nil 空切片")
	}
	empty := ExtractArtifacts(&models.ExecutionTrace{}, "")
	if empty.Items == nil {
		t.Error("空轨迹应返回非 nil 空切片")
	}
}

// TestStemAndKindOf 覆盖两个被回填逻辑依赖的纯函数。
func TestStemAndKindOf(t *testing.T) {
	cases := []struct{ in, stem, kind string }{
		{"/a/b/报告.MD", "报告", "md"},
		{`C:\Users\x\设计稿.PPTX`, "设计稿", "pptx"},
		{"无扩展名", "无扩展名", ""},
		{"a/b/c.tar.gz", "c.tar", ""},
	}
	for _, c := range cases {
		if got := Stem(c.in); got != c.stem {
			t.Errorf("Stem(%q) = %q，期望 %q", c.in, got, c.stem)
		}
		if got := KindOf(c.in); got != c.kind {
			t.Errorf("KindOf(%q) = %q，期望 %q", c.in, got, c.kind)
		}
	}
}
