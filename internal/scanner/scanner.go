// Package scanner 提供当前活动股票池的确定性双策略扫描编排。
package scanner

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	tdxtypes "github.com/bensema/gotdx/types"
	appstrategy "github.com/780662833/aShare-mvp/internal/app/strategy"
	scanmodel "github.com/780662833/aShare-mvp/internal/scanner/model"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/strategy/chan3buy"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
	"github.com/780662833/aShare-mvp/internal/strategy/trendbreakout"
)

// 扫描持久化模型放在独立子包，避免 SQLite 与扫描编排形成循环依赖。
type RunStatus = scanmodel.RunStatus

const (
	RunRunning   = scanmodel.RunRunning
	RunCompleted = scanmodel.RunCompleted
	RunPartial   = scanmodel.RunPartial
	RunFailed    = scanmodel.RunFailed
	RunCancelled = scanmodel.RunCancelled
)

type ItemStatus = scanmodel.ItemStatus

const (
	ItemPending        = scanmodel.ItemPending
	ItemSuccess        = scanmodel.ItemSuccess
	ItemDataIncomplete = scanmodel.ItemDataIncomplete
	ItemAnalysisError  = scanmodel.ItemAnalysisError
	ItemFailed         = scanmodel.ItemFailed
)

type Run = scanmodel.Run
type Item = scanmodel.Item

type Repository interface {
	ListActiveStockBasics(context.Context) ([]universe.StockBasic, error)
	CreateScanRun(context.Context, Run, []Item) error
	UpdateScanRunItem(context.Context, Item) error
	CompleteScanRun(context.Context, Run) error
}

type Evaluator interface {
	EvaluateSymbol(context.Context, string, time.Time) (appstrategy.Result, error)
}

type Scanner struct {
	repo      Repository
	evaluator Evaluator
	now       func() time.Time
}

func New(repo Repository, evaluator Evaluator) (*Scanner, error) {
	if repo == nil || evaluator == nil {
		return nil, fmt.Errorf("扫描器必须提供 Repository 和 Evaluator")
	}
	return &Scanner{repo: repo, evaluator: evaluator, now: time.Now}, nil
}

// Run 在开始时冻结当前活动普通 A 股股票池和 as_of，按 ts_code 顺序逐只复用单股评估服务。
// 本方法只支持当前股票池快照，不提供历史日期的完整上市证券池重建。
func (s *Scanner) Run(ctx context.Context, asOf time.Time) (Run, error) {
	if err := ctx.Err(); err != nil {
		return Run{}, err
	}
	if asOf.IsZero() {
		return Run{}, fmt.Errorf("扫描 as_of 不能为空")
	}
	asOf = asOf.In(marketdata.ShanghaiLocation())

	securities, err := s.repo.ListActiveStockBasics(ctx)
	if err != nil {
		return Run{}, fmt.Errorf("读取当前活动股票池失败: %w", err)
	}
	securities = append([]universe.StockBasic(nil), securities...)
	sort.Slice(securities, func(i, j int) bool {
		return securities[i].TsCode < securities[j].TsCode
	})
	for i, security := range securities {
		if err := security.Validate(); err != nil {
			return Run{}, fmt.Errorf("活动股票池第 %d 条证券资料无效: %w", i, err)
		}
		if security.ListStatus != universe.ListingActive {
			return Run{}, fmt.Errorf("活动股票池包含非明确上市证券: %s (%s)", security.TsCode, security.ListStatus)
		}
		if !tdxtypes.IsStock(security.TsCode) || tdxtypes.IsETF(security.TsCode) ||
			tdxtypes.IsIndex(security.TsCode) {
			return Run{}, fmt.Errorf("活动股票池包含非普通 A 股证券，拒绝启动扫描: %s", security.TsCode)
		}
		if i > 0 && securities[i-1].TsCode == security.TsCode {
			return Run{}, fmt.Errorf("活动股票池包含重复证券: %s", security.TsCode)
		}
	}

	startedAt := s.now().In(marketdata.ShanghaiLocation())
	id, err := newScanID()
	if err != nil {
		return Run{}, fmt.Errorf("生成 scan_id 失败: %w", err)
	}
	run := Run{ID: id, AsOf: asOf, StartedAt: startedAt, Status: RunRunning, TotalCount: len(securities)}
	items := make([]Item, 0, len(securities))
	for _, security := range securities {
		items = append(items, Item{
			ScanID: id, TsCode: security.TsCode, SecurityName: security.Name,
			Exchange: security.Exchange, ListDate: security.ListDate,
			DelistDate: security.DelistDate, Status: ItemPending,
			DecisionIDs: []string{}, ReasonCodes: []string{},
		})
	}
	if err := s.repo.CreateScanRun(ctx, run, items); err != nil {
		return Run{}, fmt.Errorf("创建扫描运行记录失败: %w", err)
	}

	if len(items) == 0 {
		run.Status = RunFailed
		run.ErrorMessage = "当前活动普通 A 股股票池为空"
		run.CompletedAt = s.now().In(marketdata.ShanghaiLocation())
		if err := s.repo.CompleteScanRun(context.WithoutCancel(ctx), run); err != nil {
			return run, fmt.Errorf("完成空股票池扫描记录失败: %w", err)
		}
		return run, nil
	}

	for i := range items {
		if err := ctx.Err(); err != nil {
			run.Status = RunCancelled
			run.ErrorMessage = err.Error()
			break
		}
		item := items[i]
		item.StartedAt = s.now().In(marketdata.ShanghaiLocation())
		result, evalErr := s.evaluator.EvaluateSymbol(ctx, item.TsCode, asOf)
		item.CompletedAt = s.now().In(marketdata.ShanghaiLocation())
		if evalErr != nil {
			item.Status = ItemFailed
			item.ErrorMessage = evalErr.Error()
			run.FailedCount++
		} else {
			item.DataVersion = result.DataVersion
			item.Status, item.DecisionIDs, item.StrategyResults, item.ReasonCodes, item.ErrorMessage = classifyResult(result, item.TsCode, asOf)
			switch item.Status {
			case ItemSuccess:
				run.SuccessCount++
			case ItemDataIncomplete:
				run.DataIncompleteCount++
			case ItemAnalysisError:
				run.AnalysisErrorCount++
			default:
				run.FailedCount++
			}
		}
		persistCtx := ctx
		if ctx.Err() != nil {
			persistCtx = context.WithoutCancel(ctx)
		}
		if err := s.repo.UpdateScanRunItem(persistCtx, item); err != nil {
			return run, fmt.Errorf("保存证券 %s 扫描结果失败: %w", item.TsCode, err)
		}
		items[i] = item
		if ctx.Err() != nil {
			run.Status = RunCancelled
			run.ErrorMessage = ctx.Err().Error()
			break
		}
	}

	if run.Status != RunCancelled {
		switch {
		case run.SuccessCount == run.TotalCount:
			run.Status = RunCompleted
		case run.SuccessCount > 0:
			run.Status = RunPartial
		default:
			run.Status = RunFailed
		}
	}
	run.CompletedAt = s.now().In(marketdata.ShanghaiLocation())
	persistCtx := ctx
	if run.Status == RunCancelled {
		persistCtx = context.WithoutCancel(ctx)
	}
	if err := s.repo.CompleteScanRun(persistCtx, run); err != nil {
		return run, fmt.Errorf("保存扫描汇总失败: %w", err)
	}
	if run.Status == RunCancelled {
		return run, ctx.Err()
	}
	return run, nil
}

func classifyResult(result appstrategy.Result, tsCode string, asOf time.Time) (ItemStatus, []string, []scanmodel.StrategyResult, []string, string) {
	decisionIDs := make([]string, 0, 2)
	strategyResults := make([]scanmodel.StrategyResult, 0, 2)
	reasons := make([]string, 0)
	if result.TsCode != tsCode || !result.AsOf.Equal(asOf) {
		return ItemFailed, decisionIDs, strategyResults, reasons, "单股评估结果的证券或 as_of 不符合契约"
	}
	if len(result.StrategyResults) > 0 {
		decisionByID := make(map[string]contract.StrategyDecision, len(result.Decisions))
		for _, d := range result.Decisions {
			if d.DecisionID == "" || d.TsCode != tsCode || !d.AsOf.Equal(asOf) {
				return ItemFailed, decisionIDs, strategyResults, reasons, "已保存决策身份或时间不符合契约"
			}
			if _, ok := decisionByID[d.DecisionID]; ok {
				return ItemFailed, decisionIDs, strategyResults, reasons, "重复 DecisionID"
			}
			decisionByID[d.DecisionID] = d
		}
		expected := map[string]bool{chan3buy.StrategyID:false, trendbreakout.StrategyID:false}
		status, anySaved := ItemAnalysisError, false
		for _, sr := range result.StrategyResults {
			if _, ok := expected[sr.StrategyID]; !ok || expected[sr.StrategyID] {
				return ItemFailed, decisionIDs, strategyResults, reasons, "未知或重复策略身份"
			}
			expected[sr.StrategyID] = true
			if !sr.AsOf.Equal(asOf) || sr.DataVersion != result.DataVersion {
				return ItemFailed, decisionIDs, strategyResults, reasons, "策略结果 as_of 或 data_version 不一致"
			}
			if sr.StrategyStatus != "SUCCEEDED" && sr.StrategyStatus != "FAILED" {
				return ItemFailed, decisionIDs, strategyResults, reasons, "未知策略评估状态"
			}
			switch sr.PersistenceStatus {
			case "SAVED":
				d, ok := decisionByID[sr.DecisionID]
				if !ok || d.StrategyID != sr.StrategyID {
					return ItemFailed, decisionIDs, strategyResults, reasons, "缺少匹配的完整已保存决策"
				}
				decisionIDs = append(decisionIDs, sr.DecisionID)
				reasons = append(reasons, d.ReasonCodes...)
				anySaved = true
				if d.Status == contract.StatusDataIncomplete { status = ItemDataIncomplete
				} else if status != ItemDataIncomplete { status = ItemSuccess }
			case "FAILED", "NOT_ATTEMPTED":
				if sr.StrategyStatus == "SUCCEEDED" && sr.PersistenceStatus == "NOT_ATTEMPTED" {
					return ItemFailed, decisionIDs, strategyResults, reasons, "策略成功但未执行持久化"
				}
			default:
				return ItemFailed, decisionIDs, strategyResults, reasons, "未知策略持久化状态"
			}
			if sr.StrategyStatus == "FAILED" || sr.PersistenceStatus == "FAILED" { reasons = append(reasons, "STRATEGY_ERROR") }
			strategyResults = append(strategyResults, scanmodel.StrategyResult{
				StrategyID:sr.StrategyID, StrategyStatus:sr.StrategyStatus, PersistenceStatus:sr.PersistenceStatus,
				DecisionID:sr.DecisionID, AsOf:sr.AsOf, DataVersion:sr.DataVersion, ErrorMessage:sr.ErrorMessage,
			})
		}
		if !expected[chan3buy.StrategyID] || !expected[trendbreakout.StrategyID] {
			return ItemFailed, decisionIDs, strategyResults, reasons, "缺少独立策略结果"
		}
		if !anySaved { status = ItemAnalysisError }
		return status, decisionIDs, strategyResults, uniqueStrings(reasons), ""
	}
	if len(result.Decisions) != 2 {
		return ItemFailed, decisionIDs, strategyResults, reasons, "单股评估结果缺少双策略决策"
	}
	seenStrategies := map[string]struct{}{}
	status := ItemSuccess
	for _, decision := range result.Decisions {
		if decision.TsCode != tsCode || !decision.AsOf.Equal(asOf) ||
			strings.TrimSpace(decision.DecisionID) == "" || strings.TrimSpace(decision.StrategyID) == "" {
			return ItemFailed, decisionIDs, strategyResults, reasons, "单股评估返回的策略决策标识或时间不符合契约"
		}
		if _, exists := seenStrategies[decision.StrategyID]; exists {
			return ItemFailed, decisionIDs, strategyResults, reasons, "单股评估返回重复策略决策"
		}
		seenStrategies[decision.StrategyID] = struct{}{}
		decisionIDs = append(decisionIDs, decision.DecisionID)
		reasons = append(reasons, decision.ReasonCodes...)
		switch decision.Status {
		case contract.StatusWatching, contract.StatusSetupReady, contract.StatusConfirmed,
			contract.StatusWaitEntry, contract.StatusBuyObservation, contract.StatusHoldObservation,
			contract.StatusReduceObservation, contract.StatusSellObservation,
			contract.StatusInvalidated, contract.StatusExpired:
		case contract.StatusDataIncomplete:
			status = ItemDataIncomplete
		case contract.StatusAnalysisError:
			if status != ItemDataIncomplete {
				status = ItemAnalysisError
			}
		default:
			return ItemFailed, decisionIDs, uniqueStrings(reasons), "单股评估返回未知策略状态，拒绝计为成功"
		}
	}
	if _, ok := seenStrategies[chan3buy.StrategyID]; !ok {
		return ItemFailed, decisionIDs, uniqueStrings(reasons), "单股评估缺少缠论三买决策"
	}
	if _, ok := seenStrategies[trendbreakout.StrategyID]; !ok {
		return ItemFailed, decisionIDs, strategyResults, uniqueStrings(reasons), "单股评估缺少趋势突破决策"
	}
	return status, decisionIDs, strategyResults, uniqueStrings(reasons), ""
}

func uniqueStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func newScanID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw[:])
	return "scan-" + hex.EncodeToString(sum[:16]), nil
}
