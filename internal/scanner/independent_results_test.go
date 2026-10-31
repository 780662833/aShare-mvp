package scanner

import (
	"testing"

	appstrategy "github.com/780662833/aShare-mvp/internal/app/strategy"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/strategy/chan3buy"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
	"github.com/780662833/aShare-mvp/internal/strategy/trendbreakout"
)

func TestClassifyResultMarksSuccessfulAnalysisWithFailedPersistenceAsFailed(t *testing.T) {
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	result := appstrategy.Result{TsCode:"600519.SH", AsOf:asOf, DataVersion:"data-v1",
		Decisions: []contract.StrategyDecision{{DecisionID:"decision-chan", TsCode:"600519.SH", StrategyID:chan3buy.StrategyID, AsOf:asOf, Status:contract.StatusWatching}},
		StrategyResults: []appstrategy.StrategyResult{
			{StrategyID:chan3buy.StrategyID, StrategyStatus:"SUCCEEDED", PersistenceStatus:"SAVED", DecisionID:"decision-chan", AsOf:asOf, DataVersion:"data-v1"},
			{StrategyID:trendbreakout.StrategyID, StrategyStatus:"SUCCEEDED", PersistenceStatus:"FAILED", DecisionID:"decision-trend", AsOf:asOf, DataVersion:"data-v1", ErrorMessage:"disk full"},
		}}
	status, ids, strategies, reasons, message := classifyResult(result, "600519.SH", asOf)
	if status != ItemFailed || len(ids) != 1 || len(strategies) != 2 || message == "" { t.Fatalf("持久化失败不得伪装为成功: status=%s ids=%v strategies=%+v message=%q", status, ids, strategies, message) }
	found := false; for _, reason := range reasons { if reason == "DECISION_PERSISTENCE_FAILED" { found = true } }; if !found { t.Fatalf("缺少持久化错误原因码: %v", reasons) }
}

func TestClassifyResultPreservesOneSuccessfulStrategy(t *testing.T) {
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	result := appstrategy.Result{
		TsCode: "600519.SH", AsOf: asOf, DataVersion: "data-v1",
		Decisions: []contract.StrategyDecision{{
			DecisionID: "decision-chan", TsCode: "600519.SH", StrategyID: chan3buy.StrategyID,
			AsOf: asOf, Status: contract.StatusWatching, ReasonCodes: []string{"CHAN_READY"},
		}},
		StrategyResults: []appstrategy.StrategyResult{
			{StrategyID: chan3buy.StrategyID, StrategyStatus: "SUCCEEDED", PersistenceStatus: "SAVED",
				DecisionID: "decision-chan", AsOf: asOf, DataVersion: "data-v1"},
			{StrategyID: trendbreakout.StrategyID, StrategyStatus: "FAILED", PersistenceStatus: "NOT_ATTEMPTED",
				AsOf: asOf, DataVersion: "data-v1", ErrorMessage: "fixture failure"},
		},
	}
	status, ids, strategies, reasons, message := classifyResult(result, "600519.SH", asOf)
	if status != ItemSuccess || len(ids) != 1 || ids[0] != "decision-chan" ||
		len(strategies) != 2 || len(reasons) != 2 || message != "" {
		t.Fatalf("部分策略成功必须保留成功决策和失败状态: status=%s ids=%v strategies=%+v reasons=%v error=%q",
			status, ids, strategies, reasons, message)
	}
}
