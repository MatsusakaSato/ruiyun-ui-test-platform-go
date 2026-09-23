package intent

import (
	"regexp"
	"strings"
)

var produceVerbs = []string{
	"生成", "输出", "导出", "做成", "整理成", "汇总成", "形成", "转成", "另存为",
	"写一份", "写个", "写一份", "给一份", "给我一份", "提供一份", "出一份", "发一份",
	"做一个", "制作", "创建", "下载", "整理一份", "列一份", "整理出",
}

type kindRule struct {
	kind  string
	words []string
}

var kindWords = []kindRule{
	{"pptx", []string{"pptx", "ppt", "幻灯片", "演示文稿", "课件"}},
	{"excel", []string{"xlsx", "excel", "xls", "csv", "电子表格", "表格文件", "表格"}},
	{"docx", []string{"docx", ".doc", "word", "文档", "文稿"}},
	{"pdf", []string{"pdf"}},
	{"html", []string{"html", "网页", "网站"}},
}

var reClause = regexp.MustCompile(`[。！？；;!?\n，,、]+`)

// InferTargetKinds 从问题原文推断需要交付的产物类型
func InferTargetKinds(prompt string) (map[string]bool, []string) {
	text := strings.ToLower(strings.TrimSpace(prompt))
	if text == "" {
		return map[string]bool{}, nil
	}
	kinds := make(map[string]bool)
	var hits []string

	clauses := reClause.Split(text, -1)
	for _, clause := range clauses {
		clause = strings.TrimSpace(clause)
		if clause == "" {
			continue
		}
		verb := ""
		for _, v := range produceVerbs {
			if strings.Contains(clause, v) {
				verb = v
				break
			}
		}
		if verb == "" {
			continue
		}
		for _, kr := range kindWords {
			for _, w := range kr.words {
				if strings.Contains(clause, w) {
					if !kinds[kr.kind] {
						kinds[kr.kind] = true
						hits = append(hits, verb+"…"+w)
					}
					break
				}
			}
		}
	}
	return kinds, hits
}

type sceneRule struct {
	scene    string
	words    []string
	patterns []*regexp.Regexp
}

var sceneRules = []sceneRule{
	{
		scene: "家校沟通",
		words: []string{"家长", "家校", "家访", "家长会", "班主任", "监护人"},
	},
	{
		scene: "作业批改",
		words: []string{"作业", "批改", "评语", "错题", "订正", "试卷", "练习题", "批阅", "命题"},
	},
	{
		scene: "备课",
		words: []string{"教学设计", "教案", "备课", "课时", "板书", "教学目标", "教学过程",
			"学情", "导学案", "说课", "课件", "单元设计", "复习课", "新课导入"},
	},
	{
		scene: "教育-其他",
		words: []string{"教育", "教学", "教师", "学生", "学校", "课堂", "教研", "课题", "教材",
			"年级", "统编版", "学科", "学业", "成绩", "考试", "课程", "学段",
			"知识点", "班级", "论文", "微课", "教研组", "学籍", "团员", "团支部",
			"少先队", "中考", "高考", "升学", "毕业", "幼儿园", "备课组"},
		patterns: []*regexp.Regexp{
			regexp.MustCompile(`[高初][一二三]`),
			regexp.MustCompile(`[一二三四五六七八九]年级`),
			regexp.MustCompile(`小[一二三四五六]|大班|中班|小班`),
		},
	},
}

var nonTeachingWords = []string{
	"合同", "发票", "报销", "财务报表", "税法", "法律", "诉讼", "简历", "招聘",
	"营销", "广告", "代码", "编程", "sql", "接口", "服务器", "运维", "数据库",
	"部署", "周报", "kpi", "旅游", "菜谱", "健身", "股票", "基金", "保险", "房产",
	"产品需求", "项目管理", "客服",
}

// InferScene 从问题原文推断场景标签
func InferScene(prompt string) (string, []string) {
	text := strings.ToLower(strings.TrimSpace(prompt))
	if text == "" {
		return "", nil
	}

	for _, rule := range sceneRules {
		for _, w := range rule.words {
			if strings.Contains(text, w) {
				return rule.scene, []string{w}
			}
		}
		for _, p := range rule.patterns {
			if m := p.FindString(text); m != "" {
				return rule.scene, []string{m}
			}
		}
	}

	for _, nw := range nonTeachingWords {
		if strings.Contains(text, nw) {
			return "非教学", []string{nw}
		}
	}

	return "", nil
}
