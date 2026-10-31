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
	ID                  string
	AsOf                time.Time
	StartedAt           time.Time
	CompletedAt         time.Time
	Status              RunStatus
	TotalCount          int
	SuccessCount        int
	DataIncompleteCount int
	AnalysisErrorCount  int
	FailedCount         int
	ExcludedCount       int
	ErrorMessage        string
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
	ScanID       string
	TsCode       string
	SecurityName string
	Exchange     universe.Exchange
	TradeDate    string
	ReasonCodes  []string
}

type Item struct {
	ScanID       string
	TsCode       string
	SecurityName string
	Exchange     universe.Exchange
	ListDate     string
	DelistDate   string
	Status       ItemStatus
	DataVersion  string
	DecisionIDs  []string
	StrategyResults []StrategyResult
	ReasonCodes  []string
	ErrorMessage string
	StartedAt    time.Time
	CompletedAt  time.Time
}
