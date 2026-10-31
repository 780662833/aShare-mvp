package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
)

func TestListStrategyDecisionsFiltersAndStablePagination(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "decision-history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	stocks := []universe.StockBasic{
		{TsCode: "600519.SH", Symbol: "600519", Name: "贵州茅台", Exchange: universe.ExchangeSSE, ListStatus: universe.ListingActive},
		{TsCode: "000858.SZ", Symbol: "000858", Name: "五粮液", Exchange: universe.ExchangeSZSE, ListStatus: universe.ListingActive},
	}
	if err := store.UpsertStockBasics(ctx, stocks); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2026, 10, 30, 10, 0, 0, 0, marketdata.ShanghaiLocation())
	decisions := []contract.StrategyDecision{
		testHistoryDecision("decision-a", "600519.SH", "chan3buy", base, contract.StatusBuyObservation),
		testHistoryDecision("decision-b", "600519.SH", "trend_breakout", base, contract.StatusWaitEntry),
		testHistoryDecision("decision-c", "600519.SH", "chan3buy", base.Add(time.Hour), contract.StatusInvalidated),
		testHistoryDecision("decision-d", "000858.SZ", "chan3buy", base.Add(2*time.Hour), contract.StatusBuyObservation),
	}
	for _, decision := range decisions {
		if err := store.SaveStrategyDecision(ctx, decision); err != nil {
			t.Fatalf("SaveStrategyDecision(%s): %v", decision.DecisionID, err)
		}
	}

	all, err := store.ListStrategyDecisions(ctx, StrategyDecisionQuery{Limit: 2, Offset: 0})
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 4 || len(all.Items) != 2 || all.Items[0].DecisionID != "decision-d" || all.Items[1].DecisionID != "decision-c" {
		t.Fatalf("第一页排序/总数错误: total=%d items=%+v", all.Total, all.Items)
	}
	next, err := store.ListStrategyDecisions(ctx, StrategyDecisionQuery{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatal(err)
	}
	if next.Total != 4 || len(next.Items) != 2 {
		t.Fatalf("第二页数量错误: total=%d items=%+v", next.Total, next.Items)
	}
	// 同一 as_of 下按 decision_id DESC 稳定排序。
	if next.Items[0].DecisionID != "decision-b" || next.Items[1].DecisionID != "decision-a" {
		t.Fatalf("同一时点排序不稳定: %+v", next.Items)
	}

	from, to := base, base
	filtered, err := store.ListStrategyDecisions(ctx, StrategyDecisionQuery{
		TsCode: "600519.SH", StrategyID: "chan3buy", Status: contract.StatusBuyObservation,
		FromAsOf: &from, ToAsOf: &to, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if filtered.Total != 1 || len(filtered.Items) != 1 {
		t.Fatalf("组合过滤条件错误: %+v", filtered)
	}
	got := filtered.Items[0]
	if got.DecisionID != "decision-a" || got.StrategyVersion != "1.2.0" ||
		len(got.EntryConditions) != 1 || got.EntryConditions[0].Code != "ENTRY_EVIDENCE" ||
		len(got.Profiles) != 1 || got.Profiles[0].Profile != "profile-v1" {
		t.Fatalf("历史查询未保留原始完整 payload: %+v", got)
	}
}

func TestListStrategyDecisionsRejectsInvalidQuery(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "decision-history-invalid.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	from := time.Date(2026, 10, 31, 0, 0, 0, 0, marketdata.ShanghaiLocation())
	to := from.Add(-time.Hour)
	cases := []struct {
		name  string
		query StrategyDecisionQuery
	}{
		{name: "zero limit", query: StrategyDecisionQuery{Limit: 0}},
		{name: "limit too large", query: StrategyDecisionQuery{Limit: 201}},
		{name: "negative offset", query: StrategyDecisionQuery{Limit: 10, Offset: -1}},
		{name: "unknown status", query: StrategyDecisionQuery{Limit: 10, Status: "UNKNOWN_STATUS"}},
		{name: "reversed time range", query: StrategyDecisionQuery{Limit: 10, FromAsOf: &from, ToAsOf: &to}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := store.ListStrategyDecisions(ctx, tc.query); err == nil {
				t.Fatal("无效查询参数应返回错误")
			}
		})
	}
}

func testHistoryDecision(id, tsCode, strategyID string, asOf time.Time, status contract.DecisionStatus) contract.StrategyDecision {
	return contract.StrategyDecision{
		DecisionID: id, TsCode: tsCode, SecurityName: "测试证券",
		StrategyID: strategyID, StrategyVersion: "1.2.0", ConfigVersion: "config-v1",
		AsOf: asOf, Status: status, InternalGrade: contract.GradeHigh,
		MarketStatus: contract.MarketNeutral,
		EntryConditions: []contract.Evidence{{Code: "ENTRY_EVIDENCE", Description: "完整证据保留"}},
		MissingConditions: []contract.Evidence{}, Invalidation: []contract.Evidence{},
		RiskWarnings: []contract.Evidence{}, ReferenceLevels: []contract.PriceLevel{},
		ReasonCodes: []string{"REASON"}, DataVersion: "data-v1",
		Profiles: []contract.ProfileReference{{Frequency: "D1", Profile: "profile-v1", ProfileVersion: "1", CZSCRevision: "revision"}},
	}
}
