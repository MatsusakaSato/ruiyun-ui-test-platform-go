package testcasedb

import (
	"fmt"
	"strings"
	"time"
)

// ---------------------------------------------------------------- 通知预设模板
//
// 复用用例库所在的 SQLite（工作区 testcases.db），与预设用例同库不同表。

const notifyTplSchema = `
CREATE TABLE IF NOT EXISTS notify_templates (
	id TEXT PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	content TEXT NOT NULL,
	created_at TEXT NOT NULL DEFAULT (datetime('now', 'localtime')),
	updated_at TEXT NOT NULL DEFAULT (datetime('now', 'localtime'))
);
`

func initNotifyTplTable(customPath string) error {
	if _, err := InitDB(customPath); err != nil {
		return err
	}
	db, err := GetConnection(customPath)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(notifyTplSchema)
	return err
}

func newNotifyTplID() string {
	return fmt.Sprintf("nt-%d-%s", time.Now().UnixNano(),
		strings.TrimPrefix(time.Now().Format("150405.000"), "0"))
}

func notifyTplRow(id, name, content, createdAt, updatedAt string) map[string]any {
	return map[string]any{
		"id":         id,
		"name":       name,
		"content":    content,
		"created_at": createdAt,
		"updated_at": updatedAt,
	}
}

func getNotifyTpl(id, customPath string) (map[string]any, error) {
	db, err := GetConnection(customPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var name, content, createdAt, updatedAt string
	err = db.QueryRow(
		"SELECT name, content, created_at, updated_at FROM notify_templates WHERE id = ?", id).
		Scan(&name, &content, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	return notifyTplRow(id, name, content, createdAt, updatedAt), nil
}

// nameTaken 模板名是否已被其它模板占用（excludeID 为空表示不排除，用于新增）
func nameTaken(name, excludeID, customPath string) (bool, error) {
	db, err := GetConnection(customPath)
	if err != nil {
		return false, err
	}
	defer db.Close()
	var n int
	q := "SELECT COUNT(1) FROM notify_templates WHERE name = ?"
	args := []any{name}
	if strings.TrimSpace(excludeID) != "" {
		q += " AND id <> ?"
		args = append(args, excludeID)
	}
	if err := db.QueryRow(q, args...).Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// ListNotifyTemplates 全部通知预设模板（按创建时间升序）
func ListNotifyTemplates(customPath string) ([]map[string]any, error) {
	if err := initNotifyTplTable(customPath); err != nil {
		return nil, err
	}
	db, err := GetConnection(customPath)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	rows, err := db.Query(
		"SELECT id, name, content, created_at, updated_at FROM notify_templates ORDER BY created_at, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []map[string]any{} // 空集合必须是 []，不能是 nil（否则 JSON 变 null）
	for rows.Next() {
		var id, name, content, createdAt, updatedAt string
		if err := rows.Scan(&id, &name, &content, &createdAt, &updatedAt); err == nil {
			out = append(out, notifyTplRow(id, name, content, createdAt, updatedAt))
		}
	}
	return out, nil
}

// AddNotifyTemplate 新增模板。名称必填且不可重复，内容不可为空。
func AddNotifyTemplate(name, content, customPath string) (bool, string, map[string]any) {
	name = strings.TrimSpace(name)
	content = strings.TrimSpace(content)
	if name == "" {
		return false, "模板名称为必填", nil
	}
	if content == "" {
		return false, "模板内容不能为空", nil
	}
	if err := initNotifyTplTable(customPath); err != nil {
		return false, fmt.Sprintf("初始化数据库失败: %v", err), nil
	}

	dbMu.Lock()
	defer dbMu.Unlock()

	taken, err := nameTaken(name, "", customPath)
	if err != nil {
		return false, fmt.Sprintf("查询模板失败: %v", err), nil
	}
	if taken {
		return false, "已存在同名模板，请换一个名称", nil
	}

	db, err := GetConnection(customPath)
	if err != nil {
		return false, fmt.Sprintf("连接数据库失败: %v", err), nil
	}
	defer db.Close()

	id := newNotifyTplID()
	if _, err := db.Exec("INSERT INTO notify_templates (id, name, content) VALUES (?, ?, ?)",
		id, name, content); err != nil {
		return false, fmt.Sprintf("保存模板失败: %v", err), nil
	}
	item, err := getNotifyTpl(id, customPath)
	if err != nil {
		return false, fmt.Sprintf("读取已保存模板失败: %v", err), nil
	}
	return true, fmt.Sprintf("已存入预设「%s」", name), item
}

// RenameNotifyTemplate 重命名模板，新名称必填且不可与其它模板重复。
func RenameNotifyTemplate(id, name, customPath string) (bool, string, map[string]any) {
	id = strings.TrimSpace(id)
	name = strings.TrimSpace(name)
	if id == "" {
		return false, "缺少模板 id", nil
	}
	if name == "" {
		return false, "模板名称为必填", nil
	}
	if err := initNotifyTplTable(customPath); err != nil {
		return false, fmt.Sprintf("初始化数据库失败: %v", err), nil
	}

	dbMu.Lock()
	defer dbMu.Unlock()

	taken, err := nameTaken(name, id, customPath)
	if err != nil {
		return false, fmt.Sprintf("查询模板失败: %v", err), nil
	}
	if taken {
		return false, "已存在同名模板，请换一个名称", nil
	}

	db, err := GetConnection(customPath)
	if err != nil {
		return false, fmt.Sprintf("连接数据库失败: %v", err), nil
	}
	defer db.Close()

	res, err := db.Exec(
		"UPDATE notify_templates SET name = ?, updated_at = datetime('now', 'localtime') WHERE id = ?",
		name, id)
	if err != nil {
		return false, fmt.Sprintf("重命名失败: %v", err), nil
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, "模板不存在或已被删除", nil
	}
	item, err := getNotifyTpl(id, customPath)
	if err != nil {
		return false, fmt.Sprintf("读取模板失败: %v", err), nil
	}
	return true, fmt.Sprintf("已重命名为「%s」", name), item
}

// DeleteNotifyTemplate 删除模板，返回被删模板名
func DeleteNotifyTemplate(id, customPath string) (bool, string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return false, "缺少模板 id"
	}
	if err := initNotifyTplTable(customPath); err != nil {
		return false, fmt.Sprintf("初始化数据库失败: %v", err)
	}

	dbMu.Lock()
	defer dbMu.Unlock()

	db, err := GetConnection(customPath)
	if err != nil {
		return false, fmt.Sprintf("连接数据库失败: %v", err)
	}
	defer db.Close()

	var oldName string
	_ = db.QueryRow("SELECT name FROM notify_templates WHERE id = ?", id).Scan(&oldName)

	res, err := db.Exec("DELETE FROM notify_templates WHERE id = ?", id)
	if err != nil {
		return false, fmt.Sprintf("删除失败: %v", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return false, "模板不存在或已被删除"
	}
	if oldName == "" {
		oldName = id
	}
	return true, fmt.Sprintf("已删除预设「%s」", oldName)
}
