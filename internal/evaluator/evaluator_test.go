package evaluator

import (
	"testing"

	"ruiyun-ui-test-platform-go/internal/artifacts"
	"ruiyun-ui-test-platform-go/internal/models"
	"ruiyun-ui-test-platform-go/internal/rubric"
)

func TestCoerceScore(t *testing.T) {
	if s := coerceScore(5, rubric.Scale1To5); s == nil || *s != 5 {
		t.Fatalf("expected 5, got %v", s)
	}
	if s := coerceScore(6, rubric.Scale1To5); s != nil {
		t.Fatalf("expected nil for 6 in 1-5, got %v", s)
	}
	if s := coerceScore(3, rubric.Scale035); s == nil || *s != 3 {
		t.Fatalf("expected 3 in 0-3-5, got %v", s)
	}
	if s := coerceScore(4, rubric.Scale035); s != nil {
		t.Fatalf("expected nil for 4 in 0-3-5, got %v", s)
	}
	if s := coerceScore(0, rubric.Scale50); s == nil || *s != 0 {
		t.Fatalf("expected 0 in 5-0, got %v", s)
	}
}

func TestParseJSONRobust(t *testing.T) {
	raw := "```json\n{\"requirements\": [\"包含教案\"], \"dimensions\": {\"correctness\": {\"score\": 5, \"reason\": \"正确\"}}}\n```"
	obj := parseJSONRobust(raw)
	if obj == nil {
		t.Fatalf("failed to parse JSON from markdown code block")
	}
	dims, ok := obj["dimensions"].(map[string]interface{})
	if !ok {
		t.Fatalf("dimensions not found")
	}
	c, ok := dims["correctness"].(map[string]interface{})
	if !ok || c["score"] != float64(5) {
		t.Fatalf("unexpected correctness score: %v", c)
	}
}

func TestEvaluateCaseWithMockJudge(t *testing.T) {
	mockJudge := func(caseItem map[string]interface{}, trace *models.ExecutionTrace,
		arts *artifacts.ArtifactSet, labels map[string]interface{},
		dims []rubric.Dimension, objective map[string]interface{}, cfg map[string]interface{},
		facts map[string]interface{}) JudgeResult {
		return JudgeResult{
			Dims: map[string]interface{}{
				"teaching_professionalism": map[string]interface{}{
					"score":  5,
					"reason": "教学逻辑清晰，学科规范",
				},
				"safety": map[string]interface{}{
					"score":  5,
					"reason": "安全合规",
				},
			},
			Requirements: []string{"包含教案"},
			Usage: map[string]interface{}{
				"input_tokens":  100,
				"output_tokens": 50,
				"total_tokens":  150,
			},
		}
	}

	caseItem := map[string]interface{}{
		"case_id":        "CASE-001",
		"name":           "初中物理备课测试",
		"prompt":         "请生成一份初中物理阿基米德原理教案",
		"status":         "PASS",
		"elapsed_s":      12.5,
		"answer_excerpt": "这是关于阿基米德原理的完整教学设计...",
	}

	bundle := &RoundBundle{
		RunID:    "round_test",
		CasesDef: []interface{}{},
		Cases:    []map[string]interface{}{caseItem},
	}

	res := evaluateCase("round_test", caseItem, bundle, map[string]map[string]interface{}{},
		map[string][]map[string]interface{}{}, nil, map[string]interface{}{}, mockJudge)

	if res["case_id"] != "CASE-001" {
		t.Fatalf("expected case_id CASE-001, got %v", res["case_id"])
	}
	scores, ok := res["scores"].([]map[string]interface{})
	if !ok || len(scores) == 0 {
		t.Fatalf("scores empty or invalid")
	}

	foundTeaching := false
	for _, s := range scores {
		if s["key"] == "teaching_professionalism" {
			foundTeaching = true
			if s["score"] != 5 {
				t.Fatalf("expected teaching_professionalism 5, got %v", s["score"])
			}
		}
	}
	if !foundTeaching {
		t.Fatalf("teaching_professionalism score not found in evaluated case")
	}
}
