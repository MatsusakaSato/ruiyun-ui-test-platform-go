// Package rounds 轮次归档读取层：
// 查询 / 加载 / 删除轮次，以及轮次产物清单的重算与回填。
//
// 严格遵循既定语义（字段名、取值优先级、分页与筛选规则），
// 下游是 4,120 行前端 JS，字段形状不得偏离。
package rounds

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"

	"ruiyun-ui-test-platform-go/internal/artifacts"
	"ruiyun-ui-test-platform-go/internal/config"
	"ruiyun-ui-test-platform-go/internal/logparser"
	"ruiyun-ui-test-platform-go/internal/models"
)

// RunIDPattern 轮次 ID 白名单：只允许字母/数字/下划线/连字符（挡 ../ 与绝对路径）
var RunIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// 产物重算缓存：(run_id, 抽取版本) -> {case_id: (artifacts, kinds)}
// 归档目录写完后内容不再变化，同进程内不必为列表/详情反复解析会话日志。
var (
	artBackfillMu    sync.Mutex
	artBackfillCache = map[string]map[string][2]any{}
)

// loadJSON 读轮次目录下的某个 JSON（缺失/损坏返回 nil）
func loadJSON(d string, name string) map[string]any {
	f := filepath.Join(d, name)
	st, err := os.Stat(f)
	if err != nil || st.IsDir() {
		return nil
	}
	raw, err := os.ReadFile(f)
	if err != nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprintf("%v", v)
}

// listRoundDirs 反向字典序枚举 rounds 目录
func listRoundDirs() []string {
	root := config.RoundsDir()
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// 排序按路径字符串比较；这里只用目录名，效果一致
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, filepath.Join(root, n))
	}
	return out
}

// QueryOptions query_rounds 的入参
type QueryOptions struct {
	DateFrom string
	DateTo   string
	Keyword  string
	Limit    int
	Offset   int
}

// Query 轮次归档的分页查询：时间范围 + 关键字，返回当前页。
//
// 两遍扫描：先只读 round_summary.json 做筛选与计数（轻），
// 再对需要返回的那一页做完整加载（含产物清单，重）。
func Query(opts QueryOptions) map[string]any {
	type row struct {
		dir string
		sum map[string]any
	}
	var rows []row
	for _, d := range listRoundDirs() {
		st, err := os.Stat(d)
		if err != nil || !st.IsDir() {
			continue
		}
		f := filepath.Join(d, "round_summary.json")
		if st2, err := os.Stat(f); err != nil || st2.IsDir() {
			continue
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s map[string]any
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		rows = append(rows, row{dir: d, sum: s})
	}
	totalAll := len(rows)

	df := firstN(asString(opts.DateFrom), 10)
	dt := firstN(asString(opts.DateTo), 10)
	kw := strings.ToLower(strings.TrimSpace(asString(opts.Keyword)))

	var filtered []row
	for _, r := range rows {
		day := firstN(asString(r.sum["finished_at"]), 10)
		if df != "" && (day == "" || day < df) {
			continue
		}
		if dt != "" && (day == "" || day > dt) {
			continue
		}
		if kw != "" {
			parts := []string{filepath.Base(r.dir), asString(r.sum["finished_at"])}
			if cs, ok := r.sum["cases"].([]any); ok {
				for _, c := range cs {
					if cm, ok := c.(map[string]any); ok {
						parts = append(parts, asString(cm["prompt"]))
					}
				}
			}
			hay := strings.ToLower(strings.Join(parts, " "))
			if !strings.Contains(hay, kw) {
				continue
			}
		}
		filtered = append(filtered, r)
	}

	total := len(filtered)
	limit := opts.Limit
	if limit == 0 {
		limit = 20
	}
	limit = maxInt(1, minInt(limit, 200))
	offset := opts.Offset
	if offset < 0 {
		offset = 0
	}
	page := sliceRows(filtered, offset, limit)

	out := []any{}
	for _, r := range page {
		detail := BackfillCaseArtifacts(loadJSON(r.dir, "round_detail.json"), r.dir)
		evaluation := loadJSON(r.dir, "evaluation.json")
		var casesOut []any
		if cs, ok := r.sum["cases"].([]any); ok {
			for _, c := range cs {
				cm, _ := c.(map[string]any)
				if cm == nil {
					continue
				}
				casesOut = append(casesOut, map[string]any{
					"case_id": asString(cm["case_id"]),
					"name":    asString(cm["name"]),
					"prompt":  asString(cm["prompt"]),
					"status":  asString(cm["status"]),
				})
			}
		}
		if casesOut == nil {
			casesOut = []any{}
		}
		summary, _ := r.sum["summary"].(map[string]any)
		if summary == nil {
			summary = map[string]any{}
		}
		reproSummary, _ := r.sum["repro_summary"].(map[string]any)
		if reproSummary == nil {
			reproSummary = map[string]any{}
		}
		roundSkills := r.sum["round_skills"]
		if roundSkills == nil {
			roundSkills = []any{}
		}
		out = append(out, map[string]any{
			"run_id":         filepath.Base(r.dir),
			"finished_at":    asValue(r.sum["finished_at"]),
			"run_mode":       asValue(r.sum["run_mode"]),
			"elapsed_s":      zeroDefault(r.sum["elapsed_s"]),
			"app_version":    asValue(r.sum["app_version"]),
			"summary":        summary,
			"repro_summary":  reproSummary,
			"round_skills":   roundSkills,
			"has_evaluation": evaluation != nil,
			"artifacts":      RoundArtifacts(r.dir, evaluation, detail),
			"cases":          casesOut,
		})
	}

	daysSet := map[string]bool{}
	for _, r := range rows {
		if fa := asString(r.sum["finished_at"]); fa != "" {
			daysSet[firstN(fa, 10)] = true
		}
	}
	days := make([]string, 0, len(daysSet))
	for d := range daysSet {
		days = append(days, d)
	}
	sort.Sort(sort.Reverse(sort.StringSlice(days)))
	latestDay := ""
	if len(days) > 0 {
		latestDay = days[0]
	}

	return map[string]any{
		"rounds":     out,
		"total":      total,
		"total_all":  totalAll,
		"offset":     offset,
		"limit":      limit,
		"latest_day": latestDay,
	}
}

// Load 读取一个轮次的完整归档（不存在返回 nil）
func Load(runID string) map[string]any {
	d := filepath.Join(config.RoundsDir(), runID)
	st, err := os.Stat(d)
	if err != nil || !st.IsDir() {
		return nil
	}
	var detail, summary map[string]any
	fd := filepath.Join(d, "round_detail.json")
	fs := filepath.Join(d, "round_summary.json")
	if st2, err := os.Stat(fd); err == nil && !st2.IsDir() {
		raw, err := os.ReadFile(fd)
		if err != nil {
			return nil
		}
		if err := json.Unmarshal(raw, &detail); err != nil {
			return nil
		}
	}
	if st2, err := os.Stat(fs); err == nil && !st2.IsDir() {
		raw, err := os.ReadFile(fs)
		if err != nil {
			return nil
		}
		if err := json.Unmarshal(raw, &summary); err != nil {
			return nil
		}
	}
	detail = BackfillCaseArtifacts(detail, d)
	evaluation := loadJSON(d, "evaluation.json")
	return map[string]any{
		"run_id":     runID,
		"summary":    summary,
		"detail":     detail,
		"evaluation": evaluation,
		"artifacts":  RoundArtifacts(d, evaluation, detail),
	}
}

// Delete 删除一个轮次归档目录。runIDPattern 校验 + 目录必须真实位于 ROUNDS 内。
// running 由调用方传入（该轮次是否正在运行），返回 (ok, msg)。
func Delete(runID string, running bool) (bool, string) {
	rid := strings.TrimSpace(asString(runID))
	if rid == "" {
		return false, "缺少 run_id"
	}
	if !RunIDPattern.MatchString(rid) {
		return false, fmt.Sprintf("run_id 含非法字符：%s", rid)
	}
	if running {
		return false, "该轮次正在运行，无法删除"
	}

	root, err := filepath.Abs(config.RoundsDir())
	if err != nil {
		root = config.RoundsDir()
	}
	target, err := filepath.Abs(filepath.Join(root, rid))
	if err != nil {
		target = filepath.Join(root, rid)
	}
	if target == root || !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return false, fmt.Sprintf("目标不在轮次目录内：%s", rid)
	}
	st, err := os.Stat(target)
	if err != nil || !st.IsDir() {
		return false, fmt.Sprintf("轮次不存在：%s", rid)
	}
	if err := os.RemoveAll(target); err != nil {
		return false, fmt.Sprintf("%T: %v", err, err)
	}
	return true, rid
}

// ArtifactItems 本轮产物条目，按来源优先级取第一份**非空**清单。
// 返回 (items, source)，source ∈ {"detail", "evaluation", ""}。
func ArtifactItems(detail map[string]any, evaluation map[string]any) ([]map[string]any, string) {
	d := detail
	if d == nil {
		d = map[string]any{}
	}
	var items []map[string]any
	if cs, ok := d["cases"].([]any); ok {
		for _, c := range cs {
			cm, _ := c.(map[string]any)
			if cm == nil {
				continue
			}
			as, _ := cm["artifacts"].([]any)
			for _, a := range as {
				am, _ := a.(map[string]any)
				if am == nil {
					continue
				}
				if asString(am["path"]) != "" || asString(am["abs_path"]) != "" {
					items = append(items, am)
				}
			}
		}
	}
	if len(items) > 0 {
		return items, "detail"
	}
	if as, ok := d["round_artifacts"].([]any); ok {
		for _, a := range as {
			am, _ := a.(map[string]any)
			if am == nil {
				continue
			}
			if asString(am["path"]) != "" || asString(am["abs_path"]) != "" {
				items = append(items, am)
			}
		}
	}
	if len(items) > 0 {
		return items, "detail"
	}
	if evaluation != nil {
		if cs, ok := evaluation["cases"].([]any); ok {
			for _, c := range cs {
				cm, _ := c.(map[string]any)
				if cm == nil {
					continue
				}
				if as, ok := cm["artifacts"].([]any); ok {
					for _, a := range as {
						if am, ok := a.(map[string]any); ok {
							items = append(items, am)
						}
					}
				}
			}
		}
	}
	if len(items) > 0 {
		return items, "evaluation"
	}
	return items, ""
}

// RoundArtifacts 本轮产物的落点概览：给界面一个「打开产物目录」的入口。
func RoundArtifacts(d string, evaluation map[string]any, detail map[string]any) map[string]any {
	items, source := ArtifactItems(detail, evaluation)

	var paths []string
	for _, a := range items {
		if p := strings.TrimSpace(asString(a["abs_path"])); p != "" {
			paths = append(paths, p)
		}
	}
	kindSet := map[string]bool{}
	for _, a := range items {
		if k := strings.TrimSpace(asString(a["kind"])); k != "" {
			kindSet[k] = true
		}
	}
	kinds := make([]string, 0, len(kindSet))
	for k := range kindSet {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)

	root := ""
	multiDir := false
	if len(paths) > 0 {
		parents := make([]string, 0, len(paths))
		parentSet := map[string]bool{}
		for _, p := range paths {
			par := filepath.Dir(p)
			parents = append(parents, par)
			parentSet[par] = true
		}
		multiDir = len(parentSet) > 1
		common := commonPath(parents)
		if common == "" {
			root = parents[0]
		} else if multiDir {
			root = common
		} else {
			root = parents[0]
		}
	}
	if root == "" && evaluation != nil {
		root = strings.TrimSpace(asString(evaluation["workspace_root"]))
	}
	if root == "" {
		root = d
	}

	uniquePaths := dedupeKeepOrder(paths)
	exists := false
	if root != "" {
		if st, err := os.Stat(root); err == nil && st.IsDir() {
			exists = true
		}
	}
	return map[string]any{
		"count":     len(items),
		"kinds":     kinds,
		"paths":     uniquePaths,
		"root":      root,
		"exists":    exists,
		"multi_dir": multiDir,
		"resolved":  len(paths) > 0,
		"source":    source,
	}
}

// BackfillCaseArtifacts 给旧归档补/重算逐用例产物清单（纯读日志、无副作用）。
func BackfillCaseArtifacts(detail map[string]any, d string) map[string]any {
	if detail == nil {
		return nil
	}
	casesAny, _ := detail["cases"].([]any)
	if len(casesAny) == 0 {
		return detail
	}
	const extractVersion = artifacts.ExtractVersion

	var stale []map[string]any
	for _, c := range casesAny {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		_, hasArtifacts := cm["artifacts"]
		if !hasArtifacts || toIntSafe(cm["artifacts_version"]) != extractVersion {
			stale = append(stale, cm)
		}
	}
	if len(stale) == 0 {
		return detail
	}

	cacheKey := ""
	if d != "" {
		cacheKey = filepath.Base(d) + "|" + fmt.Sprint(extractVersion)
	}
	if cacheKey != "" {
		artBackfillMu.Lock()
		cached := artBackfillCache[cacheKey]
		artBackfillMu.Unlock()
		if cached != nil {
			for _, c := range stale {
				cid := asString(c["case_id"])
				hit, ok := cached[cid]
				if !ok {
					continue
				}
				c["artifacts"] = hit[0]
				c["artifact_kinds"] = hit[1]
				c["artifacts_version"] = extractVersion
			}
			return detail
		}
	}

	wsRoot := ""
	if cfg, err := config.LoadConfigDict(); err == nil {
		eff := config.EffectiveConfig(cfg)
		if paths, ok := eff["paths"].(map[string]any); ok {
			wsRoot = asString(paths["workspace_root"])
		}
	}

	fresh := map[string][2]any{}
	for _, c := range stale {
		sess := strings.TrimSpace(asString(c["session_dir"]))
		arts := []any{}
		if sess != "" {
			if st, err := os.Stat(sess); err == nil && st.IsDir() {
				var trace *models.ExecutionTrace
				if t, err := logparser.ParseSession(sess); err == nil {
					trace = t
				}
				if trace != nil {
					aSet := artifacts.ExtractArtifacts(trace, wsRoot)
					for _, a := range aSet.Items {
						arts = append(arts, map[string]any{
							"kind":     a.Kind,
							"path":     a.RelPath,
							"note":     a.Note,
							"abs_path": artifacts.DisplayAbsPath(a, wsRoot),
						})
					}
				}
			}
		}
		kindSet := map[string]bool{}
		for _, a := range arts {
			am, _ := a.(map[string]any)
			if am == nil {
				continue
			}
			if k := asString(am["kind"]); k != "" {
				kindSet[k] = true
			}
		}
		kinds := make([]string, 0, len(kindSet))
		for k := range kindSet {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		c["artifacts"] = arts
		c["artifact_kinds"] = kinds
		c["artifacts_version"] = extractVersion
		fresh[asString(c["case_id"])] = [2]any{arts, kinds}
	}
	if cacheKey != "" {
		artBackfillMu.Lock()
		artBackfillCache[cacheKey] = fresh
		artBackfillMu.Unlock()
	}
	return detail
}

// ------------------------------------------------------------------ 工具

func toIntSafe(v any) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	case json.Number:
		if i, err := t.Int64(); err == nil {
			return int(i)
		}
	}
	return -1
}

func asValue(v any) any {
	if v == nil {
		return ""
	}
	return v
}

func zeroDefault(v any) any {
	if v == nil {
		return 0
	}
	return v
}

func firstN(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func sliceRows[T any](rows []T, offset, limit int) []T {
	if offset >= len(rows) {
		return nil
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[offset:end]
}

func dedupeKeepOrder(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// commonPath 求公共路径前缀（对已经是绝对路径的输入按分隔符比较）
func commonPath(paths []string) string {
	if len(paths) == 0 {
		return ""
	}
	sep := string(os.PathSeparator)
	split := func(p string) []string {
		p = filepath.Clean(p)
		parts := strings.Split(p, sep)
		return parts
	}
	common := split(paths[0])
	for _, p := range paths[1:] {
		parts := split(p)
		n := 0
		for n < len(common) && n < len(parts) && common[n] == parts[n] {
			n++
		}
		common = common[:n]
	}
	if len(common) == 0 {
		return ""
	}
	out := strings.Join(common, sep)
	if out == "" {
		out = sep
	}
	return out
}
