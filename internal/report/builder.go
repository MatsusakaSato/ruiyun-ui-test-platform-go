package report

import (
	"bytes"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ReportData 报告渲染上下文
type ReportData struct {
	Summary       map[string]any
	RuleRows      []map[string]any
	Severity      map[string]int
	CaseRows      []map[string]any
	CaseObjective map[string]map[string]any
	CaseFindings  map[string][]map[string]any
	ReproSummary  map[string]any
	MaxRule       int
	AppVersion    string
	BundleID      string
	RunMode       string
	GeneratedAt   string
	TotalElapsed  float64
}

// RenderReport 将度量渲染为自包含的 HTML 报告
func RenderReport(metrics map[string]any, outPath string, appVersion, bundleID, runMode string, totalElapsed float64) (string, error) {
	ruleRows, _ := metrics["rule_rows"].([]map[string]any)
	nonzeroRules := 0
	for _, r := range ruleRows {
		if c, ok := r["count"].(int); ok && c > nonzeroRules {
			nonzeroRules = c
		}
	}

	caseFindings := make(map[string][]map[string]any)
	if fRows, ok := metrics["findings_rows"].([]map[string]any); ok {
		for _, f := range fRows {
			cid := fmt.Sprintf("%v", f["case_id"])
			caseFindings[cid] = append(caseFindings[cid], f)
		}
	}

	summary, _ := metrics["summary"].(map[string]any)
	severity, _ := metrics["severity"].(map[string]int)
	caseRows, _ := metrics["case_rows"].([]map[string]any)
	caseObjective, _ := metrics["case_objective"].(map[string]map[string]any)
	reproSummary, _ := metrics["repro_summary"].(map[string]any)
	if reproSummary == nil {
		reproSummary = map[string]any{"verified": 0}
	}

	data := ReportData{
		Summary:       summary,
		RuleRows:      ruleRows,
		Severity:      severity,
		CaseRows:      caseRows,
		CaseObjective: caseObjective,
		CaseFindings:  caseFindings,
		ReproSummary:  reproSummary,
		MaxRule:       nonzeroRules,
		AppVersion:    appVersion,
		BundleID:      bundleID,
		RunMode:       runMode,
		GeneratedAt:   time.Now().Format("2006-01-02 15:04:05"),
		TotalElapsed:  float64(int(totalElapsed*10)) / 10.0,
	}

	tmplStr := reportTemplateHTML
	tmpl, err := template.New("report").Funcs(template.FuncMap{
		"lower": func(s any) string {
			return strings.ToLower(fmt.Sprintf("%v", s))
		},
		"round": func(f any, decimals int) string {
			var val float64
			switch v := f.(type) {
			case float64:
				val = v
			case int:
				val = float64(v)
			}
			return fmt.Sprintf(fmt.Sprintf("%%.%df", decimals), val)
		},
		"mult": func(a any, b float64) float64 {
			var val float64
			switch v := a.(type) {
			case float64:
				val = v
			case int:
				val = float64(v)
			}
			return val * b
		},
		"getObjective": func(m map[string]map[string]any, caseID string) map[string]any {
			if m == nil {
				return nil
			}
			return m[caseID]
		},
		"getFindings": func(m map[string][]map[string]any, caseID string) []map[string]any {
			if m == nil {
				return nil
			}
			return m[caseID]
		},
		"mapGet": func(m any, key string, fallback any) any {
			if m == nil {
				return fallback
			}
			if dict, ok := m.(map[string]any); ok {
				if val, exists := dict[key]; exists && val != nil {
					return val
				}
			}
			return fallback
		},
	}).Parse(tmplStr)
	if err != nil {
		return "", fmt.Errorf("解析 HTML 模板失败: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("渲染 HTML 报告失败: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(outPath), 0755); err != nil {
		return "", fmt.Errorf("创建报告目录失败: %w", err)
	}
	if err := os.WriteFile(outPath, buf.Bytes(), 0644); err != nil {
		return "", fmt.Errorf("写入报告文件失败: %w", err)
	}

	return outPath, nil
}
