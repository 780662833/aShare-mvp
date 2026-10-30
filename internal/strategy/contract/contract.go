// Package contract 定义两套策略共用的输入、输出和确定性评估接口。
package contract

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/market/universe"
)

type DecisionStatus string

const (
	StatusWatching          DecisionStatus = "WATCHING"
	StatusSetupReady        DecisionStatus = "SETUP_READY"
	StatusConfirmed         DecisionStatus = "CONFIRMED"
	StatusWaitEntry         DecisionStatus = "WAIT_ENTRY"
	StatusBuyObservation    DecisionStatus = "BUY_OBSERVATION"
	StatusHoldObservation   DecisionStatus = "HOLD_OBSERVATION"
	StatusReduceObservation DecisionStatus = "REDUCE_OBSERVATION"
	StatusSellObservation   DecisionStatus = "SELL_OBSERVATION"
	StatusInvalidated       DecisionStatus = "INVALIDATED"
	StatusExpired           DecisionStatus = "EXPIRED"
	StatusDataIncomplete    DecisionStatus = "DATA_INCOMPLETE"
	StatusAnalysisError     DecisionStatus = "ANALYSIS_ERROR"
)

type Grade string

const (
	GradeHigh   Grade = "HIGH"
	GradeMedium Grade = "MEDIUM"
	GradeLow    Grade = "LOW"
	GradeUnknown Grade = "UNKNOWN"
)

type MarketStatus string

const (
	MarketFavorable   MarketStatus = "FAVORABLE"
	MarketNeutral     MarketStatus = "NEUTRAL"
	MarketUnfavorable MarketStatus = "UNFAVORABLE"
	MarketUnknown     MarketStatus = "UNKNOWN"
)

type Evidence struct {
	Code        string    `json:"code"`
	Description string    `json:"description"`
	Value       string    `json:"value,omitempty"`
	Frequency   string    `json:"frequency,omitempty"`
	ObservedAt  time.Time `json:"observed_at,omitempty"`
	Source      string    `json:"source,omitempty"`
	Confidence  Grade     `json:"confidence,omitempty"`
}

type PriceLevel struct {
	Code       string    `json:"code"`
	Name       string    `json:"name"`
	Price      float64   `json:"price"`
	Frequency  string    `json:"frequency"`
	ObservedAt time.Time `json:"observed_at"`
	Source     string    `json:"source"`
}

type ProfileReference struct {
	Frequency       string `json:"frequency"`
	Profile         string `json:"profile"`
	ProfileVersion  string `json:"profile_version"`
	CZSCRevision    string `json:"czsc_revision"`
}

// BISummary 是 CZSC 最近已完成笔的最小策略 DTO。
type BISummary struct {
	StartTime string  `json:"start_time"`
	EndTime   string  `json:"end_time"`
	Direction string  `json:"direction"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Power     float64 `json:"power"`
	Length    int     `json:"length"`
}

// FrequencyAnalysis 是单一周期的 CZSC 快照。
// Signals 保留 CZSC 原始 key/value；策略层只解析自己白名单中的 key。
type FrequencyAnalysis struct {
	AsOf                 time.Time          `json:"as_of"`
	InputBarCount        int                `json:"input_bar_count"`
	SignalProfile        string             `json:"signal_profile"`
	SignalProfileVersion string             `json:"signal_profile_version"`
	CZSCSourceRevision   string             `json:"czsc_source_revision"`
	SignalDataStatus     string             `json:"signal_data_status"`
	Signals              map[string]string  `json:"signals"`
	BICount              int                `json:"bi_count"`
	RecentBI             []BISummary        `json:"recent_bi"`
	// SignalAgeBars is supplied by an upstream lifecycle tracker.
	// nil means age is unknown; strategies must not invent an age.
	SignalAgeBars        *int               `json:"signal_age_bars,omitempty"`
}

type MarketContext struct {
	AsOf        time.Time    `json:"as_of"`
	Status      MarketStatus `json:"status"`
	DataVersion string       `json:"data_version,omitempty"`
	RiskWarnings []Evidence  `json:"risk_warnings,omitempty"`
}

type StrategyInput struct {
	Security       universe.StockBasic                   `json:"security"`
	DailyState     universe.SecurityDailyState           `json:"daily_state"`
	AsOf           time.Time                             `json:"as_of"`
	Bars           map[marketdata.Frequency][]marketdata.Bar `json:"bars"`
	Technical      map[marketdata.Frequency]FrequencyAnalysis `json:"technical"`
	MarketContext  MarketContext                         `json:"market_context"`
	DataVersion    string                                `json:"data_version"`
	// PreviousDecision is optional lifecycle context. It is read-only and must
	// belong to the same symbol, strategy, implementation version and config.
	PreviousDecision *StrategyDecision                   `json:"previous_decision,omitempty"`
}

type StrategyDecision struct {
	DecisionID      string             `json:"decision_id"`
	TsCode          string             `json:"ts_code"`
	SecurityName    string             `json:"security_name"`
	StrategyID      string             `json:"strategy_id"`
	StrategyVersion string             `json:"strategy_version"`
	ConfigVersion   string             `json:"config_version"`
	AsOf            time.Time          `json:"as_of"`
	ConfirmedAt     *time.Time         `json:"confirmed_at,omitempty"`
	Status          DecisionStatus     `json:"status"`
	InternalGrade   Grade              `json:"internal_grade"`
	MarketStatus    MarketStatus       `json:"market_status"`
	EntryConditions []Evidence         `json:"entry_conditions"`
	MissingConditions []Evidence       `json:"missing_conditions"`
	Invalidation    []Evidence         `json:"invalidation"`
	RiskWarnings    []Evidence         `json:"risk_warnings"`
	ReferenceLevels []PriceLevel       `json:"reference_levels"`
	ReasonCodes     []string           `json:"reason_codes"`
	DataVersion     string             `json:"data_version"`
	Profiles        []ProfileReference `json:"profiles"`
}

type Strategy interface {
	ID() string
	Version() string
	RequiredFrequencies() []marketdata.Frequency
	Evaluate(ctx context.Context, input StrategyInput) (StrategyDecision, error)
}

func NewDecision(input StrategyInput, strategyID, strategyVersion, configVersion string) StrategyDecision {
	d := StrategyDecision{
		TsCode:            input.Security.TsCode,
		SecurityName:      input.Security.Name,
		StrategyID:        strategyID,
		StrategyVersion:   strategyVersion,
		ConfigVersion:     configVersion,
		AsOf:              input.AsOf,
		Status:            StatusWatching,
		InternalGrade:     GradeUnknown,
		MarketStatus:      normalizedMarketStatus(input.MarketContext.Status),
		EntryConditions:   make([]Evidence, 0),
		MissingConditions: make([]Evidence, 0),
		Invalidation:      make([]Evidence, 0),
		RiskWarnings:     make([]Evidence, 0),
		ReferenceLevels:  make([]PriceLevel, 0),
		ReasonCodes:      make([]string, 0),
		Profiles:         make([]ProfileReference, 0),
		DataVersion:      input.DataVersion,
	}
	d.DecisionID = DeterministicDecisionID(d.TsCode, strategyID, strategyVersion, configVersion, input.AsOf, input.DataVersion)
	for _, warning := range input.MarketContext.RiskWarnings {
		d.RiskWarnings = append(d.RiskWarnings, warning)
	}
	return d
}

func DeterministicDecisionID(tsCode, strategyID, strategyVersion, configVersion string, asOf time.Time, dataVersion string) string {
	material := strings.Join([]string{
		strings.ToUpper(strings.TrimSpace(tsCode)),
		strings.TrimSpace(strategyID),
		strings.TrimSpace(strategyVersion),
		strings.TrimSpace(configVersion),
		asOf.Format(time.RFC3339Nano),
		strings.TrimSpace(dataVersion),
	}, "|")
	sum := sha256.Sum256([]byte(material))
	return "decision-" + hex.EncodeToString(sum[:12])
}

// ValidateInput returns stable reason codes for input failures. It never
// sorts, repairs, fills or otherwise mutates input market data.
func ValidateInput(input StrategyInput, required []marketdata.Frequency) []string {
	issues := make([]string, 0)
	if input.AsOf.IsZero() {
		issues = append(issues, "AS_OF_MISSING")
	}
	if strings.TrimSpace(input.DataVersion) == "" {
		issues = append(issues, "DATA_VERSION_MISSING")
	}
	if err := input.Security.Validate(); err != nil {
		issues = append(issues, "SECURITY_INVALID")
	}
	if input.Security.ListStatus != universe.ListingActive {
		issues = append(issues, "SECURITY_NOT_ACTIVE")
	}
	if err := input.DailyState.Validate(); err != nil {
		issues = append(issues, "SECURITY_DAILY_STATE_INVALID")
	} else {
		if input.DailyState.TsCode != input.Security.TsCode {
			issues = append(issues, "SECURITY_STATE_SYMBOL_MISMATCH")
		}
		switch input.DailyState.STType {
		case universe.STNone:
		case universe.STUnknown:
			issues = append(issues, "SECURITY_ST_STATUS_UNKNOWN")
		default:
			issues = append(issues, "SECURITY_ST_EXCLUDED")
		}
		switch input.DailyState.SuspendType {
		case universe.SuspendNone:
		case universe.SuspendUnknown:
			issues = append(issues, "SECURITY_SUSPEND_STATUS_UNKNOWN")
		default:
			issues = append(issues, "SECURITY_SUSPENDED")
		}
		if !input.AsOf.IsZero() && input.DailyState.FetchedAt.After(input.AsOf) {
			issues = append(issues, "SECURITY_STATE_AFTER_AS_OF")
		}
	}

	var firstSource string
	var firstAdjust marketdata.AdjustMode
	for i, frequency := range required {
		bars, ok := input.Bars[frequency]
		if !ok || len(bars) == 0 {
			issues = append(issues, fmt.Sprintf("BARS_%s_MISSING", frequency))
			continue
		}
		if err := marketdata.ValidateBatch(bars); err != nil {
			issues = append(issues, fmt.Sprintf("BARS_%s_INVALID", frequency))
			continue
		}
		if bars[0].Frequency != frequency {
			issues = append(issues, fmt.Sprintf("BARS_%s_FREQUENCY_MISMATCH", frequency))
		}
		if bars[0].TsCode != input.Security.TsCode {
			issues = append(issues, fmt.Sprintf("BARS_%s_SYMBOL_MISMATCH", frequency))
		}
		for _, bar := range bars {
			if !bar.IsComplete {
				issues = append(issues, fmt.Sprintf("BARS_%s_FORMING_BAR", frequency))
				break
			}
			if !input.AsOf.IsZero() && bar.BarTime.After(input.AsOf) {
				issues = append(issues, fmt.Sprintf("BARS_%s_AFTER_AS_OF", frequency))
				break
			}
		}
		if bars[0].AdjustMode == marketdata.AdjustUnknown {
			issues = append(issues, fmt.Sprintf("BARS_%s_ADJUSTMENT_UNKNOWN", frequency))
		}
		if i == 0 {
			firstSource = bars[0].Source
			firstAdjust = bars[0].AdjustMode
		} else {
			if bars[0].Source != firstSource {
				issues = append(issues, "CROSS_FREQUENCY_SOURCE_MISMATCH")
			}
			if bars[0].AdjustMode != firstAdjust {
				issues = append(issues, "CROSS_FREQUENCY_ADJUSTMENT_MISMATCH")
			}
		}
	}
	if !input.AsOf.IsZero() && !input.MarketContext.AsOf.IsZero() && input.MarketContext.AsOf.After(input.AsOf) {
		issues = append(issues, "MARKET_CONTEXT_AFTER_AS_OF")
	}
	return uniqueStrings(issues)
}

func ValidateTechnicalSnapshot(
	input StrategyInput,
	frequency marketdata.Frequency,
	expectedProfile string,
	expectedProfileVersion string,
	expectedRevision string,
) []string {
	bars := input.Bars[frequency]
	snapshot, ok := input.Technical[frequency]
	if !ok {
		return []string{fmt.Sprintf("TECHNICAL_%s_MISSING", frequency)}
	}
	issues := make([]string, 0)
	if len(bars) == 0 {
		return []string{fmt.Sprintf("TECHNICAL_%s_WITHOUT_BARS", frequency)}
	}
	if snapshot.SignalProfile != expectedProfile {
		issues = append(issues, fmt.Sprintf("TECHNICAL_%s_PROFILE_MISMATCH", frequency))
	}
	if snapshot.SignalProfileVersion != expectedProfileVersion {
		issues = append(issues, fmt.Sprintf("TECHNICAL_%s_PROFILE_VERSION_MISMATCH", frequency))
	}
	if snapshot.CZSCSourceRevision != expectedRevision {
		issues = append(issues, fmt.Sprintf("TECHNICAL_%s_CZSC_REVISION_MISMATCH", frequency))
	}
	if snapshot.SignalDataStatus != "READY" {
		issues = append(issues, fmt.Sprintf("TECHNICAL_%s_DATA_NOT_READY", frequency))
	}
	if snapshot.InputBarCount != len(bars) {
		issues = append(issues, fmt.Sprintf("TECHNICAL_%s_BAR_COUNT_MISMATCH", frequency))
	}
	if !snapshot.AsOf.Equal(bars[len(bars)-1].BarTime) {
		issues = append(issues, fmt.Sprintf("TECHNICAL_%s_AS_OF_MISMATCH", frequency))
	}
	if len(snapshot.Signals) == 0 {
		issues = append(issues, fmt.Sprintf("TECHNICAL_%s_SIGNALS_MISSING", frequency))
	}
	return uniqueStrings(issues)
}

func AppendReason(decision *StrategyDecision, code string) {
	if decision == nil || strings.TrimSpace(code) == "" {
		return
	}
	for _, existing := range decision.ReasonCodes {
		if existing == code {
			return
		}
	}
	decision.ReasonCodes = append(decision.ReasonCodes, code)
}

func AppendProfile(decision *StrategyDecision, frequency marketdata.Frequency, snapshot FrequencyAnalysis) {
	if decision == nil {
		return
	}
	for _, existing := range decision.Profiles {
		if existing.Frequency == string(frequency) {
			return
		}
	}
	decision.Profiles = append(decision.Profiles, ProfileReference{
		Frequency:      string(frequency),
		Profile:        snapshot.SignalProfile,
		ProfileVersion: snapshot.SignalProfileVersion,
		CZSCRevision:   snapshot.CZSCSourceRevision,
	})
}

func ActivePreviousDecision(previous *StrategyDecision, current StrategyDecision) bool {
	if previous == nil {
		return false
	}
	if previous.TsCode != current.TsCode ||
		previous.StrategyID != current.StrategyID ||
		previous.StrategyVersion != current.StrategyVersion ||
		previous.ConfigVersion != current.ConfigVersion {
		return false
	}
	switch previous.Status {
	case StatusConfirmed, StatusWaitEntry, StatusBuyObservation, StatusReduceObservation:
		return true
	default:
		return false
	}
}

func normalizedMarketStatus(status MarketStatus) MarketStatus {
	switch status {
	case MarketFavorable, MarketNeutral, MarketUnfavorable:
		return status
	default:
		return MarketUnknown
	}
}

func uniqueStrings(values []string) []string {
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
