package xlsx

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

var (
	cellNL = regexp.MustCompile("[\u000b\u000c]")

	promptHeaders     = []string{"提问", "问题", "prompt", "query", "指令", "提示词", "输入", "题目", "测试内容"}
	sceneHeaders      = []string{"场景", "业务场景", "教学场景", "scene", "分类", "模块", "类别"}
	difficultyHeaders = []string{"难度", "difficulty", "难易"}
	attachmentHeaders = []string{"附件", "参考文件", "文件", "附件名", "attachment", "attachments", "file"}
)

// MaxTurnCols 多轮列的最大数量，与 testcasedb.MaxTurns 一致。
// 表头识别 prompt_1 / prompt_2 / … / prompt_8（也接受 提问1 / 提问_1 等写法）。
const MaxTurnCols = 8

// turnColRe 匹配「第 N 轮」列头：prompt_1 / 提问2 / 提问_3 / turn1 / 第1轮 等。
var turnColRe = regexp.MustCompile(`^(?:prompt|提问|问题|turn|轮次|第)[\s_\-]*(\d+)[\s_\-]*(?:轮)?$`)

// turnColIndex 解析「第 N 轮」列头，返回 1-based 轮号；不匹配返回 0。
func turnColIndex(header string) int {
	h := strings.ToLower(strings.TrimSpace(header))
	if h == "" {
		return 0
	}
	m := turnColRe.FindStringSubmatch(h)
	if m == nil {
		return 0
	}
	n := 0
	for _, r := range m[1] {
		n = n*10 + int(r-'0')
	}
	if n < 1 || n > MaxTurnCols {
		return 0
	}
	return n
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
			"difficulty_col": nil,
			"attachment_col": nil,
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

	// 列识别按固定顺序逐个收窄排除集合，保证同一列不会被认出两次：
	// prompt -> scene -> attachment -> difficulty。
	promptCol := headerCol(promptHeaders, nil, []string{"类型", "type"})

	excludeScene := make(map[int]bool)
	if promptCol != nil {
		excludeScene[*promptCol] = true
	}
	sceneCol := headerCol(sceneHeaders, excludeScene, nil)

	excludeAttach := copySet(excludeScene)
	if sceneCol != nil {
		excludeAttach[*sceneCol] = true
	}
	attachmentCol := headerCol(attachmentHeaders, excludeAttach, nil)

	excludeDifficulty := copySet(excludeAttach)
	if attachmentCol != nil {
		excludeDifficulty[*attachmentCol] = true
	}
	difficultyCol := headerCol(difficultyHeaders, excludeDifficulty, nil)

	// 多轮列：prompt_1 / 提问2 / 第3轮 …（表头必须带数字，普通「提问」列不受影响）
	turnCols := make(map[int]int) // 轮号(1-based) -> 列下标
	excludeTurn := copySet(excludeDifficulty)
	if difficultyCol != nil {
		excludeTurn[*difficultyCol] = true
	}
	if promptCol != nil {
		excludeTurn[*promptCol] = true
	}
	for i, h := range header {
		if excludeTurn[i] {
			continue
		}
		if n := turnColIndex(h); n > 0 {
			if _, dup := turnCols[n]; !dup {
				turnCols[n] = i
			}
		}
	}

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

	hasPromptHeader := promptCol != nil
	// 多轮模板：以 prompt_1 为第 1 轮，普通「提问」列可缺省
	hasTurnCols := len(turnCols) > 0
	if hasTurnCols && turnCols[1] != 0 {
		actualPromptCol = turnCols[1]
	}
	if hasTurnCols {
		hasPromptHeader = true
	}
	isValidTemplate := headIdx != nil && hasPromptHeader
	var templateErr string
	if headIdx == nil {
		templateErr = "未识别到有效的表头行，请使用系统提供的标准模板导入"
	} else if !hasPromptHeader {
		templateErr = "未找到必选列【Query内容】（或【提问】），请使用系统提供的标准模板导入"
	}

	return map[string]any{
		"header":           header,
		"head_idx":         headIdx,
		"prompt_col":       actualPromptCol,
		"prompt_col_found": hasPromptHeader,
		"valid_template":   isValidTemplate,
		"template_error":   templateErr,
		"scene_col":        sceneCol,
		"difficulty_col":   difficultyCol,
		"attachment_col":   attachmentCol,
		"turn_cols":        turnCols,
		"total_rows":       len(body),
		"preview":          body,
	}
}

func copySet(s map[int]bool) map[int]bool {
	res := make(map[int]bool, len(s))
	for k, v := range s {
		res[k] = v
	}
	return res
}

// CaseCols 描述「一行用例的各字段取自哪一列」；nil 表示没有该列。
// 用结构体而不是一串 *int，避免同类型参数按位置传错。
type CaseCols struct {
	Prompt     int
	Scene      *int
	Attachment *int
	Difficulty *int
	// TurnCols 多轮列：轮号(1-based) -> 列下标。空表示单轮模板。
	TurnCols map[int]int
}

// RowsToItems 按列下标从数据行里取值
func RowsToItems(rows [][]string, headIdx *int, cols CaseCols) map[string]any {

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
		pCol := cols.Prompt
		prompt := getVal(r, &pCol)

		// 多轮：按轮号从小到大收集非空的 prompt_N 列，拼成轮次数组。
		// 兼容两种模板：只用 prompt_1..N（无普通「提问」列），
		// 或「提问」列 + prompt_2..N（提问列即第 1 轮）。
		var turns []string
		if len(cols.TurnCols) > 0 {
			maxN := 0
			for n := range cols.TurnCols {
				if n > maxN {
					maxN = n
				}
			}
			for n := 1; n <= maxN; n++ {
				ci, ok := cols.TurnCols[n]
				if !ok {
					continue
				}
				v := getVal(r, &ci)
				if n == 1 && v == "" {
					v = prompt // 用普通「提问」列兜底第 1 轮
				}
				if v != "" {
					turns = append(turns, v)
				}
			}
		} else if prompt != "" {
			turns = append(turns, prompt)
		}

		if len(turns) == 0 {
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

		scene := getVal(r, cols.Scene)
		attVal := getVal(r, cols.Attachment)
		difficulty := getVal(r, cols.Difficulty)

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

		labels := map[string]any{
			"scene":      scene,
			"attachment": hasAttachment,
		}
		if difficulty != "" {
			labels["difficulty"] = difficulty
		}

		knownCols := map[int]bool{
			cols.Prompt: true,
		}
		// 多轮列不算「未知列」，否则 prompt_2 会被当成自定义标签写进 labels
		for _, ci := range cols.TurnCols {
			knownCols[ci] = true
		}
		if cols.Scene != nil {
			knownCols[*cols.Scene] = true
		}
		if cols.Attachment != nil {
			knownCols[*cols.Attachment] = true
		}
		if cols.Difficulty != nil {
			knownCols[*cols.Difficulty] = true
		}

		if headIdx != nil && *headIdx >= 0 && *headIdx < len(rows) {
			header := rows[*headIdx]
			for j, h := range header {
				if !knownCols[j] {
					hName := strings.TrimSpace(h)
					colJ := j
					val := getVal(r, &colJ)
					if hName != "" && val != "" {
						labels[hName] = val
					}
				}
			}
		}

		// 编号（CASE-NNN）与名称由服务端在入库时生成，这里不再从表格推导。
		// prompt 自多轮改造起是**数组**（单轮也是长度 1 的数组）。
		item := map[string]any{
			"prompt":      turns,
			"scene":       scene,
			"difficulty":  difficulty,
			"attachments": attachments,
			"attachment":  hasAttachment,
			"labels":      labels,
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
