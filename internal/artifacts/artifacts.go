package artifacts

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"ruiyun-ui-test-platform-go/internal/config"
	"ruiyun-ui-test-platform-go/internal/models"
)

const ExtractVersion = 3

var ExtKinds = map[string]string{
	"docx": "docx", "doc": "docx",
	"pptx": "pptx", "ppt": "pptx",
	"xlsx": "excel", "xls": "excel", "csv": "excel",
	"pdf":  "pdf",
	"html": "html", "htm": "html",
	"md": "md", "markdown": "md",
	"txt": "txt",
	"png": "image", "jpg": "image", "jpeg": "image",
	"webp": "image", "gif": "image", "svg": "image",
}

var TargetKinds = map[string]map[string]bool{
	"word":    {"docx": true},
	"ppt":     {"pptx": true},
	"html":    {"html": true},
	"excel":   {"excel": true},
	"pdf":     {"pdf": true},
	"network": {},
}

var reProducer = regexp.MustCompile(`(?i)(write|save|export|generate|convert|render|download|upload)`)

var excludeTools = map[string]bool{
	"todo_create": true, "todo_complete": true, "todo_remove": true,
	"cron_create_job": true, "cron_preview_job": true,
	"read_memory": true, "write_memory": true, "edit_memory": true,
	"read_file": true, "read_skill_file": true, "grep_files": true, "search_session": true,
}

var resultPathKeys = []string{
	"md_file_name", "save_image_path", "relative_path",
	"output_path", "file_path", "full_path", "path",
}

var argPathKeys = []string{
	"relative_path", "output_path", "file_path",
	"filename", "file_name", "path",
}

// 转换类工具：args 里给的是**源文件名**
var sourceNameKeys = []string{"md_file_name"}

var reDrivePrefix = regexp.MustCompile(`^[A-Za-z]:/`)

var fullKeys = []string{"full_path", "workspace_path"}
var textKeys = []string{"content", "markdown_content", "file_content", "markdown", "text", "body"}

type Artifact struct {
	Kind       string `json:"kind"`
	RelPath    string `json:"rel_path"`
	FullPath   string `json:"full_path"`
	Text       string `json:"text"`
	SourceTool string `json:"source_tool"`
	Note       string `json:"note"`
}

type ArtifactSet struct {
	Items []*Artifact
}

func (s *ArtifactSet) Kinds() map[string]bool {
	res := make(map[string]bool)
	for _, a := range s.Items {
		res[a.Kind] = true
	}
	return res
}

func (s *ArtifactSet) Paths() []string {
	var res []string
	for _, a := range s.Items {
		p := a.RelPath
		if p == "" {
			p = a.FullPath
		}
		if p != "" {
			res = append(res, p)
		}
	}
	return res
}

func (s *ArtifactSet) Texts() [][2]string {
	var res [][2]string
	for _, a := range s.Items {
		if a.Text != "" {
			label := a.RelPath
			if label == "" {
				label = a.FullPath
			}
			if label == "" {
				label = a.Kind
			}
			res = append(res, [2]string{fmt.Sprintf("产出物:%s", label), a.Text})
		}
	}
	return res
}

func KindOf(path string) string {
	norm := strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(norm, "/")
	name := parts[len(parts)-1]
	if !strings.Contains(name, ".") {
		return ""
	}
	ext := strings.ToLower(name[strings.LastIndex(name, ".")+1:])
	return ExtKinds[ext]
}

func Stem(path string) string {
	norm := strings.ReplaceAll(path, "\\", "/")
	parts := strings.Split(norm, "/")
	name := parts[len(parts)-1]
	if strings.Contains(name, ".") {
		return strings.ToLower(name[:strings.LastIndex(name, ".")])
	}
	return strings.ToLower(name)
}

func pick(d map[string]any, keys []string) string {
	for _, k := range keys {
		if v, exists := d[k]; exists && v != nil {
			s := strings.TrimSpace(fmt.Sprintf("%v", v))
			if s != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

func IsProducer(toolName string) bool {
	return reProducer.MatchString(toolName) && !excludeTools[toolName]
}

func IsDeliverable(path string) bool {
	return KindOf(path) != ""
}

// ResolveAbsPath 只做「相对路径 → 拼接」，**不做任何存在性校验**。
//
// Deprecated: 展示用的绝对路径请用 DisplayAbsPath —— Python 侧
// （core/artifacts.resolve_abs_path）每一级候选都要求 is_file() 通过，
// 解析不到返回空串，界面据此显示「本机未找到」。
// 用本函数会给出**指向不存在文件**的路径，前端「查看产物」按钮会点空。
func ResolveAbsPath(relOrAbs string, workspaceRoot string) string {
	if relOrAbs == "" {
		return ""
	}
	p := filepath.Clean(relOrAbs)
	if filepath.IsAbs(p) {
		return p
	}
	if workspaceRoot == "" {
		cfg, _ := config.LoadConfigDict()
		eff := config.EffectiveConfig(cfg)
		if paths, ok := eff["paths"].(map[string]any); ok {
			workspaceRoot = fmt.Sprintf("%v", paths["workspace_root"])
		}
	}
	if workspaceRoot == "" {
		workspaceRoot = "."
	}
	return filepath.Join(workspaceRoot, p)
}

// callFailed 复刻 Python _call_failed：只有**明确失败**才算失败
// （结果里没有这些字段的工具一律按成功处理，不误杀）。
//
// 实测事故：convert_markdown_to_docx 返回 {"success": false, "error": ...} 时，
// 旧实现照样把它记成一件产物 —— 界面上就出现一个指向**不存在文件**的链接，
// 而真正产出的文件反倒没被记录。
func callFailed(obj map[string]any) bool {
	if len(obj) == 0 {
		return false
	}
	successIsTrue := false
	if b, isBool := obj["success"].(bool); isBool {
		if !b {
			return true // success is False
		}
		successIsTrue = true
	}
	if errVal, hasErr := obj["error"]; hasErr && truthy(errVal) && !successIsTrue {
		return true // 有 error 且 success is not True
	}
	return false
}

// truthy 复刻 Python 的真值判定（用于 error 字段）
func truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0
	case int:
		return t != 0
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}

// ExtractArtifacts 从轨迹的工具调用中抽取产出物。
// 逐行对齐 Python core.artifacts.extract_artifacts。
func ExtractArtifacts(trace *models.ExecutionTrace, workspaceRoot string) *ArtifactSet {
	items := []*Artifact{}
	if trace == nil {
		return &ArtifactSet{Items: items}
	}

	written := make(map[string]string) // stem(小写) -> 正文，供转换类回填

	for _, tc := range trace.ToolCalls {
		name := tc.Name
		args, _ := tc.Arguments.(map[string]any)
		if args == nil {
			args = map[string]any{}
		}
		obj, _ := tc.ResultObj.(map[string]any)
		if obj == nil {
			obj = map[string]any{}
		}

		// 失败的调用既不记产物、也不参与「同名正文回填」
		if callFailed(obj) {
			continue
		}

		body := pick(args, textKeys)
		if relArg := pick(args, []string{"relative_path"}); relArg != "" && body != "" {
			written[Stem(relArg)] = body
		}

		if !IsProducer(name) {
			continue
		}

		path := pick(obj, resultPathKeys)
		if path == "" {
			path = pick(args, argPathKeys)
		}
		if path == "" {
			path = pick(args, sourceNameKeys)
		}
		if path == "" {
			continue
		}
		kind := KindOf(path)
		if kind == "" {
			continue
		}

		// full_path 保留应用自报的原值；「本机绝对路径」由 DisplayAbsPath 另算
		full := pick(obj, fullKeys)
		if full == "" {
			full = pick(args, []string{"full_path"})
		}

		text := body
		if text == "" {
			text = pick(obj, textKeys)
		}

		note := ""
		if text == "" {
			if back, ok := written[Stem(path)]; ok && back != "" {
				text, note = back, "正文由同名写入调用回填"
			} else {
				note = "仅确认产物存在，正文不可抽取"
			}
		}
		if kind == "image" {
			note = strings.TrimPrefix(note+"；图像为二进制产物，正文不可抽取", "；")
		}

		items = append(items, &Artifact{
			Kind:       kind,
			RelPath:    path,
			FullPath:   full,
			Text:       text,
			SourceTool: name,
			Note:       note,
		})
	}

	return &ArtifactSet{Items: items}
}

// ---------------------------------------------------------------------------
// 本机绝对路径解析（复刻 Python core.artifacts.resolve_abs_path）
//
// 用途：界面「查看产物」按钮要调 POST /api/reveal（在文件管理器中定位），
// 而它只认绝对路径。**解析不到时必须返回空串**，让界面显示「本机未找到」，
// 而不是猜一个可能不存在的路径给用户。
// ---------------------------------------------------------------------------

func expandUser(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// okFile 复刻 _ok：必须**绝对路径且真实存在为文件**才返回（否则空串）
func okFile(p string) string {
	if p == "" {
		return ""
	}
	q := expandUser(p)
	if !filepath.IsAbs(q) {
		return ""
	}
	if fi, err := os.Stat(q); err == nil && fi.Mode().IsRegular() {
		return q
	}
	return ""
}

func suffixScore(relParts []string, cand string) int {
	candParts := strings.Split(strings.ToLower(filepath.ToSlash(cand)), "/")
	score := 0
	for i := 1; i <= len(relParts) && i <= len(candParts); i++ {
		if relParts[len(relParts)-i] != candParts[len(candParts)-i] {
			break
		}
		score++
	}
	return score
}

// searchByName 在 root 下按文件名有界查找；找不到返回空串。
//
// 要求至少最后两级路径（目录名 + 文件名）一致，否则宁可不给路径 ——
// 同名文件在工作区里很常见，只比文件名会定位到**另一个**文件。
func searchByName(root, name, rel string, budget int) string {
	if name == "" || root == "" {
		return ""
	}
	base := expandUser(root)
	st, err := os.Stat(base)
	if err != nil || !st.IsDir() {
		return ""
	}

	var relParts []string
	for _, p := range strings.Split(strings.ToLower(rel), "/") {
		if p != "" && p != "." && p != ".." {
			relParts = append(relParts, p)
		}
	}
	need := 1
	if len(relParts) > 1 {
		need = 2
	}

	best, bestScore, visited := "", 0, 0
	stop := false

	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		if stop {
			return
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		var files []string
		for _, e := range entries {
			if e.IsDir() {
				if strings.HasPrefix(e.Name(), ".") || depth+1 >= 4 {
					continue // 只找浅层：产物都写在空间根/一级子目录
				}
				walk(filepath.Join(dir, e.Name()), depth+1)
				if stop {
					return
				}
			} else {
				files = append(files, e.Name())
			}
		}
		visited += len(files)
		for _, f := range files {
			if f != name {
				continue
			}
			cand := filepath.Join(dir, f)
			score := suffixScore(relParts, cand)
			if score > bestScore {
				if fi, err := os.Stat(cand); err == nil && fi.Mode().IsRegular() {
					best, bestScore = cand, score
					if bestScore >= len(relParts) { // 整段命中，无需继续找
						stop = true
						return
					}
				}
			}
		}
		if visited > budget { // 有界：超大工作区不做全量遍历
			stop = true
		}
	}
	walk(base, 0)

	if bestScore >= need {
		return best
	}
	return ""
}

// DisplayAbsPath 复刻 Python resolve_abs_path（六级回落，只读且有界）。
func DisplayAbsPath(a *Artifact, workspaceRoot string) string {
	if a == nil {
		return ""
	}
	rel := strings.ReplaceAll(strings.TrimSpace(a.RelPath), "\\", "/")
	full := strings.TrimSpace(a.FullPath)

	// 应用自报的 workspace_path 才是最权威的根：它可能是**目录**，
	// 也可能已是文件全路径。实测它与配置里假定的根**不是同一个目录**。
	var roots []string
	if full != "" {
		fp := expandUser(full)
		if st, err := os.Stat(fp); err == nil && st.IsDir() {
			roots = append(roots, fp)
		} else if filepath.Ext(fp) == "" {
			roots = append(roots, fp)
		} else {
			roots = append(roots, filepath.Dir(fp))
		}
	}

	ws := strings.TrimSpace(workspaceRoot)
	trim := func(r string) string { return strings.TrimRight(strings.TrimRight(r, "/"), "\\") }

	var cands []string
	if full != "" {
		cands = append(cands, full)
	}
	if rel != "" {
		cands = append(cands, rel)
		allRoots := append([]string{}, roots...)
		if ws != "" {
			allRoots = append(allRoots, trim(ws))
		}
		for _, r := range allRoots {
			r = trim(r)
			if r == "" {
				continue
			}
			cands = append(cands, r+"/"+strings.TrimLeft(rel, "/"))
			stripped := reDrivePrefix.ReplaceAllString(rel, "")
			cands = append(cands, r+"/"+strings.TrimLeft(stripped, "/"))
		}
	}
	for _, c := range cands {
		if hit := okFile(c); hit != "" {
			return hit
		}
	}

	name := ""
	if rel != "" {
		parts := strings.Split(rel, "/")
		name = parts[len(parts)-1]
	}
	searchRoots := append([]string{}, roots...)
	if ws != "" {
		searchRoots = append(searchRoots, ws)
	}
	for _, r := range searchRoots {
		if hit := searchByName(r, name, rel, 4000); hit != "" {
			return hit
		}
	}
	return ""
}
