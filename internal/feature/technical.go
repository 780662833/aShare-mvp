// Package feature 提供两套策略共同使用的少量、无状态技术特征函数。
package feature

import (
	"fmt"
	"math"

	"github.com/780662833/aShare-mvp/internal/marketdata"
)

type TrendState string

const (
	TrendFavorable   TrendState = "FAVORABLE"
	TrendNeutral     TrendState = "NEUTRAL"
	TrendUnfavorable TrendState = "UNFAVORABLE"
)

type TrendSummary struct {
	State       TrendState
	FastPeriod  int
	SlowPeriod  int
	FastMA      float64
	PreviousFastMA float64
	SlowMA      float64
	PreviousSlowMA float64
	LastClose   float64
}

// MovingAverage 返回最后一个窗口和紧邻其前一个窗口的简单均线。
// 要求 period+1 个值，以便同时计算当前均线和上一根均线。
func MovingAverage(values []float64, period int) (current, previous float64, err error) {
	if period <= 0 {
		return 0, 0, fmt.Errorf("均线周期必须大于 0")
	}
	if len(values) < period+1 {
		return 0, 0, fmt.Errorf("计算 SMA%d 至少需要 %d 个值，当前 %d 个", period, period+1, len(values))
	}
	current, err = average(values[len(values)-period:])
	if err != nil {
		return 0, 0, err
	}
	previous, err = average(values[len(values)-period-1 : len(values)-1])
	if err != nil {
		return 0, 0, err
	}
	return current, previous, nil
}

// ClassifyDailyTrend 使用 MA20/MA60 形成可解释的日线环境分类。
// 这只是策略环境特征，不作为独立买入信号。
func ClassifyDailyTrend(bars []marketdata.Bar, fastPeriod, slowPeriod int) (TrendSummary, error) {
	var result TrendSummary
	if len(bars) == 0 {
		return result, fmt.Errorf("日线数据不能为空")
	}
	if fastPeriod <= 0 || slowPeriod <= fastPeriod {
		return result, fmt.Errorf("日线均线周期配置无效")
	}
	closes := make([]float64, len(bars))
	for i, bar := range bars {
		closes[i] = bar.Close
	}
	fast, fastPrev, err := MovingAverage(closes, fastPeriod)
	if err != nil {
		return result, err
	}
	slow, slowPrev, err := MovingAverage(closes, slowPeriod)
	if err != nil {
		return result, err
	}
	lastClose := closes[len(closes)-1]
	state := TrendNeutral
	if lastClose >= fast && fast >= slow && fast >= fastPrev {
		state = TrendFavorable
	} else if lastClose < slow && fast < slow && fast < fastPrev && slow <= slowPrev {
		state = TrendUnfavorable
	}
	return TrendSummary{
		State:           state,
		FastPeriod:      fastPeriod,
		SlowPeriod:      slowPeriod,
		FastMA:          fast,
		PreviousFastMA:  fastPrev,
		SlowMA:          slow,
		PreviousSlowMA:  slowPrev,
		LastClose:       lastClose,
	}, nil
}

// PreviousRangeHigh 只计算当前 K 线之前的 N 根 K 线，绝不包含当前 K 线。
func PreviousRangeHigh(bars []marketdata.Bar, lookback int) (float64, error) {
	if lookback <= 0 {
		return 0, fmt.Errorf("突破区间长度必须大于 0")
	}
	if len(bars) < lookback+1 {
		return 0, fmt.Errorf("计算前 %d 根 K 线高点至少需要 %d 根数据，当前 %d 根", lookback, lookback+1, len(bars))
	}
	start := len(bars) - lookback - 1
	end := len(bars) - 1
	high := bars[start].High
	for _, bar := range bars[start+1 : end] {
		if bar.High > high {
			high = bar.High
		}
	}
	return high, nil
}

// VolumeRatio 以当前成交量除以前 period 根 K 线的平均成交量。
// 当前 K 线不进入历史均量基准；该比值不依赖成交量采用股还是手。
func VolumeRatio(bars []marketdata.Bar, period int) (float64, error) {
	if period <= 0 {
		return 0, fmt.Errorf("成交量均量周期必须大于 0")
	}
	if len(bars) < period+1 {
		return 0, fmt.Errorf("计算量比至少需要 %d 根 K 线，当前 %d 根", period+1, len(bars))
	}
	previous := bars[len(bars)-period-1 : len(bars)-1]
	var sum float64
	for _, bar := range previous {
		if math.IsNaN(bar.Volume) || math.IsInf(bar.Volume, 0) || bar.Volume < 0 {
			return 0, fmt.Errorf("历史成交量无效")
		}
		sum += bar.Volume
	}
	mean := sum / float64(period)
	if mean <= 0 {
		return 0, fmt.Errorf("历史平均成交量必须大于 0")
	}
	current := bars[len(bars)-1].Volume
	if math.IsNaN(current) || math.IsInf(current, 0) || current < 0 {
		return 0, fmt.Errorf("当前成交量无效")
	}
	return current / mean, nil
}

// ClosePosition 返回收盘价在当根 K 线振幅中的相对位置，范围为 [0, 1]。
func ClosePosition(bar marketdata.Bar) (float64, error) {
	rangeSize := bar.High - bar.Low
	if rangeSize <= 0 || math.IsNaN(rangeSize) || math.IsInf(rangeSize, 0) {
		return 0, fmt.Errorf("K 线振幅必须大于 0")
	}
	position := (bar.Close - bar.Low) / rangeSize
	if position < 0 || position > 1 || math.IsNaN(position) || math.IsInf(position, 0) {
		return 0, fmt.Errorf("收盘位置超出有效范围")
	}
	return position, nil
}

func PercentAbove(price, reference float64) (float64, error) {
	if reference <= 0 || math.IsNaN(reference) || math.IsInf(reference, 0) {
		return 0, fmt.Errorf("参考价必须是有限正数")
	}
	if price <= 0 || math.IsNaN(price) || math.IsInf(price, 0) {
		return 0, fmt.Errorf("当前价必须是有限正数")
	}
	return (price/reference - 1) * 100, nil
}

func average(values []float64) (float64, error) {
	if len(values) == 0 {
		return 0, fmt.Errorf("均值输入不能为空")
	}
	var sum float64
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return 0, fmt.Errorf("均值输入包含非有限值")
		}
		sum += value
	}
	return sum / float64(len(values)), nil
}
