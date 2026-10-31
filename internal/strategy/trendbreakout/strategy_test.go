package trendbreakout

import (
	"context"
	"testing"
	"time"

	"github.com/780662833/aShare-mvp/internal/czscclient"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
)

func trendConfig() Config {
	return Config{
		Version: "trend-breakout-test-v1",
		LookbackDays: 20,
		BreakoutBufferPct: 0.5,
		MinVolumeRatio: 1.5,
		MinAmount: 100000,
		MinClosePosition: 0.5,
		MaxChaseAboveBreakoutPct: 5,
		M30RetestTolerancePct: 0.5,
		M30RetestLookbackBars: 6,
		M30MinRetestClosePosition: 0.6,
		MinM60ClosesAboveBreakout: 1,
		M60FailureConfirmBars: 2,
		MaxSignalAgeDays: 4,
		AllowNeutralMarket: true,
		AllowUnknownMarket: false,
	}
}

func trendBar(freq marketdata.Frequency, at time.Time, close, high, low, volume float64) marketdata.Bar {
	return marketdata.Bar{
		TsCode: "600519.SH", Frequency: freq, BarTime: at,
		Open: close - 0.2, High: high, Low: low, Close: close,
		Volume: volume, Amount: volume * close,
		AdjustMode: marketdata.AdjustQFQ, VolumeUnit: marketdata.VolumeSourceNative,
		Source: "gotdx", IsComplete: true, FetchedAt: at.Add(30 * time.Minute),
	}
}

func trendInput() contract.StrategyInput {
	daily := make([]marketdata.Bar, 0, 65)
	start := marketdata.ShanghaiWallTime(2026, 8, 27, 15, 0)
	for i := 0; i < 64; i++ {
		close := 100 + float64(i)*0.2
		at := start.AddDate(0, 0, i)
		daily = append(daily, trendBar(marketdata.FrequencyD1, at, close, close+0.6, close-0.6, 1000))
	}
	currentDailyTime := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 0)
	daily = append(daily, trendBar(marketdata.FrequencyD1, currentDailyTime, 115.0, 115.8, 113.8, 1800))

	m60 := []marketdata.Bar{
		trendBar(marketdata.FrequencyM60, marketdata.ShanghaiWallTime(2026, 11, 2, 10, 30), 114.5, 115.0, 114.0, 500),
		trendBar(marketdata.FrequencyM60, marketdata.ShanghaiWallTime(2026, 11, 2, 11, 30), 114.8, 115.2, 114.2, 550),
	}
	m30 := []marketdata.Bar{
		trendBar(marketdata.FrequencyM30, marketdata.ShanghaiWallTime(2026, 11, 2, 12, 0), 114.0, 114.2, 113.4, 250),
		trendBar(marketdata.FrequencyM30, marketdata.ShanghaiWallTime(2026, 11, 2, 12, 30), 114.5, 114.8, 114.0, 280),
	}
	return contract.StrategyInput{
		Security: universe.StockBasic{
			TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
			Exchange: universe.ExchangeSSE, ListStatus: universe.ListingActive,
		},
		DailyState: universe.SecurityDailyState{
			TradeDate: "2026-11-02", TsCode: "600519.SH",
			STType: universe.STNone, SuspendType: universe.SuspendNone,
			Source: "test", FetchedAt: marketdata.ShanghaiWallTime(2026, 11, 2, 13, 30),
		},
		AsOf: marketdata.ShanghaiWallTime(2026, 11, 2, 13, 30),
		Bars: map[marketdata.Frequency][]marketdata.Bar{
			marketdata.FrequencyD1: daily,
			marketdata.FrequencyM60: m60,
			marketdata.FrequencyM30: m30,
		},
		Technical: map[marketdata.Frequency]contract.FrequencyAnalysis{
			marketdata.FrequencyD1: {
				AsOf: daily[len(daily)-1].BarTime,
				InputBarCount: len(daily),
				SignalProfile: czscclient.SignalProfileTrendD1,
				SignalProfileVersion: d1ProfileVersion,
				CZSCSourceRevision: czscclient.CZSCSourceRevision,
				SignalDataStatus: "READY",
				SignalAgeBars: intPtr(0),
				Signals: map[string]string{
					"日线_D1W21_事件V240428": "收盘新高_任意_任意_0",
					"日线_D1W31_事件V240428": "其他_任意_任意_0",
					"日线_D1W56_事件V240428": "其他_任意_任意_0",
					ma20SignalKey: "多头_向上_任意_0",
					ma60SignalKey: "多头_向上_任意_0",
					volumeSignalKey: "多头_向上_任意_0",
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
			},
		},
		MarketContext: contract.MarketContext{
			AsOf: marketdata.ShanghaiWallTime(2026, 11, 2, 13, 30),
			Status: contract.MarketFavorable,
			DataVersion: "market-context-test-v1",
		},
		DataVersion: "market-data-test-v1",
	}
}

func TestEvaluateReturnsBuyObservationWhenAllConditionsPass(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	decision, err := strategy.Evaluate(context.Background(), trendInput())
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusBuyObservation {
		t.Fatalf("状态应为 BUY_OBSERVATION，得到 %s，原因=%v，缺失=%v", decision.Status, decision.ReasonCodes, decision.MissingConditions)
	}
	if len(decision.ReferenceLevels) != 1 || decision.ReferenceLevels[0].Code != "BREAKOUT_LEVEL" {
		t.Fatalf("应输出可追溯的突破位: %+v", decision.ReferenceLevels)
	}
}

func TestEvaluateFailsClosedOnIncompleteM30Bar(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil { t.Fatal(err) }
	input := trendInput()
	bars := input.Bars[marketdata.FrequencyM30]
	bars[len(bars)-1].IsComplete = false
	input.Bars[marketdata.FrequencyM30] = bars
	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil { t.Fatal(err) }
	if decision.Status != contract.StatusDataIncomplete || !hasReason(decision, "BARS_M30_FORMING_BAR") {
		t.Fatalf("未完成 M30 K 线必须 fail-closed: %+v", decision)
	}
}

func TestEvaluateWaitsWhenM30RetestIsMissing(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil { t.Fatal(err) }
	input := trendInput()
	bars := input.Bars[marketdata.FrequencyM30]
	bars[0].Low = 114.0
	input.Bars[marketdata.FrequencyM30] = bars
	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil { t.Fatal(err) }
	if decision.Status != contract.StatusWaitEntry || !hasReason(decision, "M30_BREAKOUT_RETEST_NOT_FOUND") {
		t.Fatalf("没有回踩突破位时必须等待: %+v", decision)
	}
}

func TestEvaluateRequiresM30CloseToReclaimBreakoutAndBreakPriorHigh(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil { t.Fatal(err) }
	input := trendInput()
	bars := input.Bars[marketdata.FrequencyM30]
	bars[1].Close = 113.0
	bars[1].High = 114.0
	input.Bars[marketdata.FrequencyM30] = bars
	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil { t.Fatal(err) }
	if decision.Status != contract.StatusWaitEntry || !hasReason(decision, "M30_RETEST_NOT_RECLAIMED") {
		t.Fatalf("收盘未站回突破位必须等待: %+v", decision)
	}

	input = trendInput()
	bars = input.Bars[marketdata.FrequencyM30]
	bars[1].Close = 114.1
	bars[1].High = 114.4
	input.Bars[marketdata.FrequencyM30] = bars
	decision, err = strategy.Evaluate(context.Background(), input)
	if err != nil { t.Fatal(err) }
	if decision.Status != contract.StatusWaitEntry || !hasReason(decision, "M30_RETEST_STRENGTH_NOT_CONFIRMED") {
		t.Fatalf("收盘未突破前一根 M30 高点必须等待: %+v", decision)
	}
}

func TestEvaluateRequiresConfiguredM30RetestClosePosition(t *testing.T) {
	config := trendConfig()
	config.M30MinRetestClosePosition = 0.9
	strategy, err := New(config)
	if err != nil { t.Fatal(err) }
	decision, err := strategy.Evaluate(context.Background(), trendInput())
	if err != nil { t.Fatal(err) }
	if decision.Status != contract.StatusWaitEntry || !hasReason(decision, "M30_RETEST_CLOSE_POSITION_INSUFFICIENT") {
		t.Fatalf("转强 K 线收盘位置不足必须等待: %+v", decision)
	}
}

func TestEvaluateRequiresM30RetestAfterM60Hold(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil { t.Fatal(err) }
	input := trendInput()
	bars := input.Bars[marketdata.FrequencyM30]
	bars[0].BarTime = marketdata.ShanghaiWallTime(2026, 11, 2, 11, 0)
	bars[1].BarTime = marketdata.ShanghaiWallTime(2026, 11, 2, 11, 30)
	bars[0].FetchedAt = bars[0].BarTime.Add(30*time.Minute)
	bars[1].FetchedAt = bars[1].BarTime.Add(30*time.Minute)
	input.Bars[marketdata.FrequencyM30] = bars
	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil { t.Fatal(err) }
	if decision.Status != contract.StatusWaitEntry || !hasReason(decision, "M30_NOT_AFTER_M60_CONFIRMATION") {
		t.Fatalf("M30 必须晚于 M60 站稳确认: %+v", decision)
	}
}

func TestNewRejectsMissingM30RetestConfiguration(t *testing.T) {
	config := trendConfig()
	config.M30RetestTolerancePct = 0
	if _, err := New(config); err == nil { t.Fatal("必须显式配置 M30 回踩容忍度") }
	config = trendConfig()
	config.M30RetestLookbackBars = 1
	if _, err := New(config); err == nil { t.Fatal("回踩窗口必须至少为两根完整 M30 K 线") }
	config = trendConfig()
	config.M30MinRetestClosePosition = 1.1
	if _, err := New(config); err == nil { t.Fatal("转强收盘位置必须在 [0,1]") }
}

func TestEvaluateDoesNotConfirmIntradayOrNonClosingBreakout(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := trendInput()
	daily := input.Bars[marketdata.FrequencyD1]
	last := daily[len(daily)-1]
	last.Close = 113.0
	last.Low = 112.0
	daily[len(daily)-1] = last
	input.Bars[marketdata.FrequencyD1] = daily
	d1 := input.Technical[marketdata.FrequencyD1]
	d1.Signals["日线_D1W21_事件V240428"] = "其他_任意_任意_0"
	input.Technical[marketdata.FrequencyD1] = d1

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusWatching {
		t.Fatalf("日线未收盘确认突破不得确认为突破，得到 %s", decision.Status)
	}
}

func TestEvaluateWaitsWhenVolumeQualityIsInsufficient(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := trendInput()
	daily := input.Bars[marketdata.FrequencyD1]
	daily[len(daily)-1].Volume = 1000
	daily[len(daily)-1].Amount = daily[len(daily)-1].Close * 1000
	input.Bars[marketdata.FrequencyD1] = daily
	d1 := input.Technical[marketdata.FrequencyD1]
	d1.Signals[volumeSignalKey] = "空头_向下_任意_0"
	input.Technical[marketdata.FrequencyD1] = d1

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusWaitEntry || !hasReason(decision, "BREAKOUT_QUALITY_NOT_CONFIRMED") {
		t.Fatalf("量价不足应等待，不应升级为买入观察: %+v", decision)
	}
}

func TestEvaluateDoesNotUseM60BarsBeforeDailyBreakoutConfirmation(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := trendInput()
	m60 := input.Bars[marketdata.FrequencyM60]
	for i := range m60 {
		m60[i].BarTime = marketdata.ShanghaiWallTime(2026, 10, 30, 13+i, 0)
		m60[i].FetchedAt = m60[i].BarTime.Add(30 * time.Minute)
	}
	input.Bars[marketdata.FrequencyM60] = m60
	m30 := input.Bars[marketdata.FrequencyM30]
	m30[0].BarTime = marketdata.ShanghaiWallTime(2026, 10, 30, 14, 30)
	m30[0].FetchedAt = m30[0].BarTime.Add(30 * time.Minute)
	input.Bars[marketdata.FrequencyM30] = m30
	d1 := input.Technical[marketdata.FrequencyD1]
	d1.AsOf = input.Bars[marketdata.FrequencyD1][len(input.Bars[marketdata.FrequencyD1])-1].BarTime
	input.Technical[marketdata.FrequencyD1] = d1
	m30Tech := input.Technical[marketdata.FrequencyM30]
	m30Tech.AsOf = m30[0].BarTime
	input.Technical[marketdata.FrequencyM30] = m30Tech

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusWaitEntry || !hasReason(decision, "M60_BREAKOUT_HOLD_NOT_CONFIRMED") {
		t.Fatalf("日线确认之前的 60 分钟数据不能作为突破后站稳证据: %+v", decision)
	}
}

func TestEvaluateWaitsWhenM60DoesNotHoldBreakout(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := trendInput()
	m60 := input.Bars[marketdata.FrequencyM60]
	m60[len(m60)-1].Open = 112.2
	m60[len(m60)-1].High = 112.5
	m60[len(m60)-1].Low = 111.5
	m60[len(m60)-1].Close = 112.0
	input.Bars[marketdata.FrequencyM60] = m60

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusWaitEntry || !hasReason(decision, "M60_BREAKOUT_HOLD_NOT_CONFIRMED") {
		t.Fatalf("60 分钟未站稳时应等待: %+v", decision)
	}
}

func TestEvaluateFlagsM60BreakoutFailureRisk(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := trendInput()
	m60 := input.Bars[marketdata.FrequencyM60]
	m60[0].Open, m60[0].High, m60[0].Low, m60[0].Close = 112.7, 113.0, 112.0, 112.5
	m60[1].Open, m60[1].High, m60[1].Low, m60[1].Close = 113.0, 113.3, 112.3, 112.8
	input.Bars[marketdata.FrequencyM60] = m60

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusReduceObservation || !hasReason(decision, "M60_BREAKOUT_FAILURE_RISK") {
		t.Fatalf("连续跌回区间时应提示突破失败风险: %+v", decision)
	}
}

func TestEvaluateInvalidatesPreviousBreakoutWhenDailyCloseReturnsToRange(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := trendInput()
	previous := contract.StrategyDecision{
		TsCode: "600519.SH",
		StrategyID: StrategyID,
		StrategyVersion: StrategyVersion,
		ConfigVersion: trendConfig().Version,
		Status: contract.StatusConfirmed,
		ConfirmedAt: timePtr(marketdata.ShanghaiWallTime(2026, 10, 29, 15, 0)),
		ReferenceLevels: []contract.PriceLevel{{
			Code: "BREAKOUT_LEVEL", Name: "前20日高点", Price: 114,
			Frequency: string(marketdata.FrequencyD1),
			ObservedAt: marketdata.ShanghaiWallTime(2026, 10, 29, 15, 0),
			Source: "test",
		}},
	}
	input.PreviousDecision = &previous
	daily := input.Bars[marketdata.FrequencyD1]
	daily[len(daily)-1].Close = 113.5
	daily[len(daily)-1].Low = 112.5
	input.Bars[marketdata.FrequencyD1] = daily
	d1 := input.Technical[marketdata.FrequencyD1]
	d1.Signals["日线_D1W21_事件V240428"] = "其他_任意_任意_0"
	input.Technical[marketdata.FrequencyD1] = d1

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusInvalidated || !hasReason(decision, "DAILY_CLOSE_RETURNED_INSIDE_RANGE") {
		t.Fatalf("日线收盘回到原突破区间应失效: %+v", decision)
	}
}

func TestEvaluateExpiresWhenUpstreamSignalAgeExceedsConfig(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := trendInput()
	d1 := input.Technical[marketdata.FrequencyD1]
	age := 5
	d1.SignalAgeBars = &age
	input.Technical[marketdata.FrequencyD1] = d1

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusExpired {
		t.Fatalf("超过配置有效日线数量应过期，得到 %s", decision.Status)
	}
}

func TestEvaluateRejectsWrongCZSCRevision(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := trendInput()
	d1 := input.Technical[marketdata.FrequencyD1]
	d1.CZSCSourceRevision = "old-revision"
	input.Technical[marketdata.FrequencyD1] = d1

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusDataIncomplete {
		t.Fatalf("CZSC 版本不匹配必须标记数据不完整，得到 %s", decision.Status)
	}
}

func TestNewRejectsUnversionedOrUnspecifiedThresholds(t *testing.T) {
	config := trendConfig()
	config.MinVolumeRatio = 0
	if _, err := New(config); err == nil {
		t.Fatal("未配置量比门槛时必须拒绝创建策略")
	}
}

func TestEvaluateWaitsWhenSignalAgeIsUnknown(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := trendInput()
	d1 := input.Technical[marketdata.FrequencyD1]
	d1.SignalAgeBars = nil
	input.Technical[marketdata.FrequencyD1] = d1
	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusWaitEntry || !hasReason(decision, "BREAKOUT_SIGNAL_AGE_UNKNOWN") {
		t.Fatalf("突破信号年龄未知时必须等待: %+v", decision)
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

func timePtr(value time.Time) *time.Time { return &value }


func TestEvaluateUsesSelectedBreakoutSignalLifecycleStartTime(t *testing.T) {
	strategy, err := New(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	input := trendInput()
	d1 := input.Technical[marketdata.FrequencyD1]
	d1.SignalAgeBars = nil
	startedAt := marketdata.ShanghaiWallTime(2026, 10, 28, 15, 0)
	d1.SignalLifecycles = map[string]contract.SignalLifecycle{
		"日线_D1W21_事件V240428": {AgeBars: 2, StartedAt: startedAt},
	}
	input.Technical[marketdata.FrequencyD1] = d1

	decision, err := strategy.Evaluate(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Status != contract.StatusBuyObservation {
		t.Fatalf("有信号级生命周期的合法输入应正常评估，得到 %s: %v", decision.Status, decision.ReasonCodes)
	}
	if decision.ConfirmedAt == nil || !decision.ConfirmedAt.Equal(startedAt) {
		t.Fatalf("ConfirmedAt 必须保留所选突破信号的首次连续出现时间: %+v", decision.ConfirmedAt)
	}
}
