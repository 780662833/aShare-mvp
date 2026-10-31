// Package strategy 提供单股策略评估用例；不负责全市场扫描、行情抓取或交易执行。
package strategy

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/780662833/aShare-mvp/internal/czscclient"
	"github.com/780662833/aShare-mvp/internal/market/calendar"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/strategy/chan3buy"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
	"github.com/780662833/aShare-mvp/internal/strategy/trendbreakout"
)

type Repository interface {
	GetStockBasic(context.Context, string) (universe.StockBasic, error)
	GetSecurityDailyState(context.Context, string, string) (universe.SecurityDailyState, error)
	GetTradeDay(context.Context, calendar.Exchange, string) (calendar.TradeDay, error)
	GetLatestOpenTradeDay(context.Context, calendar.Exchange, string) (calendar.TradeDay, error)
	ListRecentBars(context.Context, string, marketdata.Frequency, marketdata.AdjustMode, time.Time, int) ([]marketdata.Bar, error)
	GetLatestStrategyDecisionBefore(context.Context, string, string, time.Time) (contract.StrategyDecision, error)
	SaveStrategyDecision(context.Context, contract.StrategyDecision) error
}

type Analyzer interface {
	Analyze(context.Context, czscclient.AnalyzeRequest) (czscclient.AnalyzeResponse, error)
}

type Config struct {
	AdjustMode  marketdata.AdjustMode
	HistoryBars int
	Chan3Buy    chan3buy.Config
	TrendBreakout trendbreakout.Config
}

type Service struct {
	repo Repository
	analyzer Analyzer
	config Config
	chanStrategy *chan3buy.Strategy
	trendStrategy *trendbreakout.Strategy
}

type StrategyResult struct {
	StrategyID string `json:"strategy_id"`
	StrategyStatus string `json:"strategy_status"`
	PersistenceStatus string `json:"persistence_status"`
	DecisionID string `json:"decision_id,omitempty"`
	AsOf time.Time `json:"as_of"`
	DataVersion string `json:"data_version"`
	ErrorMessage string `json:"error_message,omitempty"`
}

type Result struct {
	TsCode string `json:"ts_code"`
	AsOf time.Time `json:"as_of"`
	DataVersion string `json:"data_version"`
	MarketStatus contract.MarketStatus `json:"market_status"`
	Decisions []contract.StrategyDecision `json:"decisions"`
	StrategyResults []StrategyResult `json:"strategy_results"`
}

type profileRequest struct {
	frequency marketdata.Frequency
	profile string
}

func New(repo Repository, analyzer Analyzer, config Config) (*Service, error) {
	if repo == nil || analyzer == nil {
		return nil, fmt.Errorf("策略评估必须提供 Repository 和 CZSC Analyzer")
	}
	if config.AdjustMode != marketdata.AdjustQFQ &&
		config.AdjustMode != marketdata.AdjustHFQ &&
		config.AdjustMode != marketdata.AdjustNone {
		return nil, fmt.Errorf("策略评估必须显式配置有效复权口径")
	}
	if config.HistoryBars < 63 || config.HistoryBars > 5000 {
		return nil, fmt.Errorf("HistoryBars 必须在 63 到 5000 之间")
	}
	requiredDailyBars := config.Chan3Buy.DailySlowMAPeriod + 1
	if breakoutBars := config.TrendBreakout.LookbackDays + 1; breakoutBars > requiredDailyBars {
		requiredDailyBars = breakoutBars
	}
	if requiredDailyBars < 63 {
		requiredDailyBars = 63 // 锁定 CZSC D1 profile 的最小历史长度。
	}
	if config.HistoryBars < requiredDailyBars {
		return nil, fmt.Errorf("HistoryBars=%d 小于当前策略配置需要的日线历史长度 %d", config.HistoryBars, requiredDailyBars)
	}
	chanStrategy, err := chan3buy.New(config.Chan3Buy)
	if err != nil {
		return nil, fmt.Errorf("初始化缠论三买策略失败: %w", err)
	}
	trendStrategy, err := trendbreakout.New(config.TrendBreakout)
	if err != nil {
		return nil, fmt.Errorf("初始化趋势突破策略失败: %w", err)
	}
	return &Service{
		repo: repo,
		analyzer: analyzer,
		config: config,
		chanStrategy: chanStrategy,
		trendStrategy: trendStrategy,
	}, nil
}

// EvaluateSymbol 在指定 as_of 时点对单只股票执行一次双策略评估。
// 按 as_of 与交易时段分别校验 D1/M60/M30 最新完整 K 线；允许盘中较短周期先于较长周期确认。
func (s *Service) EvaluateSymbol(ctx context.Context, tsCode string, asOf time.Time) (Result, error) {
	result := Result{TsCode: tsCode, Decisions: make([]contract.StrategyDecision, 0, 2), StrategyResults: make([]StrategyResult, 0, 2)}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if strings.TrimSpace(tsCode) == "" || asOf.IsZero() {
		return result, fmt.Errorf("ts_code 和 as_of 不能为空")
	}
	asOf = asOf.In(marketdata.ShanghaiLocation())
	result.AsOf = asOf

	security, err := s.repo.GetStockBasic(ctx, tsCode)
	if err != nil {
		return result, fmt.Errorf("读取证券基础资料 %s 失败: %w", tsCode, err)
	}
	if err := security.Validate(); err != nil {
		return result, fmt.Errorf("证券基础资料无效: %w", err)
	}

	input := contract.StrategyInput{
		Security: security,
		AsOf: asOf,
		Bars: make(map[marketdata.Frequency][]marketdata.Bar, 3),
		Technical: make(map[marketdata.Frequency]contract.FrequencyAnalysis, 3),
		MarketContext: contract.MarketContext{
			AsOf: asOf,
			Status: contract.MarketUnknown,
		},
	}
	issues := make([]string, 0)
	expectedTimes, stateDate, calendarIssues := expectedBarTimes(
		ctx, s.repo, calendar.Exchange(security.Exchange), asOf,
	)
	issues = append(issues, calendarIssues...)
	if err := ctx.Err(); err != nil { return result, err }

	if stateDate != "" {
		state, stateErr := s.repo.GetSecurityDailyState(ctx, tsCode, stateDate)
		if stateErr != nil {
			issues = append(issues, "SECURITY_DAILY_STATE_MISSING")
		} else {
			input.DailyState = state
			if state.TradeDate != stateDate {
				issues = append(issues, "SECURITY_DAILY_STATE_STALE")
			}
			if state.FetchedAt.After(asOf) {
				issues = append(issues, "SECURITY_DAILY_STATE_AFTER_AS_OF")
			}
		}
	} else {
		issues = append(issues, "SECURITY_DAILY_STATE_UNAVAILABLE")
	}

	frequencies := []marketdata.Frequency{
		marketdata.FrequencyD1,
		marketdata.FrequencyM60,
		marketdata.FrequencyM30,
	}
	var canonicalSource string
	var canonicalAdjust marketdata.AdjustMode
	hasCanonicalSeries := false
	for _, frequency := range frequencies {
		if err := ctx.Err(); err != nil { return result, err }
		bars, readErr := s.repo.ListRecentBars(ctx, tsCode, frequency, s.config.AdjustMode, asOf, s.config.HistoryBars)
		if readErr != nil {
			issues = append(issues, "BARS_"+string(frequency)+"_READ_FAILED")
			continue
		}
		if len(bars) == 0 {
			issues = append(issues, "BARS_"+string(frequency)+"_MISSING")
			continue
		}
		input.Bars[frequency] = bars
		if len(bars) > 0 {
			if !hasCanonicalSeries {
				canonicalSource = bars[0].Source
				canonicalAdjust = bars[0].AdjustMode
				hasCanonicalSeries = true
			} else {
				if bars[0].Source != canonicalSource {
					issues = append(issues, "CROSS_FREQUENCY_SOURCE_MISMATCH")
				}
				if bars[0].AdjustMode != canonicalAdjust {
					issues = append(issues, "CROSS_FREQUENCY_ADJUSTMENT_MISMATCH")
				}
			}
		}
		if err := marketdata.ValidateBatch(bars); err != nil {
			issues = append(issues, "BARS_"+string(frequency)+"_INVALID")
		}
		for _, bar := range bars {
			if bar.TsCode != tsCode || bar.Frequency != frequency {
				issues = append(issues, "BARS_"+string(frequency)+"_IDENTITY_MISMATCH")
				break
			}
			if !bar.IsComplete {
				issues = append(issues, "BARS_"+string(frequency)+"_FORMING_BAR")
				break
			}
			if bar.BarTime.After(asOf) {
				issues = append(issues, "BARS_"+string(frequency)+"_AFTER_AS_OF")
				break
			}
			if bar.FetchedAt.After(asOf) {
				issues = append(issues, "BARS_"+string(frequency)+"_FETCHED_AFTER_AS_OF")
				break
			}
		}
		expectedBarTime := expectedTimes[frequency]
		if expectedBarTime.IsZero() {
			issues = append(issues, "EXPECTED_BAR_TIME_"+string(frequency)+"_UNKNOWN")
			continue
		}
		latest := bars[len(bars)-1]
		if !latest.BarTime.Equal(expectedBarTime) {
			issues = append(issues, "BARS_"+string(frequency)+"_STALE")
		}
	}
	input.DataIssues = unique(issues)
	input.DataVersion = buildDataVersion(security, input.DailyState, input.Bars)

	if len(input.DataIssues) == 0 {
		if err := s.analyzeFrequencies(ctx, input.Bars, input.Technical); err != nil {
			result.DataVersion = input.DataVersion
			result.MarketStatus = contract.MarketUnknown
			if ctx.Err() != nil { return result, ctx.Err() }
			return s.saveAnalysisError(ctx, result, input, err)
		}
	}

	result.DataVersion = input.DataVersion
	result.MarketStatus = input.MarketContext.Status

	// 两套策略分别读取自身历史、独立评估和保存；任何单套失败都不阻断另一套。
	evaluateAndSave := func(strategyID string, evaluate func(context.Context, contract.StrategyInput) (contract.StrategyDecision, error)) {
		sr := StrategyResult{StrategyID: strategyID, StrategyStatus: "NOT_EVALUATED",
			PersistenceStatus: "NOT_ATTEMPTED", AsOf: asOf, DataVersion: input.DataVersion}
		if ctx.Err() != nil { sr.ErrorMessage = ctx.Err().Error(); result.StrategyResults = append(result.StrategyResults, sr); return }
		strategyInput := input
		strategyInput.PreviousDecision = nil
		previous, previousErr := s.repo.GetLatestStrategyDecisionBefore(ctx, tsCode, strategyID, asOf)
		if previousErr == nil {
			strategyInput.PreviousDecision = &previous
		} else if previousErr != sql.ErrNoRows {
			sr.StrategyStatus = "FAILED"
			sr.ErrorMessage = fmt.Sprintf("读取上一条决策失败: %v", previousErr)
			result.StrategyResults = append(result.StrategyResults, sr)
			return
		}
		decision, evalErr := evaluate(ctx, strategyInput)
		if evalErr != nil {
			sr.StrategyStatus = "FAILED"
			sr.ErrorMessage = fmt.Sprintf("策略评估失败: %v", evalErr)
			result.StrategyResults = append(result.StrategyResults, sr)
			return
		}
		sr.StrategyStatus = "SUCCEEDED"
		sr.DecisionID = decision.DecisionID
		if saveErr := s.repo.SaveStrategyDecision(ctx, decision); saveErr != nil {
			sr.PersistenceStatus = "FAILED"
			sr.ErrorMessage = fmt.Sprintf("保存策略决策失败: %v", saveErr)
			result.StrategyResults = append(result.StrategyResults, sr)
			return
		}
		sr.PersistenceStatus = "SAVED"
		result.Decisions = append(result.Decisions, decision)
		result.StrategyResults = append(result.StrategyResults, sr)
	}
	evaluateAndSave(chan3buy.StrategyID, s.chanStrategy.Evaluate)
	evaluateAndSave(trendbreakout.StrategyID, s.trendStrategy.Evaluate)
	if err := ctx.Err(); err != nil { return result, err }
	return result, nil
}

func (s *Service) analyzeFrequencies(
	ctx context.Context,
	bars map[marketdata.Frequency][]marketdata.Bar,
	target map[marketdata.Frequency]contract.FrequencyAnalysis,
) error {
	requests := []profileRequest{
		{frequency: marketdata.FrequencyM60, profile: czscclient.SignalProfileChan3BuyM60},
		{frequency: marketdata.FrequencyM30, profile: czscclient.SignalProfileChan3BuyM30},
		{frequency: marketdata.FrequencyD1, profile: czscclient.SignalProfileTrendD1},
	}
	for _, item := range requests {
		request, err := czscclient.BuildRequestWithProfile(bars[item.frequency], item.profile)
		if err != nil {
			return fmt.Errorf("构建 %s CZSC 请求失败: %w", item.frequency, err)
		}
		response, err := s.analyzer.Analyze(ctx, request)
		if err != nil {
			return fmt.Errorf("执行 %s CZSC 分析失败: %w", item.frequency, err)
		}
		analysis, err := toFrequencyAnalysis(response)
		if err != nil {
			return fmt.Errorf("转换 %s CZSC 响应失败: %w", item.frequency, err)
		}
		target[item.frequency] = analysis
	}
	return nil
}

func toFrequencyAnalysis(response czscclient.AnalyzeResponse) (contract.FrequencyAnalysis, error) {
	asOf, err := time.Parse(time.RFC3339Nano, response.AsOf)
	if err != nil {
		return contract.FrequencyAnalysis{}, fmt.Errorf("CZSC as_of 无效: %w", err)
	}
	result := contract.FrequencyAnalysis{
		AsOf: asOf,
		InputBarCount: response.InputBarCount,
		SignalProfile: response.SignalProfile,
		SignalProfileVersion: response.SignalProfileVersion,
		CZSCSourceRevision: response.CZSCSourceRevision,
		SignalDataStatus: response.SignalDataStatus,
		Signals: response.Signals,
		BICount: response.BICount,
		RecentBI: make([]contract.BISummary, 0, len(response.RecentBI)),
		SignalLifecycles: make(map[string]contract.SignalLifecycle, len(response.SignalLifecycles)),
	}
	for _, bi := range response.RecentBI {
		result.RecentBI = append(result.RecentBI, contract.BISummary{
			StartTime: bi.StartTime, EndTime: bi.EndTime, Direction: bi.Direction,
			High: bi.High, Low: bi.Low, Power: bi.Power, Length: bi.Length,
		})
	}
	for key, lifecycle := range response.SignalLifecycles {
		startedAt, err := time.Parse(time.RFC3339Nano, lifecycle.StartedAt)
		if err != nil {
			return contract.FrequencyAnalysis{}, fmt.Errorf("信号 %q 的 started_at 无效: %w", key, err)
		}
		result.SignalLifecycles[key] = contract.SignalLifecycle{
			AgeBars: lifecycle.AgeBars,
			StartedAt: startedAt,
		}
	}
	return result, nil
}

func buildDataVersion(
	security universe.StockBasic,
	state universe.SecurityDailyState,
	bars map[marketdata.Frequency][]marketdata.Bar,
) string {
	snapshot := struct {
		Security universe.StockBasic
		State universe.SecurityDailyState
		Bars map[marketdata.Frequency][]marketdata.Bar
		Revision string
	}{
		Security: security,
		State: state,
		Bars: bars,
		Revision: czscclient.CZSCSourceRevision,
	}
	body, _ := json.Marshal(snapshot)
	sum := sha256.Sum256(body)
	return "data-" + hex.EncodeToString(sum[:16])
}

func unique(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}

func (s *Service) saveAnalysisError(ctx context.Context, result Result, input contract.StrategyInput, cause error) (Result, error) {
	decisions := []contract.StrategyDecision{
		contract.NewDecision(input, s.chanStrategy.ID(), s.chanStrategy.Version(), s.config.Chan3Buy.Version),
		contract.NewDecision(input, s.trendStrategy.ID(), s.trendStrategy.Version(), s.config.TrendBreakout.Version),
	}
	strategyIDs := []string{s.chanStrategy.ID(), s.trendStrategy.ID()}
	for i := range decisions {
		decisions[i].Status = contract.StatusAnalysisError
		contract.AppendReason(&decisions[i], "CZSC_ANALYSIS_FAILED")
		decisions[i].RiskWarnings = append(decisions[i].RiskWarnings, contract.Evidence{
			Code: "CZSC_ANALYSIS_FAILED",
			Description: "CZSC 多周期分析失败，本次不生成策略信号",
			Value: cause.Error(),
			Source: "CZSC HTTP/JSON client",
			Confidence: contract.GradeHigh,
		})
		sr := StrategyResult{StrategyID: strategyIDs[i], StrategyStatus: "FAILED", PersistenceStatus: "NOT_ATTEMPTED",
			DecisionID: decisions[i].DecisionID, AsOf: result.AsOf, DataVersion: result.DataVersion, ErrorMessage: cause.Error()}
		if err := s.repo.SaveStrategyDecision(ctx, decisions[i]); err != nil {
			sr.PersistenceStatus = "FAILED"
			sr.ErrorMessage = fmt.Sprintf("CZSC 错误决策保存失败: %v; 原因: %v", err, cause)
		} else {
			sr.PersistenceStatus = "SAVED"
			result.Decisions = append(result.Decisions, decisions[i])
		}
		result.StrategyResults = append(result.StrategyResults, sr)
	}
	return result, nil
}

func expectedBarTimes(
	ctx context.Context,
	repo Repository,
	exchange calendar.Exchange,
	asOf time.Time,
) (map[marketdata.Frequency]time.Time, string, []string) {
	expected := make(map[marketdata.Frequency]time.Time, 3)
	issues := make([]string, 0)
	date := asOf.Format("2006-01-02")
	day, err := repo.GetTradeDay(ctx, exchange, date)
	if err != nil {
		return expected, "", []string{"AS_OF_TRADING_CALENDAR_UNKNOWN"}
	}
	if day.FetchedAt.IsZero() || day.FetchedAt.After(asOf) {
		issues = append(issues, "AS_OF_TRADING_CALENDAR_AFTER_AS_OF")
	}

	var stateDate string
	var previousOpen calendar.TradeDay
	if day.IsOpen {
		stateDate = date
		currentOpen, currentErr := repo.GetLatestOpenTradeDay(ctx, exchange, date)
		if currentErr != nil || currentOpen.CalDate != date {
			issues = append(issues, "LATEST_OPEN_TRADE_DAY_UNKNOWN")
		} else if currentOpen.FetchedAt.IsZero() || currentOpen.FetchedAt.After(asOf) {
			issues = append(issues, "LATEST_OPEN_TRADE_DAY_AFTER_AS_OF")
		}

		previousOpen, err = findPreviousKnownOpenDay(ctx, repo, exchange, date, asOf)
		if err != nil {
			issues = append(issues, "PREVIOUS_TRADING_CALENDAR_UNKNOWN")
		}
	} else {
		previousOpen, err = findPreviousKnownOpenDay(ctx, repo, exchange, date, asOf)
		if err != nil {
			issues = append(issues, "LATEST_OPEN_TRADE_DAY_UNKNOWN")
			return expected, "", issues
		}
		stateDate = previousOpen.CalDate
	}

	if previousOpen.CalDate != "" {
		previousDate := asOf.AddDate(0, 0, -1).Format("2006-01-02")
		indexedPreviousOpen, indexedErr := repo.GetLatestOpenTradeDay(ctx, exchange, previousDate)
		if indexedErr != nil || indexedPreviousOpen.CalDate != previousOpen.CalDate {
			issues = append(issues, "TRADING_CALENDAR_INDEX_MISMATCH")
		} else if indexedPreviousOpen.FetchedAt.IsZero() || indexedPreviousOpen.FetchedAt.After(asOf) {
			issues = append(issues, "TRADING_CALENDAR_INDEX_AFTER_AS_OF")
		}
	}

	if !day.IsOpen {
		if previousOpen.CalDate == "" {
			issues = append(issues, "LATEST_OPEN_TRADE_DAY_UNKNOWN")
			return expected, stateDate, unique(issues)
		}
		fallback, parseErr := closeTime(previousOpen.CalDate)
		if parseErr != nil {
			issues = append(issues, "LATEST_OPEN_TRADE_DAY_INVALID")
			return expected, stateDate, unique(issues)
		}
		for _, frequency := range []marketdata.Frequency{marketdata.FrequencyD1, marketdata.FrequencyM60, marketdata.FrequencyM30} {
			expected[frequency] = fallback
		}
		return expected, stateDate, unique(issues)
	}

	priorClose := time.Time{}
	if previousOpen.CalDate != "" {
		priorClose, err = closeTime(previousOpen.CalDate)
		if err != nil {
			issues = append(issues, "PREVIOUS_TRADE_DAY_INVALID")
		}
	}

	dailyClose, _ := closeTime(date)
	if !asOf.Before(dailyClose.Add(time.Minute)) {
		expected[marketdata.FrequencyD1] = dailyClose
	} else {
		expected[marketdata.FrequencyD1] = priorClose
	}
	for _, frequency := range []marketdata.Frequency{marketdata.FrequencyM60, marketdata.FrequencyM30} {
		expected[frequency] = latestIntradayClose(frequency, date, asOf)
		if expected[frequency].IsZero() {
			expected[frequency] = priorClose
		}
	}
	return expected, stateDate, unique(issues)
}

// findPreviousKnownOpenDay 逐日核实自然日历，遇到未知日期即失败，不跨过日历缺口猜测最近开市日。
func findPreviousKnownOpenDay(
	ctx context.Context,
	repo Repository,
	exchange calendar.Exchange,
	beforeDate string,
	asOf time.Time,
) (calendar.TradeDay, error) {
	day, err := time.ParseInLocation("2006-01-02", beforeDate, marketdata.ShanghaiLocation())
	if err != nil {
		return calendar.TradeDay{}, fmt.Errorf("基准日期无效: %w", err)
	}
	const maxCalendarLookbackDays = 31
	for offset := 1; offset <= maxCalendarLookbackDays; offset++ {
		date := day.AddDate(0, 0, -offset).Format("2006-01-02")
		record, err := repo.GetTradeDay(ctx, exchange, date)
		if err != nil {
			return calendar.TradeDay{}, fmt.Errorf("交易日历缺少 %s: %w", date, err)
		}
		if record.FetchedAt.IsZero() || record.FetchedAt.After(asOf) {
			return calendar.TradeDay{}, fmt.Errorf("交易日历 %s 的记录在 as_of 时点尚不可用", date)
		}
		if record.IsOpen {
			return record, nil
		}
	}
	return calendar.TradeDay{}, fmt.Errorf("向前 %d 个自然日内没有已知开市日", maxCalendarLookbackDays)
}

func closeTime(date string) (time.Time, error) {
	day, err := time.ParseInLocation("2006-01-02", date, marketdata.ShanghaiLocation())
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(day.Year(), day.Month(), day.Day(), 15, 0, 0, 0, marketdata.ShanghaiLocation()), nil
}

func latestIntradayClose(frequency marketdata.Frequency, date string, asOf time.Time) time.Time {
	slots := []string{}
	switch frequency {
	case marketdata.FrequencyM30:
		slots = []string{"10:00", "10:30", "11:00", "11:30", "13:30", "14:00", "14:30", "15:00"}
	case marketdata.FrequencyM60:
		slots = []string{"10:30", "11:30", "14:00", "15:00"}
	default:
		return time.Time{}
	}
	var latest time.Time
	for _, slot := range slots {
		candidate, err := time.ParseInLocation("2006-01-02 15:04", date+" "+slot, marketdata.ShanghaiLocation())
		if err == nil && !asOf.Before(candidate.Add(time.Minute)) {
			latest = candidate
		}
	}
	return latest
}
