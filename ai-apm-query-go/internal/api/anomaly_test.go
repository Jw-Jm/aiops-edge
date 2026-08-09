package api

import "testing"

func TestZScoreKnownSeries(t *testing.T) {
	series := []float64{10, 11, 10, 9, 10, 11, 10, 9, 10, 11}
	// mean=10, std≈0.74；current=10 → z=0（不异常）
	z, anom := ZScore(series, 10, 3)
	if anom {
		t.Fatalf("z=%v should not be anomalous", z)
	}
	// current=13 → z≈4 > 3 异常
	z2, anom2 := ZScore(series, 13, 3)
	if !anom2 {
		t.Fatalf("z=%v should be anomalous (threshold 3)", z2)
	}
}

func TestMADKnownSeries(t *testing.T) {
	series := []float64{10, 10, 10, 10, 10, 10, 10, 10, 10, 100}
	// 中位数=10；离群点100被 MAD 稳健处理
	score, anom := MAD(series, 100, 3.5)
	_ = score
	if !anom {
		t.Fatalf("100 should be anomalous vs median 10")
	}
}

func TestMADZeroMadFallback(t *testing.T) {
	series := []float64{10, 10, 10, 10}
	// MAD=0 → 兜底：任何偏离中位数都异常
	_, anom := MAD(series, 12, 3.5)
	if !anom {
		t.Fatalf("with MAD=0, deviation should be anomalous")
	}
	_, anom2 := MAD(series, 10, 3.5)
	if anom2 {
		t.Fatalf("equal to median should not be anomalous")
	}
}

func TestComputeAnomalyDispatch(t *testing.T) {
	series := []float64{1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
	// zscore: current=5 vs std=0 → 高 z 异常
	_, anomZ := ComputeAnomaly(series, 5, "zscore", 3)
	if !anomZ {
		t.Fatalf("zscore should flag 5")
	}
	// mad: current=5 vs median=1 → 异常
	_, anomM := ComputeAnomaly(series, 5, "mad", 3.5)
	if !anomM {
		t.Fatalf("mad should flag 5")
	}
	// 未知方法默认 zscore
	_, anomD := ComputeAnomaly(series, 5, "bogus", 3)
	if !anomD {
		t.Fatalf("unknown method should default to zscore")
	}
}

func TestZScoreEmptySeries(t *testing.T) {
	_, anom := ZScore(nil, 5, 3)
	if anom {
		t.Fatalf("empty series should not be anomalous")
	}
}

func TestMADRobustAgainstOutliers(t *testing.T) {
	// 含离群点但当前值=中位数不应误报
	series := []float64{10, 10, 10, 10, 10, 1000}
	_, anom := MAD(series, 10, 3.5)
	if anom {
		t.Fatalf("median point should not be anomalous despite outliers")
	}
}
