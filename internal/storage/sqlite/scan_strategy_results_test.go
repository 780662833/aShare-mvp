package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/scanner"
)

func TestScanItemPersistsIndependentStrategyResults(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "scan-strategy-results.db"))
	if err != nil { t.Fatal(err) }
	defer store.Close()
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	started := asOf.Add(time.Minute)
	run := scanner.Run{ID:"scan-strategy-results", AsOf:asOf, StartedAt:started, Status:scanner.RunRunning, TotalCount:1}
	item := scanner.Item{ScanID:run.ID, TsCode:"600519.SH", SecurityName:"贵州茅台", Exchange:universe.ExchangeSSE, Status:scanner.ItemPending}
	if err := store.CreateScanRun(ctx, run, []scanner.Item{item}); err != nil { t.Fatal(err) }
	item.Status = scanner.ItemSuccess
	item.StartedAt, item.CompletedAt = started, started.Add(time.Second)
	item.DecisionIDs = []string{"decision-chan"}
	item.StrategyResults = []scanner.StrategyResult{
		{StrategyID:"chan3buy", StrategyStatus:"SUCCEEDED", PersistenceStatus:"SAVED", DecisionID:"decision-chan", AsOf:asOf, DataVersion:"data-v1"},
		{StrategyID:"trendbreakout", StrategyStatus:"FAILED", PersistenceStatus:"NOT_ATTEMPTED", AsOf:asOf, DataVersion:"data-v1", ErrorMessage:"strategy failed"},
	}
	if err := store.UpdateScanRunItem(ctx, item); err != nil { t.Fatal(err) }
	got, err := store.ListScanRunItems(ctx, run.ID)
	if err != nil { t.Fatal(err) }
	if len(got) != 1 || len(got[0].StrategyResults) != 2 ||
		got[0].StrategyResults[0].DecisionID != "decision-chan" ||
		got[0].StrategyResults[1].ErrorMessage != "strategy failed" {
		t.Fatalf("独立策略结果往返不一致: %+v", got)
	}
}
