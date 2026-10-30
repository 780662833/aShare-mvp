package chan3buy

import (
	"context"
	"testing"
	"time"

	"github.com/780662833/aShare-mvp/internal/czscclient"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
)

func chanConfig() Config {
	return Config{
		Version: "chan3buy-rules-test-v1",
		MaxEntryDistanceAboveDefensePct: 5,
		DefenseBreakConfirmBars: 2,
		MaxSignalAgeBars: 3,
		DailyFastMAPeriod: 20,
		DailySlowMAPeriod: 60,
		AllowNeutralMarket: true,
		AllowUnknownMarket: false,
	}
}

func chanBar(freq marketdata.Frequency, at time.Time, close float64) marketdata.Bar {
	return marketdata.Bar{
		TsCode: "600519.SH", Frequency: freq, BarTime: at,
		Open: close - 0.2, High: close + 0.5, Low: close - 0.5, Close: close,
		Volume: 1000, Amount: 1000 * close,
		AdjustMode: marketdata.AdjustQFQ, VolumeUnit: marketdata.VolumeSourceNative,
		Source: "gotdx", IsComplete: true, FetchedAt: at.Add(30 * time.Minute),
	}
}

func chanInput() contract.StrategyInput {
	daily := make([]marketdata.Bar, 0, 61)
	start := marketdata.ShanghaiWallTime(2026, 8, 31, 15, 0)
	for i := 0; i < 61; i++ {
		close := 100 + float64(i)*0.5
		at := start.AddDate(0, 0, i)
		daily = append(daily, chanBar(marketdata.FrequencyD1, at, close))
	}
	m60 := []marketdata.Bar{
		chanBar(marketdata.FrequencyM60, marketdata.ShanghaiWallTime(2026, 10, 30, 13, 0), 10.8),
		chanBar(marketdata.FrequencyM60, marketdata.ShanghaiWallTime(2026, 10, 30, 14, 0), 10.9),
	}
	m30 := []marketdata.Bar{
		chanBar(marketdata.FrequencyM30, marketdata.ShanghaiWallTime(2026, 10, 30, 14, 30), 10.8),
	}
	return contract.StrategyInput{
		Security: universe.StockBasic{
			TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
			Exchange: universe.ExchangeSSE, ListStatus: universe.ListingActive,
		},
		DailyState: universe.SecurityDailyState{
			TradeDate: "2026-10-30", TsCode: "600519.SH",
			STType: universe.STNone, SuspendType: universe.SuspendNone,
			Source: "test", FetchedAt: marketdata.ShanghaiWallTime(2026, 10, 30, 16, 0),
		},
		AsOf: marketdata.ShanghaiWallTime(2026, 10, 30, 16, 0),
		Bars: map[marketdata.Frequency][]marketdata.Bar{
			marketdata.FrequencyD1: daily,
			marketdata.FrequencyM60: m60,
			marketdata.FrequencyM30: m30,
		},
		Technical: map[marketdata.Frequency]contract.FrequencyAnalysis{
			marketdata.FrequencyM60: {
				AsOf: m60[len(m60)-1].BarTime,
				InputBarCount: len(m60),
				SignalProfile: czscclient.SignalProfileChan3BuyM60,
				SignalProfileVersion: m60ProfileVersion,
				CZSCSourceRevision: czscclient.CZSCSourceRevision,
				SignalDataStatus: "READY",
				Signals: map[string]string{
					mainSignalKey: "三买_6笔_任意_0",
					helper318Key: "三买_任意_任意_0",
					helper319Key: "三买_均线新高_任意_0",
				},
				BICount: 12,
				SignalAgeBars: intPtr(0),
				RecentBI: []contract.BISummary{
					{StartTime: "2026-10-30T08:00:00+08:00", EndTime: "2026-10-30T09:00:00+08:00", Direction: "Up", High: 10.5, Low: 9.0},
					{StartTime: "2026-10-30T09:00:00+08:00", EndTime: "2026-10-30T10:00:00+08:00", Direction: "Down", High: 10.2, Low: 9.2},
					{StartTime: "2026-10-30T10:00:00+08:00", EndTime: "2026-10-30T11:00:00+08:00", Direction: "Up", High: 11.0, Low: 9.5},
					{StartTime: "2026-10-30T11:00:00+08:00", EndTime: "2026-10-30T12:00:00+08:00", Direction: "Down", High: 10.9, Low: 10.1},
					{StartTime: "2026-10-30T12:00:00+08:00", EndTime: "2026-10-30T14:00:00+08:00", Direction: "Down", High: 10.9, Low: 10.6},
				},
			},
			marketdata.FrequencyM30: {
				AsOf: m30[len(m30)-1].BarTime,
				InputBarCount: len(m30),
				SignalProfile: czscclient.SignalProfileChan3BuyM30,
				SignalProfileVersion: m30ProfileVersion,
				CZSCSourceRevision: czscclient.CZSCSourceRevision,
				SignalDataStatus: "READY",
				Signals: map[string]string{m30TimingKey: "向下_底分_任意_0"},
				BICount: 4,
			},
		},
		MarketContext: contract.MarketContext{
			AsOf: marketdata.ShanghaiWallTime(2026, 10, 30, 15, 0),
			Status: contract.MarketFavorable,
			DataVersion: "market-context-test-v1",
		},
		DataVersion: "market-data-test-v1",
	}
}

func TestEvaluateReturnsBuyObservationWhenAllConditionsPass(t *testing.T) {
	strategy, err := New(chanConfig())
	if err != nil {
		t.Fatal(err)
	}
	decision, err := strategy.Evaluate(context.Background(), chanInput())
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusBuyObservation {
		t.Fatalf("状态应为 BUY_OBSERVATION，得到 %s，原因=%v，缺失=%v", decision.Status, decision.ReasonCodes, decision.MissingConditions)
	}
	if len(decision.ReferenceLevels) != 1 || decision.ReferenceLevels[0].Code != "THIRD_BUY_DEFENSE" {
		t.Fatalf("应输出可追溯的防守位: %+v", decision.ReferenceLevels)
	}
	if decision.ReferenceLevels[0].Price != 10.5 {
		t.Fatalf("防守位应由 B1/B3 交集上沿计算为 10.5，得到 %.4f", decision.ReferenceLevels[0].Price)
	}
}

func TestEvaluateDoesNotUseMainSignalAloneAsBuyObservation(t *testing.T) {
	strategy, err := New(chanConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := chanInput()
	m60 := input.Technical[marketdata.FrequencyM60]
	m60.Signals[helper318Key] = "其他_任意_任意_0"
	m60.Signals[helper319Key] = "其他_均线否定_任意_0"
	input.Technical[marketdata.FrequencyM60] = m60

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusWaitEntry {
		t.Fatalf("主信号没有可追溯防守位时必须等待，得到 %s", decision.Status)
	}
	if hasReason(decision, "THIRD_BUY_DEFENSE_NOT_TRACEABLE") == false {
		t.Fatalf("缺少防守价原因码: %v", decision.ReasonCodes)
	}
}

func TestEvaluateWaitsWhenM30TimingNotReady(t *testing.T) {
	strategy, err := New(chanConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := chanInput()
	m30 := input.Technical[marketdata.FrequencyM30]
	m30.Signals[m30TimingKey] = "向下_延伸_任意_0"
	input.Technical[marketdata.FrequencyM30] = m30

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusWaitEntry || !hasReason(decision, "M30_TURN_UP_NOT_CONFIRMED") {
		t.Fatalf("30 分钟仍向下延伸时应等待: %+v", decision)
	}
}

func TestEvaluateInvalidatesAfterConfirmedDefenseBreak(t *testing.T) {
	strategy, err := New(chanConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := chanInput()
	m60 := input.Bars[marketdata.FrequencyM60]
	m60[0].Close = 10.4
	m60[0].Low = 10.0
	m60[1].Close = 10.3
	m60[1].Low = 10.0
	input.Bars[marketdata.FrequencyM60] = m60

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusInvalidated {
		t.Fatalf("连续收盘跌破防守位应失效，得到 %s", decision.Status)
	}
}

func TestEvaluateRejectsWrongTechnicalProfileAsIncomplete(t *testing.T) {
	strategy, err := New(chanConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := chanInput()
	m60 := input.Technical[marketdata.FrequencyM60]
	m60.SignalProfileVersion = "old-profile"
	input.Technical[marketdata.FrequencyM60] = m60

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusDataIncomplete {
		t.Fatalf("信号版本不匹配必须数据不完整，得到 %s", decision.Status)
	}
}

func TestEvaluateExpiresWhenUpstreamAgeExceedsConfiguration(t *testing.T) {
	strategy, err := New(chanConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := chanInput()
	m60 := input.Technical[marketdata.FrequencyM60]
	age := 4
	m60.SignalAgeBars = &age
	input.Technical[marketdata.FrequencyM60] = m60

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusExpired {
		t.Fatalf("超过配置有效 K 线数量应过期，得到 %s", decision.Status)
	}
}

func TestEvaluateWaitsWhenSignalAgeIsUnknown(t *testing.T) {
	strategy, err := New(chanConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := chanInput()
	m60 := input.Technical[marketdata.FrequencyM60]
	m60.SignalAgeBars = nil
	input.Technical[marketdata.FrequencyM60] = m60
	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusWaitEntry || !hasReason(decision, "THIRD_BUY_SIGNAL_AGE_UNKNOWN") {
		t.Fatalf("信号年龄未知时必须等待: %+v", decision)
	}
}

func intPtr(value int) *int { return &value }

func hasReason(decision contract.StrategyDecision, code string) bool {
	for _, reason := range decision.ReasonCodes {
		if reason == code {
			return true
		}
	}
	return false
}
