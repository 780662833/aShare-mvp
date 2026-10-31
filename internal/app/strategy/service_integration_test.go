package strategy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/780662833/aShare-mvp/internal/czscclient"
	"github.com/780662833/aShare-mvp/internal/market/calendar"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	sqlitestore "github.com/780662833/aShare-mvp/internal/storage/sqlite"
	"github.com/780662833/aShare-mvp/internal/strategy/chan3buy"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
	"github.com/780662833/aShare-mvp/internal/strategy/trendbreakout"
)

func TestEvaluateSymbolSQLiteCZSCAndDualStrategiesEndToEnd(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, filepath.Join(t.TempDir(), "s3-integration.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	security := universe.StockBasic{
		TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
		Exchange: universe.ExchangeSSE, ListStatus: universe.ListingActive,
	}
	if err := store.UpsertStockBasics(ctx, []universe.StockBasic{security}); err != nil {
		t.Fatal(err)
	}

	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 11, 1)
	fetchedAt := asOf
	if err := store.UpsertDailyStates(ctx, []universe.SecurityDailyState{{
		TradeDate: "2026-10-30", TsCode: security.TsCode,
		STType: universe.STNone, SuspendType: universe.SuspendNone,
		Source: "integration-fixture", FetchedAt: marketdata.ShanghaiWallTime(2026, 10, 30, 10, 30),
	}}); err != nil {
		t.Fatal(err)
	}
	calendarRows := []calendar.TradeDay{
		{
			Exchange: calendar.SSE, CalDate: "2026-10-29", IsOpen: true,
			Source: calendar.SourceExchangeNotice, SourceURL: "https://example.test/calendar",
			FetchedAt: marketdata.ShanghaiWallTime(2026, 10, 1, 9, 0),
		},
		{
			Exchange: calendar.SSE, CalDate: "2026-10-30", IsOpen: true,
			Source: calendar.SourceExchangeNotice, SourceURL: "https://example.test/calendar",
			FetchedAt: marketdata.ShanghaiWallTime(2026, 10, 1, 9, 0),
		},
	}
	if err := store.UpsertTradeCalendar(ctx, calendarRows); err != nil {
		t.Fatal(err)
	}

	dailyLatest := marketdata.ShanghaiWallTime(2026, 10, 29, 15, 0)
	m60Latest := marketdata.ShanghaiWallTime(2026, 10, 30, 10, 30)
	m30Latest := marketdata.ShanghaiWallTime(2026, 10, 30, 11, 0)
	for _, batch := range [][]marketdata.Bar{
		makeS3IntegrationBars(marketdata.FrequencyD1, 250, dailyLatest, fetchedAt),
		makeS3IntegrationBars(marketdata.FrequencyM60, 250, m60Latest, fetchedAt),
		makeS3IntegrationBars(marketdata.FrequencyM30, 250, m30Latest, fetchedAt),
	} {
		if err := store.UpsertBars(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}

	var analyzerCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/analyze" {
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		var request czscclient.AnalyzeRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		analyzerCalls++
		signals, lifecycles, profileVersion, ok := s3IntegrationSignals(request)
		if !ok {
			http.Error(w, "unexpected signal profile", http.StatusBadRequest)
			return
		}
		response := czscclient.AnalyzeResponse{
			SchemaVersion: czscclient.SchemaVersion,
			TsCode: request.TsCode,
			Freq: request.Freq,
			AdjustMode: request.AdjustMode,
			Source: request.Source,
			SignalProfile: request.SignalProfile,
			SignalProfileVersion: profileVersion,
			CZSCSourceRevision: czscclient.CZSCSourceRevision,
			SignalDataStatus: "READY",
			Signals: signals,
			SignalLifecycles: lifecycles,
			AsOf: request.Bars[len(request.Bars)-1].DT,
			InputBarCount: len(request.Bars),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client, err := czscclient.New(server.URL, 2*time.Second, "")
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(store, client, validConfig())
	if err != nil {
		t.Fatal(err)
	}

	result, err := service.EvaluateSymbol(ctx, security.TsCode, asOf)
	if err != nil {
		t.Fatal(err)
	}
	if analyzerCalls != 3 {
		t.Fatalf("应通过真实 CZSC HTTP 客户端调用三个 profile，实际 %d 次", analyzerCalls)
	}
	if result.MarketStatus != contract.MarketUnknown {
		t.Fatalf("未接入市场环境数据时必须保留 UNKNOWN，得到 %s", result.MarketStatus)
	}
	if len(result.Decisions) != 2 {
		t.Fatalf("应返回两份独立策略决策，得到 %d", len(result.Decisions))
	}
	if result.Decisions[0].StrategyID != chan3buy.StrategyID ||
		result.Decisions[1].StrategyID != trendbreakout.StrategyID {
		t.Fatalf("策略结果身份错误：%+v", result.Decisions)
	}
	for _, decision := range result.Decisions {
		if decision.Status != contract.StatusWatching {
			t.Fatalf("测试信号均为非命中状态，期望 WATCHING，得到 %s: %v", decision.Status, decision.ReasonCodes)
		}
	}
	if result.Decisions[0].DataVersion != result.DataVersion ||
		result.Decisions[1].DataVersion != result.DataVersion {
		t.Fatal("两套策略必须引用同一份标准化输入数据版本")
	}

	// 同一 as_of、同一数据和配置重放必须幂等，不得重复插入决策记录。
	replayed, err := service.EvaluateSymbol(ctx, security.TsCode, asOf)
	if err != nil {
		t.Fatalf("同一快照重放失败：%v", err)
	}
	if len(replayed.Decisions) != 2 {
		t.Fatalf("重放必须仍返回两份策略结果，得到 %d", len(replayed.Decisions))
	}
	var persisted int
	if err := store.DB().QueryRowContext(ctx,
		"SELECT COUNT(*) FROM strategy_decisions WHERE ts_code = ?", security.TsCode,
	).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if persisted != 2 {
		t.Fatalf("同一快照幂等重放后应仅保留两条策略记录，实际 %d", persisted)
	}
}

func makeS3IntegrationBars(
	frequency marketdata.Frequency,
	count int,
	latest time.Time,
	fetchedAt time.Time,
) []marketdata.Bar {
	bars := make([]marketdata.Bar, 0, count)
	start := latest.AddDate(0, 0, -(count - 1))
	for i := 0; i < count; i++ {
		closePrice := 100 + float64(i)*0.05
		bars = append(bars, marketdata.Bar{
			TsCode: "600519.SH", Frequency: frequency,
			BarTime: start.AddDate(0, 0, i),
			Open: closePrice - 0.2, High: closePrice + 0.5,
			Low: closePrice - 0.5, Close: closePrice,
			Volume: 1000, Amount: closePrice * 1000,
			AdjustMode: marketdata.AdjustQFQ,
			VolumeUnit: marketdata.VolumeSourceNative,
			Source: "gotdx", IsComplete: true, FetchedAt: fetchedAt,
		})
	}
	return bars
}

func s3IntegrationSignals(
	request czscclient.AnalyzeRequest,
) (map[string]string, map[string]czscclient.SignalLifecycle, string, bool) {
	keys := []string{}
	version := ""
	switch request.SignalProfile {
	case czscclient.SignalProfileChan3BuyM60:
		version = "chan3buy-m60-v1"
		keys = []string{
			"60分钟_D1_三买辅助V230228",
			"60分钟_D1#SMA#34_BS3辅助V230318",
			"60分钟_D1#SMA#34_BS3辅助V230319",
		}
	case czscclient.SignalProfileChan3BuyM30:
		version = "chan3buy-m30-timing-v1"
		keys = []string{"30分钟_D1_表里关系V230102"}
	case czscclient.SignalProfileTrendD1:
		version = "trend-breakout-d1-v1"
		keys = []string{
			"日线_D1W21_事件V240428",
			"日线_D1SMA#20_分类V221101",
			"日线_D1SMA#60_分类V221101",
			"日线_D1VOL#SMA#20_分类V230214",
		}
	default:
		return nil, nil, "", false
	}
	signals := make(map[string]string, len(keys))
	lifecycles := make(map[string]czscclient.SignalLifecycle, len(keys))
	startedAt := request.Bars[len(request.Bars)-1].DT
	for _, key := range keys {
		signals[key] = "其他_任意_任意_0"
		lifecycles[key] = czscclient.SignalLifecycle{AgeBars: 0, StartedAt: startedAt}
	}
	return signals, lifecycles, version, true
}
