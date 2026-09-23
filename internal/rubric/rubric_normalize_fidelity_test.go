package rubric

import "testing"

func f(v float64) *float64 { return &v }

// Python: normalize(score, scale)
//
//	SCALE_1_5   → round((s-1)/4, 4)
//	SCALE_0_3_5 → round(s/5, 4)
//	SCALE_5_0   → round(s/5, 4)
//	SCALE_RATIO → round(s/5, 4)
//	SCALE_RECORD / None → None
func TestNormalizeMatchesPython(t *testing.T) {
	cases := []struct {
		score float64
		scale string
		want  *float64
	}{
		{1, Scale1To5, f(0)}, {2, Scale1To5, f(0.25)},
		{3, Scale1To5, f(0.5)}, {4, Scale1To5, f(0.75)}, {5, Scale1To5, f(1)},
		{0, Scale035, f(0)}, {3, Scale035, f(0.6)}, {5, Scale035, f(1)},
		{0, Scale50, f(0)}, {5, Scale50, f(1)},
		{5, ScaleRatio, f(1)},
		{0, ScaleRecord, nil},
		{3, "不存在的档位", nil},
	}
	for _, c := range cases {
		got := Normalize(&c.score, c.scale)
		if (got == nil) != (c.want == nil) {
			t.Errorf("Normalize(%v, %s) 期望 %v，实际 %v", c.score, c.scale, c.want, got)
			continue
		}
		if got != nil && *got != *c.want {
			t.Errorf("Normalize(%v, %s) 期望 %v，实际 %v", c.score, c.scale, *c.want, *got)
		}
	}
}

func TestNormalizeNilScore(t *testing.T) {
	if got := Normalize(nil, Scale1To5); got != nil {
		t.Errorf("nil 分值应返回 nil，实际 %v", got)
	}
}

// ⚠️ 可达性说明（诚实记录，避免以后误以为这里修掉了一个线上 bug）：
//
// 这两个分支的合法输入都是**整数分值**（1-5 / 0,3,5 / 0,5），
// 而 (s-1)/4 与 s/5 对整数 s 都是**二进制精确**的（0, .25, .5, .75 / 0, .2, .4, .6, .8, 1），
// round(x, 4) 在这种输入上恒等于 x 本身 —— 所以旧的 math.Round(x*10000)/10000
// 与 Python 在**所有可达输入上结果相同**。
//
// 实测评测：50 万随机样本 0 分歧；构造精确十进制 tie（y=k/10000+0.00005）
// 才出现分歧，但那种 y 对应 s 是非整数，而 _coerce_score 只放行整数档位。
//
// 结论：本处改动是**语义统一**（全项目只剩一套 Python 舍入语义），
// **不是一个可达的行为修复**。真正可达的是 evaluator 的 calcMean 与 overall_score_100。
func TestNormalizeIntegerScoresAreExact(t *testing.T) {
	for _, s := range []float64{1, 2, 3, 4, 5} {
		v := s
		if got := Normalize(&v, Scale1To5); *got != (s-1)/4 {
			t.Errorf("1-5 档 %v 应为精确值，得到 %v", s, *got)
		}
	}
	for _, s := range []float64{0, 3, 5} {
		v := s
		if got := Normalize(&v, Scale035); *got != s/5 {
			t.Errorf("0-3-5 档 %v 应为精确值，得到 %v", s, *got)
		}
	}
}
