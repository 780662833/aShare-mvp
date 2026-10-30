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
