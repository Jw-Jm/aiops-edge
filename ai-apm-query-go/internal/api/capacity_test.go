package api

import (
	"math"
	"testing"
)

// 线性回归：合成 y=2x+1 序列（x=0..9），断言 slope≈2、intercept≈1。
func TestLinearRegressionKnownLine(t *testing.T) {
	series := make([]float64, 10)
	for x := 0; x < 10; x++ {
		series[x] = float64(2*x + 1)
	}
	slope, intercept := LinearRegression(series)
	if math.Abs(slope-2) > 1e-6 || math.Abs(intercept-1) > 1e-6 {
		t.Fatalf("slope=%v intercept=%v, want slope≈2 intercept≈1", slope, intercept)
	}
}

// 线性回归：常数列 → slope=0，intercept=均值。
func TestLinearRegressionConstant(t *testing.T) {
	slope, intercept := LinearRegression([]float64{5, 5, 5, 5})
	if math.Abs(slope) > 1e-9 || math.Abs(intercept-5) > 1e-9 {
		t.Fatalf("constant series: slope=%v intercept=%v, want 0/5", slope, intercept)
	}
}

// 线性回归：空序列/单点 → 不 panic，返回 (0,0) 或 (0,mean)。
func TestLinearRegressionShort(t *testing.T) {
	if _, i := LinearRegression(nil); i != 0 {
		t.Fatalf("nil series intercept=%v, want 0", i)
	}
	if s, _ := LinearRegression([]float64{7}); s != 0 {
		t.Fatalf("single point slope=%v, want 0", s)
	}
}

// EWMA：常数列平滑后不变。
func TestEWMAConstant(t *testing.T) {
	out := EWMA([]float64{3, 3, 3, 3}, 0.3)
	for _, v := range out {
		if v != 3 {
			t.Fatalf("EWMA constant = %v, want 3", v)
		}
	}
}

// EWMA：单调上升序列，平滑结果首项等于 y[0]，且整体上升（未截断异常）。
func TestEWMAMonotonic(t *testing.T) {
	series := []float64{1, 2, 3, 4, 5, 6}
	out := EWMA(series, 0.3)
	if out[0] != 1 {
		t.Fatalf("out[0]=%v, want 1", out[0])
	}
	if out[len(out)-1] <= out[0] {
		t.Fatalf("EWMA should trend up for increasing input: %v", out)
	}
}

// EWMA：alpha 越界 → 回退 0.3，不 panic。
func TestEWMAInvalidAlpha(t *testing.T) {
	if out := EWMA([]float64{1, 2, 3}, 0); out[len(out)-1] != out[len(out)-1] {
		t.Fatalf("alpha=0 should fallback, got %v", out)
	}
	if out := EWMA([]float64{1, 2, 3}, 2.0); out[len(out)-1] != out[len(out)-1] {
		t.Fatalf("alpha=2 should fallback, got %v", out)
	}
}

// ETT：斜率 2、intercept 1、当前 x=9 处 y=19，threshold=25 → 需要 y>=25。
// 未来 x=10,11,12 → y=21,23,25 → 第 3 个未来点达到 → k=3。
func TestEstimateTimeToThresholdHit(t *testing.T) {
	k, ok := EstimateTimeToThreshold(2, 1, 10, 10, 25)
	if !ok || k != 3 {
		t.Fatalf("k=%v ok=%v, want k=3 ok=true", k, ok)
	}
}

// ETT：斜率 2、intercept 1、threshold 远高于预测范围内 → 未达到。
func TestEstimateTimeToThresholdMiss(t *testing.T) {
	k, ok := EstimateTimeToThreshold(2, 1, 10, 10, 100)
	if ok || k != 0 {
		t.Fatalf("k=%v ok=%v, want k=0 ok=false", k, ok)
	}
}

// ETT：斜率下降（slope=-1, intercept=90, 当前 x=9 处 y=81）但阈值较高（threshold=100），
// 未来 x=10..19 的 y=80..71 全部 <100 → 返回 k=0 ok=false（下降趋势不会达到较高阈值）。
func TestEstimateTimeToThresholdAlreadyBreachedSlopeDown(t *testing.T) {
	k, ok := EstimateTimeToThreshold(-1, 90, 10, 10, 100)
	if ok || k != 0 {
		t.Fatalf("k=%v ok=%v, want k=0 ok=false (slope down, won't breach)", k, ok)
	}
}

// ETT：阈值在第 1 个未来点就达到。
func TestEstimateTimeToThresholdFirstStep(t *testing.T) {
	k, ok := EstimateTimeToThreshold(2, 1, 10, 10, 21)
	if !ok || k != 1 {
		t.Fatalf("k=%v ok=%v, want k=1 ok=true", k, ok)
	}
}
