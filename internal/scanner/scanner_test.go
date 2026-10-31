package scanner

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appstrategy "github.com/780662833/aShare-mvp/internal/app/strategy"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/strategy/chan3buy"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
	"github.com/780662833/aShare-mvp/internal/strategy/trendbreakout"
)

type fakeRepository struct {
	securities []universe.StockBasic
	run        Run
	items      []Item
	updated    []Item
}

func (f *fakeRepository) ListActiveStockBasics(context.Context) ([]universe.StockBasic, error) {
	return f.securities, nil
}
func (f *fakeRepository) CreateScanRun(_ context.Context, run Run, items []Item) error {
	f.run = run
	f.items = append([]Item(nil), items...)
	return nil
}
func (f *fakeRepository) UpdateScanRunItem(_ context.Context, item Item) error {
	f.updated = append(f.updated, item)
	return nil
}
func (f *fakeRepository) CompleteScanRun(_ context.Context, run Run) error {
	f.run = run
	return nil
}

type fakeEvaluator struct {
	calls  []string
	asOf   []time.Time
	result map[string]appstrategy.Result
	errors map[string]error
	cancel context.CancelFunc
}

func (f *fakeEvaluator) EvaluateSymbol(_ context.Context, code string, asOf time.Time) (appstrategy.Result, error) {
	f.calls = append(f.calls, code)
	f.asOf = append(f.asOf, asOf)
	if f.cancel != nil && len(f.calls) == 1 {
		f.cancel()
	}
	return f.result[code], f.errors[code]
}

func stock(code, name string) universe.StockBasic {
	exchange := universe.ExchangeSSE
	if strings.HasSuffix(code, ".SZ") {
		exchange = universe.ExchangeSZSE
	} else if strings.HasSuffix(code, ".BJ") {
		exchange = universe.ExchangeBSE
	}
	return universe.StockBasic{
		TsCode: code, Symbol: code[:6], Name: name,
		Exchange: exchange, ListStatus: universe.ListingActive,
	}
}

func validResult(code string, asOf time.Time, status contract.DecisionStatus) appstrategy.Result {
	return appstrategy.Result{
		TsCode: code, AsOf: asOf, DataVersion: "data-" + code,
		Decisions: []contract.StrategyDecision{
			{DecisionID: "decision-chan-" + code, TsCode: code, StrategyID: chan3buy.StrategyID, AsOf: asOf, Status: status, ReasonCodes: []string{"TEST_REASON"}},
			{DecisionID: "decision-trend-" + code, TsCode: code, StrategyID: trendbreakout.StrategyID, AsOf: asOf, Status: status},
		},
	}
}

func TestRunUsesStablePoolAndClassifiesIncompleteSeparately(t *testing.T) {
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	repo := &fakeRepository{securities: []universe.StockBasic{
		stock("600519.SH", "贵州茅台"), stock("000001.SZ", "平安银行"), stock("600000.SH", "浦发银行"),
	}}
	evaluator := &fakeEvaluator{
		result: map[string]appstrategy.Result{
			"000001.SZ": validResult("000001.SZ", asOf, contract.StatusWatching),
			"600000.SH": validResult("600000.SH", asOf, contract.StatusDataIncomplete),
		},
		errors: map[string]error{"600519.SH": errors.New("fixture evaluation error")},
	}
	s, err := New(repo, evaluator)
	if err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return marketdata.ShanghaiWallTime(2026, 10, 30, 15, 2) }

	run, err := s.Run(context.Background(), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != RunPartial || run.TotalCount != 3 || run.SuccessCount != 1 ||
		run.DataIncompleteCount != 1 || run.FailedCount != 1 {
		t.Fatalf("扫描汇总状态或计数错误: %+v", run)
	}
	want := []string{"000001.SZ", "600000.SH", "600519.SH"}
	if len(evaluator.calls) != len(want) {
		t.Fatalf("每只证券应只评估一次: %v", evaluator.calls)
	}
	for i := range want {
		if evaluator.calls[i] != want[i] || !evaluator.asOf[i].Equal(asOf) {
			t.Fatalf("扫描顺序或 as_of 不稳定: calls=%v asOf=%v", evaluator.calls, evaluator.asOf)
		}
	}
	if len(repo.items) != 3 || repo.items[0].Status != ItemPending {
		t.Fatalf("开始扫描前必须冻结完整股票池快照: %+v", repo.items)
	}
	if len(repo.updated) != 3 || repo.updated[1].Status != ItemDataIncomplete {
		t.Fatalf("逐股扫描状态未正确持久化: %+v", repo.updated)
	}
}

func TestRunAllSuccessfulCompletes(t *testing.T) {
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	repo := &fakeRepository{securities: []universe.StockBasic{stock("000001.SZ", "平安银行")}}
	evaluator := &fakeEvaluator{result: map[string]appstrategy.Result{
		"000001.SZ": validResult("000001.SZ", asOf, contract.StatusWatching),
	}}
	s, err := New(repo, evaluator)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Run(context.Background(), asOf)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != RunCompleted || run.SuccessCount != 1 || run.TotalCount != 1 {
		t.Fatalf("全部成功时扫描必须标记 COMPLETED: %+v", run)
	}
}

func TestRunCancellationNeverCompletesSuccessfully(t *testing.T) {
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	ctx, cancel := context.WithCancel(context.Background())
	repo := &fakeRepository{securities: []universe.StockBasic{
		stock("000001.SZ", "平安银行"), stock("600519.SH", "贵州茅台"),
	}}
	evaluator := &fakeEvaluator{
		result: map[string]appstrategy.Result{
			"000001.SZ": validResult("000001.SZ", asOf, contract.StatusWatching),
		},
		cancel: cancel,
	}
	s, err := New(repo, evaluator)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.Run(ctx, asOf)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消扫描应返回 context.Canceled，得到 %v", err)
	}
	if run.Status != RunCancelled || run.SuccessCount != 1 || run.TotalCount != 2 {
		t.Fatalf("中断扫描不得标记成功: %+v", run)
	}
	if len(evaluator.calls) != 1 {
		t.Fatalf("取消后不得继续评估剩余证券: %v", evaluator.calls)
	}
}

func TestRunRejectsDuplicateNonActiveAndNonStockPoolEntries(t *testing.T) {
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	for name, securities := range map[string][]universe.StockBasic{
		"duplicate": {stock("000001.SZ", "A"), stock("000001.SZ", "A")},
		"unknown":   {{TsCode: "000001.SZ", Symbol: "000001", Name: "A", Exchange: universe.ExchangeSZSE, ListStatus: universe.ListingUnknown}},
		"etf":       {stock("510300.SH", "沪深300ETF")},
		"index":     {stock("000001.SH", "上证指数")},
	} {
		t.Run(name, func(t *testing.T) {
			repo := &fakeRepository{securities: securities}
			evaluator := &fakeEvaluator{}
			s, err := New(repo, evaluator)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Run(context.Background(), asOf); err == nil {
				t.Fatal("不合法股票池必须拒绝启动")
			}
			if len(evaluator.calls) != 0 || repo.run.ID != "" {
				t.Fatal("非法股票池不得创建扫描记录或执行策略")
			}
		})
	}
}
