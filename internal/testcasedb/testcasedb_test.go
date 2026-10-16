package testcasedb

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func createTempDB(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "testcases.db")
	_, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	return dbPath
}

func TestCaseDedupeKey(t *testing.T) {
	// 1. 同 prompt + 同 scene + 同 difficulty + 同附件状态（首尾空白不同）
	k1 := CaseDedupeKey(" 写一首诗 ", " 创作 ", " 难 ", false)
	k2 := CaseDedupeKey("写一首诗", "创作", "难", false)
	if k1 != k2 {
		t.Errorf("expected k1 == k2, got k1=%q, k2=%q", k1, k2)
	}

	// 键的字段顺序固定为 prompt||scene||difficulty||附件段
	if got, want := k2, "写一首诗||创作||难||0"; got != want {
		t.Errorf("去重键格式变了，got %q, want %q", got, want)
	}

	// 2. attachment 差异
	k3 := CaseDedupeKey("写一首诗", "创作", "难", true)
	if k1 == k3 {
		t.Errorf("expected k1 != k3 when attachment differs")
	}

	// 3. scene 差异
	k4 := CaseDedupeKey("写一首诗", "教育", "难", false)
	if k1 == k4 {
		t.Errorf("expected k1 != k4 when scene differs")
	}

	// 4. prompt 差异
	k6 := CaseDedupeKey("写一首散文", "创作", "难", false)
	if k1 == k6 {
		t.Errorf("expected k1 != k6 when prompt differs")
	}

	// 5. difficulty 差异：难度是标签，必须参与拼键
	k7 := CaseDedupeKey("写一首诗", "创作", "易", false)
	if k1 == k7 {
		t.Errorf("expected k1 != k7 when difficulty differs")
	}
	// 空难度与写了难度也不同（老数据难度为空串，不能与任何一档混同）
	k8 := CaseDedupeKey("写一首诗", "创作", "", false)
	if k7 == k8 {
		t.Errorf("expected 易 != 空难度")
	}
}

// 调用方传进来的 id / name 一律不再沿用：编号与名称都由服务端生成。
func TestAddPresetCase_IgnoresSuppliedIDAndName(t *testing.T) {
	dbPath := createTempDB(t)

	ok, msg, c := AddPresetCase(map[string]any{
		"id":     "MY-OWN-001",
		"name":   "我自己起的名字",
		"prompt": []string{"写一份班会主持稿"},
		"scene":  "备课",
	}, dbPath)
	if !ok || c == nil {
		t.Fatalf("AddPresetCase failed: %s", msg)
	}
	if c["id"] != "CASE-001" {
		t.Errorf("编号应服务端生成为 CASE-001，实际 %v", c["id"])
	}
	if c["name"] != "备课-001" {
		t.Errorf("名称应服务端生成为 备课-001，实际 %v", c["name"])
	}

	// 库里也只应有服务端生成的那一条，且 id 不带调用方的前缀
	ids := []string{}
	db, err := GetConnection(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Query("SELECT id, name FROM testcases")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatal(err)
		}
		if name != "备课-001" {
			t.Errorf("库里的 name 应自动生成，实际 %q", name)
		}
		ids = append(ids, id)
	}
	if len(ids) != 1 || ids[0] != "CASE-001" {
		t.Errorf("库里应只有一条 CASE-001，实际 %v", ids)
	}
}

func TestAddPresetCase_Deduplication(t *testing.T) {
	dbPath := createTempDB(t)

	// 1. 成功添加第一条用例
	item1 := map[string]any{
		"prompt": []string{"查询2024年GDP数据"},
		"labels": map[string]any{
			"scene": "数据统计",
		},
	}
	ok, msg, c1 := AddPresetCase(item1, dbPath)
	if !ok || c1 == nil {
		t.Fatalf("AddPresetCase failed: %s", msg)
	}
	if c1["id"] != "CASE-001" {
		t.Errorf("expected id CASE-001, got %v", c1["id"])
	}

	// 验证 labels
	labels, ok := c1["labels"].(map[string]any)
	if !ok || labels["scene"] != "数据统计" {
		t.Errorf("expected scene 数据统计, got %v", labels)
	}

	// 2. 添加提问和标签完全一致的用例（带首尾空白）-> 应自动去重失败
	itemDup := map[string]any{
		"prompt": []string{" 查询2024年GDP数据 "},
		"labels": map[string]any{
			"scene": "数据统计",
		},
	}
	okDup, msgDup, cDup := AddPresetCase(itemDup, dbPath)
	if okDup {
		t.Errorf("expected duplicate addition to fail, but succeeded: %v", cDup)
	}
	if cDup != nil {
		t.Errorf("expected nil case on duplicate error")
	}
	t.Logf("Duplicate rejection message: %s", msgDup)

	// 验证总数仍为 1
	count, err := CountCases(dbPath)
	if err != nil || count != 1 {
		t.Fatalf("expected count 1, got %d (err: %v)", count, err)
	}

	// 3. 相同 prompt 但不同标签（scene 不同） -> 允许添加
	itemDiffScene := map[string]any{
		"prompt": []string{"查询2024年GDP数据"},
		"labels": map[string]any{
			"scene": "经济分析",
		},
	}
	okDiff, msgDiff, cDiff := AddPresetCase(itemDiffScene, dbPath)
	if !okDiff || cDiff == nil {
		t.Fatalf("AddPresetCase with diff scene failed: %s", msgDiff)
	}
	if cDiff["id"] != "CASE-002" {
		t.Errorf("expected id CASE-002, got %v", cDiff["id"])
	}

	// 4. 相同 prompt 但 attachment 不同 -> 允许添加
	itemDiffAtt := map[string]any{
		"prompt": []string{"查询2024年GDP数据"},
		"labels": map[string]any{
			"scene":      "数据统计",
			"attachment": true,
		},
	}
	okDiffAtt, msgDiffAtt, cDiffAtt := AddPresetCase(itemDiffAtt, dbPath)
	if !okDiffAtt || cDiffAtt == nil {
		t.Fatalf("AddPresetCase with diff attachment failed: %s", msgDiffAtt)
	}
	if cDiffAtt["id"] != "CASE-003" {
		t.Errorf("expected id CASE-003, got %v", cDiffAtt["id"])
	}

	total, _ := CountCases(dbPath)
	if total != 3 {
		t.Errorf("expected total 3, got %d", total)
	}
}

func TestAddPresetCases_BatchDeduplication(t *testing.T) {
	dbPath := createTempDB(t)

	// 先添加一条既有用例
	_, _, _ = AddPresetCase(map[string]any{
		"prompt": []string{"既有用例1"},
		"scene":  "默认场景",
	}, dbPath)

	// 批量导入列表：含与既有用例重复、批次内自身重复、以及新用例
	items := []map[string]any{
		{
			"prompt": []string{"既有用例1"}, // 与既有用例完全相同 -> 应跳过
			"scene":  "默认场景",
		},
		{
			"prompt": []string{"新用例A"},
			"scene":  "场景A",
		},
		{
			"prompt": []string{"新用例A"}, // 批次内重复 -> 应跳过
			"scene":  "场景A",
		},
		{
			"prompt": []string{"新用例B"},
			"scene":  "场景B",
		},
	}

	ok, msg, added := AddPresetCases(items, false, dbPath)
	if !ok {
		t.Fatalf("AddPresetCases failed: %s", msg)
	}
	if added != 2 {
		t.Errorf("expected 2 added, got %d", added)
	}
	t.Logf("Batch import result: %s", msg)

	total, _ := CountCases(dbPath)
	if total != 3 {
		t.Errorf("expected total 3, got %d", total)
	}

	// 测试清空覆盖导入：即使 replace=true，批次内部重复仍应去重
	replaceItems := []map[string]any{
		{"prompt": []string{"覆盖用例1"}, "scene": "S1"},
		{"prompt": []string{"覆盖用例1"}, "scene": "S1"}, // 批次内重复
		{"prompt": []string{"覆盖用例2"}, "scene": "S2"},
	}
	okRep, msgRep, addedRep := AddPresetCases(replaceItems, true, dbPath)
	if !okRep {
		t.Fatalf("AddPresetCases replace failed: %s", msgRep)
	}
	if addedRep != 2 {
		t.Errorf("expected 2 added on replace, got %d", addedRep)
	}
	t.Logf("Replace import result: %s", msgRep)

	totalRep, _ := CountCases(dbPath)
	if totalRep != 2 {
		t.Errorf("expected total 2 on replace, got %d", totalRep)
	}
}

func TestRemoveAttachmentFromAllCases(t *testing.T) {
	dbPath := createTempDB(t)

	// a：两个附件，其中一个将被删；删完仍有附件 → 保留「需要附件」标记
	if ok, msg, _ := AddPresetCase(map[string]any{
		"prompt":      []string{"带两个附件的用例"},
		"attachments": []string{"/tmp/up/keep.docx", "/tmp/up/gone.pdf"},
	}, dbPath); !ok {
		t.Fatalf("add a failed: %s", msg)
	}
	// b：只挂一个附件，删完变空
	if ok, msg, _ := AddPresetCase(map[string]any{
		"prompt":      []string{"只挂一个附件的用例"},
		"attachments": []string{"/tmp/up/gone.pdf"},
	}, dbPath); !ok {
		t.Fatalf("add b failed: %s", msg)
	}
	// c：只有标记、没有附件路径 → 不该被这次清理碰到
	if ok, msg, _ := AddPresetCase(map[string]any{
		"prompt": []string{"只要附件不下文件"},
		"labels": map[string]any{"attachment": true},
	}, dbPath); !ok {
		t.Fatalf("add c failed: %s", msg)
	}

	// 路径带冗余段也要能匹配上（按 filepath.Clean 归一化后比较）
	n, err := RemoveAttachmentFromAllCases("/tmp/up/./sub/../gone.pdf", dbPath)
	if err != nil {
		t.Fatalf("RemoveAttachmentFromAllCases failed: %v", err)
	}
	if n != 2 {
		t.Errorf("expected 2 cases cleaned, got %d", n)
	}

	cases, err := GetPresetCases(dbPath)
	if err != nil {
		t.Fatalf("GetPresetCases failed: %v", err)
	}
	byPrompt := map[string]map[string]any{}
	for _, c := range cases {
		byPrompt[firstTurn(c)] = c
	}

	a := byPrompt["带两个附件的用例"]
	if got, _ := a["attachments"].([]string); len(got) != 1 || got[0] != "/tmp/up/keep.docx" {
		t.Errorf("a 应只剩 keep.docx，实际 %v", a["attachments"])
	}
	if labels, _ := a["labels"].(map[string]any); labels == nil || labels["attachment"] != true {
		t.Errorf("a 还有附件，应保持「需要附件」，labels=%v", a["labels"])
	}

	b := byPrompt["只挂一个附件的用例"]
	if got, _ := b["attachments"].([]string); len(got) != 0 {
		t.Errorf("b 的附件应被清空，实际 %v", b["attachments"])
	}
	if labels, _ := b["labels"].(map[string]any); labels != nil {
		if _, has := labels["attachment"]; has {
			t.Errorf("b 附件清空后不该再标记「需要附件」，labels=%v", labels)
		}
	}

	c := byPrompt["只要附件不下文件"]
	if labels, _ := c["labels"].(map[string]any); labels == nil || labels["attachment"] != true {
		t.Errorf("c 未被本次清理波及，标记应保留，labels=%v", c["labels"])
	}

	// 幂等：同一条路径再清一次不应有命中
	if n, err := RemoveAttachmentFromAllCases("/tmp/up/gone.pdf", dbPath); err != nil || n != 0 {
		t.Errorf("重复清理应为 0，实际 n=%d err=%v", n, err)
	}
	// 空路径直接返回，不误伤
	if n, err := RemoveAttachmentFromAllCases("   ", dbPath); err != nil || n != 0 {
		t.Errorf("空路径应直接返回 0，实际 n=%d err=%v", n, err)
	}
	if total, _ := CountCases(dbPath); total != 3 {
		t.Errorf("清理引用不应删除用例，期望 3 条，实际 %d", total)
	}
}

func TestCaseDedupe_DistinctAttachmentsNotDeduplicated(t *testing.T) {
	dbPath := createTempDB(t)

	// 用例1：提问相同，但附件是试卷A.docx
	ok1, _, c1 := AddPresetCase(map[string]any{
		"prompt":      []string{"请批改试卷并给出分析"},
		"scene":       "教学评测",
		"attachments": []string{"/path/to/试卷A.docx"},
	}, dbPath)
	if !ok1 || c1 == nil {
		t.Fatalf("添加用例1失败")
	}

	// 用例2：提问相同、场景相同，但附件是试卷B.docx -> 不应被误判为重复！
	ok2, msg2, c2 := AddPresetCase(map[string]any{
		"prompt":      []string{"请批改试卷并给出分析"},
		"scene":       "教学评测",
		"attachments": []string{"/path/to/试卷B.docx"},
	}, dbPath)
	if !ok2 || c2 == nil {
		t.Fatalf("不同附件的用例不应被去重拦截: %s", msg2)
	}

	// 用例3：提问、场景、附件完全相同 -> 应该被拦截去重
	ok3, _, _ := AddPresetCase(map[string]any{
		"prompt":      []string{"请批改试卷并给出分析"},
		"scene":       "教学评测",
		"attachments": []string{"/other/folder/试卷A.docx"}, // 基名相同
	}, dbPath)
	if ok3 {
		t.Fatalf("完全相同附件的用例应被去重拦截，但成功了")
	}

	total, _ := CountCases(dbPath)
	if total != 2 {
		t.Fatalf("期望数据库中有2条不同附件的用例，实际 %d", total)
	}
}

// 内部空白差异（全角空格 / NBSP / 换行）不应绕过去重：导入去重键与运行时预设
// 索引键共用「删除全部空白」归一化，否则两条用例都入库、索引却互相覆盖。
func TestCaseDedupeKey_IgnoresInternalWhitespace(t *testing.T) {
	k1 := CaseDedupeKey("请批改 试卷", "教学 评测", "中 等", false)
	k2 := CaseDedupeKey("请批改\u3000试卷", "教学\u00a0评测", "中\u3000等", false)
	if k1 != k2 {
		t.Errorf("仅内部空白不同的用例应得到同一去重键：\n%q\n%q", k1, k2)
	}
}

func TestAddPresetCases_WhitespaceOnlyDifferenceDeduped(t *testing.T) {
	dbPath := createTempDB(t)

	items := []map[string]any{
		{"prompt": []string{"请批改试卷并给出分析"}, "scene": "教学评测"},
		{"prompt": []string{"请批改 试卷并给出分析"}, "scene": "教学评测"}, // 内部多一个空格
	}
	ok, msg, added := AddPresetCases(items, false, dbPath)
	if !ok {
		t.Fatalf("AddPresetCases failed: %s", msg)
	}
	if added != 1 {
		t.Errorf("内部空白差异应被去重，期望新增 1 条，实际 %d（msg=%s）", added, msg)
	}
	total, _ := CountCases(dbPath)
	if total != 1 {
		t.Errorf("期望库中 1 条，实际 %d", total)
	}
}

// 单条新增走全表候选扫描：既有用例内部带空白、新用例不带，仍应判重。
func TestAddPresetCase_WhitespaceCandidateScan(t *testing.T) {
	dbPath := createTempDB(t)

	if ok, msg, _ := AddPresetCase(map[string]any{
		"prompt": []string{"查询 2024 年数据"}, "scene": "统计",
	}, dbPath); !ok {
		t.Fatalf("首条添加失败: %s", msg)
	}

	ok, msg, c := AddPresetCase(map[string]any{
		"prompt": []string{"查询2024年数据"}, "scene": "统计",
	}, dbPath)
	if ok || c != nil {
		t.Fatalf("仅内部空白不同的用例应被判重，实际 ok=%v case=%v", ok, c)
	}
	t.Logf("判重信息: %s", msg)
	if total, _ := CountCases(dbPath); total != 1 {
		t.Errorf("期望库中 1 条，实际 %d", total)
	}
}

// 难度是标签：写入时存进 difficulty 列、读回时出现在 labels.difficulty。
func TestAddPresetCase_DifficultyRoundTrip(t *testing.T) {
	dbPath := createTempDB(t)

	ok, msg, c := AddPresetCase(map[string]any{
		"prompt": []string{"帮我写一份教学反思"},
		"labels": map[string]any{"scene": "备课", "difficulty": "难"},
	}, dbPath)
	if !ok || c == nil {
		t.Fatalf("AddPresetCase failed: %s", msg)
	}
	if labels, _ := c["labels"].(map[string]any); labels["difficulty"] != "难" {
		t.Fatalf("返回的 labels.difficulty 应为 难，实际 %v", c["labels"])
	}

	// 顶层 difficulty（前端手填用例走这条路）同样要落库
	if ok, msg, _ := AddPresetCase(map[string]any{
		"prompt":     []string{"帮我写一份班会主持稿"},
		"scene":      "备课",
		"difficulty": "易",
	}, dbPath); !ok {
		t.Fatalf("顶层 difficulty 写入失败: %s", msg)
	}

	cases, err := GetPresetCases(dbPath)
	if err != nil {
		t.Fatalf("GetPresetCases failed: %v", err)
	}
	got := map[string]any{}
	names := map[string]any{}
	for _, cc := range cases {
		labels, _ := cc["labels"].(map[string]any)
		got[firstTurn(cc)] = labels["difficulty"]
		names[firstTurn(cc)] = cc["name"]
	}
	if got["帮我写一份教学反思"] != "难" {
		t.Errorf("读回的 labels.difficulty 应为 难，实际 %v", got["帮我写一份教学反思"])
	}
	if got["帮我写一份班会主持稿"] != "易" {
		t.Errorf("顶层 difficulty 读回应为 易，实际 %v", got["帮我写一份班会主持稿"])
	}
	// 名称由服务端按场景生成，而不是沿用调用方传的
	if names["帮我写一份教学反思"] != "备课-001" {
		t.Errorf("名称应自动生成为 备课-001，实际 %v", names["帮我写一份教学反思"])
	}
}

// 难度参与去重键：同提问同场景下难度不同算两条，难度相同判重。
func TestAddPresetCase_DifficultyParticipatesInDedupe(t *testing.T) {
	dbPath := createTempDB(t)

	item := func(difficulty string) map[string]any {
		return map[string]any{
			"prompt": []string{"请批改试卷并给出分析"},
			"labels": map[string]any{
				"scene":      "教学评测",
				"difficulty": difficulty,
			},
		}
	}

	ok1, msg1, c1 := AddPresetCase(item("易"), dbPath)
	if !ok1 || c1 == nil {
		t.Fatalf("难度 易 的第一条应入库: %s", msg1)
	}
	ok2, msg2, c2 := AddPresetCase(item("难"), dbPath)
	if !ok2 || c2 == nil {
		t.Fatalf("难度不同的用例不应被判重: %s", msg2)
	}
	if c1["id"] != "CASE-001" || c2["id"] != "CASE-002" {
		t.Errorf("编号应为 CASE-001 / CASE-002，实际 %v / %v", c1["id"], c2["id"])
	}

	// 同难度（带内部空白）-> 判重
	ok3, msg3, c3 := AddPresetCase(item(" 易 "), dbPath)
	if ok3 || c3 != nil {
		t.Fatalf("难度相同（仅空白差异）的用例应被判重，实际 ok=%v case=%v", ok3, c3)
	}
	t.Logf("判重信息: %s", msg3)

	if total, _ := CountCases(dbPath); total != 2 {
		t.Errorf("期望库中 2 条（两个难度各一条），实际 %d", total)
	}
}

// 已下线的「测试目标」不再参与去重：老库里的行仍带着 targets 数据，
// 新提交的同 prompt / scene / difficulty 必须被判为重复，且历史数据原样保留。
func TestDedupeIgnoresLegacyTargets(t *testing.T) {
	dbPath := createTempDB(t)

	db, err := GetConnection(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	// 历史行按**新格式**（prompt 为 JSON 数组）构造：
	// 多轮改造后 prompt 只认数组，裸字符串会解析成空轮次。
	if _, err := db.Exec(`INSERT INTO testcases (id, name, prompt, scene, difficulty, targets, attachment, expect_tools, attachments, seq)
		VALUES ('CASE-001', '教学评测-001', '["请批改试卷并给出分析"]', '教学评测', '易', '["word","ppt"]', 0, '[]', '[]', 1)`); err != nil {
		db.Close()
		t.Fatalf("构造历史行失败: %v", err)
	}
	db.Close()

	// 单条新增：labels.targets 与历史值不同（excel vs word,ppt），仍应判重
	ok, msg, c := AddPresetCase(map[string]any{
		"prompt": []string{"请批改试卷并给出分析"},
		"labels": map[string]any{
			"scene":      "教学评测",
			"difficulty": "易",
			"targets":    []string{"excel"},
		},
	}, dbPath)
	if ok || c != nil {
		t.Fatalf("只有 targets 与历史不同的用例应判重，实际 ok=%v case=%v", ok, c)
	}
	t.Logf("判重信息: %s", msg)

	// 批量导入同一条：同样应被去重跳过
	okBatch, msgBatch, added := AddPresetCases([]map[string]any{{
		"prompt": []string{"请批改试卷并给出分析"},
		"labels": map[string]any{"scene": "教学评测", "difficulty": "易"},
	}}, false, dbPath)
	if !okBatch || added != 0 {
		t.Fatalf("批量导入应全部去重跳过，实际 ok=%v added=%d（%s）", okBatch, added, msgBatch)
	}

	// 历史数据原样保留：targets 列没有被读改写坏
	db2, err := GetConnection(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var rawTargets string
	if err := db2.QueryRow("SELECT targets FROM testcases WHERE id = 'CASE-001'").Scan(&rawTargets); err != nil {
		t.Fatalf("读历史行的 targets 失败: %v", err)
	}
	if rawTargets != `["word","ppt"]` {
		t.Errorf("历史 targets 数据应原样保留，实际 %q", rawTargets)
	}

	// 读用例接口不再回传 targets
	cases, err := GetPresetCases(dbPath)
	if err != nil {
		t.Fatalf("GetPresetCases failed: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("库中应只有历史那一条，实际 %d", len(cases))
	}
	labels, _ := cases[0]["labels"].(map[string]any)
	if _, has := labels["targets"]; has {
		t.Errorf("labels 里不应再有 targets，实际 %v", labels)
	}
	if _, has := cases[0]["targets"]; has {
		t.Errorf("顶层不应有 targets 字段，实际 %v", cases[0])
	}
}

// 难度筛选：多选取并集、可与场景/附件组合，选项列表来自库内实际值（空难度不出现）。
func TestQueryPresetCases_DifficultyFilter(t *testing.T) {
	dbPath := createTempDB(t)

	// 空库：difficulties 必须是 [] 而不是 nil（null）
	empty, err := QueryPresetCases("", nil, nil, nil, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if list, ok := empty["difficulties"].([]string); !ok || list == nil || len(list) != 0 {
		t.Fatalf("空库的 difficulties 应是非 nil 空切片，实际 %#v", empty["difficulties"])
	}

	seedCase(t, dbPath, "备课易题", "备课", "易", false)
	seedCase(t, dbPath, "备课难题", "备课", "难", false)
	seedCase(t, dbPath, "评测易题带附件", "评测", "易", true)
	seedCase(t, dbPath, "评测中题", "评测", "中", false)
	seedCase(t, dbPath, "没有难度的题", "备课", "", false)

	prompts := func(res map[string]any) []string {
		t.Helper()
		out := []string{}
		for _, c := range res["cases"].([]map[string]any) {
			out = append(out, firstTurn(c))
		}
		return out
	}

	// 1. 多选难度取并集（空难度的行不被任何一档命中）
	res, err := QueryPresetCases("", nil, []string{"易", "难", "中"}, nil, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if got := prompts(res); len(got) != 4 {
		t.Errorf("difficulties=易,难,中 应命中 4 条（不含空难度），实际 %v", got)
	}
	// 选项列表来自库内实际值、按升序，且不含空难度
	diffs, ok := res["difficulties"].([]string)
	if !ok {
		t.Fatalf("difficulties 应是 []string，实际 %T", res["difficulties"])
	}
	if len(diffs) != 3 || diffs[0] != "中" || diffs[1] != "易" || diffs[2] != "难" {
		t.Errorf("difficulties 应为库内实际值升序 [中 易 难]，实际 %v", diffs)
	}

	// 2. 与场景组合
	res, err = QueryPresetCases("", []string{"评测"}, []string{"易"}, nil, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if got := prompts(res); len(got) != 1 || got[0] != "评测易题带附件" {
		t.Errorf("难度+场景组合应只剩「评测易题带附件」，实际 %v", got)
	}

	// 3. 与附件筛选组合（带附件 / 不带附件各一条）
	res, err = QueryPresetCases("", nil, []string{"易"}, nil, "yes", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if got := prompts(res); len(got) != 1 || got[0] != "评测易题带附件" {
		t.Errorf("难度+有附件应只剩「评测易题带附件」，实际 %v", got)
	}
	res, err = QueryPresetCases("", nil, []string{"易"}, nil, "no", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if got := prompts(res); len(got) != 1 || got[0] != "备课易题" {
		t.Errorf("难度+无附件应只剩「备课易题」，实际 %v", got)
	}

	// 4. 不传难度即不过滤，空难度的行照常返回
	res, err = QueryPresetCases("", nil, nil, nil, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if got := prompts(res); len(got) != 5 {
		t.Errorf("不传难度应返回全部 5 条，实际 %v", got)
	}
	if total, _ := res["total"].(int); total != 5 {
		t.Errorf("total 应为 5，实际 %v", res["total"])
	}
}

func seedCase(t *testing.T, dbPath, prompt, scene, difficulty string, attachment bool) {
	t.Helper()
	labels := map[string]any{"scene": scene}
	if difficulty != "" {
		labels["difficulty"] = difficulty
	}
	if attachment {
		labels["attachment"] = true
	}
	if ok, msg, _ := AddPresetCase(map[string]any{"prompt": []string{prompt}, "labels": labels}, dbPath); !ok {
		t.Fatalf("准备用例 %q 失败: %s", prompt, msg)
	}
}

// 老库（testcases 表还没有 difficulty 列）必须被幂等迁移补列，且既有数据一行不丢。
func TestInitDB_MigratesLegacyTableWithoutDifficulty(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy.db")

	db, err := GetConnection(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
	CREATE TABLE testcases (
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
	INSERT INTO testcases (id, name, prompt, scene, targets, attachment, expect_tools, attachments, seq)
	VALUES ('CASE-007', '备课-007', '老库里的用例', '备课', '["word"]', 0, '[]', '[]', 7);
	`); err != nil {
		db.Close()
		t.Fatalf("构造老库失败: %v", err)
	}
	db.Close()

	if _, err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB 迁移失败: %v", err)
	}

	db2, err := GetConnection(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	if !hasColumn(t, db2, "difficulty") {
		t.Fatal("迁移后 testcases 应已补上 difficulty 列")
	}
	var raw string
	if err := db2.QueryRow("SELECT difficulty FROM testcases WHERE id = 'CASE-007'").Scan(&raw); err != nil {
		t.Fatalf("读老行的 difficulty 失败: %v", err)
	}
	if raw != "" {
		t.Errorf("老行补列后难度应为空串，实际 %q", raw)
	}

	cases, err := GetPresetCases(dbPath)
	if err != nil {
		t.Fatalf("GetPresetCases failed: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("迁移不应丢数据，期望 1 条，实际 %d", len(cases))
	}
	c := cases[0]
	if c["id"] != "CASE-007" || c["name"] != "备课-007" {
		t.Errorf("老行数据被改动了：%v", c)
	}
	// prompt 自多轮改造起只认 JSON 数组：老库里的裸字符串不再是合法格式，
	// 解析为空轮次（旧数据已按用户要求清空，无需兼容裸字符串）。
	if got := c["prompt"]; len(got.([]string)) != 0 {
		t.Errorf("非数组的 prompt 应解析为空轮次，实际 %v", got)
	}
	labels, _ := c["labels"].(map[string]any)
	if labels["scene"] != "备课" {
		t.Errorf("老行的 scene 应保留，实际 %v", c["labels"])
	}
	if _, has := labels["difficulty"]; has {
		t.Errorf("难度为空时不应出现在 labels 里，实际 %v", c["labels"])
	}

	// 迁移后库能照常写入，且序号接着最大的 CASE-NNN 往下排
	ok, msg, added := AddPresetCases([]map[string]any{{
		"prompt": []string{"迁移后新增的用例"},
		"labels": map[string]any{"scene": "备课", "difficulty": "难"},
	}}, false, dbPath)
	if !ok || added != 1 {
		t.Fatalf("迁移后写入失败: %s", msg)
	}
	after, err := GetPresetCases(dbPath)
	if err != nil {
		t.Fatalf("GetPresetCases failed: %v", err)
	}
	if len(after) != 2 || after[1]["id"] != "CASE-008" {
		t.Fatalf("迁移后新增应用 CASE-008，实际 %v", after)
	}
	if labels, _ := after[1]["labels"].(map[string]any); labels["difficulty"] != "难" {
		t.Errorf("迁移后新增的难度应为 难，实际 %v", after[1]["labels"])
	}
}

func hasColumn(t *testing.T, db *sql.DB, column string) bool {
	t.Helper()
	rows, err := db.Query("PRAGMA table_info(testcases)")
	if err != nil {
		t.Fatalf("PRAGMA table_info 失败: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, ctype string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &dflt, &pk); err != nil {
			t.Fatalf("扫描 table_info 失败: %v", err)
		}
		if name == column {
			return true
		}
	}
	return false
}

// 自定义标签：落库到 tags 列、读回出现在 labels.tags；没有标签时不出现在 labels 里。
// 顶层 tags 与 labels.tags 两条入口都要认（前端平铺提交走前者）。
func TestAddPresetCase_TagsRoundTrip(t *testing.T) {
	dbPath := createTempDB(t)

	ok, msg, c := AddPresetCase(map[string]any{
		"prompt": []string{"帮我写一份教学反思"},
		"labels": map[string]any{
			"scene": "备课",
			"tags":  []any{" 回归 ", "冒烟", "回归", "", "备课"},
		},
	}, dbPath)
	if !ok || c == nil {
		t.Fatalf("AddPresetCase failed: %s", msg)
	}
	labels, _ := c["labels"].(map[string]any)
	got, isList := labels["tags"].([]string)
	if !isList {
		t.Fatalf("labels.tags 应是字符串数组，实际 %T（%v）", labels["tags"], labels["tags"])
	}
	// 逐个去空白、丢空串、按首次出现去重（顺序即输入顺序），数量不设上限
	if len(got) != 3 || got[0] != "回归" || got[1] != "冒烟" || got[2] != "备课" {
		t.Errorf("标签应归一化为 [回归 冒烟 备课]，实际 %v", got)
	}

	// 顶层 tags（与 scene / difficulty 的取值口径一致）
	okTop, msgTop, cTop := AddPresetCase(map[string]any{
		"prompt": []string{"帮我写一份班会主持稿"},
		"scene":  "备课",
		"tags":   []string{"回归"},
	}, dbPath)
	if !okTop || cTop == nil {
		t.Fatalf("顶层 tags 写入失败: %s", msgTop)
	}
	if tl, _ := cTop["labels"].(map[string]any); tl != nil {
		topTags, _ := tl["tags"].([]string)
		if len(topTags) != 1 || topTags[0] != "回归" {
			t.Errorf("顶层 tags 应落库并回读，实际 %v", cTop["labels"])
		}
	} else {
		t.Errorf("顶层 tags 写入后应能取到 labels，实际 %v", cTop)
	}

	// 非数组的 tags（Excel 里同名列会是字符串）忽略掉，不能写坏标签列
	okStr, msgStr, cStr := AddPresetCase(map[string]any{
		"prompt": []string{"标签传成字符串的用例"},
		"labels": map[string]any{"scene": "备课", "tags": "回归,冒烟"},
	}, dbPath)
	if !okStr || cStr == nil {
		t.Fatalf("字符串 tags 应被忽略而不是写入失败: %s", msgStr)
	}
	if sl, _ := cStr["labels"].(map[string]any); sl["tags"] != nil {
		t.Errorf("非数组 tags 应被忽略，实际 %v", cStr["labels"])
	}

	// 没有标签的用例：labels 里不出现 tags 键，落库列仍是 '[]' 而不是 null
	okNo, msgNo, cNo := AddPresetCase(map[string]any{
		"prompt": []string{"没有标签的用例"},
		"labels": map[string]any{"scene": "备课"},
	}, dbPath)
	if !okNo || cNo == nil {
		t.Fatalf("无标签用例写入失败: %s", msgNo)
	}
	if nl, _ := cNo["labels"].(map[string]any); nl["tags"] != nil {
		t.Errorf("没有标签时不应出现 labels.tags，实际 %v", cNo["labels"])
	}
	db, err := GetConnection(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var raw string
	if err := db.QueryRow("SELECT tags FROM testcases WHERE id = ?", cNo["id"]).Scan(&raw); err != nil {
		t.Fatalf("读 tags 列失败: %v", err)
	}
	if raw != "[]" {
		t.Errorf("无标签时 tags 列应为 []，实际 %q", raw)
	}

	// 整表回读：标签跟着用例一起读出来
	cases, err := GetPresetCases(dbPath)
	if err != nil {
		t.Fatalf("GetPresetCases failed: %v", err)
	}
	back := map[string][]string{}
	for _, cc := range cases {
		l, _ := cc["labels"].(map[string]any)
		tl, _ := l["tags"].([]string)
		back[firstTurn(cc)] = tl
	}
	if len(back["帮我写一份教学反思"]) != 3 {
		t.Errorf("回读的标签应保留 3 个，实际 %v", back["帮我写一份教学反思"])
	}
	if len(back["没有标签的用例"]) != 0 {
		t.Errorf("无标签用例回读不应有标签，实际 %v", back["没有标签的用例"])
	}
}

// 编辑用例：编号 / 序号 / 创建时间不变，内容与名称按新值改写；判重跳过自己那一行。
func TestUpdatePresetCase(t *testing.T) {
	dbPath := createTempDB(t)

	ok, msg, c1 := AddPresetCase(map[string]any{
		"prompt": []string{"原提问"},
		"labels": map[string]any{"scene": "备课", "difficulty": "易", "tags": []string{"回归"}},
	}, dbPath)
	if !ok || c1 == nil {
		t.Fatalf("准备用例失败: %s", msg)
	}
	if _, msg2, c2 := AddPresetCase(map[string]any{
		"prompt": []string{"另一条用例"},
		"labels": map[string]any{"scene": "备课", "difficulty": "易"},
	}, dbPath); c2 == nil {
		t.Fatalf("准备第二条用例失败: %s", msg2)
	}

	db, err := GetConnection(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	var seqBefore int
	var createdBefore string
	if err := db.QueryRow("SELECT seq, created_at FROM testcases WHERE id = 'CASE-001'").Scan(&seqBefore, &createdBefore); err != nil {
		db.Close()
		t.Fatalf("读编辑前状态失败: %v", err)
	}
	db.Close()

	// 1. 正常改写：提问 / 场景 / 难度 / 标签全换
	okUp, msgUp, cUp := UpdatePresetCase(map[string]any{
		"id":     "CASE-001",
		"prompt": []string{"改后的提问"},
		"labels": map[string]any{"scene": "评测", "difficulty": "难", "tags": []string{"冒烟", "回归"}},
	}, dbPath)
	if !okUp || cUp == nil {
		t.Fatalf("UpdatePresetCase failed: %s", msgUp)
	}
	if cUp["id"] != "CASE-001" {
		t.Errorf("编号不应变，实际 %v", cUp["id"])
	}
	if cUp["name"] != "评测-001" {
		t.Errorf("名称应按新场景与原序号重算为 评测-001，实际 %v", cUp["name"])
	}
	upLabels, _ := cUp["labels"].(map[string]any)
	if l, _ := upLabels["tags"].([]string); len(l) != 2 || l[0] != "冒烟" || l[1] != "回归" {
		t.Errorf("标签应被改写为 [冒烟 回归]，实际 %v", upLabels["tags"])
	}

	db2, err := GetConnection(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	var seqAfter int
	var createdAfter, promptAfter string
	if err := db2.QueryRow("SELECT seq, created_at, prompt FROM testcases WHERE id = 'CASE-001'").Scan(&seqAfter, &createdAfter, &promptAfter); err != nil {
		t.Fatalf("读编辑后状态失败: %v", err)
	}
	if seqAfter != seqBefore {
		t.Errorf("序号不应变：%d -> %d", seqBefore, seqAfter)
	}
	if createdAfter != createdBefore {
		t.Errorf("创建时间不应变：%s -> %s", createdBefore, createdAfter)
	}
	if promptAfter != `["改后的提问"]` {
		t.Errorf("库里提问应已改写，实际 %q", promptAfter)
	}

	// 2. 只改自己的标签：不能和自己撞车
	if ok2, msg2, _ := UpdatePresetCase(map[string]any{
		"id":     "CASE-001",
		"prompt": []string{"改后的提问"},
		"labels": map[string]any{"scene": "评测", "difficulty": "难", "tags": []string{"只改标签"}},
	}, dbPath); !ok2 {
		t.Fatalf("只改标签不应被自己判重: %s", msg2)
	}

	// 3. 改成与另一条用例完全一致 -> 拒绝
	okDup, msgDup, cDup := UpdatePresetCase(map[string]any{
		"id":     "CASE-001",
		"prompt": []string{"另一条用例"},
		"labels": map[string]any{"scene": "备课", "difficulty": "易"},
	}, dbPath)
	if okDup || cDup != nil {
		t.Fatalf("与别的用例完全一致时应拒绝，实际 ok=%v case=%v", okDup, cDup)
	}
	if !strings.Contains(msgDup, "CASE-002") {
		t.Errorf("判重提示应指向撞车的那条 CASE-002，实际 %q", msgDup)
	}

	// 4. 编号不存在 / 提问为空
	if okNo, msgNo, _ := UpdatePresetCase(map[string]any{"id": "CASE-999", "prompt": []string{"x"}}, dbPath); okNo {
		t.Error("不存在的编号应报错")
	} else if !strings.Contains(msgNo, "不存在") {
		t.Errorf("不存在的编号应给出明确提示，实际 %q", msgNo)
	}
	if okEmpty, msgEmpty, _ := UpdatePresetCase(map[string]any{"id": "CASE-001", "prompt": []string{"  "}}, dbPath); okEmpty {
		t.Error("提问为空应报错")
	} else if msgEmpty != "提问为必填" {
		t.Errorf("提问为空的提示应为「提问为必填」，实际 %q", msgEmpty)
	}
	if _, msgNoID, _ := UpdatePresetCase(map[string]any{"prompt": []string{"x"}}, dbPath); !strings.Contains(msgNoID, "id") {
		t.Errorf("缺 id 应给出明确提示，实际 %q", msgNoID)
	}
}

// 标签筛选：多选取**交集**（所选标签必须全部命中）、可与场景 / 难度 / 附件组合；
// 选项列表来自库内实际值（去重升序）；空库与「选了标签但没人命中」都返回空集而不是报错。
func TestQueryPresetCases_TagFilter(t *testing.T) {
	dbPath := createTempDB(t)

	// 空库：tags 必须是非 nil 空切片（nil 会被 JSON 序列化成 null）
	empty, err := QueryPresetCases("", nil, nil, []string{"回归"}, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if list, ok := empty["tags"].([]string); !ok || list == nil || len(list) != 0 {
		t.Fatalf("空库的 tags 应是非 nil 空切片，实际 %#v", empty["tags"])
	}
	if cs, _ := empty["cases"].([]map[string]any); len(cs) != 0 {
		t.Errorf("空库里选任何标签都应是空集，实际 %v", cs)
	}

	seed := func(prompt, scene, difficulty string, tags []string, attachment bool) {
		t.Helper()
		labels := map[string]any{"scene": scene}
		if difficulty != "" {
			labels["difficulty"] = difficulty
		}
		if len(tags) > 0 {
			labels["tags"] = tags
		}
		if attachment {
			labels["attachment"] = true
		}
		if ok, msg, _ := AddPresetCase(map[string]any{"prompt": []string{prompt}, "labels": labels}, dbPath); !ok {
			t.Fatalf("准备用例 %q 失败: %s", prompt, msg)
		}
	}
	seed("带回归的题", "备课", "易", []string{"回归", "P0"}, false)
	seed("带冒烟的题", "备课", "难", []string{"冒烟"}, false)
	seed("回归加冒烟", "评测", "易", []string{"冒烟", "回归"}, true)
	seed("没有标签的题", "备课", "", nil, false)

	prompts := func(res map[string]any) []string {
		t.Helper()
		out := []string{}
		for _, c := range res["cases"].([]map[string]any) {
			out = append(out, firstTurn(c))
		}
		return out
	}
	sorted := func(xs []string) string {
		cp := append([]string{}, xs...)
		sort.Strings(cp)
		return strings.Join(cp, ",")
	}

	// 1. 单选标签：带这个标签的都留下
	res, err := QueryPresetCases("", nil, nil, []string{"回归"}, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if got := sorted(prompts(res)); got != "回归加冒烟,带回归的题" {
		t.Errorf("tags=回归 应命中 2 条，实际 %v", got)
	}

	// 2. 多选取交集：两个标签都带的才留下（只带其中一个的被排除）
	res, err = QueryPresetCases("", nil, nil, []string{"回归", "冒烟"}, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if got := prompts(res); len(got) != 1 || got[0] != "回归加冒烟" {
		t.Errorf("tags=回归,冒烟 取交集应只剩「回归加冒烟」，实际 %v", got)
	}
	// 选项列表仍是全局的库内实际值（去重升序、不含空标签），不随当前所选标签收窄
	tags, ok := res["tags"].([]string)
	if !ok {
		t.Fatalf("tags 应是 []string，实际 %T", res["tags"])
	}
	if len(tags) != 3 || tags[0] != "P0" || tags[1] != "冒烟" || tags[2] != "回归" {
		t.Errorf("tags 应为库内实际值升序去重 [P0 冒烟 回归]，实际 %v", tags)
	}

	// 3. 交集里有一个标签没有任何用例同时满足 -> 空集
	res, err = QueryPresetCases("", nil, nil, []string{"回归", "P0", "冒烟"}, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if got := prompts(res); len(got) != 0 {
		t.Errorf("没有用例同时带 回归+P0+冒烟，应是空集，实际 %v", got)
	}

	// 4. 与场景组合（场景仍是「或」，这里的「或」只在候选值之间生效）
	res, err = QueryPresetCases("", []string{"备课"}, nil, []string{"回归"}, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if got := prompts(res); len(got) != 1 || got[0] != "带回归的题" {
		t.Errorf("标签+场景组合应只剩「带回归的题」，实际 %v", got)
	}

	// 5. 与难度组合
	res, err = QueryPresetCases("", nil, []string{"难"}, []string{"冒烟"}, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if got := prompts(res); len(got) != 1 || got[0] != "带冒烟的题" {
		t.Errorf("标签+难度组合应只剩「带冒烟的题」，实际 %v", got)
	}

	// 6. 与附件筛选组合
	res, err = QueryPresetCases("", nil, nil, []string{"回归"}, "yes", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if got := prompts(res); len(got) != 1 || got[0] != "回归加冒烟" {
		t.Errorf("标签+有附件应只剩「回归加冒烟」，实际 %v", got)
	}

	// 7. 选了不存在的标签：空集，且不能拼出非法 SQL
	res, err = QueryPresetCases("", nil, nil, []string{"没有这个标签"}, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("选不存在的标签不应报错: %v", err)
	}
	if got := prompts(res); len(got) != 0 {
		t.Errorf("选不存在的标签应是空集，实际 %v", got)
	}
	if total, _ := res["total"].(int); total != 0 {
		t.Errorf("空集的 total 应为 0，实际 %v", res["total"])
	}

	// 8. 不传标签即不过滤，没有标签的行照常返回
	res, err = QueryPresetCases("", nil, nil, nil, "", nil, 50, 0, "desc", dbPath)
	if err != nil {
		t.Fatalf("QueryPresetCases failed: %v", err)
	}
	if got := prompts(res); len(got) != 4 {
		t.Errorf("不传标签应返回全部 4 条，实际 %v", got)
	}
}

// 老库（testcases 表还没有 tags 列）必须被幂等迁移补列，既有行一行不丢、标签为空。
func TestInitDB_MigratesLegacyTableWithoutTags(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "legacy_tags.db")

	db, err := GetConnection(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
	CREATE TABLE testcases (
		id TEXT PRIMARY KEY,
		name TEXT NOT NULL DEFAULT '',
		prompt TEXT NOT NULL,
		scene TEXT NOT NULL DEFAULT '',
		difficulty TEXT NOT NULL DEFAULT '',
		targets TEXT NOT NULL DEFAULT '[]',
		attachment INTEGER NOT NULL DEFAULT 0,
		expect_tools TEXT NOT NULL DEFAULT '[]',
		attachments TEXT NOT NULL DEFAULT '[]',
		seq INTEGER NOT NULL DEFAULT 0,
		created_at TEXT NOT NULL DEFAULT (datetime('now', 'localtime')),
		updated_at TEXT NOT NULL DEFAULT (datetime('now', 'localtime'))
	);
	INSERT INTO testcases (id, name, prompt, scene, difficulty, attachment, expect_tools, attachments, seq)
	VALUES ('CASE-003', '备课-003', '迁移前的老用例', '备课', '易', 0, '[]', '[]', 3);
	`); err != nil {
		db.Close()
		t.Fatalf("构造老库失败: %v", err)
	}
	db.Close()

	if _, err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB 迁移失败: %v", err)
	}
	// 幂等：再跑一次不能报错
	if _, err := InitDB(dbPath); err != nil {
		t.Fatalf("InitDB 应可重复执行: %v", err)
	}

	db2, err := GetConnection(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()

	if !hasColumn(t, db2, "tags") {
		t.Fatal("迁移后 testcases 应已补上 tags 列")
	}
	var rawTags, rawPrompt string
	if err := db2.QueryRow("SELECT tags, prompt FROM testcases WHERE id = 'CASE-003'").Scan(&rawTags, &rawPrompt); err != nil {
		t.Fatalf("读老行的 tags 失败: %v", err)
	}
	if rawTags != "[]" {
		t.Errorf("老行补列后 tags 应为 []，实际 %q", rawTags)
	}
	if rawPrompt != "迁移前的老用例" {
		t.Errorf("老行数据不应被改动，实际 %q", rawPrompt)
	}

	cases, err := GetPresetCases(dbPath)
	if err != nil {
		t.Fatalf("GetPresetCases failed: %v", err)
	}
	if len(cases) != 1 {
		t.Fatalf("迁移不应丢数据，期望 1 条，实际 %d", len(cases))
	}
	if labels, _ := cases[0]["labels"].(map[string]any); labels["tags"] != nil {
		t.Errorf("老行没有标签，labels 里不应出现 tags，实际 %v", cases[0]["labels"])
	}

	// 迁移后可照常写入并读出标签
	ok, msg, c := AddPresetCase(map[string]any{
		"prompt": []string{"迁移后新增的带标签用例"},
		"labels": map[string]any{"scene": "备课", "tags": []string{"回归"}},
	}, dbPath)
	if !ok || c == nil {
		t.Fatalf("迁移后写入失败: %s", msg)
	}
	after, err := GetPresetCases(dbPath)
	if err != nil {
		t.Fatalf("GetPresetCases failed: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("迁移后应有 2 条，实际 %d", len(after))
	}
	got := map[string][]string{}
	for _, cc := range after {
		l, _ := cc["labels"].(map[string]any)
		tl, _ := l["tags"].([]string)
		got[firstTurn(cc)] = tl
	}
	if len(got["迁移后新增的带标签用例"]) != 1 || got["迁移后新增的带标签用例"][0] != "回归" {
		t.Errorf("迁移后写入的标签应能读回，实际 %v", got["迁移后新增的带标签用例"])
	}
}

// firstTurn 取用例的第 1 轮提问。
// prompt 自多轮改造起是数组（单轮也是长度 1 的数组），测试里按第 1 轮建索引。
func firstTurn(c map[string]any) string {
	switch v := c["prompt"].(type) {
	case []string:
		if len(v) > 0 {
			return v[0]
		}
	case []any:
		if len(v) > 0 {
			return fmt.Sprintf("%v", v[0])
		}
	case string:
		return v
	}
	return ""
}
