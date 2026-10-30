package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/780662833/aShare-mvp/internal/market/calendar"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
)

func TestOpenMigrateAndUpsertBars(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "ashare.db"))
	if err != nil {
		t.Fatalf("Open 失败: %v", err)
	}
	defer store.Close()

	stock := universe.StockBasic{
		TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
		Exchange: universe.ExchangeSSE, ListStatus: universe.ListingActive,
	}
	if err := store.UpsertStockBasics(ctx, []universe.StockBasic{stock}); err != nil {
		t.Fatalf("UpsertStockBasics 失败: %v", err)
	}

	fetchedAt := time.Date(2026, 10, 30, 16, 0, 0, 0, marketdata.ShanghaiLocation())
	day := calendar.TradeDay{
		Exchange: calendar.SSE, CalDate: "2026-10-30", IsOpen: true,
		Source: calendar.SourceExchangeNotice, SourceURL: "https://example.test/notice",
		FetchedAt: fetchedAt,
	}
	if err := store.UpsertTradeCalendar(ctx, []calendar.TradeDay{day}); err != nil {
		t.Fatalf("UpsertTradeCalendar 失败: %v", err)
	}

	bar := marketdata.Bar{
		TsCode: "600519.SH", Frequency: marketdata.FrequencyD1,
		BarTime: time.Date(2026, 10, 30, 15, 0, 0, 0, marketdata.ShanghaiLocation()),
		Open: 1500, High: 1510, Low: 1490, Close: 1505,
		Volume: 12345, Amount: 18500000, AdjustMode: marketdata.AdjustQFQ,
		VolumeUnit: marketdata.VolumeSourceNative, Source: "gotdx",
		IsComplete: true, FetchedAt: fetchedAt,
	}
	if err := store.UpsertBars(ctx, []marketdata.Bar{bar}); err != nil {
		t.Fatalf("UpsertBars 失败: %v", err)
	}
	got, err := store.ListBars(ctx, "600519.SH", marketdata.FrequencyD1, marketdata.AdjustQFQ)
	if err != nil {
		t.Fatalf("ListBars 失败: %v", err)
	}
	if len(got) != 1 || !got[0].BarTime.Equal(bar.BarTime) {
		t.Fatalf("K 线读回不一致: %+v", got)
	}
}


func TestUpsertStockBasicsDoesNotEraseEnrichedFieldsWithSparseData(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "stock-basic.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	full := universe.StockBasic{
		TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
		Area: "贵州", Industry: "白酒", Exchange: universe.ExchangeSSE,
		ListStatus: universe.ListingActive, ListDate: "20010827",
	}
	if err := store.UpsertStockBasics(ctx, []universe.StockBasic{full}); err != nil {
		t.Fatal(err)
	}
	sparse := universe.StockBasic{
		TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
		Exchange: universe.ExchangeSSE, ListStatus: universe.ListingUnknown,
	}
	if err := store.UpsertStockBasics(ctx, []universe.StockBasic{sparse}); err != nil {
		t.Fatal(err)
	}

	var area, industry, status, listDate string
	if err := store.DB().QueryRowContext(ctx,
		"SELECT area, industry, list_status, list_date FROM stock_basic WHERE ts_code = ?",
		"600519.SH",
	).Scan(&area, &industry, &status, &listDate); err != nil {
		t.Fatal(err)
	}
	if area != "贵州" || industry != "白酒" || status != string(universe.ListingActive) || listDate != "20010827" {
		t.Fatalf("稀疏行情来源覆盖了已有证券资料: area=%q industry=%q status=%q list_date=%q",
			area, industry, status, listDate)
	}
}

func TestUpsertTradeCalendarHonorsSourcePriority(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "calendar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	fetchedAt := time.Date(2026, 10, 30, 16, 0, 0, 0, marketdata.ShanghaiLocation())
	authoritative := calendar.TradeDay{
		Exchange: calendar.SSE, CalDate: "2026-10-30", IsOpen: true,
		Source: calendar.SourceExchangeNotice, SourceURL: "https://exchange.example/notice",
		FetchedAt: fetchedAt,
	}
	lowerPriority := calendar.TradeDay{
		Exchange: calendar.SSE, CalDate: "2026-10-30", IsOpen: false,
		Source: calendar.SourceTushare, FetchedAt: fetchedAt.Add(time.Minute),
	}
	if err := store.UpsertTradeCalendar(ctx, []calendar.TradeDay{authoritative}); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertTradeCalendar(ctx, []calendar.TradeDay{lowerPriority}); err != nil {
		t.Fatal(err)
	}

	var isOpen int
	var source string
	if err := store.DB().QueryRowContext(ctx,
		"SELECT is_open, source FROM trade_calendar WHERE exchange = ? AND cal_date = ?",
		string(calendar.SSE), "2026-10-30",
	).Scan(&isOpen, &source); err != nil {
		t.Fatal(err)
	}
	if isOpen != 1 || source != string(calendar.SourceExchangeNotice) {
		t.Fatalf("低优先级来源覆盖了交易所公告: is_open=%d source=%q", isOpen, source)
	}

	manual := lowerPriority
	manual.IsOpen = false
	manual.Source = calendar.SourceManualOverride
	if err := store.UpsertTradeCalendar(ctx, []calendar.TradeDay{manual}); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(ctx,
		"SELECT is_open, source FROM trade_calendar WHERE exchange = ? AND cal_date = ?",
		string(calendar.SSE), "2026-10-30",
	).Scan(&isOpen, &source); err != nil {
		t.Fatal(err)
	}
	if isOpen != 0 || source != string(calendar.SourceManualOverride) {
		t.Fatalf("人工覆盖未生效: is_open=%d source=%q", isOpen, source)
	}
}

func TestUpsertBarsRejectsSilentSourceSwitch(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "source.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	stock := universe.StockBasic{
		TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
		Exchange: universe.ExchangeSSE, ListStatus: universe.ListingActive,
	}
	if err := store.UpsertStockBasics(ctx, []universe.StockBasic{stock}); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, marketdata.ShanghaiLocation())
	fetchedAt := time.Date(2026, 10, 30, 16, 0, 0, 0, marketdata.ShanghaiLocation())
	bar := marketdata.Bar{
		TsCode: "600519.SH", Frequency: marketdata.FrequencyM30, BarTime: at,
		Open: 100, High: 103, Low: 99, Close: 102, Volume: 1000, Amount: 102000,
		AdjustMode: marketdata.AdjustQFQ, VolumeUnit: marketdata.VolumeSourceNative,
		Source: "provider-a", IsComplete: true, FetchedAt: fetchedAt,
	}
	if err := store.UpsertBars(ctx, []marketdata.Bar{bar}); err != nil {
		t.Fatal(err)
	}

	switched := bar
	switched.Source = "provider-b"
	switched.Close = 101
	if err := store.UpsertBars(ctx, []marketdata.Bar{switched}); err == nil {
		t.Fatal("数据源切换不得静默覆盖已有 K 线")
	}

	var source string
	var closePrice float64
	if err := store.DB().QueryRowContext(ctx,
		"SELECT source, close FROM stock_kline WHERE ts_code = ? AND freq = ? AND bar_time = ?",
		bar.TsCode, string(bar.Frequency), bar.BarTime.Format(time.RFC3339Nano),
	).Scan(&source, &closePrice); err != nil {
		t.Fatal(err)
	}
	if source != "provider-a" || closePrice != bar.Close {
		t.Fatalf("拒绝切换来源后原始 K 线发生变化: source=%q close=%v", source, closePrice)
	}
}
