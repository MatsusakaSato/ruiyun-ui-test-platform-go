package repro

import (
	"testing"

	"ruiyun-ui-test-platform-go/internal/models"
)

func TestClassify(t *testing.T) {
	if c := Classify(1.0); c != "必现" {
		t.Fatalf("expected 必现, got %s", c)
	}
	if c := Classify(0.8); c != "高概率复现" {
		t.Fatalf("expected 高概率复现, got %s", c)
	}
	if c := Classify(0.5); c != "偶发" {
		t.Fatalf("expected 偶发, got %s", c)
	}
}

func TestBuildRecipe(t *testing.T) {
	step := 2
	finding := &models.Finding{
		Rule:      "TOOL_CALL_FAILED",
		Severity:  "P0",
		SessionID: "sess_001",
		Tool:      "read_memory",
		Detail:    "调用失败: 找不到文件",
		Evidence:  "error: not found",
		StepIndex: &step,
	}

	trace := &models.ExecutionTrace{
		SessionID:  "sess_001",
		UserPrompt: "请读取记忆文件内容",
		ToolCalls: []*models.ToolCall{
			{
				Index:      1,
				Name:       "read_memory",
				TurnPrompt: "请读取记忆文件",
				Arguments:  map[string]any{"path": "mem.json"},
			},
		},
	}

	traces := map[string]*models.ExecutionTrace{
		"sess_001": trace,
	}

	recipe := BuildRecipe(finding, traces, map[string]any{})
	if recipe.Key != "TOOL_CALL_FAILED:read_memory" {
		t.Fatalf("unexpected recipe key: %s", recipe.Key)
	}
	if recipe.Prompt != "请读取记忆文件" {
		t.Fatalf("unexpected recipe prompt: %s", recipe.Prompt)
	}
	if recipe.PromptSource != "original" {
		t.Fatalf("unexpected prompt source: %s", recipe.PromptSource)
	}
	if len(recipe.Steps) != 5 {
		t.Fatalf("expected 5 steps, got %d", len(recipe.Steps))
	}
}
