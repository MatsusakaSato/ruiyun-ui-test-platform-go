package testcasedb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"

	_ "modernc.org/sqlite"

	"ruiyun-ui-test-platform-go/internal/config"
	"ruiyun-ui-test-platform-go/internal/models"
)

var (
	dbMu sync.Mutex
)

// GetDBPath 获取数据库文件路径
func GetDBPath(customPath string) string {
	if customPath != "" {
		p := config.ExpandHome(customPath)
		_ = os.MkdirAll(filepath.Dir(p), 0755)
		return p
	}
	return config.PresetPath()
}

// GetConnection 获取 SQLite 连接（纯 Go，启 WAL）
func GetConnection(customPath string) (*sql.DB, error) {
	path := GetDBPath(customPath)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	_, _ = db.Exec("PRAGMA journal_mode=WAL;")
	_, _ = db.Exec("PRAGMA foreign_keys=ON;")
	return db, nil
}

// InitDB 初始化数据库表与索引
func InitDB(customPath string) (string, error) {
	path := GetDBPath(customPath)
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return "", err
	}
	defer db.Close()

	schema := `
	CREATE TABLE IF NOT EXISTS testcases (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL,
		scene TEXT NOT NULL DEFAULT '',
		difficulty TEXT NOT NULL DEFAULT '',
		-- 历史遗留列：已无任何代码读写（原来的「测试目标」功能已下线）。
		-- 保留是为了不做破坏性的数据迁移 —— 老库里的既有数据原样留着，
		-- 新写入的行走该列的 DEFAULT '[]'。
		targets TEXT NOT NULL DEFAULT '[]',
		attachment INTEGER NOT NULL DEFAULT 0,
		expect_tools TEXT NOT NULL DEFAULT '[]',
		attachments TEXT NOT NULL DEFAULT '[]',
		-- 自定义标签：JSON 字符串数组，数量与内容都不限（逗号除外，见下）。
		-- 逗号是标签输入的确认键、也是筛选参数的传输分隔符，所以标签内容里不会有逗号。
		tags TEXT NOT NULL DEFAULT '[]',
		seq INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now', 'localtime')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now', 'localtime'))
	);
	CREATE INDEX IF NOT EXISTS idx_testcases_seq ON testcases(seq);
	CREATE INDEX IF NOT EXISTS idx_testcases_scene ON testcases(scene);
	CREATE INDEX IF NOT EXISTS idx_testcases_attachment ON testcases(attachment);
	`
	if _, err := db.Exec(schema); err != nil {
		return "", err
	}
	// 老库里的 testcases 已存在，CREATE TABLE IF NOT EXISTS 不会补列，
	// 因此这里逐列做一次幂等迁移（新库刚建出来时该列已存在，直接跳过）。
	if err := ensureColumn(db, "testcases", "difficulty", "TEXT NOT NULL DEFAULT ''"); err != nil {
		return "", err
	}
	if err := ensureColumn(db, "testcases", "tags", "TEXT NOT NULL DEFAULT '[]'"); err != nil {
		return "", err
	}
	return path, nil
}

// ensureColumn 若表里没有该列就 ADD COLUMN 补上（SQLite 的 ALTER TABLE ADD COLUMN）。
// 只补列、不重建表，既有行的数据原样保留，新列取 decl 里的默认值。
func ensureColumn(db *sql.DB, table, column, decl string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return err
	}
	found := false
	for rows.Next() {
		var (
			cid       int
			name      string
			ctype     string
			notNull   int
			dfltValue sql.NullString
			pk        int
		)
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dfltValue, &pk); err != nil {
			rows.Close()
			return err
		}
		if name == column {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	if found {
		return nil
	}
	_, err = db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, column, decl))
	return err
}

func rowToCase(id, name, prompt, scene, difficulty, tagsStr string, attachment int, expectToolsStr, attachmentsStr string) map[string]any {
	var expectTools []string
	if expectToolsStr != "" {
		_ = json.Unmarshal([]byte(expectToolsStr), &expectTools)
	}
	if expectTools == nil {
		expectTools = []string{}
	}

	var attachments []string
	if attachmentsStr != "" {
		_ = json.Unmarshal([]byte(attachmentsStr), &attachments)
	}
	if attachments == nil {
		attachments = []string{}
	}

	var tags []string
	if tagsStr != "" {
		_ = json.Unmarshal([]byte(tagsStr), &tags)
	}

	labels := make(map[string]any)
	if scene != "" {
		labels["scene"] = scene
	}
	if difficulty != "" {
		labels["difficulty"] = difficulty
	}
	// 自定义标签以字符串数组挂在 labels.tags 下（沿用难度「只作为标签回传」的口径）；
	// 一条都没有时不出现该键，前端据此判断有没有标签。
	if len(tags) > 0 {
		labels["tags"] = tags
	}
	if attachment != 0 {
		labels["attachment"] = true
	}

	// 字段集合必须严格固定为：
	// id / name / prompt / expect_tools / labels / attachments（+ 有附件时 attachment）。
	// 不得多输出顶层 scene —— 前端只读 labels.scene，属契约偏离（HTTP 实测确认），已删除。
	// 难度同理，只作为 labels 里的一个标签回传，不新增顶层字段；自定义标签同理（labels.tags）。
	res := map[string]any{
		"id":           id,
		"name":         name,
		"prompt":       prompt,
		"expect_tools": expectTools,
		"labels":       labels,
		"attachments":  attachments,
	}
	if len(attachments) > 0 {
		res["attachment"] = attachments[0]
	}
	return res
}

// CountCases 获取预设库中的用例总数
func CountCases(customPath string) (int, error) {
	_, err := InitDB(customPath)
	if err != nil {
		return 0, err
	}
	db, err := GetConnection(customPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM testcases").Scan(&count)
	return count, err
}

// GetPresetCases 读取预设用例库中的全部用例
func GetPresetCases(customPath string) ([]map[string]any, error) {
	_, err := InitDB(customPath)
	if err != nil {
		return nil, err
	}
	db, err := GetConnection(customPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query("SELECT id, name, prompt, scene, difficulty, tags, attachment, expect_tools, attachments FROM testcases ORDER BY seq ASC, id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cases []map[string]any
	for rows.Next() {
		var id, name, prompt, scene, difficulty, tagsStr, expectToolsStr, attachmentsStr string
		var attachment int
		if err := rows.Scan(&id, &name, &prompt, &scene, &difficulty, &tagsStr, &attachment, &expectToolsStr, &attachmentsStr); err == nil {
			cases = append(cases, rowToCase(id, name, prompt, scene, difficulty, tagsStr, attachment, expectToolsStr, attachmentsStr))
		}
	}
	return cases, nil
}

// QueryPresetCases 预设用例库分页与条件筛选查询
// scenes / difficulties / tags 为多选筛选（空切片表示不限），attachment 取 "" / "yes" / "no"。
// 多选语义两套：scenes / difficulties 命中任意一个即算匹配（「或」）；
// tags 要求所选标签全部命中（「且」）—— 标签是用来「同时满足几个条件」地收窄的。
func QueryPresetCases(keyword string, scenes, difficulties, tags []string, attachment string, ids []string, limit, offset int, order, customPath string) (map[string]any, error) {
	_, err := InitDB(customPath)
	if err != nil {
		return nil, err
	}
	db, err := GetConnection(customPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	// 1. 全局筛选属性
	scRows, err := db.Query("SELECT DISTINCT scene FROM testcases WHERE scene != '' ORDER BY scene")
	allScenes := []string{} // 必须是非 nil 空切片：nil 切片会 JSON 序列化成 null
	if err == nil {
		for scRows.Next() {
			var sc string
			_ = scRows.Scan(&sc)
			allScenes = append(allScenes, sc)
		}
		scRows.Close()
	}

	allDifficulties := []string{} // 同上：空集合必须是 []，不能是 null
	dfRows, err := db.Query("SELECT DISTINCT difficulty FROM testcases WHERE difficulty != '' ORDER BY difficulty")
	if err == nil {
		for dfRows.Next() {
			var df string
			_ = dfRows.Scan(&df)
			allDifficulties = append(allDifficulties, df)
		}
		dfRows.Close()
	}

	// 自定义标签是标签列里的 JSON 数组，SQL 侧没有 DISTINCT 可用的单值；
	// 选项列表与（有筛选时的）命中 id 都从这一次全表扫里算出来。
	allTags, taggedIDs := scanTags(db)

	// 2. 动态过滤条件
	var whereClauses []string
	var params []any
	whereClauses = append(whereClauses, "1=1")

	if len(ids) > 0 {
		var validIDs []string
		for _, id := range ids {
			s := strings.TrimSpace(id)
			if s != "" {
				validIDs = append(validIDs, s)
			}
		}
		if len(validIDs) > 0 {
			placeholders := make([]string, len(validIDs))
			for i, vid := range validIDs {
				placeholders[i] = "?"
				params = append(params, vid)
			}
			whereClauses = append(whereClauses, fmt.Sprintf("id IN (%s)", strings.Join(placeholders, ",")))
		}
	}

	kw := strings.ToLower(strings.TrimSpace(keyword))
	if kw != "" {
		whereClauses = append(whereClauses, "(LOWER(id) LIKE ? OR LOWER(name) LIKE ? OR LOWER(prompt) LIKE ?)")
		kwParam := "%" + kw + "%"
		params = append(params, kwParam, kwParam, kwParam)
	}

	// 多选场景同样是「或」语义：命中任意一个即算匹配（scene 列仍是单值，
	// 多选只是把多个候选值放进 IN，不改变存储格式与去重键）。
	var scList []string
	for _, sc := range scenes {
		if s := strings.TrimSpace(sc); s != "" {
			scList = append(scList, s)
		}
	}
	if len(scList) > 0 {
		ph := strings.TrimRight(strings.Repeat("?,", len(scList)), ",")
		whereClauses = append(whereClauses, fmt.Sprintf("scene IN (%s)", ph))
		for _, s := range scList {
			params = append(params, s)
		}
	}

	// 严格只认 yes / no，其余值一律忽略筛选。
	// 即 `if attachment == "yes": ... elif attachment == "no":` 的语义，
	// 既不 trim 也不小写 —— 多接受 "1"/"true" 会让同一请求得到不同结果。
	if attachment == "yes" {
		whereClauses = append(whereClauses, "attachment = 1")
	} else if attachment == "no" {
		whereClauses = append(whereClauses, "attachment = 0")
	}

	// 多选难度同样是「或」语义，与 scene 一致按列精确匹配。
	// 难度值是自由文本（界面上给 易/中/难，导入可能带别的值），不做大小写/模糊处理。
	// 难度为空串的行（老数据）既不出现在选项里，也不会被任何一档筛中 ——
	// IN 里的候选值都是非空串，空难度自然匹配不上。
	var dfList []string
	for _, df := range difficulties {
		if d := strings.TrimSpace(df); d != "" {
			dfList = append(dfList, d)
		}
	}
	if len(dfList) > 0 {
		ph := strings.TrimRight(strings.Repeat("?,", len(dfList)), ",")
		whereClauses = append(whereClauses, fmt.Sprintf("difficulty IN (%s)", ph))
		for _, d := range dfList {
			params = append(params, d)
		}
	}

	// 自定义标签是「且」语义（与场景 / 难度的「或」不同）：所选标签必须**全部命中**才算匹配。
	// 标签存在 JSON 数组列里，没法直接用 IN —— 这里换成「在 Go 侧算出命中的 id 集合」：
	//   * 不用 LIKE '%"tag"%'：标签里带引号/反斜杠时 LIKE 会误判，也不能正确转义；
	//   * 不依赖 SQLite 的 JSON1（json_each）：少一个运行时能力假设。
	// 命中集为空时补 1=0 —— 拼 id IN () 是非法 SQL，且「没有任何用例同时带着这几个标签」本来就该返回空集。
	var tagList []string
	seenTag := make(map[string]bool)
	for _, tg := range tags {
		t := strings.TrimSpace(tg)
		if t != "" && !seenTag[t] {
			seenTag[t] = true
			tagList = append(tagList, t)
		}
	}
	if len(tagList) > 0 {
		var hitIDs []string
		for id, list := range taggedIDs {
			if hasAllTags(list, tagList) {
				hitIDs = append(hitIDs, id)
			}
		}
		if len(hitIDs) == 0 {
			whereClauses = append(whereClauses, "1=0")
		} else {
			sort.Strings(hitIDs) // 参数顺序稳定，便于排查
			ph := strings.TrimRight(strings.Repeat("?,", len(hitIDs)), ",")
			whereClauses = append(whereClauses, fmt.Sprintf("id IN (%s)", ph))
			for _, id := range hitIDs {
				params = append(params, id)
			}
		}
	}

	whereSQL := strings.Join(whereClauses, " AND ")

	// 3. 统计总数
	countSQL := fmt.Sprintf("SELECT COUNT(*) FROM testcases WHERE %s", whereSQL)
	var total int
	if err := db.QueryRow(countSQL, params...).Scan(&total); err != nil {
		return nil, err
	}

	// 4. 分页查询
	sortOrder := "DESC"
	if strings.ToLower(order) == "asc" {
		sortOrder = "ASC"
	}
	// limit 的规则是 max(1, min(limit, 500))，但 0 视为「未指定」而回落到 50。
	// 关键是 0 这一路只在 limit 恰为 0 时生效，负数不回落到 50，
	// 所以 limit=-5 应当被钳到 1 而不是回落到 50；上限 500 也必须有。
	if limit == 0 {
		limit = 50
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}

	listSQL := fmt.Sprintf("SELECT id, name, prompt, scene, difficulty, tags, attachment, expect_tools, attachments FROM testcases WHERE %s ORDER BY seq %s, id %s LIMIT ? OFFSET ?",
		whereSQL, sortOrder, sortOrder)
	listParams := append(params, limit, offset)

	rows, err := db.Query(listSQL, listParams...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cases := []map[string]any{} // 空结果必须是 []，不能是 null
	for rows.Next() {
		var id, name, prompt, scene, difficulty, tagsStr, expectToolsStr, attachmentsStr string
		var attachment int
		if err := rows.Scan(&id, &name, &prompt, &scene, &difficulty, &tagsStr, &attachment, &expectToolsStr, &attachmentsStr); err == nil {
			cases = append(cases, rowToCase(id, name, prompt, scene, difficulty, tagsStr, attachment, expectToolsStr, attachmentsStr))
		}
	}

	return map[string]any{
		"cases":        cases,
		"total":        total,
		"offset":       offset,
		"limit":        limit,
		"scenes":       allScenes,
		"difficulties": allDifficulties,
		"tags":         allTags,
	}, nil
}

// scanTags 一次扫全表：返回
//
//	allTags    标签选项列表（去重、升序；空集合是 [] 而不是 null）
//	taggedIDs  case id -> 该用例的标签（只含确有标签的行）
//
// 标签列是 JSON 字符串数组，解析失败（老数据 / 手工改坏）的行按「没有标签」处理，不报错。
func scanTags(db *sql.DB) (allTags []string, taggedIDs map[string][]string) {
	allTags = []string{}
	taggedIDs = map[string][]string{}

	rows, err := db.Query("SELECT id, tags FROM testcases")
	if err != nil {
		return allTags, taggedIDs
	}
	defer rows.Close()

	seen := map[string]bool{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			continue
		}
		var list []string
		if raw == "" || raw == "[]" || json.Unmarshal([]byte(raw), &list) != nil {
			continue
		}
		if len(list) == 0 {
			continue
		}
		taggedIDs[id] = list
		for _, t := range list {
			if t == "" || seen[t] {
				continue
			}
			seen[t] = true
			allTags = append(allTags, t)
		}
	}
	sort.Strings(allTags)
	return allTags, taggedIDs
}

// hasAllTags 判断一条用例的标签里是否**同时**包含 want 里的每一个（「且」语义的来源）。
// 用例标签数一般只有几个，直接双循环即可；want 已在调用侧去过重。
func hasAllTags(caseTags, want []string) bool {
	has := make(map[string]bool, len(caseTags))
	for _, t := range caseTags {
		has[t] = true
	}
	for _, w := range want {
		if !has[w] {
			return false
		}
	}
	return true
}

// CaseDedupeKeyWithAtts 支持将具体附件文件名纳入去重键计算，避免不同附件的同名 Prompt 被误判为重复。
//
// 键格式（字段顺序固定，按 `||` 拼接）：
//
//	CleanWhitespace(prompt) || CleanWhitespace(scene) || CleanWhitespace(difficulty) || 附件段
//
// 附件段：无可识别附件时为 "0"；只有「需要附件」标记而无文件名时为 "1"；
// 有文件名时为 "att:" + 基名去重排序后逗号连接。
//
// prompt / scene / difficulty 一律按「删除全部空白」归一化（models.CleanWhitespace，
// 与评估器 normPrompt、预设索引键同一套空白语义）。若这里只用 TrimSpace，
// 「写 一首诗」与「写一首诗」会各自入库，但运行时索引键相同 —— 索引互相覆盖，
// 去重也就名存实亡。全角空格/NBSP/换行是最常见的来源（中英混排与 Word 复制）。
// 难度是标签的一部分，因此与 scene 同等对待、一起参与拼键。
//
// 已下线的「测试目标」不再进键：历史上 targets 字段不同的两条用例，
// 只要 prompt / scene / difficulty / 附件相同就算重复。
func CaseDedupeKeyWithAtts(prompt, scene, difficulty string, hasAttachment bool, attachments []string) string {
	normPrompt := models.CleanWhitespace(prompt)
	normScene := models.CleanWhitespace(scene)
	normDifficulty := models.CleanWhitespace(difficulty)

	var cleanAtts []string
	seenAtt := make(map[string]bool)
	for _, a := range attachments {
		ca := strings.TrimSpace(a)
		if ca != "" {
			base := filepath.Base(ca)
			if !seenAtt[base] {
				seenAtt[base] = true
				cleanAtts = append(cleanAtts, base)
			}
		}
	}
	sort.Strings(cleanAtts)
	attPart := "0"
	if len(cleanAtts) > 0 {
		attPart = "att:" + strings.Join(cleanAtts, ",")
	} else if hasAttachment {
		attPart = "1"
	}
	return fmt.Sprintf("%s||%s||%s||%s", normPrompt, normScene, normDifficulty, attPart)
}

// CaseDedupeKey 计算用例去重唯一键：prompt + scene + difficulty + attachment
func CaseDedupeKey(prompt, scene, difficulty string, hasAttachment bool) string {
	return CaseDedupeKeyWithAtts(prompt, scene, difficulty, hasAttachment, nil)
}

func extractCaseFields(item map[string]any) (prompt, scene, difficulty string, tags []string, hasAttachment int, expectTools, attachments []string) {
	prompt = strings.TrimSpace(fmt.Sprintf("%v", item["prompt"]))
	if prompt == "<nil>" {
		prompt = ""
	}

	// 优先从 labels 提取
	rawLabels, _ := item["labels"].(map[string]any)
	if rawLabels != nil {
		if s, ok := rawLabels["scene"]; ok && s != nil {
			scene = strings.TrimSpace(fmt.Sprintf("%v", s))
			if scene == "<nil>" {
				scene = ""
			}
		}
		if s, ok := rawLabels["difficulty"]; ok && s != nil {
			difficulty = strings.TrimSpace(fmt.Sprintf("%v", s))
			if difficulty == "<nil>" {
				difficulty = ""
			}
		}
		if att, ok := rawLabels["attachment"].(bool); ok && att {
			hasAttachment = 1
		}
	}

	// 自定义标签：labels.tags 优先，顶层 tags 兜底（与 scene / difficulty 同一套取值口径）。
	// 只认数组 —— Excel 里恰好有个叫 tags 的列时会是字符串，那种值忽略掉，别写坏标签列。
	if rawLabels != nil {
		tags = cleanTags(rawLabels["tags"])
	}
	if len(tags) == 0 {
		tags = cleanTags(item["tags"])
	}

	// 顶层覆盖/兜底
	if scene == "" {
		if s, ok := item["scene"]; ok && s != nil {
			scene = strings.TrimSpace(fmt.Sprintf("%v", s))
			if scene == "<nil>" {
				scene = ""
			}
		}
	}
	if difficulty == "" {
		if s, ok := item["difficulty"]; ok && s != nil {
			difficulty = strings.TrimSpace(fmt.Sprintf("%v", s))
			if difficulty == "<nil>" {
				difficulty = ""
			}
		}
	}
	if hasAttachment == 0 {
		if item["attachment"] == true {
			hasAttachment = 1
		}
	}

	if att, ok := item["attachments"].([]any); ok {
		for _, a := range att {
			s := strings.TrimSpace(fmt.Sprintf("%v", a))
			if s != "" && s != "<nil>" {
				attachments = append(attachments, s)
			}
		}
	} else if att, ok := item["attachments"].([]string); ok {
		attachments = att
	}
	if len(attachments) > 0 {
		hasAttachment = 1
	}

	if exp, ok := item["expect_tools"].([]any); ok {
		for _, e := range exp {
			s := strings.TrimSpace(fmt.Sprintf("%v", e))
			if s != "" && s != "<nil>" {
				expectTools = append(expectTools, s)
			}
		}
	} else if exp, ok := item["expect_tools"].([]string); ok {
		expectTools = exp
	}
	// 该列是 TEXT NOT NULL DEFAULT '[]'：nil 会被 json.Marshal 成 "null" 写进列里，
	// 而契约要求是数组。空时统一给空切片。
	if expectTools == nil {
		expectTools = []string{}
	}

	return
}

// cleanTags 归一化自定义标签：只接受字符串数组（[]any / []string），
// 逐个 TrimSpace、丢掉空串、按首次出现去重（顺序即用户输入顺序）；其余类型一律当作没有标签。
// 标签数量与内容都不设上限 —— 只有空串和重复项会被丢掉。
// 返回的切片永远非 nil：调用方要直接 json.Marshal 落库，nil 会编成 "null" 而不是 "[]"。
func cleanTags(raw any) []string {
	var src []string
	switch v := raw.(type) {
	case []string:
		src = v
	case []any:
		for _, it := range v {
			src = append(src, fmt.Sprintf("%v", it))
		}
	default:
		return []string{}
	}

	out := make([]string, 0, len(src))
	seen := make(map[string]bool, len(src))
	for _, t := range src {
		s := strings.TrimSpace(t)
		if s == "" || s == "<nil>" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// AddPresetCase 单条新增预设用例（支持提问与标签完全一致自动去重）
func AddPresetCase(item map[string]any, customPath string) (bool, string, map[string]any) {
	_, err := InitDB(customPath)
	if err != nil {
		return false, fmt.Sprintf("初始化数据库失败: %v", err), nil
	}

	dbMu.Lock()
	defer dbMu.Unlock()

	db, err := GetConnection(customPath)
	if err != nil {
		return false, fmt.Sprintf("连接数据库失败: %v", err), nil
	}
	defer db.Close()

	prompt, scene, difficulty, tags, hasAttachment, expectTools, attachments := extractCaseFields(item)
	if prompt == "" {
		return false, "提问为必填", nil
	}

	tx, err := db.Begin()
	if err != nil {
		return false, fmt.Sprintf("开启事务失败: %v", err), nil
	}
	defer tx.Rollback()

	// 查重：提问和标签（scene, difficulty, attachment/attachments）完全一致的用例视为重复。
	// 去重键按「删除全部空白」归一化，SQLite 的 trim()/replace() 覆盖不了 29 个空白码点，
	// 因此这里取全表候选、在 Go 侧算键比对；单条新增是用户动作，全表扫描的量级无关紧要。
	currDedupeKey := CaseDedupeKeyWithAtts(prompt, scene, difficulty, hasAttachment == 1, attachments)
	existingRows, err := tx.Query("SELECT id, prompt, scene, difficulty, attachment, attachments FROM testcases")
	if err == nil {
		defer existingRows.Close()
		for existingRows.Next() {
			var eid, eprompt, escene, edifficulty, eattachmentsStr string
			var eattachment int
			if err := existingRows.Scan(&eid, &eprompt, &escene, &edifficulty, &eattachment, &eattachmentsStr); err == nil {
				var eattachments []string
				if eattachmentsStr != "" {
					_ = json.Unmarshal([]byte(eattachmentsStr), &eattachments)
				}
				if CaseDedupeKeyWithAtts(eprompt, escene, edifficulty, eattachment == 1, eattachments) == currDedupeKey {
					return false, fmt.Sprintf("用例已存在（编号：%s）", eid), nil
				}
			}
		}
	}

	var maxSeq int
	_ = tx.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM testcases").Scan(&maxSeq)

	// 同时扫描所有已有的 CASE-NNN 编号，确保序号永不冲突
	idRows, err := tx.Query("SELECT id FROM testcases WHERE id LIKE 'CASE-%'")
	if err == nil {
		defer idRows.Close()
		for idRows.Next() {
			var cid string
			if err := idRows.Scan(&cid); err == nil {
				if s := CaseSeq(cid); s > maxSeq {
					maxSeq = s
				}
			}
		}
	}
	nextSeq := maxSeq + 1

	// 编号与名称一律由服务端生成：编号 = CASE-NNN（序号），名称 = 场景-NNN（无场景时 自定义-NNN）。
	caseID := fmt.Sprintf("CASE-%03d", nextSeq)
	name := ""
	if scene != "" {
		name = fmt.Sprintf("%s-%03d", scene, nextSeq)
	} else {
		name = fmt.Sprintf("自定义-%03d", nextSeq)
	}

	tagsJSON, _ := json.Marshal(tags)
	attJSON, _ := json.Marshal(attachments)
	expJSON, _ := json.Marshal(expectTools)

	insertSQL := `
	INSERT INTO testcases (id, name, prompt, scene, difficulty, tags, attachment, expect_tools, attachments, seq, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now', 'localtime'), datetime('now', 'localtime'))
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name,
		prompt=excluded.prompt,
		scene=excluded.scene,
		difficulty=excluded.difficulty,
		tags=excluded.tags,
		attachment=excluded.attachment,
		expect_tools=excluded.expect_tools,
		attachments=excluded.attachments,
		seq=excluded.seq,
		updated_at=datetime('now', 'localtime')
	`
	if _, err := tx.Exec(insertSQL, caseID, name, prompt, scene, difficulty, string(tagsJSON), hasAttachment, string(expJSON), string(attJSON), nextSeq); err != nil {
		return false, fmt.Sprintf("写入数据库失败: %v", err), nil
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Sprintf("提交事务失败: %v", err), nil
	}

	resCase := rowToCase(caseID, name, prompt, scene, difficulty, string(tagsJSON), hasAttachment, string(expJSON), string(attJSON))
	return true, fmt.Sprintf("已成功添加用例 %s", caseID), resCase
}

// UpdatePresetCase 改写已有预设用例：编号（id）、序号（seq）、创建时间都不动，只改内容与 updated_at。
//
// item 必须带 id，字段口径与 AddPresetCase 完全一致（prompt / labels / attachments）。
// 判重跳过自己那一行 —— 只改自定义标签（或任何字段原地改回原值）不会和自己撞车，
// 而改完与**另一条**用例完全一致时仍要拒绝，否则库里会出现两条一模一样的用例。
func UpdatePresetCase(item map[string]any, customPath string) (bool, string, map[string]any) {
	id := strings.TrimSpace(fmt.Sprintf("%v", item["id"]))
	if id == "<nil>" {
		id = ""
	}
	if id == "" {
		return false, "缺少用例编号（id）", nil
	}

	if _, err := InitDB(customPath); err != nil {
		return false, fmt.Sprintf("初始化数据库失败: %v", err), nil
	}

	dbMu.Lock()
	defer dbMu.Unlock()
	db, err := GetConnection(customPath)
	if err != nil {
		return false, fmt.Sprintf("连接数据库失败: %v", err), nil
	}
	defer db.Close()

	prompt, scene, difficulty, tags, hasAttachment, expectTools, attachments := extractCaseFields(item)
	if prompt == "" {
		return false, "提问为必填", nil
	}

	tx, err := db.Begin()
	if err != nil {
		return false, fmt.Sprintf("开启事务失败: %v", err), nil
	}
	defer tx.Rollback()

	// 1. 目标行必须在；顺带把 seq 取出来（编号与序号都不变，名称按新场景用原 seq 重算）
	var seq int
	if err := tx.QueryRow("SELECT seq FROM testcases WHERE id = ?", id).Scan(&seq); err != nil {
		if err == sql.ErrNoRows {
			return false, fmt.Sprintf("用例不存在（编号：%s），可能已被删除", id), nil
		}
		return false, fmt.Sprintf("读取用例失败: %v", err), nil
	}

	// 2. 判重：提问 + 场景 + 难度 + 附件 与别人完全一致就拒绝（自定义标签不参与，见 CaseDedupeKeyWithAtts）
	currDedupeKey := CaseDedupeKeyWithAtts(prompt, scene, difficulty, hasAttachment == 1, attachments)
	dupRows, err := tx.Query("SELECT id, prompt, scene, difficulty, attachment, attachments FROM testcases WHERE id != ?", id)
	if err == nil {
		defer dupRows.Close()
		for dupRows.Next() {
			var oid, oprompt, oscene, odifficulty, oattachmentsStr string
			var oattachment int
			if err := dupRows.Scan(&oid, &oprompt, &oscene, &odifficulty, &oattachment, &oattachmentsStr); err == nil {
				var oattachments []string
				if oattachmentsStr != "" {
					_ = json.Unmarshal([]byte(oattachmentsStr), &oattachments)
				}
				if CaseDedupeKeyWithAtts(oprompt, oscene, odifficulty, oattachment == 1, oattachments) == currDedupeKey {
					return false, fmt.Sprintf("用例已存在（编号：%s）", oid), nil
				}
			}
		}
	}

	// 3. 名称一律由服务端按场景生成，与新增同口径（无场景时「自定义-NNN」）
	name := ""
	if scene != "" {
		name = fmt.Sprintf("%s-%03d", scene, seq)
	} else {
		name = fmt.Sprintf("自定义-%03d", seq)
	}

	tagsJSON, _ := json.Marshal(tags)
	attJSON, _ := json.Marshal(attachments)
	expJSON, _ := json.Marshal(expectTools)

	if _, err := tx.Exec(`
	UPDATE testcases
	SET name = ?, prompt = ?, scene = ?, difficulty = ?, tags = ?, attachment = ?,
	    expect_tools = ?, attachments = ?, updated_at = datetime('now', 'localtime')
	WHERE id = ?`,
		name, prompt, scene, difficulty, string(tagsJSON), hasAttachment,
		string(expJSON), string(attJSON), id); err != nil {
		return false, fmt.Sprintf("更新数据库失败: %v", err), nil
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Sprintf("提交事务失败: %v", err), nil
	}

	resCase := rowToCase(id, name, prompt, scene, difficulty, string(tagsJSON), hasAttachment, string(expJSON), string(attJSON))
	return true, fmt.Sprintf("已更新用例 %s", id), resCase
}

// AddPresetCasesEx 批量新增或覆盖预设用例，额外返回去重跳过条数
func AddPresetCasesEx(items []map[string]any, replace bool, customPath string) (bool, string, int, int) {
	if len(items) == 0 {
		return false, "没有可导入的用例（提问列全为空？）", 0, 0
	}

	_, err := InitDB(customPath)
	if err != nil {
		return false, fmt.Sprintf("初始化数据库失败: %v", err), 0, 0
	}

	dbMu.Lock()
	defer dbMu.Unlock()
	db, err := GetConnection(customPath)
	if err != nil {
		return false, fmt.Sprintf("连接数据库失败: %v", err), 0, 0
	}
	defer db.Close()

	tx, err := db.Begin()
	if err != nil {
		return false, fmt.Sprintf("开启事务失败: %v", err), 0, 0
	}
	defer tx.Rollback()

	if replace {
		if _, err := tx.Exec("DELETE FROM testcases"); err != nil {
			return false, fmt.Sprintf("清空既有用例失败: %v", err), 0, 0
		}
	}

	// 加载已有用例用于去重
	existingMap := make(map[string]string)
	if !replace {
		rows, err := tx.Query("SELECT id, prompt, scene, difficulty, attachment, attachments FROM testcases")
		if err == nil {
			for rows.Next() {
				var eid, eprompt, escene, edifficulty, eattsStr string
				var eatt int
				if err := rows.Scan(&eid, &eprompt, &escene, &edifficulty, &eatt, &eattsStr); err == nil {
					var eatts []string
					if eattsStr != "" {
						_ = json.Unmarshal([]byte(eattsStr), &eatts)
					}
					existingMap[CaseDedupeKeyWithAtts(eprompt, escene, edifficulty, eatt == 1, eatts)] = eid
				}
			}
			rows.Close()
		}
	}

	var maxSeq int
	_ = tx.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM testcases").Scan(&maxSeq)
	idRows, err := tx.Query("SELECT id FROM testcases WHERE id LIKE 'CASE-%'")
	if err == nil {
		for idRows.Next() {
			var cid string
			if err := idRows.Scan(&cid); err == nil {
				if s := CaseSeq(cid); s > maxSeq {
					maxSeq = s
				}
			}
		}
		idRows.Close()
	}

	stmt, err := tx.Prepare(`
	INSERT INTO testcases (id, name, prompt, scene, difficulty, tags, attachment, expect_tools, attachments, seq, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now', 'localtime'), datetime('now', 'localtime'))
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name,
		prompt=excluded.prompt,
		scene=excluded.scene,
		difficulty=excluded.difficulty,
		tags=excluded.tags,
		attachment=excluded.attachment,
		expect_tools=excluded.expect_tools,
		attachments=excluded.attachments,
		seq=excluded.seq,
		updated_at=datetime('now', 'localtime')
	`)
	if err != nil {
		return false, fmt.Sprintf("准备插入语句失败: %v", err), 0, 0
	}
	defer stmt.Close()

	seenInBatch := make(map[string]bool)
	added := 0
	skippedDup := 0

	for _, it := range items {
		prompt, scene, difficulty, tags, hasAttachment, expectTools, attachments := extractCaseFields(it)
		if prompt == "" {
			continue
		}

		dedupeKey := CaseDedupeKeyWithAtts(prompt, scene, difficulty, hasAttachment == 1, attachments)
		if (len(existingMap) > 0 && existingMap[dedupeKey] != "") || seenInBatch[dedupeKey] {
			skippedDup++
			continue
		}
		seenInBatch[dedupeKey] = true

		maxSeq++
		// 编号与名称一律由服务端生成（导入路径无场景时用「导入-NNN」）。
		caseID := fmt.Sprintf("CASE-%03d", maxSeq)
		name := ""
		if scene != "" {
			name = fmt.Sprintf("%s-%03d", scene, maxSeq)
		} else {
			name = fmt.Sprintf("导入-%03d", maxSeq)
		}

		tagsJSON, _ := json.Marshal(tags)
		attJSON, _ := json.Marshal(attachments)
		expJSON, _ := json.Marshal(expectTools)

		if _, err := stmt.Exec(caseID, name, prompt, scene, difficulty, string(tagsJSON), hasAttachment, string(expJSON), string(attJSON), maxSeq); err != nil {
			return false, fmt.Sprintf("批量写入失败: %v", err), 0, 0
		}
		added++
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Sprintf("提交事务失败: %v", err), 0, 0
	}

	var total int
	_ = db.QueryRow("SELECT COUNT(*) FROM testcases").Scan(&total)
	modeText := "新增"
	if replace {
		modeText = "覆盖"
	}

	var msg string
	if skippedDup > 0 {
		msg = fmt.Sprintf("已%s导入 %d 条，自动去重跳过 %d 条（预设共 %d 条）", modeText, added, skippedDup, total)
	} else {
		msg = fmt.Sprintf("已%s导入 %d 条（预设共 %d 条）", modeText, added, total)
	}
	return true, msg, added, skippedDup
}

// AddPresetCases 批量新增或覆盖预设用例（保持原有签名，兼容老调用方）
func AddPresetCases(items []map[string]any, replace bool, customPath string) (bool, string, int) {
	ok, msg, added, _ := AddPresetCasesEx(items, replace, customPath)
	return ok, msg, added
}

// DeletePresetCases 批量删除预设用例
func DeletePresetCases(ids []string, customPath string) (bool, string, int) {
	var validIDs []string
	for _, id := range ids {
		s := strings.TrimSpace(id)
		if s != "" {
			validIDs = append(validIDs, s)
		}
	}
	if len(validIDs) == 0 {
		return false, "未指定要删除的用例 id", 0
	}

	_, err := InitDB(customPath)
	if err != nil {
		return false, fmt.Sprintf("初始化数据库失败: %v", err), 0
	}

	dbMu.Lock()
	defer dbMu.Unlock()
	db, err := GetConnection(customPath)
	if err != nil {
		return false, fmt.Sprintf("连接数据库失败: %v", err), 0
	}
	defer db.Close()

	placeholders := make([]string, len(validIDs))
	args := make([]any, len(validIDs))
	for i, vid := range validIDs {
		placeholders[i] = "?"
		args[i] = vid
	}

	delSQL := fmt.Sprintf("DELETE FROM testcases WHERE id IN (%s)", strings.Join(placeholders, ","))
	res, err := db.Exec(delSQL, args...)
	if err != nil {
		return false, fmt.Sprintf("删除失败: %v", err), 0
	}
	removed, _ := res.RowsAffected()
	if removed == 0 {
		return false, "没有匹配到要删除的预设用例（可能已被删除）", 0
	}

	var remaining int
	_ = db.QueryRow("SELECT COUNT(*) FROM testcases").Scan(&remaining)

	return true, fmt.Sprintf("已删除 %d 条（预设剩余 %d 条）", removed, remaining), int(removed)
}

// RemoveAttachmentFromAllCases 从所有用例的 attachments 中摘掉指定路径（附件库删文件后调用），
// 返回被清理的用例数。
//
// 附件库删掉的文件若仍被用例引用，库里留下的就是悬空路径，只有真正跑用例时才会暴露成
// 「附件不存在，未投递」。所以在删除文件的同时同步摘掉引用。
// 摘完变空数组的用例会把 attachment 标记一并归零——「需要附件」是随附件来的；
// 仍留有其它附件的用例保持标记不变。
func RemoveAttachmentFromAllCases(path, customPath string) (int, error) {
	target := strings.TrimSpace(path)
	if target == "" {
		return 0, nil
	}
	if abs, err := filepath.Abs(target); err == nil {
		target = abs
	}
	target = filepath.Clean(target)

	if _, err := InitDB(customPath); err != nil {
		return 0, err
	}

	dbMu.Lock()
	defer dbMu.Unlock()

	db, err := GetConnection(customPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	rows, err := db.Query("SELECT id, attachments FROM testcases WHERE attachments NOT IN ('', '[]')")
	if err != nil {
		return 0, err
	}

	type update struct {
		id    string
		atts  string
		empty bool
	}
	var updates []update
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			continue
		}
		var list []string
		if json.Unmarshal([]byte(raw), &list) != nil {
			continue
		}
		kept := make([]string, 0, len(list))
		hit := false
		for _, p := range list {
			if samePath(p, target) {
				hit = true
				continue
			}
			kept = append(kept, p)
		}
		if !hit {
			continue
		}
		// make 出来的非 nil 空切片会编码成 []，而不是 null —— 前端按数组解析
		encoded, _ := json.Marshal(kept)
		updates = append(updates, update{id: id, atts: string(encoded), empty: len(kept) == 0})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	for _, u := range updates {
		flag := 1
		if u.empty {
			flag = 0
		}
		if _, err := db.Exec(
			"UPDATE testcases SET attachments = ?, attachment = ?, updated_at = datetime('now','localtime') WHERE id = ?",
			u.atts, flag, u.id); err != nil {
			return 0, err
		}
	}
	return len(updates), nil
}

// samePath 判断两个附件路径是否指向同一文件。
// Windows 路径大小写不敏感，其余平台按字节比较。
func samePath(a, b string) bool {
	ca, cb := filepath.Clean(a), filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(ca, cb)
	}
	return ca == cb
}

// GetPresetLabelsIndex 生成评估器专用索引：CleanWhitespace(prompt) -> labels
func GetPresetLabelsIndex(customPath string) (map[string]map[string]any, error) {
	_, err := InitDB(customPath)
	if err != nil {
		return nil, err
	}
	db, err := GetConnection(customPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query("SELECT prompt, scene, attachment FROM testcases")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]map[string]any)
	for rows.Next() {
		var prompt, scene string
		var attachment int
		if err := rows.Scan(&prompt, &scene, &attachment); err == nil {
			key := models.CleanWhitespace(prompt)
			if key == "" {
				continue
			}
			labels := make(map[string]any)
			if scene != "" {
				labels["scene"] = scene
			}
			if attachment != 0 {
				labels["attachment"] = true
			}
			out[key] = labels
		}
	}
	return out, nil
}

// CaseSeq 从 CASE-NNN 提取数字
func CaseSeq(caseID string) int {
	re := regexp.MustCompile(`(?i)^CASE-(\d+)$`)
	m := re.FindStringSubmatch(strings.TrimSpace(caseID))
	if len(m) > 1 {
		seq, _ := strconv.Atoi(m[1])
		return seq
	}
	return 0
}
