package xlsx

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"

	"ruiyun-ui-test-platform-go/internal/intent"
)

var (
	cellNL = regexp.MustCompile("[\u000b\u000c]")

	promptHeaders     = []string{"提问", "问题", "prompt", "query", "指令", "提示词", "输入", "题目", "测试内容"}
	sceneHeaders      = []string{"场景", "业务场景", "教学场景", "scene", "分类", "模块", "类别"}
	targetHeaders     = []string{"测试目标", "产物格式", "目标", "target", "交付物"}
	nameHeaders       = []string{"序号", "编号", "用例名称", "用例名", "名称", "标题", "id", "name", "seq", "no"}
	attachmentHeaders = []string{"附件", "参考文件", "文件", "附件名", "attachment", "attachments", "file"}
	skillHeaders      = []string{"skill", "技能", "期望工具", "工具", "expect_tools", "tools"}

	knownTargets = []string{"word", "ppt", "html", "pdf", "excel", "network"}

	// kindOrder 固定第 3 步（语义推断）的遍历顺序。
	// 若此处遍历 set，顺序会随哈希种子随机化 —— 同一份输入
	// 多次运行会得到不同顺序的 targets。这里显式固定顺序以保证可复现。
	kindOrder = []string{"docx", "pptx", "excel", "pdf", "html", "network"}
)

func matchWordBoundary(text, target string) bool {
	tLower := strings.ToLower(text)
	targetLower := strings.ToLower(target)
	idx := 0
	for {
		pos := strings.Index(tLower[idx:], targetLower)
		if pos == -1 {
			return false
		}
		actualPos := idx + pos
		leftOk := (actualPos == 0) || !unicode.IsLetter(rune(tLower[actualPos-1]))
		rightIdx := actualPos + len(targetLower)
		rightOk := (rightIdx == len(tLower)) || !unicode.IsLetter(rune(tLower[rightIdx]))
		if leftOk && rightOk {
			return true
		}
		idx = actualPos + 1
	}
}

// hitKey 报告 text 是否命中 keys 中的任意一个关键词。
func hitKey(text string, keys []string) bool {
	for _, k := range keys {
		if strings.Contains(text, k) {
			return true
		}
	}
	return false
}

// InferTargets 从显式目标列、附件后缀或提问语义推断测试目标
func InferTargets(prompt, attachmentName, rawTarget string) []string {
	// 初始化为空切片而非 nil：nil 会被 encoding/json 序列化成 null，
	// 而契约要求返回 []，前端依赖二者语义一致。
	targets := []string{}

	// 1. 显式白名单匹配
	if rawTarget != "" {
		for _, t := range knownTargets {
			if matchWordBoundary(rawTarget, t) {
				targets = append(targets, t)
			}
		}
		if len(targets) > 0 {
			return targets
		}
	}

	// 2. 从附件文件名后缀推断
	attLower := strings.ToLower(attachmentName)
	if strings.HasSuffix(attLower, ".docx") || strings.HasSuffix(attLower, ".doc") || strings.HasSuffix(attLower, ".dotx") {
		targets = append(targets, "word")
	} else if strings.HasSuffix(attLower, ".pptx") || strings.HasSuffix(attLower, ".ppt") || strings.HasSuffix(attLower, ".potx") {
		targets = append(targets, "ppt")
	} else if strings.HasSuffix(attLower, ".xlsx") || strings.HasSuffix(attLower, ".xls") || strings.HasSuffix(attLower, ".csv") {
		targets = append(targets, "excel")
	} else if strings.HasSuffix(attLower, ".pdf") {
		targets = append(targets, "pdf")
	} else if strings.HasSuffix(attLower, ".html") || strings.HasSuffix(attLower, ".htm") {
		targets = append(targets, "html")
	}

	// 3. 从提问文本语义推断
	kinds, _ := intent.InferTargetKinds(prompt)
	kindMap := map[string]string{
		"docx": "word", "pptx": "ppt", "excel": "excel",
		"pdf": "pdf", "html": "html", "network": "network",
	}
	for _, k := range kindOrder {
		if !kinds[k] {
			continue
		}
		mapped := kindMap[k]
		if mapped != "" {
			already := false
			for _, t := range targets {
				if t == mapped {
					already = true
					break
				}
			}
			if !already {
				targets = append(targets, mapped)
			}
		}
	}

	// 4. 中文关键词特征补全
	pLower := strings.ToLower(prompt)
	if len(targets) == 0 {
		wordKeys := []string{"教案", "教学设计", "文档", "word", "撰写", "通报", "总结", "方案", "通知", "倡议书", "计划", "发言稿", "讲话稿"}
		pptKeys := []string{"ppt", "课件", "幻灯片", "演示文稿"}
		excelKeys := []string{"excel", "表格", "成绩表", "统计表", "排班表", "清单"}

		hitWord := false
		for _, w := range wordKeys {
			if strings.Contains(pLower, w) {
				targets = append(targets, "word")
				hitWord = true
				break
			}
		}
		// 这里是 if/elif 互斥链：命中前一个分支就不再判后面的。
		// 必须保持互斥，否则同时含「课件」和「表格」的提问会同时补出
		// ppt 与 excel（多推一个目标，下游断言会跟着变）。
		if !hitWord {
			if hitKey(pLower, pptKeys) {
				targets = append(targets, "ppt")
			} else if hitKey(pLower, excelKeys) {
				targets = append(targets, "excel")
			} else if strings.Contains(pLower, "pdf") {
				targets = append(targets, "pdf")
			} else if strings.Contains(pLower, "html") || strings.Contains(pLower, "网页") {
				targets = append(targets, "html")
			}
		}
	}

	// 5. 兜底为 word
	if len(targets) == 0 {
		targets = append(targets, "word")
	}

	return targets
}

func isHeaderLike(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" || len([]rune(v)) > 12 {
		return false
	}
	rePunct := regexp.MustCompile(`[。？?!！,，;；]`)
	return !rePunct.MatchString(v)
}

// DetectColumns 认出表头行与各列下标
func DetectColumns(rows [][]string) map[string]any {
	// 去掉尾部整行空行
	for len(rows) > 0 {
		last := rows[len(rows)-1]
		hasContent := false
		for _, c := range last {
			if strings.TrimSpace(c) != "" {
				hasContent = true
				break
			}
		}
		if hasContent {
			break
		}
		rows = rows[:len(rows)-1]
	}

	if len(rows) == 0 {
		return map[string]any{
			"header":         []string{},
			"head_idx":       nil,
			"prompt_col":     0,
			"scene_col":      nil,
			"target_col":     nil,
			"name_col":       nil,
			"attachment_col": nil,
			"skill_col":      nil,
			"total_rows":     0,
			"preview":        [][]string{},
		}
	}

	rowWidth := func(r []string) int {
		maxIdx := -1
		for i, c := range r {
			if strings.TrimSpace(c) != "" {
				maxIdx = i
			}
		}
		return maxIdx + 1
	}

	var headIdx *int
	best := 0

	scanLimit := 10
	if len(rows) < scanLimit {
		scanLimit = len(rows)
	}

	for i := 0; i < scanLimit; i++ {
		r := rows[i]
		var cells []string
		for _, c := range r {
			if strings.TrimSpace(c) != "" {
				cells = append(cells, c)
			}
		}
		if len(cells) == 0 {
			continue
		}
		allHeaderLike := true
		for _, c := range cells {
			if !isHeaderLike(c) {
				allHeaderLike = false
				break
			}
		}
		if len(cells) > best && allHeaderLike {
			idxCopy := i
			headIdx = &idxCopy
			best = len(cells)
		}
	}

	var header []string
	var body [][]string
	if headIdx != nil {
		header = rows[*headIdx]
		body = rows[*headIdx+1:]
	} else {
		header = []string{}
		body = rows
	}

	maxWidth := rowWidth(header)
	for _, r := range body {
		w := rowWidth(r)
		if w > maxWidth {
			maxWidth = w
		}
	}
	if maxWidth < 1 {
		maxWidth = 1
	}

	headerCol := func(names []string, exclude map[int]bool, forbid []string) *int {
		for i := 0; i < maxWidth; i++ {
			text := ""
			if i < len(header) {
				text = strings.ToLower(strings.TrimSpace(header[i]))
			}
			if text == "" || exclude[i] {
				continue
			}
			forbidden := false
			for _, f := range forbid {
				if strings.Contains(text, strings.ToLower(f)) {
					forbidden = true
					break
				}
			}
			if forbidden {
				continue
			}
			for _, nm := range names {
				if strings.Contains(text, strings.ToLower(nm)) {
					val := i
					return &val
				}
			}
		}
		return nil
	}

	promptCol := headerCol(promptHeaders, nil, []string{"类型", "type"})

	excludeScene := make(map[int]bool)
	if promptCol != nil {
		excludeScene[*promptCol] = true
	}
	sceneCol := headerCol(sceneHeaders, excludeScene, nil)

	excludeName := make(map[int]bool)
	if promptCol != nil {
		excludeName[*promptCol] = true
	}
	if sceneCol != nil {
		excludeName[*sceneCol] = true
	}
	nameCol := headerCol(nameHeaders, excludeName, nil)

	excludeAttach := copySet(excludeName)
	if nameCol != nil {
		excludeAttach[*nameCol] = true
	}
	attachmentCol := headerCol(attachmentHeaders, excludeAttach, nil)

	excludeSkill := copySet(excludeAttach)
	if attachmentCol != nil {
		excludeSkill[*attachmentCol] = true
	}
	skillCol := headerCol(skillHeaders, excludeSkill, nil)

	excludeTarget := copySet(excludeSkill)
	if skillCol != nil {
		excludeTarget[*skillCol] = true
	}
	targetCol := headerCol(targetHeaders, excludeTarget, []string{"输入类型"})

	actualPromptCol := 0
	if promptCol != nil {
		actualPromptCol = *promptCol
	} else {
		// 没有可识别的表头 -> 取正文里平均文本最长的一列
		sums := make(map[int]int)
		counts := make(map[int]int)
		for _, r := range body {
			for i := 0; i < maxWidth; i++ {
				v := ""
				if i < len(r) {
					v = r[i]
				}
				if strings.TrimSpace(v) != "" {
					sums[i] += len([]rune(v))
					counts[i]++
				}
			}
		}
		bestAvg := -1.0
		for i := 0; i < maxWidth; i++ {
			c := counts[i]
			if c == 0 {
				continue
			}
			avg := float64(sums[i]) / float64(c)
			if avg > bestAvg {
				bestAvg = avg
				actualPromptCol = i
			}
		}
	}

	return map[string]any{
		"header":         header,
		"head_idx":       headIdx,
		"prompt_col":     actualPromptCol,
		"scene_col":      sceneCol,
		"target_col":     targetCol,
		"name_col":       nameCol,
		"attachment_col": attachmentCol,
		"skill_col":      skillCol,
		"total_rows":     len(body),
		"preview":        body,
	}
}

func copySet(s map[int]bool) map[int]bool {
	res := make(map[int]bool, len(s))
	for k, v := range s {
		res[k] = v
	}
	return res
}

// RowsToItems 按列下标从数据行里取值
func RowsToItems(rows [][]string, headIdx *int, promptCol int,
	sceneCol, targetCol, nameCol, attachmentCol, skillCol *int) map[string]any {

	startIdx := 0
	if headIdx != nil {
		startIdx = *headIdx + 1
	}
	if startIdx > len(rows) {
		startIdx = len(rows)
	}
	body := rows[startIdx:]

	// 空切片而非 nil：nil 会序列化成 null，契约要求返回 []。
	items := []map[string]any{}
	skipped := 0

	getVal := func(r []string, col *int) string {
		if col == nil || *col < 0 || *col >= len(r) {
			return ""
		}
		return strings.TrimSpace(r[*col])
	}

	for _, r := range body {
		pCol := promptCol
		prompt := getVal(r, &pCol)
		if prompt == "" {
			hasAny := false
			for _, c := range r {
				if strings.TrimSpace(c) != "" {
					hasAny = true
					break
				}
			}
			if hasAny {
				skipped++
			}
			continue
		}

		scene := getVal(r, sceneCol)
		rawTargets := getVal(r, targetCol)
		attVal := getVal(r, attachmentCol)
		nameVal := getVal(r, nameCol)
		skillVal := getVal(r, skillCol)

		targets := InferTargets(prompt, attVal, rawTargets)

		attachments := []string{}
		if attVal != "" {
			attachments = append(attachments, attVal)
		}
		hasAttachment := len(attachments) > 0
		if !hasAttachment {
			for _, c := range r {
				if strings.Contains(c, "附件") {
					hasAttachment = true
					break
				}
			}
		}

		cid := ""
		cname := ""
		isAllDigits := true
		for _, ch := range nameVal {
			if !unicode.IsDigit(ch) {
				isAllDigits = false
				break
			}
		}
		if nameVal != "" && isAllDigits {
			seq, err := strconv.Atoi(nameVal)
			if err == nil {
				cid = fmt.Sprintf("CASE-%03d", seq)
				if scene != "" {
					cname = fmt.Sprintf("%s-%03d", scene, seq)
				} else {
					cname = fmt.Sprintf("用例-%03d", seq)
				}
			} else {
				cname = nameVal
			}
		} else if nameVal != "" {
			cname = nameVal
		}

		expectTools := []string{}
		if skillVal != "" {
			for _, s := range strings.Split(skillVal, ",") {
				s = strings.TrimSpace(s)
				if s != "" {
					expectTools = append(expectTools, s)
				}
			}
		}

		labels := map[string]any{
			"scene":      scene,
			"targets":    targets,
			"attachment": hasAttachment,
		}

		knownCols := map[int]bool{
			promptCol: true,
		}
		if sceneCol != nil {
			knownCols[*sceneCol] = true
		}
		if targetCol != nil {
			knownCols[*targetCol] = true
		}
		if nameCol != nil {
			knownCols[*nameCol] = true
		}
		if attachmentCol != nil {
			knownCols[*attachmentCol] = true
		}
		if skillCol != nil {
			knownCols[*skillCol] = true
		}

		if headIdx != nil && *headIdx >= 0 && *headIdx < len(rows) {
			header := rows[*headIdx]
			for j, h := range header {
				if !knownCols[j] {
					hName := strings.TrimSpace(h)
					colJ := j
					val := getVal(r, &colJ)
					if hName != "" && val != "" {
						if strings.Contains(hName, "学科") {
							labels["subject"] = val
						} else if strings.Contains(hName, "难度") {
							labels["difficulty"] = val
						} else if strings.Contains(hName, "输入类型") {
							labels["input_type"] = val
						} else if strings.Contains(hName, "强项") {
							labels["is_strength"] = val
						} else {
							labels[hName] = val
						}
					}
				}
			}
		}

		cleanPrompt := cellNL.ReplaceAllString(prompt, "\n")
		item := map[string]any{
			"prompt":       cleanPrompt,
			"scene":        scene,
			"targets":      targets,
			"raw_target":   rawTargets,
			"name":         cname,
			"attachments":  attachments,
			"attachment":   hasAttachment,
			"expect_tools": expectTools,
			"labels":       labels,
		}
		if cid != "" {
			item["id"] = cid
		}
		items = append(items, item)
	}

	return map[string]any{
		"items":   items,
		"skipped": skipped,
	}
}

// ReadXLSX 读取 xlsx
func ReadXLSX(data []byte, maxRows int) (map[string]any, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("读取 xlsx 失败: %w", err)
	}
	defer f.Close()

	sheetList := f.GetSheetList()
	if len(sheetList) == 0 {
		return map[string]any{"rows": [][]string{}, "sheet": "sheet1"}, nil
	}

	sheetName := sheetList[0]
	rows, err := f.GetRows(sheetName)
	if err != nil {
		return nil, fmt.Errorf("读取表格行失败: %w", err)
	}

	var resRows [][]string
	for i, r := range rows {
		if maxRows > 0 && i >= maxRows {
			break
		}
		var cleanRow []string
		for _, c := range r {
			cleanRow = append(cleanRow, cellNL.ReplaceAllString(c, "\n"))
		}
		// 去除行末尾纯空字符串单元格
		for len(cleanRow) > 0 && strings.TrimSpace(cleanRow[len(cleanRow)-1]) == "" {
			cleanRow = cleanRow[:len(cleanRow)-1]
		}
		resRows = append(resRows, cleanRow)
	}

	// 保持既定的返回约定：sheet 名统一小写
	return map[string]any{
		"rows":  resRows,
		"sheet": strings.ToLower(sheetName),
	}, nil
}

// ReadCSV 读取 csv/tsv
func ReadCSV(data []byte, maxRows int) (map[string]any, error) {
	// 去除 UTF-8 BOM
	if bytes.HasPrefix(data, []byte{0xEF, 0xBB, 0xBF}) {
		data = data[3:]
	}

	var text string
	// 尝试 UTF-8
	if isUTF8(data) {
		text = string(data)
	} else {
		// 尝试 GBK / GB18030
		reader := transform.NewReader(bytes.NewReader(data), simplifiedchinese.GB18030.NewDecoder())
		decoded, err := io.ReadAll(reader)
		if err == nil {
			text = string(decoded)
		} else {
			return nil, fmt.Errorf("无法识别文件编码（试过 UTF-8 / GBK）")
		}
	}

	sampleLen := 4096
	if len(text) < sampleLen {
		sampleLen = len(text)
	}
	sample := text[:sampleLen]
	delim := ','
	if strings.Count(sample, "\t") > strings.Count(sample, ",") {
		delim = '\t'
	}

	r := csv.NewReader(strings.NewReader(text))
	r.Comma = delim
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	var rows [][]string
	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		var cleanRec []string
		for _, c := range rec {
			cleanRec = append(cleanRec, strings.TrimSpace(c))
		}
		rows = append(rows, cleanRec)
		if maxRows > 0 && len(rows) >= maxRows {
			break
		}
	}

	return map[string]any{
		"rows":  rows,
		"sheet": "csv",
	}, nil
}

func isUTF8(b []byte) bool {
	return bytes.Equal(b, []byte(string(b)))
}

// ReadTable 统一读取表格入口
func ReadTable(filename string, data []byte, maxRows int) (map[string]any, error) {
	name := strings.ToLower(filename)
	if strings.HasSuffix(name, ".xlsx") || strings.HasSuffix(name, ".xlsm") ||
		strings.HasSuffix(name, ".xltx") || strings.HasSuffix(name, ".xltm") {
		return ReadXLSX(data, maxRows)
	}
	return ReadCSV(data, maxRows)
}
