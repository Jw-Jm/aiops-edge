package api

// LinearRegression 用最小二乘对 (x=0..n-1, y) 拟合 y = intercept + slope*x。
// n<2 或分母为 0 时返回 (0, mean(series))。
func LinearRegression(series []float64) (slope, intercept float64) {
	n := len(series)
	if n == 0 {
		return 0, 0
	}
	if n < 2 {
		return 0, series[0]
	}
	var sumX, sumY, sumXY, sumX2 float64
	for i, y := range series {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
	}
	denom := float64(n)*sumX2 - sumX*sumX
	if denom == 0 {
		return 0, mean(series)
	}
	slope = (float64(n)*sumXY - sumX*sumY) / denom
	intercept = (sumY - slope*sumX) / float64(n)
	return slope, intercept
}

// EWMA 指数加权平滑：s[0]=y[0]，s[t]=alpha*y[t]+(1-alpha)*s[t-1]。
// alpha<=0 || alpha>1 时回退默认 0.3。
func EWMA(series []float64, alpha float64) []float64 {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.3
	}
	out := make([]float64, len(series))
	if len(series) == 0 {
		return out
	}
	out[0] = series[0]
	for t := 1; t < len(series); t++ {
		out[t] = alpha*series[t] + (1-alpha)*out[t-1]
	}
	return out
}

// EstimateTimeToThreshold 沿线性回归预测 y(x)=intercept+slope*x 求未来首个 y>=threshold 的步数。
// 未来 x 取 n-1+1 .. n-1+horizon。达到则返回 (k, true)（k 为第几个未来点，从 1 起）；
// 否则返回 (0, false)。
func EstimateTimeToThreshold(slope, intercept float64, n, horizon int, threshold float64) (int, bool) {
	for k := 1; k <= horizon; k++ {
		x := float64(n - 1 + k)
		y := intercept + slope*x
		if y >= threshold {
			return k, true
		}
	}
	return 0, false
}
