// Package trendbreakout 实现独立的日线趋势突破观察策略。
package trendbreakout

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/780662833/aShare-mvp/internal/czscclient"
	"github.com/780662833/aShare-mvp/internal/feature"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
)

const (
	StrategyID      = "trend_breakout"
	StrategyVersion = "1.0.0"

	d1Profile        = czscclient.SignalProfileTrendD1
	d1ProfileVersion = "trend-breakout-d1-v1"
	m30Profile       = czscclient.SignalProfileChan3BuyM30
	m30ProfileVersion = "chan3buy-m30-timing-v1"

	volumeAveragePeriod = 20
	dailyFastMAPeriod   = 20
	dailySlowMAPeriod   = 60

	volumeSignalKey = "日线_D1VOL#SMA#20_分类V230214"
	ma20SignalKey   = "日线_D1SMA#20_分类V221101"
	ma60SignalKey   = "日线_D1SMA#60_分类V221101"
	m30TimingKey    = "30分钟_D1_表里关系V230102"
)

// Config 中没有在产品规范中冻结的阈值必须由调用方显式提供。
type Config struct {
	Version                       string
	LookbackDays                  int
	BreakoutBufferPct             float64
	MinVolumeRatio                float64
	MinAmount                     float64
	MinClosePosition              float64
	MaxChaseAboveBreakoutPct      float64
	MinM60ClosesAboveBreakout     int
	M60FailureConfirmBars         int
	MaxSignalAgeDays              int
	AllowNeutralMarket            bool
	AllowUnknownMarket            bool
}

type Strategy struct {
	config Config
}

func New(config Config) (*Strategy, error) {
	if strings.TrimSpace(config.Version) == "" {
		return nil, fmt.Errorf("趋势突破策略 Config.Version 不能为空")
	}
	switch config.LookbackDays {
	case 20, 30, 55:
	default:
		return nil, fmt.Errorf("LookbackDays 只支持 20、30、55")
	}
	if !finiteNonNegative(config.BreakoutBufferPct) {
		return nil, fmt.Errorf("BreakoutBufferPct 必须是有限非负数")
	}
	if !finitePositive(config.MinVolumeRatio) {
		return nil, fmt.Errorf("MinVolumeRatio 必须是有限正数")
	}
	if !finitePositive(config.MinAmount) {
		return nil, fmt.Errorf("MinAmount 必须是有限正数（人民币元）")
	}
	if config.MinClosePosition < 0 || config.MinClosePosition > 1 {
		return nil, fmt.Errorf("MinClosePosition 必须在 [0, 1] 范围内")
	}
	if !finitePositive(config.MaxChaseAboveBreakoutPct) {
		return nil, fmt.Errorf("MaxChaseAboveBreakoutPct 必须是有限正数")
	}
	if config.MinM60ClosesAboveBreakout < 1 || config.M60FailureConfirmBars < 1 {
		return nil, fmt.Errorf("M60 确认 K 线数量必须至少为 1")
	}
	if config.MaxSignalAgeDays < 1 {
		return nil, fmt.Errorf("MaxSignalAgeDays 必须至少为 1")
	}
	return &Strategy{config: config}, nil
}

func (s *Strategy) ID() string      { return StrategyID }
func (s *Strategy) Version() string { return StrategyVersion }

func (s *Strategy) RequiredFrequencies() []marketdata.Frequency {
	return []marketdata.Frequency{
		marketdata.FrequencyD1,
		marketdata.FrequencyM60,
		marketdata.FrequencyM30,
	}
}

func (s *Strategy) Evaluate(ctx context.Context, input contract.StrategyInput) (contract.StrategyDecision, error) {
	if err := ctx.Err(); err != nil {
		return contract.StrategyDecision{}, err
	}
	decision := contract.NewDecision(input, s.ID(), s.Version(), s.config.Version)
	issues := contract.ValidateInput(input, s.RequiredFrequencies())
	if len(issues) > 0 {
		return dataIncomplete(decision, issues), nil
	}

	d1 := input.Technical[marketdata.FrequencyD1]
	m30 := input.Technical[marketdata.FrequencyM30]
	d1Issues := contract.ValidateTechnicalSnapshot(
		input, marketdata.FrequencyD1, d1Profile, d1ProfileVersion, czscclient.CZSCSourceRevision,
	)
	m30Issues := contract.ValidateTechnicalSnapshot(
		input, marketdata.FrequencyM30, m30Profile, m30ProfileVersion, czscclient.CZSCSourceRevision,
	)
	if len(d1Issues) > 0 || len(m30Issues) > 0 {
		return dataIncomplete(decision, append(d1Issues, m30Issues...)), nil
	}
	contract.AppendProfile(&decision, marketdata.FrequencyD1, d1)
	contract.AppendProfile(&decision, marketdata.FrequencyM30, m30)
	breakoutSignalKey := fmt.Sprintf("日线_D1W%d_事件V240428", s.config.LookbackDays+1)
	for _, key := range []string{breakoutSignalKey, ma20SignalKey, ma60SignalKey, volumeSignalKey} {
		if _, ok := d1.Signals[key]; !ok {
			return dataIncomplete(decision, []string{"TECHNICAL_D1_REQUIRED_SIGNAL_MISSING"}), nil
		}
	}
	if _, ok := m30.Signals[m30TimingKey]; !ok {
		return dataIncomplete(decision, []string{"TECHNICAL_M30_REQUIRED_SIGNAL_MISSING"}), nil
	}

	dailyBars := input.Bars[marketdata.FrequencyD1]
	rangeHigh, err := feature.PreviousRangeHigh(dailyBars, s.config.LookbackDays)
	if err != nil {
		return dataIncomplete(decision, []string{"BREAKOUT_HISTORY_INSUFFICIENT"}), nil
	}
	breakoutThreshold := rangeHigh * (1 + s.config.BreakoutBufferPct/100)
	latestDaily := dailyBars[len(dailyBars)-1]
	breakoutParts := signalParts(d1.Signals[breakoutSignalKey])
	breakoutHit := len(breakoutParts) > 0 && breakoutParts[0] == "收盘新高" &&
		latestDaily.Close > breakoutThreshold

	if !breakoutHit {
		if previous := contract.ActivePreviousDecision(input.PreviousDecision, decision); previous {
			if previousLevel, ok := findLevel(*input.PreviousDecision, "BREAKOUT_LEVEL"); ok {
				decision.ReferenceLevels = append(decision.ReferenceLevels, previousLevel)
				if latestDaily.Close <= previousLevel.Price {
					decision.Status = contract.StatusInvalidated
					contract.AppendReason(&decision, "DAILY_CLOSE_RETURNED_INSIDE_RANGE")
					decision.Invalidation = append(decision.Invalidation, contract.Evidence{
						Code: "DAILY_CLOSE_RETURNED_INSIDE_RANGE",
						Description: "已完成日线收盘回到上一轮突破的原整理区间内",
						Value: fmt.Sprintf("close=%.4f; breakout_level=%.4f", latestDaily.Close, previousLevel.Price),
						Frequency: string(marketdata.FrequencyD1),
						ObservedAt: latestDaily.BarTime,
						Source: "marketdata.Bar",
						Confidence: contract.GradeHigh,
					})
					return decision, nil
				}
				if expired(dailyBars, input.PreviousDecision.ConfirmedAt, s.config.MaxSignalAgeDays) {
					decision.Status = contract.StatusExpired
					contract.AppendReason(&decision, "BREAKOUT_SETUP_EXPIRED")
					return decision, nil
				}
				decision.Status = contract.StatusWaitEntry
				contract.AppendReason(&decision, "DAILY_BREAKOUT_NOT_NEW_HIGH")
				decision.MissingConditions = append(decision.MissingConditions, contract.Evidence{
					Code: "DAILY_BREAKOUT_NOT_NEW_HIGH",
					Description: "当前日线没有产生新的收盘新高；上一轮突破仍在区间上方，继续等待但不重复确认",
					Frequency: string(marketdata.FrequencyD1),
					ObservedAt: latestDaily.BarTime,
					Source: breakoutSignalKey,
					Confidence: contract.GradeMedium,
				})
				return decision, nil
			}
		}
		contract.AppendReason(&decision, "DAILY_CLOSE_BREAKOUT_NOT_CONFIRMED")
		decision.MissingConditions = append(decision.MissingConditions, contract.Evidence{
			Code: "DAILY_CLOSE_BREAKOUT_NOT_CONFIRMED",
			Description: "日线收盘未同时满足 CZSC 收盘新高信号和配置的突破缓冲",
			Value: fmt.Sprintf("close=%.4f; threshold=%.4f; CZSC=%q", latestDaily.Close, breakoutThreshold, strings.Join(breakoutParts, "_")),
			Frequency: string(marketdata.FrequencyD1),
			ObservedAt: latestDaily.BarTime,
			Source: breakoutSignalKey,
			Confidence: contract.GradeHigh,
		})
		return decision, nil
	}

	confirmedAt := latestDaily.BarTime
	decision.ConfirmedAt = &confirmedAt
	decision.Status = contract.StatusConfirmed
	contract.AppendReason(&decision, "DAILY_CLOSE_BREAKOUT_CONFIRMED")
	decision.ReferenceLevels = append(decision.ReferenceLevels, contract.PriceLevel{
		Code: "BREAKOUT_LEVEL",
		Name: fmt.Sprintf("前 %d 日关键高点", s.config.LookbackDays),
		Price: rangeHigh,
		Frequency: string(marketdata.FrequencyD1),
		ObservedAt: latestDaily.BarTime,
		Source: fmt.Sprintf("Go feature.PreviousRangeHigh; prior %d completed daily bars", s.config.LookbackDays),
	})
	decision.EntryConditions = append(decision.EntryConditions, contract.Evidence{
		Code: "DAILY_CLOSE_BREAKOUT_CONFIRMED",
		Description: "日线 CZSC 收盘新高与 Go 前序区间高点计算均确认突破",
		Value: fmt.Sprintf("close=%.4f; range_high=%.4f; buffer=%.4f%%", latestDaily.Close, rangeHigh, s.config.BreakoutBufferPct),
		Frequency: string(marketdata.FrequencyD1),
		ObservedAt: latestDaily.BarTime,
		Source: breakoutSignalKey,
		Confidence: contract.GradeHigh,
	})

	if d1.SignalAgeBars == nil {
		return wait(decision, "BREAKOUT_SIGNAL_AGE_UNKNOWN", "缺少日线突破信号持续交易日年龄，无法确认信号仍处于有效观察窗口"), nil
	}
	if *d1.SignalAgeBars > s.config.MaxSignalAgeDays {
		decision.Status = contract.StatusExpired
		contract.AppendReason(&decision, "BREAKOUT_SETUP_EXPIRED")
		decision.RiskWarnings = append(decision.RiskWarnings, contract.Evidence{
			Code: "BREAKOUT_SETUP_EXPIRED",
			Description: "突破信号超过配置允许的已完成日线数量",
			Value: strconv.Itoa(*d1.SignalAgeBars),
			Frequency: string(marketdata.FrequencyD1),
			ObservedAt: latestDaily.BarTime,
			Source: "upstream signal lifecycle",
			Confidence: contract.GradeHigh,
		})
		return decision, nil
	}

	trend, err := feature.ClassifyDailyTrend(dailyBars, dailyFastMAPeriod, dailySlowMAPeriod)
	if err != nil {
		return dataIncomplete(decision, []string{"DAILY_TREND_DATA_INSUFFICIENT"}), nil
	}
	ma20 := signalParts(d1.Signals[ma20SignalKey])
	ma60 := signalParts(d1.Signals[ma60SignalKey])
	volumeSignal := signalParts(d1.Signals[volumeSignalKey])
	decision.EntryConditions = append(decision.EntryConditions, contract.Evidence{
		Code: "DAILY_TREND_CONTEXT",
		Description: "MA20/MA60 趋势上下文，同时保留 CZSC 分类信号",
		Value: fmt.Sprintf("trend=%s; MA20=%s; MA60=%s; SMA20=%.4f/%.4f",
			trend.State, strings.Join(ma20, "_"), strings.Join(ma60, "_"), trend.FastMA, trend.SlowMA),
		Frequency: string(marketdata.FrequencyD1),
		ObservedAt: latestDaily.BarTime,
		Source: "Go feature.ClassifyDailyTrend and CZSC tas_ma_base_V221101",
		Confidence: contract.GradeMedium,
	})
	if trend.State == feature.TrendUnfavorable {
		return wait(decision, "DAILY_TREND_UNFAVORABLE", "日线趋势环境明确不利，突破事实保留但不升级为买入观察"), nil
	}
	if decision.MarketStatus == contract.MarketUnfavorable {
		return wait(decision, "MARKET_ENVIRONMENT_UNFAVORABLE", "市场环境明确不利，突破事实保留但不升级为买入观察"), nil
	}
	if decision.MarketStatus == contract.MarketUnknown && !s.config.AllowUnknownMarket {
		return wait(decision, "MARKET_ENVIRONMENT_UNKNOWN", "市场环境未知，当前配置不允许在缺少市场证据时升级"), nil
	}
	if decision.MarketStatus == contract.MarketNeutral && !s.config.AllowNeutralMarket {
		return wait(decision, "MARKET_ENVIRONMENT_NEUTRAL", "市场环境中性，当前配置要求等待更明确的市场条件"), nil
	}

	volumeRatio, err := feature.VolumeRatio(dailyBars, volumeAveragePeriod)
	if err != nil {
		return dataIncomplete(decision, []string{"VOLUME_HISTORY_INSUFFICIENT"}), nil
	}
	closePosition, err := feature.ClosePosition(latestDaily)
	if err != nil {
		return dataIncomplete(decision, []string{"DAILY_CLOSE_POSITION_INVALID"}), nil
	}
	volumePass := volumeRatio >= s.config.MinVolumeRatio
	amountPass := latestDaily.Amount >= s.config.MinAmount
	closePositionPass := closePosition >= s.config.MinClosePosition
	volumeSignalPass := len(volumeSignal) >= 2 && volumeSignal[0] == "多头" && volumeSignal[1] == "向上"

	if !volumePass || !amountPass || !closePositionPass || !volumeSignalPass {
		decision.Status = contract.StatusWaitEntry
		contract.AppendReason(&decision, "BREAKOUT_QUALITY_NOT_CONFIRMED")
		decision.MissingConditions = append(decision.MissingConditions, contract.Evidence{
			Code: "BREAKOUT_QUALITY_NOT_CONFIRMED",
			Description: "突破已收盘确认，但量价质量未全部达到版本化配置",
			Value: fmt.Sprintf("volume_ratio=%.4f/%.4f; amount=%.2f/%.2f; close_position=%.4f/%.4f; CZSC_volume=%s",
				volumeRatio, s.config.MinVolumeRatio, latestDaily.Amount, s.config.MinAmount,
				closePosition, s.config.MinClosePosition, strings.Join(volumeSignal, "_")),
			Frequency: string(marketdata.FrequencyD1),
			ObservedAt: latestDaily.BarTime,
			Source: "Go feature metrics and CZSC vol_single_ma_V230214",
			Confidence: contract.GradeHigh,
		})
		return decision, nil
	}
	decision.EntryConditions = append(decision.EntryConditions,
		contract.Evidence{
			Code: "VOLUME_RATIO_CONFIRMED",
			Description: "当前日线成交量相对前 20 根已完成日线均量达到配置门槛",
			Value: fmt.Sprintf("ratio=%.4f; minimum=%.4f", volumeRatio, s.config.MinVolumeRatio),
			Frequency: string(marketdata.FrequencyD1),
			ObservedAt: latestDaily.BarTime,
			Source: "Go feature.VolumeRatio",
			Confidence: contract.GradeHigh,
		},
		contract.Evidence{
			Code: "TURNOVER_AND_CLOSE_POSITION_CONFIRMED",
			Description: "成交额和收盘位置均达到版本化配置门槛",
			Value: fmt.Sprintf("amount=%.2f; minimum_amount=%.2f; close_position=%.4f; minimum_position=%.4f",
				latestDaily.Amount, s.config.MinAmount, closePosition, s.config.MinClosePosition),
			Frequency: string(marketdata.FrequencyD1),
			ObservedAt: latestDaily.BarTime,
			Source: "marketdata.Bar and Go feature.ClosePosition",
			Confidence: contract.GradeHigh,
		},
	)

	m60Bars := barsAfter(input.Bars[marketdata.FrequencyM60], latestDaily.BarTime)
	if consecutiveClosesBelow(m60Bars, rangeHigh, s.config.M60FailureConfirmBars) {
		decision.Status = contract.StatusReduceObservation
		contract.AppendReason(&decision, "M60_BREAKOUT_FAILURE_RISK")
		decision.RiskWarnings = append(decision.RiskWarnings, contract.Evidence{
			Code: "M60_BREAKOUT_FAILURE_RISK",
			Description: "连续 60 分钟收盘跌回突破区间内部，提示突破失败风险；不代表系统持有真实仓位",
			Value: fmt.Sprintf("close < %.4f for %d bars", rangeHigh, s.config.M60FailureConfirmBars),
			Frequency: string(marketdata.FrequencyM60),
			ObservedAt: m60Bars[len(m60Bars)-1].BarTime,
			Source: "marketdata.Bar",
			Confidence: contract.GradeHigh,
		})
		return decision, nil
	}
	if !consecutiveClosesAbove(m60Bars, rangeHigh, s.config.MinM60ClosesAboveBreakout) {
		return wait(decision, "M60_BREAKOUT_HOLD_NOT_CONFIRMED", "60 分钟尚未连续满足站稳突破位的配置要求"), nil
	}
	decision.EntryConditions = append(decision.EntryConditions, contract.Evidence{
		Code: "M60_BREAKOUT_HOLD_CONFIRMED",
		Description: "最近连续已完成 60 分钟 K 线收盘保持在突破位上方",
		Value: fmt.Sprintf("level=%.4f; consecutive_bars=%d", rangeHigh, s.config.MinM60ClosesAboveBreakout),
		Frequency: string(marketdata.FrequencyM60),
		ObservedAt: m60Bars[len(m60Bars)-1].BarTime,
		Source: "marketdata.Bar",
		Confidence: contract.GradeMedium,
	})

	m30Bars := input.Bars[marketdata.FrequencyM30]
	if len(m60Bars) == 0 || len(m30Bars) == 0 ||
		!m30Bars[len(m30Bars)-1].BarTime.After(m60Bars[len(m60Bars)-1].BarTime) {
		return wait(decision, "M30_NOT_AFTER_M60_CONFIRMATION", "30 分钟择时 K 线必须晚于日线突破后的 60 分钟确认 K 线"), nil
	}
	m30Parts := signalParts(m30.Signals[m30TimingKey])
	if !isShortCycleTurnUp(m30Parts) {
		return wait(decision, "M30_ENTRY_TIMING_NOT_CONFIRMED", "30 分钟尚未出现预定义的底分确认或向上笔延续证据"), nil
	}
	currentPrice := m30Bars[len(m30Bars)-1].Close
	distancePct, err := feature.PercentAbove(currentPrice, rangeHigh)
	if err != nil {
		return dataIncomplete(decision, []string{"BREAKOUT_ENTRY_PRICE_INVALID"}), nil
	}
	if distancePct > s.config.MaxChaseAboveBreakoutPct {
		decision.Status = contract.StatusWaitEntry
		contract.AppendReason(&decision, "PRICE_TOO_FAR_ABOVE_BREAKOUT")
		decision.RiskWarnings = append(decision.RiskWarnings, contract.Evidence{
			Code: "PRICE_TOO_FAR_ABOVE_BREAKOUT",
			Description: "当前价相对突破位偏离超过配置上限，不追高",
			Value: fmt.Sprintf("distance=%.4f%%; max=%.4f%%", distancePct, s.config.MaxChaseAboveBreakoutPct),
			Frequency: string(marketdata.FrequencyM30),
			ObservedAt: m30Bars[len(m30Bars)-1].BarTime,
			Source: "Go feature.PercentAbove",
			Confidence: contract.GradeHigh,
		})
		return decision, nil
	}
	decision.EntryConditions = append(decision.EntryConditions, contract.Evidence{
		Code: "M30_ENTRY_TIMING_CONFIRMED",
		Description: "30 分钟笔状态信号满足回踩底分确认或向上延续条件",
		Value: strings.Join(m30Parts, "_"),
		Frequency: string(marketdata.FrequencyM30),
		ObservedAt: m30.AsOf,
		Source: m30TimingKey,
		Confidence: contract.GradeMedium,
	}, contract.Evidence{
		Code: "ENTRY_PRICE_WITHIN_CONFIGURED_ZONE",
		Description: "当前 30 分钟收盘价位于突破位上方且未超过配置的追高距离",
		Value: fmt.Sprintf("price=%.4f; breakout_level=%.4f; distance=%.4f%%", currentPrice, rangeHigh, distancePct),
		Frequency: string(marketdata.FrequencyM30),
		ObservedAt: m30Bars[len(m30Bars)-1].BarTime,
		Source: "marketdata.Bar and Go feature.PercentAbove",
		Confidence: contract.GradeHigh,
	})

	decision.Status = contract.StatusBuyObservation
	decision.InternalGrade = contract.GradeHigh
	contract.AppendReason(&decision, "TREND_BREAKOUT_ENTRY_CONDITIONS_MET")
	return decision, nil
}

func dataIncomplete(decision contract.StrategyDecision, issues []string) contract.StrategyDecision {
	decision.Status = contract.StatusDataIncomplete
	decision.InternalGrade = contract.GradeUnknown
	for _, issue := range issues {
		contract.AppendReason(&decision, issue)
		decision.MissingConditions = append(decision.MissingConditions, contract.Evidence{
			Code: issue,
			Description: "策略输入或技术分析快照不满足数据质量门",
			Confidence: contract.GradeHigh,
		})
	}
	return decision
}

func wait(decision contract.StrategyDecision, reason, description string) contract.StrategyDecision {
	decision.Status = contract.StatusWaitEntry
	contract.AppendReason(&decision, reason)
	decision.MissingConditions = append(decision.MissingConditions, contract.Evidence{
		Code: reason,
		Description: description,
		Confidence: contract.GradeMedium,
	})
	return decision
}

func signalParts(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return strings.Split(value, "_")
}

func isShortCycleTurnUp(parts []string) bool {
	if len(parts) < 2 {
		return false
	}
	return (parts[0] == "向下" && parts[1] == "底分") ||
		(parts[0] == "向上" && parts[1] == "延伸")
}

func barsAfter(bars []marketdata.Bar, after time.Time) []marketdata.Bar {
	result := make([]marketdata.Bar, 0, len(bars))
	for _, bar := range bars {
		if bar.BarTime.After(after) {
			result = append(result, bar)
		}
	}
	return result
}

func consecutiveClosesAbove(bars []marketdata.Bar, level float64, required int) bool {
	if required < 1 || len(bars) < required {
		return false
	}
	for _, bar := range bars[len(bars)-required:] {
		if bar.Close <= level {
			return false
		}
	}
	return true
}

func consecutiveClosesBelow(bars []marketdata.Bar, level float64, required int) bool {
	if required < 1 || len(bars) < required {
		return false
	}
	for _, bar := range bars[len(bars)-required:] {
		if bar.Close >= level {
			return false
		}
	}
	return true
}

func expired(bars []marketdata.Bar, confirmedAt *time.Time, maxAge int) bool {
	if confirmedAt == nil || maxAge < 1 {
		return false
	}
	age := 0
	for _, bar := range bars {
		if bar.BarTime.After(*confirmedAt) {
			age++
		}
	}
	return age > maxAge
}

func findLevel(decision contract.StrategyDecision, code string) (contract.PriceLevel, bool) {
	for _, level := range decision.ReferenceLevels {
		if level.Code == code {
			return level, true
		}
	}
	return contract.PriceLevel{}, false
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finiteNonNegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}
