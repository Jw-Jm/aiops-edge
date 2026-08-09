package api

import "math"

// ZScore 计算当前值偏离历史均值的标准差倍数。|z|>threshold 判定异常（默认 threshold=3）。
func ZScore(series []float64, current, threshold float64) (float64, bool) {
	if len(series) == 0 {
		return 0, false
	}
	m := mean(series)
	sd := stddev(series, m)
	if sd == 0 {
		// 标准差为 0（全相同）：仅当偏离才异常
		return 0, current != m
	}
	z := (current - m) / sd
	return z, math.Abs(z) > threshold
}

// MAD 计算中位数绝对偏差稳健检测。|current-median|/(1.4826*MAD) > threshold（默认 3.5）。
// 对离群点比 zscore 更稳健（离群点不放大统计量）。
func MAD(series []float64, current, threshold float64) (float64, bool) {
	if len(series) == 0 {
		return 0, false
	}
	med := median(series)
	// 计算绝对偏差序列的中位数
	devs := make([]float64, len(series))
	for i, v := range series {
		devs[i] = math.Abs(v - med)
	}
	mad := median(devs)
	if mad == 0 {
		// MAD 为 0（一半以上相同）：任何偏离中位数都视为异常
		return 0, current != med
	}
	score := math.Abs(current-med) / (1.4826 * mad)
	return score, score > threshold
}

// ComputeAnomaly 统一异常检测入口。method: zscore|mad（默认 zscore）。
func ComputeAnomaly(series []float64, current float64, method string, threshold float64) (float64, bool) {
	switch method {
	case "mad":
		return MAD(series, current, threshold)
	default: // zscore / "" / 未知
		return ZScore(series, current, threshold)
	}
}

func mean(nums []float64) float64 {
	if len(nums) == 0 {
		return 0
	}
	s := 0.0
	for _, v := range nums {
		s += v
	}
	return s / float64(len(nums))
}

func stddev(nums []float64, m float64) float64 {
	if len(nums) < 2 {
		return 0
	}
	var s float64
	for _, v := range nums {
		d := v - m
		s += d * d
	}
	return math.Sqrt(s / float64(len(nums)-1))
}

func median(nums []float64) float64 {
	n := len(nums)
	if n == 0 {
		return 0
	}
	sorted := make([]float64, n)
	copy(sorted, nums)
	// 简单选择排序（序列通常 ≤ 数百点，O(n^2) 足够；如需优化可换 sort.Float64s）
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if sorted[j] < sorted[i] {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
