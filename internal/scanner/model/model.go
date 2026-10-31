// Package model contains persistence-neutral scan run and item models.
package model

import (
	"time"

	"github.com/780662833/aShare-mvp/internal/market/universe"
)

type RunStatus string

const (
	RunRunning   RunStatus = "RUNNING"
	RunCompleted RunStatus = "COMPLETED"
	RunPartial   RunStatus = "PARTIAL_SUCCESS"
	RunFailed    RunStatus = "FAILED"
	RunCancelled RunStatus = "CANCELLED"
)

type ItemStatus string

const (
	ItemPending        ItemStatus = "PENDING"
	ItemSuccess        ItemStatus = "SUCCESS"
	ItemDataIncomplete ItemStatus = "DATA_INCOMPLETE"
	ItemAnalysisError  ItemStatus = "ANALYSIS_ERROR"
	ItemFailed         ItemStatus = "FAILED"
)

type Run struct {
	ID                  string    `json:"scan_id"`
	AsOf                time.Time `json:"as_of"`
	StartedAt           time.Time `json:"started_at"`
	CompletedAt         time.Time `json:"completed_at,omitempty"`
	Status              RunStatus `json:"status"`
	TotalCount          int       `json:"total_count"`
	SuccessCount        int       `json:"success_count"`
	DataIncompleteCount int       `json:"data_incomplete_count"`
	AnalysisErrorCount  int       `json:"analysis_error_count"`
	FailedCount         int       `json:"failed_count"`
	ExcludedCount       int       `json:"excluded_count"`
	ErrorMessage        string    `json:"error_message,omitempty"`
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

type Exclusion struct {
	ScanID       string            `json:"scan_id"`
	TsCode       string            `json:"ts_code"`
	SecurityName string            `json:"security_name"`
	Exchange     universe.Exchange `json:"exchange"`
	TradeDate    string            `json:"trade_date"`
	ReasonCodes  []string          `json:"reason_codes"`
}

type Item struct {
	ScanID          string            `json:"scan_id"`
	TsCode          string            `json:"ts_code"`
	SecurityName    string            `json:"security_name"`
	Exchange        universe.Exchange `json:"exchange"`
	ListDate        string            `json:"list_date,omitempty"`
	DelistDate      string            `json:"delist_date,omitempty"`
	Status          ItemStatus        `json:"status"`
	DataVersion     string            `json:"data_version,omitempty"`
	DecisionIDs     []string          `json:"decision_ids"`
	StrategyResults []StrategyResult  `json:"strategy_results"`
	ReasonCodes     []string          `json:"reason_codes"`
	ErrorMessage    string            `json:"error_message,omitempty"`
	StartedAt       time.Time         `json:"started_at,omitempty"`
	CompletedAt     time.Time         `json:"completed_at,omitempty"`
}
