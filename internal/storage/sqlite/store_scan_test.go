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

func TestScanRunRepositoryRoundTripAndTerminalGuard(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "scan-runs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	started := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 2)
	completed := started.Add(time.Minute)
	run := scanner.Run{
		ID: "scan-test-001", AsOf: asOf, StartedAt: started,
		Status: scanner.RunRunning, TotalCount: 2,
	}
	items := []scanner.Item{
		{ScanID: run.ID, TsCode: "000001.SZ", SecurityName: "平安银行", Exchange: universe.ExchangeSZSE, Status: scanner.ItemPending},
		{ScanID: run.ID, TsCode: "600519.SH", SecurityName: "贵州茅台", Exchange: universe.ExchangeSSE, Status: scanner.ItemPending},
	}
	if err := store.CreateScanRun(ctx, run, items); err != nil {
		t.Fatal(err)
	}
	first := items[0]
	first.Status = scanner.ItemSuccess
	first.DataVersion = "data-v1"
	first.DecisionIDs = []string{"decision-a", "decision-b"}
	first.ReasonCodes = []string{"REASON_A"}
	first.StartedAt = started
	first.CompletedAt = completed
	if err := store.UpdateScanRunItem(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := items[1]
	second.Status = scanner.ItemDataIncomplete
	second.ReasonCodes = []string{"SECURITY_DAILY_STATE_MISSING"}
	second.StartedAt = started
	second.CompletedAt = completed
	if err := store.UpdateScanRunItem(ctx, second); err != nil {
		t.Fatal(err)
	}

	run.Status = scanner.RunPartial
	run.SuccessCount = 1
	run.DataIncompleteCount = 1
	run.CompletedAt = completed
	if err := store.CompleteScanRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	gotRun, err := store.GetScanRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotRun.Status != scanner.RunPartial || gotRun.TotalCount != 2 ||
		gotRun.SuccessCount != 1 || gotRun.DataIncompleteCount != 1 ||
		!gotRun.AsOf.Equal(asOf) || !gotRun.CompletedAt.Equal(completed) {
		t.Fatalf("扫描运行记录往返不一致: %+v", gotRun)
	}
	gotItems, err := store.ListScanRunItems(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(gotItems) != 2 || gotItems[0].TsCode != "000001.SZ" ||
		gotItems[0].Status != scanner.ItemSuccess || len(gotItems[0].DecisionIDs) != 2 ||
		gotItems[1].Status != scanner.ItemDataIncomplete ||
		gotItems[1].ReasonCodes[0] != "SECURITY_DAILY_STATE_MISSING" {
		t.Fatalf("逐股扫描记录往返不一致: %+v", gotItems)
	}
	if err := store.UpdateScanRunItem(ctx, first); err == nil {
		t.Fatal("已完成逐股记录不得覆盖")
	}
	if err := store.CompleteScanRun(ctx, run); err == nil {
		t.Fatal("已终结扫描运行不得重复终结")
	}
}

func TestCompleteCancelledScanAllowsPendingSnapshotItems(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "scan-cancelled.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	started := asOf.Add(time.Minute)
	run := scanner.Run{ID: "scan-cancelled-001", AsOf: asOf, StartedAt: started, Status: scanner.RunRunning, TotalCount: 2}
	items := []scanner.Item{
		{ScanID: run.ID, TsCode: "000001.SZ", SecurityName: "平安银行", Exchange: universe.ExchangeSZSE, Status: scanner.ItemPending},
		{ScanID: run.ID, TsCode: "600519.SH", SecurityName: "贵州茅台", Exchange: universe.ExchangeSSE, Status: scanner.ItemPending},
	}
	if err := store.CreateScanRun(ctx, run, items); err != nil {
		t.Fatal(err)
	}
	finished := started.Add(time.Second)
	item := items[0]
	item.Status = scanner.ItemSuccess
	item.StartedAt = started
	item.CompletedAt = finished
	if err := store.UpdateScanRunItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	run.Status = scanner.RunCancelled
	run.SuccessCount = 1
	run.CompletedAt = finished
	run.ErrorMessage = "context canceled"
	if err := store.CompleteScanRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetScanRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != scanner.RunCancelled || got.SuccessCount != 1 {
		t.Fatalf("取消扫描状态不正确: %+v", got)
	}
}

func TestScanRunPersistsAndReadsEligibilityExclusions(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "scan-exclusions.db")); if err != nil { t.Fatal(err) }; defer store.Close()
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	started := asOf.Add(time.Minute); completed := started.Add(time.Minute)
	run := scanner.Run{ID:"scan-exclusions-001", AsOf:asOf, StartedAt:started, Status:scanner.RunRunning, TotalCount:2, ExcludedCount:1}
	items := []scanner.Item{{ScanID:run.ID, TsCode:"000001.SZ", SecurityName:"平安银行", Exchange:universe.ExchangeSZSE, Status:scanner.ItemPending}}
	exclusions := []scanner.Exclusion{{ScanID:run.ID, TsCode:"600519.SH", SecurityName:"贵州茅台", Exchange:universe.ExchangeSSE, TradeDate:"2026-10-30", ReasonCodes:[]string{"ST_STAR_SECURITY"}}}
	if err := store.CreateScanRunWithExclusions(ctx, run, items, exclusions); err != nil { t.Fatal(err) }
	item := items[0]; item.Status = scanner.ItemSuccess; item.StartedAt = started; item.CompletedAt = completed
	if err := store.UpdateScanRunItem(ctx, item); err != nil { t.Fatal(err) }
	run.Status = scanner.RunCompleted; run.SuccessCount = 1; run.CompletedAt = completed
	if err := store.CompleteScanRun(ctx, run); err != nil { t.Fatal(err) }
	result, err := store.GetScanResult(ctx, run.ID); if err != nil { t.Fatal(err) }
	if result.Run.TotalCount != 2 || result.Run.ExcludedCount != 1 || len(result.Items) != 1 || len(result.Exclusions) != 1 { t.Fatalf("扫描排除快照读取不完整: %+v", result) }
	if result.Exclusions[0].TsCode != "600519.SH" || result.Exclusions[0].TradeDate != "2026-10-30" || len(result.Exclusions[0].ReasonCodes) != 1 || result.Exclusions[0].ReasonCodes[0] != "ST_STAR_SECURITY" { t.Fatalf("排除原因往返不一致: %+v", result.Exclusions[0]) }
}

func TestRecoverInterruptedScanRunsPreservesProgress(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "scan-recovery.db"))
	if err != nil { t.Fatal(err) }
	defer store.Close()
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	started := asOf.Add(time.Minute)
	run := scanner.Run{ID:"scan-interrupted-001", AsOf:asOf, StartedAt:started, Status:scanner.RunRunning, TotalCount:3}
	items := []scanner.Item{
		{ScanID:run.ID, TsCode:"000001.SZ", Exchange:universe.ExchangeSZSE, Status:scanner.ItemPending},
		{ScanID:run.ID, TsCode:"600000.SH", Exchange:universe.ExchangeSSE, Status:scanner.ItemPending},
		{ScanID:run.ID, TsCode:"600519.SH", Exchange:universe.ExchangeSSE, Status:scanner.ItemPending},
	}
	if err := store.CreateScanRun(ctx, run, items); err != nil { t.Fatal(err) }
	finished := started.Add(time.Second)
	completed := items[0]; completed.Status = scanner.ItemSuccess; completed.StartedAt = started; completed.CompletedAt = finished
	if err := store.UpdateScanRunItem(ctx, completed); err != nil { t.Fatal(err) }
	recoveredAt := finished.Add(time.Minute)
	count, err := store.RecoverInterruptedScanRuns(ctx, recoveredAt)
	if err != nil { t.Fatal(err) }
	if count != 1 { t.Fatalf("recovered count=%d, want 1", count) }
	got, pending, err := store.GetScanRunProgress(ctx, run.ID)
	if err != nil { t.Fatal(err) }
	if got.Status != scanner.RunCancelled || got.SuccessCount != 1 || got.FailedCount != 0 || pending != 2 || !got.CompletedAt.Equal(recoveredAt) {
		t.Fatalf("recovered run lost progress or misclassified pending items: run=%+v pending=%d", got, pending)
	}
	if count, err := store.RecoverInterruptedScanRuns(ctx, recoveredAt.Add(time.Minute)); err != nil || count != 0 {
		t.Fatalf("recovery must be idempotent: count=%d err=%v", count, err)
	}
}

