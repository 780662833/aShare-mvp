// Package chan3buy 实现独立的 60 分钟缠论三买观察策略。
package chan3buy

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
	StrategyID      = "chan3buy"
	StrategyVersion = "1.0.0"

	m60Profile        = czscclient.SignalProfileChan3BuyM60
	m60ProfileVersion = "chan3buy-m60-v1"
	m30Profile        = czscclient.SignalProfileChan3BuyM30
	m30ProfileVersion = "chan3buy-m30-timing-v1"

	mainSignalKey = "60分钟_D1_三买辅助V230228"
	helper318Key  = "60分钟_D1#SMA#34_BS3辅助V230318"
	helper319Key  = "60分钟_D1#SMA#34_BS3辅助V230319"
	m30TimingKey  = "30分钟_D1_表里关系V230102"
)

// Config 的数值必须由调用方按配置版本显式提供；本策略不暗设追高阈值。
type Config struct {
	Version                    string
	MaxEntryDistanceAboveDefensePct float64
	DefenseBreakConfirmBars    int
	MaxSignalAgeBars            int
	DailyFastMAPeriod           int
	DailySlowMAPeriod           int
	AllowNeutralMarket          bool
	AllowUnknownMarket          bool
}

type Strategy struct {
	config Config
}

func New(config Config) (*Strategy, error) {
	if strings.TrimSpace(config.Version) == "" {
		return nil, fmt.Errorf("三买策略 Config.Version 不能为空")
	}
	if !finitePositive(config.MaxEntryDistanceAboveDefensePct) {
		return nil, fmt.Errorf("MaxEntryDistanceAboveDefensePct 必须是有限正数")
	}
	if config.DefenseBreakConfirmBars < 1 {
		return nil, fmt.Errorf("DefenseBreakConfirmBars 必须至少为 1")
	}
	if config.MaxSignalAgeBars < 1 {
		return nil, fmt.Errorf("MaxSignalAgeBars 必须至少为 1")
	}
	if config.DailyFastMAPeriod <= 0 || config.DailySlowMAPeriod <= config.DailyFastMAPeriod {
		return nil, fmt.Errorf("日线均线周期配置无效")
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

	m60 := input.Technical[marketdata.FrequencyM60]
	m30 := input.Technical[marketdata.FrequencyM30]
	m60Issues := contract.ValidateTechnicalSnapshot(
		input, marketdata.FrequencyM60, m60Profile, m60ProfileVersion, czscclient.CZSCSourceRevision,
	)
	m30Issues := contract.ValidateTechnicalSnapshot(
		input, marketdata.FrequencyM30, m30Profile, m30ProfileVersion, czscclient.CZSCSourceRevision,
	)
	if len(m60Issues) > 0 || len(m30Issues) > 0 {
		return dataIncomplete(decision, append(m60Issues, m30Issues...)), nil
	}
	contract.AppendProfile(&decision, marketdata.FrequencyM60, m60)
	contract.AppendProfile(&decision, marketdata.FrequencyM30, m30)
	for _, key := range []string{mainSignalKey, helper318Key, helper319Key} {
		if _, ok := m60.Signals[key]; !ok {
			return dataIncomplete(decision, []string{"TECHNICAL_M60_REQUIRED_SIGNAL_MISSING"}), nil
		}
	}
	if _, ok := m30.Signals[m30TimingKey]; !ok {
		return dataIncomplete(decision, []string{"TECHNICAL_M30_REQUIRED_SIGNAL_MISSING"}), nil
	}

	dailyTrend, err := feature.ClassifyDailyTrend(
		input.Bars[marketdata.FrequencyD1],
		s.config.DailyFastMAPeriod,
		s.config.DailySlowMAPeriod,
	)
	if err != nil {
		return dataIncomplete(decision, []string{"DAILY_TREND_DATA_INSUFFICIENT"}), nil
	}
	decision.EntryConditions = append(decision.EntryConditions, contract.Evidence{
		Code: "DAILY_TREND_CONTEXT",
		Description: "日线趋势环境由可复算的简单均线特征分类",
		Value: fmt.Sprintf("%s; MA%d=%.4f; MA%d=%.4f",
			dailyTrend.State, dailyTrend.FastPeriod, dailyTrend.FastMA,
			dailyTrend.SlowPeriod, dailyTrend.SlowMA),
		Frequency: string(marketdata.FrequencyD1),
		ObservedAt: input.Bars[marketdata.FrequencyD1][len(input.Bars[marketdata.FrequencyD1])-1].BarTime,
		Source: "Go feature.ClassifyDailyTrend",
		Confidence: contract.GradeMedium,
	})

	mainSignal := signalParts(m60.Signals[mainSignalKey])
	if len(mainSignal) == 0 || mainSignal[0] != "三买" {
		if previous := contract.ActivePreviousDecision(input.PreviousDecision, decision); previous {
			if priorGuard, ok := findLevel(*input.PreviousDecision, "THIRD_BUY_DEFENSE"); ok {
				decision.ReferenceLevels = append(decision.ReferenceLevels, priorGuard)
				if consecutiveClosesBelow(input.Bars[marketdata.FrequencyM60], priorGuard.Price, s.config.DefenseBreakConfirmBars) {
					decision.Status = contract.StatusInvalidated
					contract.AppendReason(&decision, "THIRD_BUY_DEFENSE_BROKEN")
					decision.Invalidation = append(decision.Invalidation, contract.Evidence{
						Code: "THIRD_BUY_DEFENSE_BROKEN",
						Description: "连续已完成 60 分钟 K 线收盘跌破上一决策的三买防守位",
						Value: fmt.Sprintf("close < %.4f for %d bars", priorGuard.Price, s.config.DefenseBreakConfirmBars),
						Frequency: string(marketdata.FrequencyM60),
						ObservedAt: input.Bars[marketdata.FrequencyM60][len(input.Bars[marketdata.FrequencyM60])-1].BarTime,
						Source: "marketdata.Bar",
						Confidence: contract.GradeHigh,
					})
					return decision, nil
				}
				if expired(input.Bars[marketdata.FrequencyM60], input.PreviousDecision.ConfirmedAt, s.config.MaxSignalAgeBars) {
					decision.Status = contract.StatusExpired
					contract.AppendReason(&decision, "THIRD_BUY_SETUP_EXPIRED")
					return decision, nil
				}
				decision.Status = contract.StatusWaitEntry
				contract.AppendReason(&decision, "THIRD_BUY_MAIN_SIGNAL_NOT_CURRENT")
				decision.MissingConditions = append(decision.MissingConditions, contract.Evidence{
					Code: "THIRD_BUY_MAIN_SIGNAL_NOT_CURRENT",
					Description: "当前 60 分钟快照未继续确认主三买信号，保留上一条决策仅供观察",
				})
				return decision, nil
			}
		}
		contract.AppendReason(&decision, "THIRD_BUY_MAIN_SIGNAL_NOT_CONFIRMED")
		decision.MissingConditions = append(decision.MissingConditions, contract.Evidence{
			Code: "THIRD_BUY_MAIN_SIGNAL_NOT_CONFIRMED",
			Description: "60 分钟 CZSC 主信号 cxt_third_buy_V230228 尚未返回三买",
			Frequency: string(marketdata.FrequencyM60),
			ObservedAt: m60.AsOf,
			Source: mainSignalKey,
			Confidence: contract.GradeHigh,
		})
		return decision, nil
	}

	confirmedAt := m60.AsOf
	decision.ConfirmedAt = &confirmedAt
	decision.Status = contract.StatusConfirmed
	contract.AppendReason(&decision, "THIRD_BUY_MAIN_SIGNAL_CONFIRMED")
	decision.EntryConditions = append(decision.EntryConditions, contract.Evidence{
		Code: "THIRD_BUY_MAIN_SIGNAL_CONFIRMED",
		Description: "60 分钟 CZSC 主三买辅助信号已确认",
		Value: strings.Join(mainSignal, "_"),
		Frequency: string(marketdata.FrequencyM60),
		ObservedAt: m60.AsOf,
		Source: mainSignalKey,
		Confidence: contract.GradeHigh,
	})

	if m60.SignalAgeBars == nil {
		return wait(decision, "THIRD_BUY_SIGNAL_AGE_UNKNOWN", "缺少主三买信号持续 K 线年龄，无法确认信号仍处于有效观察窗口"), nil
	}
	if *m60.SignalAgeBars > s.config.MaxSignalAgeBars {
		decision.Status = contract.StatusExpired
		contract.AppendReason(&decision, "THIRD_BUY_SETUP_EXPIRED")
		decision.RiskWarnings = append(decision.RiskWarnings, contract.Evidence{
			Code: "THIRD_BUY_SETUP_EXPIRED",
			Description: "主三买信号超过配置允许的已完成 60 分钟 K 线数量",
			Value: strconv.Itoa(*m60.SignalAgeBars),
			Frequency: string(marketdata.FrequencyM60),
			ObservedAt: m60.AsOf,
			Source: "upstream signal lifecycle",
			Confidence: contract.GradeHigh,
		})
		return decision, nil
	}

	parts318 := signalParts(m60.Signals[helper318Key])
	parts319 := signalParts(m60.Signals[helper319Key])
	helper318Buy := len(parts318) > 0 && parts318[0] == "三买"
	helper319Buy := len(parts319) > 0 && parts319[0] == "三买"
	helperConflict := (len(parts318) > 0 && parts318[0] == "三卖") ||
		(len(parts319) > 0 && parts319[0] == "三卖")

	if helperConflict {
		decision.Status = contract.StatusWaitEntry
		contract.AppendReason(&decision, "THIRD_BUY_AUXILIARY_CONFLICT")
		decision.RiskWarnings = append(decision.RiskWarnings, contract.Evidence{
			Code: "THIRD_BUY_AUXILIARY_CONFLICT",
			Description: "三买辅助结构出现三卖标签，必须保留冲突并人工复核",
			Value: fmt.Sprintf("V230318=%q; V230319=%q", strings.Join(parts318, "_"), strings.Join(parts319, "_")),
			Frequency: string(marketdata.FrequencyM60),
			ObservedAt: m60.AsOf,
			Source: "CZSC V230318/V230319",
			Confidence: contract.GradeHigh,
		})
		return decision, nil
	}

	if helper318Buy || helper319Buy {
		decision.EntryConditions = append(decision.EntryConditions, contract.Evidence{
			Code: "THIRD_BUY_AUXILIARY_CONFIRMED",
			Description: "至少一个独立的 CZSC 均线三买辅助信号同时确认",
			Value: fmt.Sprintf("V230318=%q; V230319=%q", strings.Join(parts318, "_"), strings.Join(parts319, "_")),
			Frequency: string(marketdata.FrequencyM60),
			ObservedAt: m60.AsOf,
			Source: "CZSC V230318/V230319",
			Confidence: contract.GradeMedium,
		})
	} else {
		decision.Status = contract.StatusWaitEntry
		contract.AppendReason(&decision, "THIRD_BUY_DEFENSE_NOT_TRACEABLE")
		decision.MissingConditions = append(decision.MissingConditions, contract.Evidence{
			Code: "THIRD_BUY_DEFENSE_NOT_TRACEABLE",
			Description: "主三买信号没有与最近五笔结构相匹配的 V230318/V230319 辅助确认，当前响应不足以可靠计算防守价",
			Frequency: string(marketdata.FrequencyM60),
			ObservedAt: m60.AsOf,
			Source: "CZSC recent_bi and V230318/V230319",
			Confidence: contract.GradeHigh,
		})
		return decision, nil
	}

	defense, err := defenseFromRecentBI(m60.RecentBI)
	if err != nil {
		decision.Status = contract.StatusWaitEntry
		contract.AppendReason(&decision, "THIRD_BUY_DEFENSE_NOT_TRACEABLE")
		decision.MissingConditions = append(decision.MissingConditions, contract.Evidence{
			Code: "THIRD_BUY_DEFENSE_NOT_TRACEABLE",
			Description: "辅助三买信号已命中，但最近五笔结构无法重建有效中枢上沿",
			Value: err.Error(),
			Frequency: string(marketdata.FrequencyM60),
			ObservedAt: m60.AsOf,
			Source: "CZSC recent_bi",
			Confidence: contract.GradeHigh,
		})
		return decision, nil
	}
	decision.ReferenceLevels = append(decision.ReferenceLevels, defense)

	if consecutiveClosesBelow(input.Bars[marketdata.FrequencyM60], defense.Price, s.config.DefenseBreakConfirmBars) {
		decision.Status = contract.StatusInvalidated
		contract.AppendReason(&decision, "THIRD_BUY_DEFENSE_BROKEN")
		decision.Invalidation = append(decision.Invalidation, contract.Evidence{
			Code: "THIRD_BUY_DEFENSE_BROKEN",
			Description: "连续已完成 60 分钟 K 线收盘跌破可追溯的三买中枢上沿防守位",
			Value: fmt.Sprintf("close < %.4f for %d bars", defense.Price, s.config.DefenseBreakConfirmBars),
			Frequency: string(marketdata.FrequencyM60),
			ObservedAt: input.Bars[marketdata.FrequencyM60][len(input.Bars[marketdata.FrequencyM60])-1].BarTime,
			Source: defense.Source,
			Confidence: contract.GradeHigh,
		})
		return decision, nil
	}

	if dailyTrend.State == feature.TrendUnfavorable {
		return wait(decision, "DAILY_TREND_UNFAVORABLE", "日线环境明确不利，三买结构保留但不升级为买入观察"), nil
	}
	if decision.MarketStatus == contract.MarketUnfavorable {
		return wait(decision, "MARKET_ENVIRONMENT_UNFAVORABLE", "市场环境明确不利，三买结构保留但不升级为买入观察"), nil
	}
	if decision.MarketStatus == contract.MarketUnknown && !s.config.AllowUnknownMarket {
		return wait(decision, "MARKET_ENVIRONMENT_UNKNOWN", "市场环境未知，当前配置不允许在缺少市场证据时升级"), nil
	}
	if decision.MarketStatus == contract.MarketNeutral && !s.config.AllowNeutralMarket {
		return wait(decision, "MARKET_ENVIRONMENT_NEUTRAL", "市场环境中性，当前配置要求等待更明确的市场条件"), nil
	}

	m30Parts := signalParts(m30.Signals[m30TimingKey])
	if !isShortCycleTurnUp(m30Parts) {
		return wait(decision, "M30_TURN_UP_NOT_CONFIRMED", "30 分钟尚未出现预定义的底分确认或向上笔延续证据"), nil
	}
	decision.EntryConditions = append(decision.EntryConditions, contract.Evidence{
		Code: "M30_TURN_UP_CONFIRMED",
		Description: "30 分钟笔状态信号满足底分确认或向上延续条件",
		Value: strings.Join(m30Parts, "_"),
		Frequency: string(marketdata.FrequencyM30),
		ObservedAt: m30.AsOf,
		Source: m30TimingKey,
		Confidence: contract.GradeMedium,
	})

	currentPrice := input.Bars[marketdata.FrequencyM30][len(input.Bars[marketdata.FrequencyM30])-1].Close
	if currentPrice < defense.Price {
		return wait(decision, "M30_PRICE_BELOW_DEFENSE", "30 分钟最新收盘仍低于三买防守位"), nil
	}
	distancePct, err := feature.PercentAbove(currentPrice, defense.Price)
	if err != nil {
		return dataIncomplete(decision, []string{"THIRD_BUY_ENTRY_PRICE_INVALID"}), nil
	}
	if distancePct > s.config.MaxEntryDistanceAboveDefensePct {
		decision.Status = contract.StatusWaitEntry
		contract.AppendReason(&decision, "ENTRY_PRICE_TOO_FAR_ABOVE_DEFENSE")
		decision.RiskWarnings = append(decision.RiskWarnings, contract.Evidence{
			Code: "ENTRY_PRICE_TOO_FAR_ABOVE_DEFENSE",
			Description: "当前价相对结构防守位偏离超过配置上限，不追高",
			Value: fmt.Sprintf("distance=%.4f%%; max=%.4f%%", distancePct, s.config.MaxEntryDistanceAboveDefensePct),
			Frequency: string(marketdata.FrequencyM30),
			ObservedAt: m30.AsOf,
			Source: "Go feature.PercentAbove",
			Confidence: contract.GradeHigh,
		})
		return decision, nil
	}

	decision.Status = contract.StatusBuyObservation
	contract.AppendReason(&decision, "THIRD_BUY_ENTRY_CONDITIONS_MET")
	if helper318Buy && helper319Buy {
		decision.InternalGrade = contract.GradeHigh
	} else {
		decision.InternalGrade = contract.GradeMedium
	}
	decision.EntryConditions = append(decision.EntryConditions, contract.Evidence{
		Code: "ENTRY_PRICE_WITHIN_CONFIGURED_ZONE",
		Description: "30 分钟最新收盘价位于结构防守位上方且未超过配置的追高距离",
		Value: fmt.Sprintf("price=%.4f; defense=%.4f; distance=%.4f%%", currentPrice, defense.Price, distancePct),
		Frequency: string(marketdata.FrequencyM30),
		ObservedAt: m30.AsOf,
		Source: "marketdata.Bar and Go feature.PercentAbove",
		Confidence: contract.GradeHigh,
	})
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

func defenseFromRecentBI(bis []contract.BISummary) (contract.PriceLevel, error) {
	if len(bis) < 5 {
		return contract.PriceLevel{}, fmt.Errorf("最近笔数不足五笔")
	}
	recent := bis[len(bis)-5:]
	b1 := recent[0]
	b3 := recent[2]
	if !finitePositive(b1.High) || !finitePositive(b1.Low) ||
		!finitePositive(b3.High) || !finitePositive(b3.Low) {
		return contract.PriceLevel{}, fmt.Errorf("构造中枢的笔高低点无效")
	}
	zoneBottom := math.Max(b1.Low, b3.Low)
	zoneTop := math.Min(b1.High, b3.High)
	if zoneTop < zoneBottom || zoneTop <= 0 {
		return contract.PriceLevel{}, fmt.Errorf("第一笔与第三笔没有有效重叠区间")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, b3.EndTime)
	if err != nil {
		return contract.PriceLevel{}, fmt.Errorf("第三笔结束时间无效: %w", err)
	}
	return contract.PriceLevel{
		Code: "THIRD_BUY_DEFENSE",
		Name: "三买辅助结构对应的中枢上沿",
		Price: zoneTop,
		Frequency: string(marketdata.FrequencyM60),
		ObservedAt: observedAt,
		Source: "CZSC V230318/V230319: min(BI[1].high, BI[3].high)",
	}, nil
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
