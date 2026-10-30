package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

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
	fetchedAt := marketdata.ShanghaiWallTime(2026, 10, 30, 16, 0)
	bars := make([]marketdata.Bar, 0, 4)
	for i, minute := range []int{10, 30, 60, 90} {
		bar := marketdata.Bar{
			TsCode: "600519.SH", Frequency: marketdata.FrequencyM30,
			BarTime: marketdata.ShanghaiWallTime(2026, 10, 30, 9, 30).Add(time.Duration(minute) * time.Minute),
			Open: 100, High: 103, Low: 99, Close: 101 + float64(i),
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
