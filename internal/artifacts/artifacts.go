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

// ExtractArtifacts 从轨迹的工具调用中抽取产出物
func ExtractArtifacts(trace *models.ExecutionTrace, workspaceRoot string) *ArtifactSet {
	if trace == nil {
		return &ArtifactSet{Items: []*Artifact{}}
	}

	byStem := make(map[string]*Artifact)
	var artifacts []*Artifact

	for _, tc := range trace.ToolCalls {
		if tc.Failed {
			continue
		}
		if !IsProducer(tc.Name) {
			continue
		}

		args, _ := tc.Arguments.(map[string]any)
		resObj, _ := tc.ResultObj.(map[string]any)

		resPath := pick(resObj, resultPathKeys)
		argPath := pick(args, argPathKeys)
		chosen := resPath
		if chosen == "" {
			chosen = argPath
		}
		if chosen == "" || !IsDeliverable(chosen) {
			continue
		}

		full := pick(resObj, fullKeys)
		if full == "" {
			full = pick(args, fullKeys)
		}
		if full == "" {
			full = ResolveAbsPath(chosen, workspaceRoot)
		}

		text := pick(args, textKeys)
		if text == "" {
			text = pick(resObj, textKeys)
		}
		if text == "" && full != "" {
			if KindOf(chosen) == "md" || KindOf(chosen) == "txt" || KindOf(chosen) == "html" {
				if fi, err := os.Stat(full); err == nil && fi.Size() < 2*1024*1024 {
					data, err := os.ReadFile(full)
					if err == nil {
						text = string(data)
					}
				}
			}
		}

		art := &Artifact{
			Kind:       KindOf(chosen),
			RelPath:    chosen,
			FullPath:   full,
			Text:       text,
			SourceTool: tc.Name,
		}

		// 回填 .md 正文给转化后的 docx
		st := Stem(chosen)
		if art.Kind == "docx" && art.Text == "" {
			if prev, ok := byStem[st]; ok && prev.Text != "" {
				art.Text = prev.Text
				art.Note = fmt.Sprintf("正文来自前序 %s", prev.RelPath)
			}
		}
		byStem[st] = art
		artifacts = append(artifacts, art)
	}

	return &ArtifactSet{Items: artifacts}
}
