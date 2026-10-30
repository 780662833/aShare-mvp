package contract

import (
	"testing"
	"time"

	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/market/universe"
)

func contractBar(freq marketdata.Frequency, at time.Time) marketdata.Bar {
	return marketdata.Bar{
		TsCode: "600519.SH", Frequency: freq, BarTime: at,
		Open: 100, High: 103, Low: 99, Close: 102,
		Volume: 1000, Amount: 102000,
		AdjustMode: marketdata.AdjustQFQ, VolumeUnit: marketdata.VolumeSourceNative,
		Source: "gotdx", IsComplete: true, FetchedAt: at.Add(time.Hour),
	}
}

func validInput() StrategyInput {
	at := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 0)
	return StrategyInput{
		Security: universe.StockBasic{
			TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
			Exchange: universe.ExchangeSSE, ListStatus: universe.ListingActive,
		},
		DailyState: universe.SecurityDailyState{
			TradeDate: "2026-10-30", TsCode: "600519.SH",
			STType: universe.STNone, SuspendType: universe.SuspendNone,
			Source: "test", FetchedAt: at.Add(time.Hour),
		},
		AsOf:        at.Add(time.Hour),
		Bars:        map[marketdata.Frequency][]marketdata.Bar{marketdata.FrequencyD1: {contractBar(marketdata.FrequencyD1, at)}},
		Technical:   map[marketdata.Frequency]FrequencyAnalysis{},
		MarketContext: MarketContext{AsOf: at, Status: MarketUnknown},
		DataVersion: "fixture-data-v1",
	}
}

func TestValidateInputAcceptsCompleteSingleFrequencyInput(t *testing.T) {
	input := validInput()
	if issues := ValidateInput(input, []marketdata.Frequency{marketdata.FrequencyD1}); len(issues) != 0 {
		t.Fatalf("有效输入不应产生校验错误: %v", issues)
	}
}

func TestValidateInputRejectsFormingBars(t *testing.T) {
	input := validInput()
	bars := input.Bars[marketdata.FrequencyD1]
	bars[0].IsComplete = false
	input.Bars[marketdata.FrequencyD1] = bars
	issues := ValidateInput(input, []marketdata.Frequency{marketdata.FrequencyD1})
	if !contains(issues, "BARS_D1_FORMING_BAR") {
		t.Fatalf("未完成 K 线必须被拒绝: %v", issues)
	}
}

func TestValidateInputRejectsMixedSourceAndAdjustment(t *testing.T) {
	input := validInput()
	at := marketdata.ShanghaiWallTime(2026, 10, 30, 14, 0)
	m30 := contractBar(marketdata.FrequencyM30, at)
	m30.Source = "other-provider"
	m30.AdjustMode = marketdata.AdjustNone
	input.Bars[marketdata.FrequencyM30] = []marketdata.Bar{m30}
	issues := ValidateInput(input, []marketdata.Frequency{marketdata.FrequencyD1, marketdata.FrequencyM30})
	if !contains(issues, "CROSS_FREQUENCY_SOURCE_MISMATCH") ||
		!contains(issues, "CROSS_FREQUENCY_ADJUSTMENT_MISMATCH") {
		t.Fatalf("跨周期来源或复权不一致必须被拒绝: %v", issues)
	}
}

func TestValidateInputRejectsUnknownSTAndSuspensionStatus(t *testing.T) {
	input := validInput()
	input.DailyState.STType = universe.STUnknown
	input.DailyState.SuspendType = universe.SuspendUnknown
	issues := ValidateInput(input, []marketdata.Frequency{marketdata.FrequencyD1})
	if !contains(issues, "SECURITY_ST_STATUS_UNKNOWN") ||
		!contains(issues, "SECURITY_SUSPEND_STATUS_UNKNOWN") {
		t.Fatalf("未知证券状态必须被显式标记: %v", issues)
	}
}

func TestValidateTechnicalSnapshotChecksProfileAndAsOf(t *testing.T) {
	input := validInput()
	bar := input.Bars[marketdata.FrequencyD1][0]
	input.Technical[marketdata.FrequencyD1] = FrequencyAnalysis{
		AsOf: bar.BarTime.Add(time.Minute),
		InputBarCount: 1,
		SignalProfile: "trend_breakout_d1_v1",
		SignalProfileVersion: "wrong-version",
		CZSCSourceRevision: "wrong-revision",
		SignalDataStatus: "READY",
		Signals: map[string]string{"some-key": "some-value"},
	}
	issues := ValidateTechnicalSnapshot(
		input, marketdata.FrequencyD1, "trend_breakout_d1_v1",
		"trend-breakout-d1-v1", "expected-revision",
	)
	if !contains(issues, "TECHNICAL_D1_PROFILE_VERSION_MISMATCH") ||
		!contains(issues, "TECHNICAL_D1_CZSC_REVISION_MISMATCH") ||
		!contains(issues, "TECHNICAL_D1_AS_OF_MISMATCH") {
		t.Fatalf("技术分析版本和时间范围不匹配必须被拒绝: %v", issues)
	}
}

func TestDecisionIDIsDeterministicAndInputSensitive(t *testing.T) {
	at := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 0)
	first := DeterministicDecisionID("600519.SH", "chan3buy", "1.0.0", "cfg-a", at, "data-v1")
	second := DeterministicDecisionID("600519.SH", "chan3buy", "1.0.0", "cfg-a", at, "data-v1")
	changed := DeterministicDecisionID("600519.SH", "chan3buy", "1.0.0", "cfg-a", at, "data-v2")
	if first != second {
		t.Fatalf("相同输入必须生成相同 DecisionID: %s != %s", first, second)
	}
	if first == changed {
		t.Fatal("数据版本变化后 DecisionID 必须变化")
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
