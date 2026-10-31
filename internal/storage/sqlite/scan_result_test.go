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
		wantErr      bool
	}{
		{name: "关联完整决策", decisionCode: "600519.SH", decisionID: "decision-ok", strategyID: "chan3buy", itemIDs: []string{"decision-ok"}},
		{name: "缺失决策", decisionCode: "600519.SH", decisionID: "decision-missing", strategyID: "chan3buy", itemIDs: []string{"decision-missing"}, wantErr: true},
		{name: "重复决策引用", decisionCode: "600519.SH", decisionID: "decision-duplicate", strategyID: "chan3buy", itemIDs: []string{"decision-duplicate", "decision-duplicate"}, wantErr: true},
		{name: "跨证券决策", decisionCode: "000001.SZ", decisionID: "decision-cross-symbol", strategyID: "chan3buy", itemIDs: []string{"decision-cross-symbol"}, wantErr: true},
		{name: "跨时间决策", decisionCode: "600519.SH", decisionAsOf: marketdata.ShanghaiWallTime(2026, 10, 29, 15, 0), decisionID: "decision-cross-time", strategyID: "chan3buy", itemIDs: []string{"decision-cross-time"}, wantErr: true},
		{name: "跨策略决策", decisionCode: "600519.SH", decisionID: "decision-cross-strategy", strategyID: "trendbreakout", itemIDs: []string{"decision-cross-strategy"}, wantErr: true},
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
			}

			item.Status = scanmodel.ItemSuccess
			item.DataVersion = "data-v1"
			item.DecisionIDs = tt.itemIDs
			item.StartedAt, item.CompletedAt = run.StartedAt, run.StartedAt.Add(time.Second)
			item.StrategyResults = []scanmodel.StrategyResult{{
				StrategyID: "chan3buy", StrategyStatus: "SUCCEEDED", PersistenceStatus: "SAVED",
				DecisionID: tt.decisionID, AsOf: asOf, DataVersion: "data-v1",
			}}
			if err := store.UpdateScanRunItem(ctx, item); err != nil { t.Fatal(err) }

			got, err := store.GetScanResult(ctx, run.ID)
			if tt.wantErr {
				if err == nil { t.Fatalf("期望校验失败，得到结果: %+v", got) }
				return
			}
			if err != nil { t.Fatal(err) }
			if got.Run.ID != run.ID || len(got.Items) != 1 || len(got.Decisions) != 1 ||
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
