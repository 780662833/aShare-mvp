package strategy

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/780662833/aShare-mvp/internal/czscclient"
	"github.com/780662833/aShare-mvp/internal/market/calendar"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/strategy/chan3buy"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
	"github.com/780662833/aShare-mvp/internal/strategy/trendbreakout"
)

type fakeRepository struct {
	security universe.StockBasic
	state universe.SecurityDailyState
	days map[string]calendar.TradeDay
	bars map[marketdata.Frequency][]marketdata.Bar
	saved []contract.StrategyDecision
}

func (r *fakeRepository) GetStockBasic(context.Context, string) (universe.StockBasic, error) {
	return r.security, nil
}
func (r *fakeRepository) GetSecurityDailyState(context.Context, string, string) (universe.SecurityDailyState, error) {
	return r.state, nil
}
func (r *fakeRepository) GetTradeDay(_ context.Context, exchange calendar.Exchange, date string) (calendar.TradeDay, error) {
	day, ok := r.days[date]
	if !ok || day.Exchange != exchange {
		return calendar.TradeDay{}, sql.ErrNoRows
	}
	return day, nil
}
func (r *fakeRepository) GetLatestOpenTradeDay(_ context.Context, exchange calendar.Exchange, date string) (calendar.TradeDay, error) {
	var selected calendar.TradeDay
	for key, day := range r.days {
		if key <= date && day.Exchange == exchange && day.IsOpen && key >= selected.CalDate {
			selected = day
		}
	}
	if selected.CalDate == "" {
		return calendar.TradeDay{}, sql.ErrNoRows
	}
	return selected, nil
}
func (r *fakeRepository) ListRecentBars(_ context.Context, tsCode string, frequency marketdata.Frequency, adjust marketdata.AdjustMode, asOf time.Time, limit int) ([]marketdata.Bar, error) {
	bars := r.bars[frequency]
	filtered := make([]marketdata.Bar, 0)
	for _, bar := range bars {
		if bar.TsCode == tsCode && bar.AdjustMode == adjust && bar.IsComplete && !bar.BarTime.After(asOf) {
			filtered = append(filtered, bar)
		}
	}
	if len(filtered) > limit {
		filtered = filtered[len(filtered)-limit:]
	}
	return filtered, nil
}
func (r *fakeRepository) GetLatestStrategyDecisionBefore(context.Context, string, string, time.Time) (contract.StrategyDecision, error) {
	return contract.StrategyDecision{}, sql.ErrNoRows
}
func (r *fakeRepository) SaveStrategyDecision(_ context.Context, decision contract.StrategyDecision) error {
	r.saved = append(r.saved, decision)
	return nil
}

type fakeAnalyzer struct { calls int }
func (a *fakeAnalyzer) Analyze(context.Context, czscclient.AnalyzeRequest) (czscclient.AnalyzeResponse, error) {
	a.calls++
	return czscclient.AnalyzeResponse{}, errors.New("unexpected CZSC call in preflight failure test")
}

func validRepository() *fakeRepository {
	return &fakeRepository{
		security: universe.StockBasic{
			TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
			Exchange: universe.ExchangeSSE, ListStatus: universe.ListingActive,
		},
		state: universe.SecurityDailyState{
			TradeDate: "2026-10-30", TsCode: "600519.SH",
			STType: universe.STNone, SuspendType: universe.SuspendNone,
			Source: "test-fixture",
			FetchedAt: marketdata.ShanghaiWallTime(2026, 10, 30, 10, 0),
		},
		days: map[string]calendar.TradeDay{
			"2026-10-29": {Exchange: calendar.SSE, CalDate: "2026-10-29", IsOpen: true, Source: calendar.SourceExchangeNotice, SourceURL: "https://example.test/calendar", FetchedAt: marketdata.ShanghaiWallTime(2026, 10, 1, 10, 0)},
			"2026-10-30": {Exchange: calendar.SSE, CalDate: "2026-10-30", IsOpen: true, Source: calendar.SourceExchangeNotice, SourceURL: "https://example.test/calendar", FetchedAt: marketdata.ShanghaiWallTime(2026, 10, 1, 10, 0)},
		},
		bars: make(map[marketdata.Frequency][]marketdata.Bar),
	}
}

func validConfig() Config {
	return Config{
		AdjustMode: marketdata.AdjustQFQ,
		HistoryBars: 250,
		Chan3Buy: chan3buy.Config{
			Version: "chan3buy-test-v1",
			MaxEntryDistanceAboveDefensePct: 5,
			DefenseBreakConfirmBars: 2,
			MaxSignalAgeBars: 3,
			DailyFastMAPeriod: 20,
			DailySlowMAPeriod: 60,
			AllowUnknownMarket: true,
		},
		TrendBreakout: trendbreakout.Config{
			Version: "trendbreakout-test-v1",
			LookbackDays: 20,
			BreakoutBufferPct: 0.1,
			MinVolumeRatio: 1.2,
			MinAmount: 1000000,
			MinClosePosition: 0.7,
			MaxChaseAboveBreakoutPct: 5,
			MinM60ClosesAboveBreakout: 1,
			M60FailureConfirmBars: 1,
			MaxSignalAgeDays: 3,
			AllowUnknownMarket: true,
		},
	}
}

func TestExpectedBarTimesPreserveM30AfterM60OrderingIntraday(t *testing.T) {
	repo := validRepository()
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 11, 1)
	expected, stateDate, issues := expectedBarTimes(context.Background(), repo, calendar.SSE, asOf)
	if len(issues) != 0 {
		t.Fatalf("有效交易日历不应产生问题: %v", issues)
	}
	if stateDate != "2026-10-30" {
		t.Fatalf("证券状态日期应为当前交易日，得到 %q", stateDate)
	}
	want := map[marketdata.Frequency]time.Time{
		marketdata.FrequencyD1: marketdata.ShanghaiWallTime(2026, 10, 29, 15, 0),
		marketdata.FrequencyM60: marketdata.ShanghaiWallTime(2026, 10, 30, 10, 30),
		marketdata.FrequencyM30: marketdata.ShanghaiWallTime(2026, 10, 30, 11, 0),
	}
	for frequency, expectedTime := range want {
		if !expected[frequency].Equal(expectedTime) {
			t.Errorf("%s 期望 %s，得到 %s", frequency, expectedTime, expected[frequency])
		}
	}
	if !expected[marketdata.FrequencyM30].After(expected[marketdata.FrequencyM60]) {
		t.Fatal("M30最新完成K线必须允许晚于M60，从而满足三买时序约束")
	}
}

func TestEvaluateSymbolFailsClosedWhenStoredBarsAreMissing(t *testing.T) {
	repo := validRepository()
	analyzer := &fakeAnalyzer{}
	service, err := New(repo, analyzer, validConfig())
	if err != nil {
		t.Fatal(err)
	}
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 11, 1)
	result, err := service.EvaluateSymbol(context.Background(), "600519.SH", asOf)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Decisions) != 2 || len(repo.saved) != 2 {
		t.Fatalf("应分别产生并持久化两条策略数据不完整结果: result=%+v saved=%d", result, len(repo.saved))
	}
	for _, decision := range result.Decisions {
		if decision.Status != contract.StatusDataIncomplete {
			t.Errorf("%s 应为 DATA_INCOMPLETE，得到 %s", decision.StrategyID, decision.Status)
		}
	}
	if analyzer.calls != 0 {
		t.Fatalf("数据质量预检失败后不应调用 CZSC，调用次数=%d", analyzer.calls)
	}
}
