package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/780662833/aShare-mvp/internal/market/calendar"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
)

func TestListRecentBarsAppliesAsOfCompletionAndLimit(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "recent-bars.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.UpsertStockBasics(ctx, []universe.StockBasic{{
		TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
		Exchange: universe.ExchangeSSE, ListStatus: universe.ListingActive,
	}}); err != nil {
		t.Fatal(err)
	}
	fetchedAt := marketdata.ShanghaiWallTime(2026, 10, 30, 10, 40)
	bars := make([]marketdata.Bar, 0, 4)
	for i, minute := range []int{10, 30, 60, 90} {
		bar := marketdata.Bar{
			TsCode: "600519.SH", Frequency: marketdata.FrequencyM30,
			BarTime: marketdata.ShanghaiWallTime(2026, 10, 30, 9, 30).Add(time.Duration(minute) * time.Minute),
			Open: 100, High: 110, Low: 99, Close: 101 + float64(i),
			Volume: 1000, Amount: 100000, AdjustMode: marketdata.AdjustQFQ,
			VolumeUnit: marketdata.VolumeSourceNative, Source: "gotdx",
			IsComplete: i != 1, FetchedAt: fetchedAt,
		}
		bars = append(bars, bar)
	}
	if err := store.UpsertBars(ctx, bars); err != nil {
		t.Fatal(err)
	}

	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 10, 45)
	got, err := store.ListRecentBars(ctx, "600519.SH", marketdata.FrequencyM30, marketdata.AdjustQFQ, asOf, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("应返回截止时点前最近两根完整 K 线，得到 %d 根", len(got))
	}
	if !got[0].BarTime.Equal(bars[0].BarTime) || !got[1].BarTime.Equal(bars[2].BarTime) {
		t.Fatalf("K 线结果必须过滤未完成/未来数据并按时间正序返回: %+v", got)
	}
}

func TestStrategyDecisionRepositoryRoundTripAndIdempotency(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "strategy-decisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	if err := store.UpsertStockBasics(ctx, []universe.StockBasic{{
		TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
		Exchange: universe.ExchangeSSE, ListStatus: universe.ListingActive,
	}}); err != nil {
		t.Fatal(err)
	}
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	confirmedAt := marketdata.ShanghaiWallTime(2026, 10, 30, 10, 30)
	decision := contract.StrategyDecision{
		DecisionID: "decision-test-001", TsCode: "600519.SH", SecurityName: "贵州茅台",
		StrategyID: "chan3buy", StrategyVersion: "1.0.0", ConfigVersion: "chan-v1",
		AsOf: asOf, ConfirmedAt: &confirmedAt, Status: contract.StatusWaitEntry,
		InternalGrade: contract.GradeMedium, MarketStatus: contract.MarketUnknown,
		EntryConditions: []contract.Evidence{{Code: "THIRD_BUY_MAIN_SIGNAL_CONFIRMED"}},
		ReasonCodes: []string{"THIRD_BUY_MAIN_SIGNAL_CONFIRMED"},
		DataVersion: "data-test-001",
	}
	if err := store.SaveStrategyDecision(ctx, decision); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveStrategyDecision(ctx, decision); err != nil {
		t.Fatalf("相同 decision_id 应幂等保存: %v", err)
	}

	got, err := store.GetLatestStrategyDecisionBefore(ctx, decision.TsCode, decision.StrategyID, asOf.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.DecisionID != decision.DecisionID || got.Status != decision.Status ||
		got.ConfirmedAt == nil || !got.ConfirmedAt.Equal(confirmedAt) ||
		len(got.EntryConditions) != 1 || got.EntryConditions[0].Code != "THIRD_BUY_MAIN_SIGNAL_CONFIRMED" {
		t.Fatalf("策略决策往返持久化不一致: %+v", got)
	}
	if _, err := store.GetLatestStrategyDecisionBefore(ctx, decision.TsCode, decision.StrategyID, asOf); err != sql.ErrNoRows {
		t.Fatalf("同一 as_of 的决策不应作为 PreviousDecision，err=%v", err)
	}
}


func TestStrategyInputRepositoriesReadTypedSnapshots(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "typed-reads.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	security := universe.StockBasic{
		TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
		Area: "贵州", Industry: "白酒", Exchange: universe.ExchangeSSE,
		ListStatus: universe.ListingActive, ListDate: "20010827",
	}
	if err := store.UpsertStockBasics(ctx, []universe.StockBasic{security}); err != nil {
		t.Fatal(err)
	}
	gotSecurity, err := store.GetStockBasic(ctx, security.TsCode)
	if err != nil {
		t.Fatal(err)
	}
	if gotSecurity.TsCode != security.TsCode || gotSecurity.Industry != security.Industry ||
		gotSecurity.ListStatus != universe.ListingActive {
		t.Fatalf("证券基础资料读取不一致: %+v", gotSecurity)
	}

	fetchedAt := marketdata.ShanghaiWallTime(2026, 10, 30, 8, 0)
	states := []universe.SecurityDailyState{
		{
			TradeDate: "2026-10-29", TsCode: security.TsCode,
			STType: universe.STNone, SuspendType: universe.SuspendNone,
			Source: "state-provider", FetchedAt: fetchedAt,
		},
		{
			TradeDate: "2026-10-30", TsCode: security.TsCode,
			STType: universe.STNone, SuspendType: universe.SuspendNone,
			Source: "state-provider", FetchedAt: fetchedAt,
		},
	}
	if err := store.UpsertDailyStates(ctx, states); err != nil {
		t.Fatal(err)
	}
	gotState, err := store.GetSecurityDailyState(ctx, security.TsCode, "2026-10-29")
	if err != nil {
		t.Fatal(err)
	}
	if gotState.TradeDate != "2026-10-29" || gotState.STType != universe.STNone ||
		gotState.SuspendType != universe.SuspendNone {
		t.Fatalf("应按截止日期读取最近证券状态: %+v", gotState)
	}

	calendarRows := []calendar.TradeDay{
		{
			Exchange: calendar.SSE, CalDate: "2026-10-29", IsOpen: true,
			Source: calendar.SourceExchangeNotice, SourceURL: "https://example.test/calendar",
			FetchedAt: fetchedAt,
		},
		{
			Exchange: calendar.SSE, CalDate: "2026-10-30", IsOpen: true,
			Source: calendar.SourceExchangeNotice, SourceURL: "https://example.test/calendar",
			FetchedAt: fetchedAt,
		},
	}
	if err := store.UpsertTradeCalendar(ctx, calendarRows); err != nil {
		t.Fatal(err)
	}
	gotDay, err := store.GetTradeDay(ctx, calendar.SSE, "2026-10-30")
	if err != nil {
		t.Fatal(err)
	}
	if !gotDay.IsOpen || gotDay.CalDate != "2026-10-30" {
		t.Fatalf("交易日精确读取不一致: %+v", gotDay)
	}
	latestOpen, err := store.GetLatestOpenTradeDay(ctx, calendar.SSE, "2026-10-29")
	if err != nil {
		t.Fatal(err)
	}
	if latestOpen.CalDate != "2026-10-29" {
		t.Fatalf("最近开市日读取错误: %+v", latestOpen)
	}
}

func TestGetLatestStrategyDecisionBeforeOrdersSameAsOfBySavedAt(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "decision-order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.UpsertStockBasics(ctx, []universe.StockBasic{{
		TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台",
		Exchange: universe.ExchangeSSE, ListStatus: universe.ListingActive,
	}}); err != nil {
		t.Fatal(err)
	}

	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 10, 0)
	base := contract.StrategyDecision{
		TsCode: "600519.SH", SecurityName: "贵州茅台",
		StrategyID: "chan3buy", StrategyVersion: "1.0.0", ConfigVersion: "chan-v1",
		AsOf: asOf, Status: contract.StatusWatching,
		InternalGrade: contract.GradeUnknown, MarketStatus: contract.MarketUnknown,
		DataVersion: "data-test",
	}
	first := base
	first.DecisionID = "decision-a"
	if err := store.SaveStrategyDecision(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := base
	second.DecisionID = "decision-b"
	second.Status = contract.StatusWaitEntry
	if err := store.SaveStrategyDecision(ctx, second); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx,
		"UPDATE strategy_decisions SET saved_at = ? WHERE decision_id = ?",
		"2026-10-30T10:01:00+08:00", "decision-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(ctx,
		"UPDATE strategy_decisions SET saved_at = ? WHERE decision_id = ?",
		"2026-10-30T10:02:00+08:00", "decision-b"); err != nil {
		t.Fatal(err)
	}

	got, err := store.GetLatestStrategyDecisionBefore(ctx, base.TsCode, base.StrategyID, asOf.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if got.DecisionID != "decision-b" || got.Status != contract.StatusWaitEntry {
		t.Fatalf("同一 as_of 的历史快照应按 saved_at 选取最新记录: %+v", got)
	}
}



// seedBulkReadStockBasics 为 SQLite 批量读取测试准备证券主数据。
func seedBulkReadStockBasics(t *testing.T, ctx context.Context, store *Store, codes ...string) {
	t.Helper()
	items := make([]universe.StockBasic, 0, len(codes))
	for _, code := range codes {
		symbol := code[:6]
		exchange := universe.ExchangeSSE
		if code[0] == '0' || code[0] == '3' {
			exchange = universe.ExchangeSZSE
		} else if code[0] == '9' {
			exchange = universe.ExchangeBSE
		}
		items = append(items, universe.StockBasic{
			TsCode: code, Symbol: symbol, Name: "测试证券-" + symbol,
			Exchange: exchange, ListStatus: universe.ListingActive,
		})
	}
	if err := store.UpsertStockBasics(ctx, items); err != nil {
		t.Fatalf("准备证券基础资料失败: %v", err)
	}
}

func TestListActiveStockBasicsFiltersAndOrders(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "active-stock-basics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	items := []universe.StockBasic{
		{TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台", Exchange: universe.ExchangeSSE, ListStatus: universe.ListingActive},
		{TsCode: "000001.SZ", Symbol: "000001", Name: "平安银行", Exchange: universe.ExchangeSZSE, ListStatus: universe.ListingActive},
		{TsCode: "300750.SZ", Symbol: "300750", Name: "宁德时代", Exchange: universe.ExchangeSZSE, ListStatus: universe.ListingUnknown},
		{TsCode: "000002.SZ", Symbol: "000002", Name: "退市样本", Exchange: universe.ExchangeSZSE, ListStatus: universe.ListingDelisted},
		{TsCode: "600000.SH", Symbol: "600000", Name: "暂停上市样本", Exchange: universe.ExchangeSSE, ListStatus: universe.ListingPaused},
	}
	if err := store.UpsertStockBasics(ctx, items); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListActiveStockBasics(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("只应返回明确上市证券，实际得到 %d 条: %+v", len(got), got)
	}
	if got[0].TsCode != "000001.SZ" || got[1].TsCode != "600519.SH" {
		t.Fatalf("证券结果必须按 ts_code 升序: %+v", got)
	}
	if got[0].Exchange != universe.ExchangeSZSE || got[1].ListStatus != universe.ListingActive {
		t.Fatalf("证券基础资料字段映射错误: %+v", got)
	}
}

func TestListSecurityDailyStatesReadsExactTradeDate(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "daily-states-exact-date.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedBulkReadStockBasics(t, ctx, store, "600519.SH", "000001.SZ", "300750.SZ")

	fetchedAt := marketdata.ShanghaiWallTime(2026, 10, 30, 18, 0)
	states := []universe.SecurityDailyState{
		{TradeDate: "2026-10-29", TsCode: "600519.SH", STType: universe.STNone, SuspendType: universe.SuspendNone, Source: "test", FetchedAt: fetchedAt},
		{TradeDate: "2026-10-30", TsCode: "600519.SH", STType: universe.ST, SuspendType: universe.SuspendNone, Source: "test", FetchedAt: fetchedAt},
		{TradeDate: "2026-10-30", TsCode: "000001.SZ", STType: universe.STUnknown, SuspendType: universe.SuspendUnknown, Source: "test", FetchedAt: fetchedAt},
		{TradeDate: "2026-10-31", TsCode: "300750.SZ", STType: universe.STNone, SuspendType: universe.SuspendNone, Source: "test", FetchedAt: fetchedAt},
	}
	if err := store.UpsertDailyStates(ctx, states); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListSecurityDailyStates(ctx, "2026-10-30")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("只能返回指定交易日的状态，实际得到 %d 条: %+v", len(got), got)
	}
	for _, item := range got {
		if item.TradeDate != "2026-10-30" {
			t.Fatalf("批量状态读取混入其他日期: %+v", item)
		}
		if !item.FetchedAt.Equal(fetchedAt) {
			t.Fatalf("fetched_at 未正确保留: %+v", item)
		}
	}
	if got[0].TsCode != "000001.SZ" || got[0].STType != universe.STUnknown ||
		got[0].SuspendType != universe.SuspendUnknown {
		t.Fatalf("未知状态必须原样返回，由扫描层 fail closed: %+v", got[0])
	}
	if got[1].TsCode != "600519.SH" || got[1].STType != universe.ST {
		t.Fatalf("ST 状态字段映射错误: %+v", got[1])
	}
}

func TestListSecurityDailyStatesOrdersByTsCode(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "daily-states-order.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seedBulkReadStockBasics(t, ctx, store, "600519.SH", "000001.SZ", "300750.SZ")

	fetchedAt := marketdata.ShanghaiWallTime(2026, 10, 30, 18, 0)
	states := []universe.SecurityDailyState{
		{TradeDate: "2026-10-30", TsCode: "600519.SH", STType: universe.STNone, SuspendType: universe.SuspendNone, Source: "test", FetchedAt: fetchedAt},
		{TradeDate: "2026-10-30", TsCode: "300750.SZ", STType: universe.STNone, SuspendType: universe.SuspendNone, Source: "test", FetchedAt: fetchedAt},
		{TradeDate: "2026-10-30", TsCode: "000001.SZ", STType: universe.STNone, SuspendType: universe.SuspendNone, Source: "test", FetchedAt: fetchedAt},
	}
	if err := store.UpsertDailyStates(ctx, states); err != nil {
		t.Fatal(err)
	}

	got, err := store.ListSecurityDailyStates(ctx, "2026-10-30")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"000001.SZ", "300750.SZ", "600519.SH"}
	if len(got) != len(want) {
		t.Fatalf("状态数量错误: got=%d want=%d", len(got), len(want))
	}
	for i := range want {
		if got[i].TsCode != want[i] {
			t.Fatalf("状态结果未按 ts_code 稳定排序: got=%+v want=%v", got, want)
		}
	}
}

func TestListSecurityDailyStatesReturnsEmptyWhenMissing(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "daily-states-empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	got, err := store.ListSecurityDailyStates(ctx, "2026-10-30")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("无记录时应返回非 nil 空集合: %#v", got)
	}
	if _, err := store.ListSecurityDailyStates(ctx, "2026-2-1"); err == nil {
		t.Fatal("非法交易日格式必须被拒绝")
	}
}
