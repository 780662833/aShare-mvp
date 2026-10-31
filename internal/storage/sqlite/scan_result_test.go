package sqlite

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	scanmodel "github.com/780662833/aShare-mvp/internal/scanner/model"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
)

func TestGetScanResultJoinsAndValidatesOriginalDecisions(t *testing.T) {
	tests := []struct {
		name         string
		decisionCode string
		decisionAsOf time.Time
		decisionID   string
		itemIDs      []string
		strategyID   string
		strategyResults []scanmodel.StrategyResult
		secondDecisionID string
		secondStrategyID string
		wantErr      bool
	}{
		{name: "关联完整决策", decisionCode: "600519.SH", decisionID: "decision-ok", strategyID: "chan3buy", itemIDs: []string{"decision-ok"}},
		{name: "缺失决策", decisionCode: "600519.SH", decisionID: "decision-missing", strategyID: "chan3buy", itemIDs: []string{"decision-missing"}, wantErr: true},
		{name: "重复决策引用", decisionCode: "600519.SH", decisionID: "decision-duplicate", strategyID: "chan3buy", itemIDs: []string{"decision-duplicate", "decision-duplicate"}, wantErr: true},
		{name: "跨证券决策", decisionCode: "000001.SZ", decisionID: "decision-cross-symbol", strategyID: "chan3buy", itemIDs: []string{"decision-cross-symbol"}, wantErr: true},
		{name: "跨时间决策", decisionCode: "600519.SH", decisionAsOf: marketdata.ShanghaiWallTime(2026, 10, 29, 15, 0), decisionID: "decision-cross-time", strategyID: "chan3buy", itemIDs: []string{"decision-cross-time"}, wantErr: true},
		{name: "跨策略决策", decisionCode: "600519.SH", decisionID: "decision-cross-strategy", strategyID: "trendbreakout", itemIDs: []string{"decision-cross-strategy"}, wantErr: true},
		{name: "空策略 ID", decisionCode: "600519.SH", decisionID: "decision-empty-strategy", strategyID: "chan3buy", itemIDs: []string{"decision-empty-strategy"}, strategyResults: []scanmodel.StrategyResult{{StrategyID: "", PersistenceStatus: "SAVED", DecisionID: "decision-empty-strategy"}}, wantErr: true},
		{name: "多个策略重复引用同一决策", decisionCode: "600519.SH", decisionID: "decision-shared", strategyID: "chan3buy", itemIDs: []string{"decision-shared"}, strategyResults: []scanmodel.StrategyResult{{StrategyID: "chan3buy", PersistenceStatus: "SAVED", DecisionID: "decision-shared"}, {StrategyID: "trendbreakout", PersistenceStatus: "SAVED", DecisionID: "decision-shared"}}, wantErr: true},
		{name: "决策索引缺少对应策略结果", decisionCode: "600519.SH", decisionID: "decision-one", strategyID: "chan3buy", itemIDs: []string{"decision-one", "decision-two"}, wantErr: true},
		{name: "策略结果引用索引外决策", decisionCode: "600519.SH", decisionID: "decision-outside", strategyID: "chan3buy", itemIDs: []string{"decision-other"}, wantErr: true},
		{name: "双策略一一对应", decisionCode: "600519.SH", decisionID: "decision-chan", strategyID: "chan3buy", itemIDs: []string{"decision-chan", "decision-trend"}, secondDecisionID: "decision-trend", secondStrategyID: "trendbreakout"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := Open(ctx, filepath.Join(t.TempDir(), "scan-result.db"))
			if err != nil { t.Fatal(err) }
			defer store.Close()

			asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 0)
			for _, stock := range []struct{ code, name, exchange string }{
				{"600519.SH", "贵州茅台", "SSE"}, {"000001.SZ", "平安银行", "SZSE"},
			} {
				_, err = store.DB().ExecContext(ctx, `INSERT INTO stock_basic
					(ts_code,symbol,name,exchange,list_status,updated_at)
					VALUES (?,?,?,?,?,?)`, stock.code, stock.code[:6], stock.name, stock.exchange, "L", asOf.Format(time.RFC3339Nano))
				if err != nil { t.Fatal(err) }
			}

			run := scanmodel.Run{ID: "scan-" + strings.ReplaceAll(tt.name, " ", "-"), AsOf: asOf,
				StartedAt: asOf.Add(time.Minute), Status: scanmodel.RunRunning, TotalCount: 1}
			item := scanmodel.Item{ScanID: run.ID, TsCode: "600519.SH", SecurityName: "贵州茅台",
				Exchange: universe.ExchangeSSE, Status: scanmodel.ItemPending}
			if err := store.CreateScanRun(ctx, run, []scanmodel.Item{item}); err != nil { t.Fatal(err) }

			if tt.name != "缺失决策" && tt.name != "重复决策引用" {
				decisionAsOf := tt.decisionAsOf
				if decisionAsOf.IsZero() { decisionAsOf = asOf }
				decision := contract.StrategyDecision{
					DecisionID: tt.decisionID, TsCode: tt.decisionCode, SecurityName: "fixture",
					StrategyID: tt.strategyID, StrategyVersion: "v1", ConfigVersion: "c1",
					AsOf: decisionAsOf, Status: contract.StatusWatching,
					InternalGrade: contract.GradeUnknown, MarketStatus: contract.MarketUnknown,
					EntryConditions: []contract.Evidence{}, MissingConditions: []contract.Evidence{},
					Invalidation: []contract.Evidence{}, RiskWarnings: []contract.Evidence{},
					ReferenceLevels: []contract.PriceLevel{}, ReasonCodes: []string{},
					DataVersion: "data-v1", Profiles: []contract.ProfileReference{},
				}
				if err := store.SaveStrategyDecision(ctx, decision); err != nil { t.Fatal(err) }
				if tt.secondDecisionID != "" {
					second := decision
					second.DecisionID = tt.secondDecisionID
					second.StrategyID = tt.secondStrategyID
					if err := store.SaveStrategyDecision(ctx, second); err != nil { t.Fatal(err) }
				}
			}

			item.Status = scanmodel.ItemSuccess
			item.DataVersion = "data-v1"
			item.DecisionIDs = tt.itemIDs
			item.StartedAt, item.CompletedAt = run.StartedAt, run.StartedAt.Add(time.Second)
			item.StrategyResults = tt.strategyResults
			if item.StrategyResults == nil {
				item.StrategyResults = []scanmodel.StrategyResult{{
					StrategyID: "chan3buy", StrategyStatus: "SUCCEEDED", PersistenceStatus: "SAVED",
					DecisionID: tt.decisionID, AsOf: asOf, DataVersion: "data-v1",
				}}
				if tt.secondDecisionID != "" {
					item.StrategyResults = append(item.StrategyResults, scanmodel.StrategyResult{
						StrategyID: tt.secondStrategyID, StrategyStatus: "SUCCEEDED", PersistenceStatus: "SAVED",
						DecisionID: tt.secondDecisionID, AsOf: asOf, DataVersion: "data-v1",
					})
				}
			} else {
				for i := range item.StrategyResults {
					item.StrategyResults[i].AsOf = asOf
					item.StrategyResults[i].DataVersion = "data-v1"
					item.StrategyResults[i].StrategyStatus = "SUCCEEDED"
				}
			}
			if err := store.UpdateScanRunItem(ctx, item); err != nil { t.Fatal(err) }

			got, err := store.GetScanResult(ctx, run.ID)
			if tt.wantErr {
				if err == nil { t.Fatalf("期望校验失败，得到结果: %+v", got) }
				return
			}
			if err != nil { t.Fatal(err) }
			if got.Run.ID != run.ID || len(got.Items) != 1 || len(got.Decisions) != len(tt.itemIDs) ||
				got.Decisions[0].DecisionID != tt.decisionID || got.Decisions[0].TsCode != "600519.SH" {
				t.Fatalf("扫描结果关联不完整: %+v", got)
			}
		})
	}
}

func TestGetScanResultRejectsEmptyScanID(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "scan-result-empty.db"))
	if err != nil { t.Fatal(err) }
	defer store.Close()
	if _, err := store.GetScanResult(ctx, " "); err == nil {
		t.Fatal("空 scan_id 必须返回错误")
	}
}


func TestGetScanResultValidatesSummaryConsistency(t *testing.T) {
	tests := []struct {
		name string
		total int
		runStatus scanmodel.RunStatus
		success int
		incomplete int
		analysisError int
		failed int
		itemStatuses []scanmodel.ItemStatus
		wantErr string
	}{
		{name: "明细数量与总数不一致", total: 3, runStatus: scanmodel.RunRunning, itemStatuses: []scanmodel.ItemStatus{scanmodel.ItemPending, scanmodel.ItemPending}, wantErr: "明细数量"},
		{name: "SUCCESS 汇总不一致", total: 2, runStatus: scanmodel.RunCompleted, success: 1, itemStatuses: []scanmodel.ItemStatus{scanmodel.ItemSuccess, scanmodel.ItemSuccess}, wantErr: "汇总与逐股明细"},
		{name: "DATA_INCOMPLETE 汇总不一致", total: 2, runStatus: scanmodel.RunPartial, success: 1, incomplete: 0, itemStatuses: []scanmodel.ItemStatus{scanmodel.ItemSuccess, scanmodel.ItemDataIncomplete}, wantErr: "汇总与逐股明细"},
		{name: "ANALYSIS_ERROR 汇总不一致", total: 2, runStatus: scanmodel.RunPartial, success: 1, analysisError: 0, itemStatuses: []scanmodel.ItemStatus{scanmodel.ItemSuccess, scanmodel.ItemAnalysisError}, wantErr: "汇总与逐股明细"},
		{name: "FAILED 汇总不一致", total: 2, runStatus: scanmodel.RunPartial, success: 1, failed: 0, itemStatuses: []scanmodel.ItemStatus{scanmodel.ItemSuccess, scanmodel.ItemFailed}, wantErr: "汇总与逐股明细"},
		{name: "取消扫描允许 PENDING", total: 2, runStatus: scanmodel.RunCancelled, success: 1, itemStatuses: []scanmodel.ItemStatus{scanmodel.ItemSuccess, scanmodel.ItemPending}},
		{name: "未知明细状态", total: 2, runStatus: scanmodel.RunRunning, itemStatuses: []scanmodel.ItemStatus{scanmodel.ItemPending, scanmodel.ItemStatus("CORRUPTED")}, wantErr: "未知状态"},
		{name: "正常 COMPLETED", total: 2, runStatus: scanmodel.RunCompleted, success: 2, itemStatuses: []scanmodel.ItemStatus{scanmodel.ItemSuccess, scanmodel.ItemSuccess}},
		{name: "RUNNING 不比较未终态汇总", total: 2, runStatus: scanmodel.RunRunning, itemStatuses: []scanmodel.ItemStatus{scanmodel.ItemPending, scanmodel.ItemPending}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			store, err := Open(ctx, filepath.Join(t.TempDir(), "summary-check.db"))
			if err != nil { t.Fatal(err) }
			defer store.Close()
			asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 0)
			started := asOf.Add(time.Minute)
			run := scanmodel.Run{ID: "summary-check", AsOf: asOf, StartedAt: started, Status: scanmodel.RunRunning, TotalCount: 2}
			items := make([]scanmodel.Item, 0, 2)
			for _, stock := range []struct{ code, name, exchange string }{
				{"600519.SH", "贵州茅台", "SSE"},
				{"000001.SZ", "平安银行", "SZSE"},
			} {
				_, err := store.DB().ExecContext(ctx, `INSERT INTO stock_basic
					(ts_code,symbol,name,exchange,list_status,updated_at)
					VALUES (?,?,?,?,?,?)`,
					stock.code, stock.code[:6], stock.name, stock.exchange, "L", asOf.Format(time.RFC3339Nano))
				if err != nil { t.Fatal(err) }
				exchange := universe.ExchangeSSE
				if stock.exchange == "SZSE" { exchange = universe.ExchangeSZSE }
				items = append(items, scanmodel.Item{
					ScanID: run.ID, TsCode: stock.code, SecurityName: stock.name,
					Exchange: exchange, Status: scanmodel.ItemPending,
				})
			}
			if err := store.CreateScanRun(ctx, run, items); err != nil { t.Fatal(err) }
			for i, status := range tt.itemStatuses {
				item := items[i]
				if status == scanmodel.ItemPending { continue }
				if status == scanmodel.ItemStatus("CORRUPTED") {
					// 仅在这个测试连接上临时忽略 CHECK，模拟历史损坏数据；
					// 写入端约束仍保持不变，读取端必须拒绝未知状态。
					store.DB().SetMaxOpenConns(1)
					if _, err := store.DB().ExecContext(ctx, "PRAGMA ignore_check_constraints = ON"); err != nil { t.Fatal(err) }
					if _, err := store.DB().ExecContext(ctx,
						"UPDATE scan_run_items SET status = ? WHERE scan_id = ? AND ts_code = ?",
						string(status), run.ID, item.TsCode); err != nil { t.Fatal(err) }
					continue
				}
				item.Status = status
				item.StartedAt = started
				item.CompletedAt = started.Add(time.Second)
				if err := store.UpdateScanRunItem(ctx, item); err != nil { t.Fatal(err) }
			}
			// 直接构造历史汇总异常，不经过写入端 CompleteScanRun 的一致性校验。
			_, err = store.DB().ExecContext(ctx, `UPDATE scan_runs SET total_count=?, status=?, success_count=?, data_incomplete_count=?, analysis_error_count=?, failed_count=? WHERE scan_id=?`,
				tt.total, string(tt.runStatus), tt.success, tt.incomplete, tt.analysisError, tt.failed, run.ID)
			if err != nil { t.Fatal(err) }
			_, err = store.GetScanResult(ctx, run.ID)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("期望错误包含 %q，实际: %v", tt.wantErr, err)
				}
				return
			}
			if err != nil { t.Fatalf("期望读取成功，实际: %v", err) }
		})
	}
}
