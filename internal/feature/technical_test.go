package feature

import (
	"testing"
	"time"

	"github.com/780662833/aShare-mvp/internal/marketdata"
)

func featureBar(index int, high, low, close, volume float64) marketdata.Bar {
	at := marketdata.ShanghaiWallTime(2026, 10, 1, 15, 0).AddDate(0, 0, index)
	return marketdata.Bar{
		TsCode: "600519.SH", Frequency: marketdata.FrequencyD1, BarTime: at,
		Open: low, High: high, Low: low, Close: close,
		Volume: volume, Amount: volume * close,
		AdjustMode: marketdata.AdjustQFQ, VolumeUnit: marketdata.VolumeShare,
		Source: "test", IsComplete: true, FetchedAt: at.Add(time.Hour),
	}
}

func TestMovingAverageReturnsCurrentAndPreviousWindows(t *testing.T) {
	current, previous, err := MovingAverage([]float64{1, 2, 3, 4}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if current != 3 || previous != 2 {
		t.Fatalf("均线结果不正确: current=%v previous=%v", current, previous)
	}
}

func TestPreviousRangeHighExcludesCurrentBar(t *testing.T) {
	bars := []marketdata.Bar{
		featureBar(0, 10, 8, 9, 100),
		featureBar(1, 12, 9, 11, 100),
		featureBar(2, 99, 10, 98, 100),
	}
	high, err := PreviousRangeHigh(bars, 2)
	if err != nil {
		t.Fatal(err)
	}
	if high != 12 {
		t.Fatalf("前序区间高点应排除当前 K 线，得到 %v", high)
	}
}

func TestPreviousRangeHighIncludesOldestPriorBarAndExcludesBarsOutsideWindow(t *testing.T) {
	bars := []marketdata.Bar{
		featureBar(0, 100, 90, 95, 100), // 超出窗口的旧高点
		featureBar(1, 11, 9, 10, 100),
		featureBar(2, 15, 10, 14, 100), // N 根窗口最老边界，必须纳入
		featureBar(3, 12, 10, 11, 100),
		featureBar(4, 99, 10, 98, 100), // 当前 K 线，不得纳入
	}
	high, err := PreviousRangeHigh(bars, 3)
	if err != nil { t.Fatal(err) }
	if high != 15 {
		t.Fatalf("必须纳入 N 根窗口最老 K 线、排除窗口外旧高和当前 K 线，得到 %v", high)
	}
}

func TestVolumeRatioUsesOnlyPreviousBarsForBaseline(t *testing.T) {
	bars := []marketdata.Bar{
		featureBar(0, 11, 9, 10, 100),
		featureBar(1, 11, 9, 10, 100),
		featureBar(2, 11, 9, 10, 100),
		featureBar(3, 11, 9, 10, 200),
	}
	ratio, err := VolumeRatio(bars, 3)
	if err != nil {
		t.Fatal(err)
	}
	if ratio != 2 {
		t.Fatalf("量比应为 2，得到 %v", ratio)
	}
}

func TestClosePosition(t *testing.T) {
	bar := featureBar(0, 12, 8, 11, 100)
	position, err := ClosePosition(bar)
	if err != nil {
		t.Fatal(err)
	}
	if position != 0.75 {
		t.Fatalf("收盘位置应为 0.75，得到 %v", position)
	}
}

func TestClassifyDailyTrend(t *testing.T) {
	bars := make([]marketdata.Bar, 0, 5)
	for i, close := range []float64{1, 2, 3, 4, 5} {
		bars = append(bars, featureBar(i, close+0.5, close-0.5, close, 100))
	}
	result, err := ClassifyDailyTrend(bars, 3, 4)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != TrendFavorable {
		t.Fatalf("连续上行样本应被分类为有利趋势，得到 %s", result.State)
	}
}
