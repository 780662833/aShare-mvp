// Package universe 定义 D1 证券基础资料与每日风险状态。
package universe

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type Exchange string

const (
	ExchangeSSE  Exchange = "SSE"
	ExchangeSZSE Exchange = "SZSE"
	ExchangeBSE  Exchange = "BSE"
)

type ListingStatus string

const (
	ListingActive  ListingStatus = "L"
	ListingDelisted ListingStatus = "D"
	ListingPaused  ListingStatus = "P"
	ListingUnknown ListingStatus = "UNKNOWN"
)

// StockBasic 字段以 go-stock 的 StockBasic 为基线。
// 这是本项目 stock_basic 表的领域模型，不导入 go-stock 整个 Wails 应用。
type StockBasic struct {
	TsCode     string        `json:"ts_code"`
	Symbol     string        `json:"symbol"`
	Name       string        `json:"name"`
	Area       string        `json:"area,omitempty"`
	Industry   string        `json:"industry,omitempty"`
	Fullname   string        `json:"fullname,omitempty"`
	Ename      string        `json:"enname,omitempty"`
	Cnspell    string        `json:"cnspell,omitempty"`
	Market     string        `json:"market,omitempty"`
	Exchange   Exchange      `json:"exchange"`
	CurrType   string        `json:"curr_type,omitempty"`
	ListStatus ListingStatus `json:"list_status"`
	ListDate   string        `json:"list_date,omitempty"`
	DelistDate string        `json:"delist_date,omitempty"`
	IsHS       string        `json:"is_hs,omitempty"`
}

func (s StockBasic) Validate() error {
	if strings.TrimSpace(s.TsCode) == "" || strings.TrimSpace(s.Symbol) == "" {
		return errors.New("ts_code 和 symbol 不能为空")
	}
	if strings.TrimSpace(s.Name) == "" {
		return errors.New("证券名称不能为空")
	}
	if !s.Exchange.Valid() {
		return fmt.Errorf("不支持的交易所: %q", s.Exchange)
	}
	wantSuffix := "." + s.Exchange.Code()
	if !strings.HasSuffix(strings.ToUpper(s.TsCode), wantSuffix) {
		return fmt.Errorf("ts_code %q 与交易所 %s 不匹配", s.TsCode, s.Exchange)
	}
	if strings.TrimSuffix(strings.ToUpper(s.TsCode), wantSuffix) != strings.ToUpper(s.Symbol) {
		return fmt.Errorf("ts_code %q 与 symbol %q 不匹配", s.TsCode, s.Symbol)
	}
	if s.ListStatus == "" {
		return errors.New("list_status 不能为空，未知状态应显式写 UNKNOWN")
	}
	if s.ListDate != "" {
		if _, err := time.Parse("20060102", s.ListDate); err != nil {
			return fmt.Errorf("list_date 格式必须为 YYYYMMDD: %w", err)
		}
	}
	if s.DelistDate != "" {
		if _, err := time.Parse("20060102", s.DelistDate); err != nil {
			return fmt.Errorf("delist_date 格式必须为 YYYYMMDD: %w", err)
		}
	}
	return nil
}

func (e Exchange) Valid() bool {
	switch e {
	case ExchangeSSE, ExchangeSZSE, ExchangeBSE:
		return true
	default:
		return false
	}
}

func (e Exchange) Code() string {
	switch e {
	case ExchangeSSE:
		return "SH"
	case ExchangeSZSE:
		return "SZ"
	case ExchangeBSE:
		return "BJ"
	default:
		return ""
	}
}

type STType string

const (
	STNone    STType = "NONE"
	ST        STType = "ST"
	STStar    STType = "ST_STAR"
	STUnknown STType = "UNKNOWN"
)

type SuspendType string

const (
	SuspendNone     SuspendType = "NONE"
	SuspendFull     SuspendType = "FULL"
	SuspendIntraday SuspendType = "INTRADAY"
	SuspendUnknown  SuspendType = "UNKNOWN"
)

// SecurityDailyState 是 D1 定义的最小按日状态快照，不做复杂事件溯源。
type SecurityDailyState struct {
	TradeDate      string      `json:"trade_date"`
	TsCode         string      `json:"ts_code"`
	STType         STType      `json:"st_type"`
	SuspendType    SuspendType `json:"suspend_type"`
	SuspendTiming  string      `json:"suspend_timing,omitempty"`
	Source         string      `json:"source"`
	FetchedAt      time.Time   `json:"fetched_at"`
}

func (s SecurityDailyState) Validate() error {
	if _, err := time.Parse("2006-01-02", s.TradeDate); err != nil {
		return fmt.Errorf("trade_date 必须为 YYYY-MM-DD: %w", err)
	}
	if strings.TrimSpace(s.TsCode) == "" {
		return errors.New("ts_code 不能为空")
	}
	switch s.STType {
	case STNone, ST, STStar, STUnknown:
	default:
		return fmt.Errorf("无效 st_type: %q", s.STType)
	}
	switch s.SuspendType {
	case SuspendNone, SuspendFull, SuspendIntraday, SuspendUnknown:
	default:
		return fmt.Errorf("无效 suspend_type: %q", s.SuspendType)
	}
	if strings.TrimSpace(s.Source) == "" {
		return errors.New("状态来源不能为空")
	}
	if s.FetchedAt.IsZero() {
		return errors.New("fetched_at 不能为空")
	}
	return nil
}
