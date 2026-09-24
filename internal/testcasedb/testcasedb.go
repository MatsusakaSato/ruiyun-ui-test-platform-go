package testcasedb

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
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
		targets TEXT NOT NULL DEFAULT '[]',
		attachment INTEGER NOT NULL DEFAULT 0,
		expect_tools TEXT NOT NULL DEFAULT '[]',
		attachments TEXT NOT NULL DEFAULT '[]',
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
	return path, nil
}

func normLabels(raw any) map[string]any {
	out := make(map[string]any)
	m, ok := raw.(map[string]any)
	if !ok {
		return out
	}
	scene := strings.TrimSpace(fmt.Sprintf("%v", m["scene"]))
	if scene != "" && scene != "<nil>" {
		out["scene"] = scene
	}

	var tgList []string
	if tg, ok := m["targets"].([]any); ok {
		for _, t := range tg {
			s := strings.TrimSpace(fmt.Sprintf("%v", t))
			if s != "" {
				tgList = append(tgList, s)
			}
		}
	} else if tg, ok := m["targets"].([]string); ok {
		for _, s := range tg {
			s = strings.TrimSpace(s)
			if s != "" {
				tgList = append(tgList, s)
			}
		}
	}
	if len(tgList) > 0 {
		out["targets"] = tgList
	}
	if att, ok := m["attachment"].(bool); ok && att {
		out["attachment"] = true
	}
	return out
}

func rowToCase(id, name, prompt, scene, targetsStr string, attachment int, expectToolsStr, attachmentsStr string) map[string]any {
	var targets []string
	if targetsStr != "" {
		_ = json.Unmarshal([]byte(targetsStr), &targets)
	}
	if targets == nil {
		targets = []string{}
	}

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

	labels := make(map[string]any)
	if scene != "" {
		labels["scene"] = scene
	}
	if len(targets) > 0 {
		labels["targets"] = targets
	}
	if attachment != 0 {
		labels["attachment"] = true
	}

	// 字段集合必须与 Python `core/testcase_db.py` 完全一致：
	// id / name / prompt / expect_tools / labels / attachments（+ 有附件时 attachment）。
	// 旧实现多输出了顶层 scene / targets —— 前端只读 labels.scene / labels.targets，
	// 属契约偏离（HTTP 差分实测确认），已删除。
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

	rows, err := db.Query("SELECT id, name, prompt, scene, targets, attachment, expect_tools, attachments FROM testcases ORDER BY seq ASC, id ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cases []map[string]any
	for rows.Next() {
		var id, name, prompt, scene, targetsStr, expectToolsStr, attachmentsStr string
		var attachment int
		if err := rows.Scan(&id, &name, &prompt, &scene, &targetsStr, &attachment, &expectToolsStr, &attachmentsStr); err == nil {
			cases = append(cases, rowToCase(id, name, prompt, scene, targetsStr, attachment, expectToolsStr, attachmentsStr))
		}
	}
	return cases, nil
}

// QueryPresetCases 预设用例库分页与条件筛选查询
func QueryPresetCases(keyword, scene string, targets []string, attachment string, ids []string, limit, offset int, order, customPath string) (map[string]any, error) {
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
	scenes := []string{} // Python 列表推导天然给 []；Go 的 nil 切片会序列化成 null
	if err == nil {
		for scRows.Next() {
			var sc string
			_ = scRows.Scan(&sc)
			scenes = append(scenes, sc)
		}
		scRows.Close()
	}

	allTargets := []string{} // 同上：空集合必须是 []，不能是 null
	tgRows, err := db.Query("SELECT DISTINCT value FROM testcases, json_each(testcases.targets) WHERE value != '' ORDER BY value")
	if err == nil {
		for tgRows.Next() {
			var tg string
			_ = tgRows.Scan(&tg)
			allTargets = append(allTargets, tg)
		}
		tgRows.Close()
	}

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

	sc := strings.TrimSpace(scene)
	if sc != "" {
		whereClauses = append(whereClauses, "scene = ?")
		params = append(params, sc)
	}

	// 与 Python 原版严格对齐：只认 yes / no，其余值一律忽略筛选。
	// Python 侧是 `if attachment == "yes": ... elif attachment == "no":`，
	// 既不 trim 也不小写 —— 多接受 "1"/"true" 会让同一请求在两侧得到不同结果。
	if attachment == "yes" {
		whereClauses = append(whereClauses, "attachment = 1")
	} else if attachment == "no" {
		whereClauses = append(whereClauses, "attachment = 0")
	}

	// 多选目标是「或」语义：命中任意一个即算匹配。
	// Python 原版用 `value IN (...)` 单条 EXISTS —— 若改成按目标逐个 EXISTS
	// 再用 AND 串起来，就变成「且」语义，结果集会大幅缩小
	// （真实库实测：或=863 条，且=7 条）。
	var tgList []string
	for _, tg := range targets {
		if t := strings.TrimSpace(tg); t != "" {
			tgList = append(tgList, t)
		}
	}
	if len(tgList) > 0 {
		ph := strings.TrimRight(strings.Repeat("?,", len(tgList)), ",")
		whereClauses = append(whereClauses,
			fmt.Sprintf("EXISTS (SELECT 1 FROM json_each(testcases.targets) WHERE value IN (%s))", ph))
		for _, t := range tgList {
			params = append(params, t)
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
	// 与 Python 原版对齐：limit 的规则是 max(1, min(limit or 50, 500))。
	// 关键是 «or 50» 只在 limit 为 0 时生效（Python 里 0 是假值，负数仍是真值），
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

	listSQL := fmt.Sprintf("SELECT id, name, prompt, scene, targets, attachment, expect_tools, attachments FROM testcases WHERE %s ORDER BY seq %s, id %s LIMIT ? OFFSET ?",
		whereSQL, sortOrder, sortOrder)
	listParams := append(params, limit, offset)

	rows, err := db.Query(listSQL, listParams...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cases := []map[string]any{} // 空结果必须是 []，Python 的列表推导不会给 null
	for rows.Next() {
		var id, name, prompt, scene, targetsStr, expectToolsStr, attachmentsStr string
		var attachment int
		if err := rows.Scan(&id, &name, &prompt, &scene, &targetsStr, &attachment, &expectToolsStr, &attachmentsStr); err == nil {
			cases = append(cases, rowToCase(id, name, prompt, scene, targetsStr, attachment, expectToolsStr, attachmentsStr))
		}
	}

	return map[string]any{
		"cases":   cases,
		"total":   total,
		"offset":  offset,
		"limit":   limit,
		"scenes":  scenes,
		"targets": allTargets,
	}, nil
}

// CaseDedupeKey 计算用例去重唯一键：prompt + scene + sorted(targets) + attachment
func CaseDedupeKey(prompt, scene string, targets []string, hasAttachment bool) string {
	normPrompt := strings.TrimSpace(prompt)
	normScene := strings.TrimSpace(scene)

	seen := make(map[string]bool)
	var cleanTargets []string
	for _, t := range targets {
		ct := strings.TrimSpace(t)
		if ct != "" && !seen[ct] {
			seen[ct] = true
			cleanTargets = append(cleanTargets, ct)
		}
	}
	sort.Strings(cleanTargets)
	tgStr := strings.Join(cleanTargets, ",")

	attStr := "0"
	if hasAttachment {
		attStr = "1"
	}
	return fmt.Sprintf("%s||%s||%s||%s", normPrompt, normScene, tgStr, attStr)
}

func extractCaseFields(item map[string]any) (prompt, scene, name, caseID string, targets []string, hasAttachment int, expectTools, attachments []string) {
	prompt = strings.TrimSpace(fmt.Sprintf("%v", item["prompt"]))
	if prompt == "<nil>" {
		prompt = ""
	}

	name = strings.TrimSpace(fmt.Sprintf("%v", item["name"]))
	if name == "<nil>" {
		name = ""
	}

	caseID = strings.TrimSpace(fmt.Sprintf("%v", item["id"]))
	if caseID == "<nil>" {
		caseID = ""
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
		if tg, ok := rawLabels["targets"].([]any); ok {
			for _, t := range tg {
				st := strings.TrimSpace(fmt.Sprintf("%v", t))
				if st != "" && st != "<nil>" {
					targets = append(targets, st)
				}
			}
		} else if tg, ok := rawLabels["targets"].([]string); ok {
			for _, st := range tg {
				st = strings.TrimSpace(st)
				if st != "" {
					targets = append(targets, st)
				}
			}
		}
		if att, ok := rawLabels["attachment"].(bool); ok && att {
			hasAttachment = 1
		}
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
	if len(targets) == 0 {
		if tg, ok := item["targets"].([]any); ok {
			for _, t := range tg {
				st := strings.TrimSpace(fmt.Sprintf("%v", t))
				if st != "" && st != "<nil>" {
					targets = append(targets, st)
				}
			}
		} else if tg, ok := item["targets"].([]string); ok {
			for _, st := range tg {
				st = strings.TrimSpace(st)
				if st != "" {
					targets = append(targets, st)
				}
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

	return
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

	prompt, scene, name, caseID, targets, hasAttachment, expectTools, attachments := extractCaseFields(item)
	if prompt == "" {
		return false, "提问为必填", nil
	}

	tx, err := db.Begin()
	if err != nil {
		return false, fmt.Sprintf("开启事务失败: %v", err), nil
	}
	defer tx.Rollback()

	// 查重：提问和标签（scene, targets, attachment）完全一致的用例视为重复
	currDedupeKey := CaseDedupeKey(prompt, scene, targets, hasAttachment == 1)
	existingRows, err := tx.Query("SELECT id, prompt, scene, targets, attachment FROM testcases WHERE trim(prompt) = ?", prompt)
	if err == nil {
		defer existingRows.Close()
		for existingRows.Next() {
			var eid, eprompt, escene, etargetsStr string
			var eattachment int
			if err := existingRows.Scan(&eid, &eprompt, &escene, &etargetsStr, &eattachment); err == nil {
				var etargets []string
				if etargetsStr != "" {
					_ = json.Unmarshal([]byte(etargetsStr), &etargets)
				}
				if CaseDedupeKey(eprompt, escene, etargets, eattachment == 1) == currDedupeKey {
					return false, fmt.Sprintf("用例已存在（编号：%s），提问与标签完全一致，已自动去重", eid), nil
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

	if caseID == "" {
		caseID = fmt.Sprintf("CASE-%03d", nextSeq)
	}

	if name == "" {
		if scene != "" {
			name = fmt.Sprintf("%s-%03d", scene, nextSeq)
		} else {
			name = fmt.Sprintf("自定义-%03d", nextSeq)
		}
	}

	tgJSON, _ := json.Marshal(targets)
	attJSON, _ := json.Marshal(attachments)
	expJSON, _ := json.Marshal(expectTools)

	insertSQL := `
	INSERT INTO testcases (id, name, prompt, scene, targets, attachment, expect_tools, attachments, seq, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now', 'localtime'), datetime('now', 'localtime'))
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name,
		prompt=excluded.prompt,
		scene=excluded.scene,
		targets=excluded.targets,
		attachment=excluded.attachment,
		expect_tools=excluded.expect_tools,
		attachments=excluded.attachments,
		seq=excluded.seq,
		updated_at=datetime('now', 'localtime')
	`
	if _, err := tx.Exec(insertSQL, caseID, name, prompt, scene, string(tgJSON), hasAttachment, string(expJSON), string(attJSON), nextSeq); err != nil {
		return false, fmt.Sprintf("写入数据库失败: %v", err), nil
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Sprintf("提交事务失败: %v", err), nil
	}

	resCase := rowToCase(caseID, name, prompt, scene, string(tgJSON), hasAttachment, string(expJSON), string(attJSON))
	return true, fmt.Sprintf("已成功添加用例 %s", caseID), resCase
}

// AddPresetCases 批量新增或覆盖预设用例（支持自动去重已存在或批次内重复的用例）
func AddPresetCases(items []map[string]any, replace bool, customPath string) (bool, string, int) {
	if len(items) == 0 {
		return false, "没有可导入的用例（提问列全为空？）", 0
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

	tx, err := db.Begin()
	if err != nil {
		return false, fmt.Sprintf("开启事务失败: %v", err), 0
	}
	defer tx.Rollback()

	if replace {
		if _, err := tx.Exec("DELETE FROM testcases"); err != nil {
			return false, fmt.Sprintf("清空既有用例失败: %v", err), 0
		}
	}

	// 加载已有用例用于去重
	existingMap := make(map[string]string)
	if !replace {
		rows, err := tx.Query("SELECT id, prompt, scene, targets, attachment FROM testcases")
		if err == nil {
			for rows.Next() {
				var eid, eprompt, escene, etgStr string
				var eatt int
				if err := rows.Scan(&eid, &eprompt, &escene, &etgStr, &eatt); err == nil {
					var etg []string
					if etgStr != "" {
						_ = json.Unmarshal([]byte(etgStr), &etg)
					}
					existingMap[CaseDedupeKey(eprompt, escene, etg, eatt == 1)] = eid
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
	INSERT INTO testcases (id, name, prompt, scene, targets, attachment, expect_tools, attachments, seq, created_at, updated_at)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, datetime('now', 'localtime'), datetime('now', 'localtime'))
	ON CONFLICT(id) DO UPDATE SET
		name=excluded.name,
		prompt=excluded.prompt,
		scene=excluded.scene,
		targets=excluded.targets,
		attachment=excluded.attachment,
		expect_tools=excluded.expect_tools,
		attachments=excluded.attachments,
		seq=excluded.seq,
		updated_at=datetime('now', 'localtime')
	`)
	if err != nil {
		return false, fmt.Sprintf("准备插入语句失败: %v", err), 0
	}
	defer stmt.Close()

	seenInBatch := make(map[string]bool)
	added := 0
	skippedDup := 0

	for _, it := range items {
		prompt, scene, name, caseID, targets, hasAttachment, expectTools, attachments := extractCaseFields(it)
		if prompt == "" {
			continue
		}

		dedupeKey := CaseDedupeKey(prompt, scene, targets, hasAttachment == 1)
		if (len(existingMap) > 0 && existingMap[dedupeKey] != "") || seenInBatch[dedupeKey] {
			skippedDup++
			continue
		}
		seenInBatch[dedupeKey] = true

		maxSeq++
		if caseID == "" || !replace {
			caseID = fmt.Sprintf("CASE-%03d", maxSeq)
		}

		if name == "" {
			if scene != "" {
				name = fmt.Sprintf("%s-%03d", scene, maxSeq)
			} else {
				name = fmt.Sprintf("导入-%03d", maxSeq)
			}
		}

		tgJSON, _ := json.Marshal(targets)
		attJSON, _ := json.Marshal(attachments)
		expJSON, _ := json.Marshal(expectTools)

		if _, err := stmt.Exec(caseID, name, prompt, scene, string(tgJSON), hasAttachment, string(expJSON), string(attJSON), maxSeq); err != nil {
			return false, fmt.Sprintf("批量写入失败: %v", err), 0
		}
		added++
	}

	if err := tx.Commit(); err != nil {
		return false, fmt.Sprintf("提交事务失败: %v", err), 0
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
	return true, msg, added
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

	rows, err := db.Query("SELECT prompt, scene, targets, attachment FROM testcases")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string]map[string]any)
	for rows.Next() {
		var prompt, scene, targetsStr string
		var attachment int
		if err := rows.Scan(&prompt, &scene, &targetsStr, &attachment); err == nil {
			key := models.CleanWhitespace(prompt)
			if key == "" {
				continue
			}
			labels := make(map[string]any)
			if scene != "" {
				labels["scene"] = scene
			}
			var tg []string
			if targetsStr != "" {
				_ = json.Unmarshal([]byte(targetsStr), &tg)
			}
			if len(tg) > 0 {
				labels["targets"] = tg
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
