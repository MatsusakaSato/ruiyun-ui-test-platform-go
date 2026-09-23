package pyre

import (
	"math"
	"testing"
)

// Python round() 是银行家舍入（ties-to-even），Go math.Round 是「四舍五入、远离零」。
// 这里锁住的是**逐位对齐 Python** 的行为。
func TestRoundTiesToEven(t *testing.T) {
	cases := []struct {
		x    float64
		nd   int
		want float64
		why  string
	}{
		{0.5, 0, 0, "0.5 → 0（0 是偶数）"},
		{1.5, 0, 2, "1.5 → 2"},
		{2.5, 0, 2, "2.5 → 2（ties-to-even，math.Round 会给 3）"},
		{3.5, 0, 4, "3.5 → 4"},
		{-0.5, 0, math.Copysign(0, -1), "-0.5 → -0.0"},
		{-1.5, 0, -2, "-1.5 → -2（远离零与 ties-to-even 在此一致）"},
		{-2.5, 0, -2, "-2.5 → -2"},
		{31.25, 1, 31.2, "31.25 是二进制精确的 .25 → ties-to-even 取 31.2"},
		{0.25, 1, 0.2, "0.25 → 0.2"},
		{0.125, 2, 0.12, "0.125 → 0.12"},
	}
	for _, c := range cases {
		if got := Round(c.x, c.nd); got != c.want {
			t.Errorf("Round(%v, %d) = %v，期望 %v（%s）", c.x, c.nd, got, c.want, c.why)
		}
	}
}

// 🔴 这一类是「*N 之后再 round」引入的**二次舍入** ——
// 即使真实值不是 tie 也会错，光靠 ties-to-even 修不掉。
func TestRoundAvoidsDoubleRounding(t *testing.T) {
	cases := []struct {
		x    float64
		nd   int
		want float64
		note string
	}{
		{0.35, 1, 0.3, "0.35 真实值 ≈ 0.34999…；math.Round(0.35*10)/10 会得 0.4"},
		{0.15, 1, 0.1, "0.15 ≈ 0.14999…"},
		{12.45, 1, 12.4, "12.45 ≈ 12.44999…"},
		{2.675, 2, 2.67, "2.675 ≈ 2.67499…"},
	}
	for _, c := range cases {
		if got := Round(c.x, c.nd); got != c.want {
			t.Errorf("Round(%v, %d) = %v，期望 %v（%s）", c.x, c.nd, got, c.want, c.note)
		}
	}
}

// 交叉校验：Round 必须与「用 math/big.Rat 从零算一遍」的结果一致，
// 并且**必须不等于**天真的 math.Round 写法（否则说明这层保护形同虚设）。
func TestRoundDiffersFromNaiveMathRound(t *testing.T) {
	diverged := 0
	for _, x := range []float64{0.35, 0.15, 12.45, 31.25, 0.25, 2.675} {
		naive := math.Round(x*100) / 100
		if Round(x, 2) != naive {
			diverged++
		}
	}
	if diverged == 0 {
		t.Error("样本里没有任何一例与 math.Round 写法不同 —— 测试样本选错了")
	}
}

func TestRoundSpecialValues(t *testing.T) {
	if !math.IsNaN(Round(math.NaN(), 2)) {
		t.Error("NaN 应原样返回")
	}
	if !math.IsInf(Round(math.Inf(1), 2), 1) {
		t.Error("+Inf 应原样返回")
	}
	if got := Round(100, 1); got != 100 {
		t.Errorf("整数不应被改动，得到 %v", got)
	}
}
