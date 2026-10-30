// Package calendar 定义交易所自然日历契约。缺少日历记录不等于开市。
package calendar

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

type Exchange string

const (
	SSE  Exchange = "SSE"
	SZSE Exchange = "SZSE"
	BSE  Exchange = "BSE"
)

type Source string

const (
	SourceExchangeNotice Source = "EXCHANGE_NOTICE"
	SourceTushare        Source = "TUSHARE"
	SourceManualOverride Source = "MANUAL_OVERRIDE"
)

type TradeDay struct {
	Exchange     Exchange  `json:"exchange"`
	CalDate      string    `json:"cal_date"`
	IsOpen       bool      `json:"is_open"`
	PretradeDate string    `json:"pretrade_date,omitempty"`
	Source       Source    `json:"source"`
	SourceURL    string    `json:"source_url,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
}

func (d TradeDay) Validate() error {
	switch d.Exchange {
	case SSE, SZSE, BSE:
	default:
		return fmt.Errorf("不支持的交易所: %q", d.Exchange)
	}
	if _, err := time.Parse("2006-01-02", d.CalDate); err != nil {
		return fmt.Errorf("cal_date 必须为 YYYY-MM-DD: %w", err)
	}
	if d.PretradeDate != "" {
		if _, err := time.Parse("2006-01-02", d.PretradeDate); err != nil {
			return fmt.Errorf("pretrade_date 必须为 YYYY-MM-DD: %w", err)
		}
	}
	switch d.Source {
	case SourceExchangeNotice, SourceTushare, SourceManualOverride:
	default:
		return fmt.Errorf("无效交易日历来源: %q", d.Source)
	}
	if d.Source == SourceExchangeNotice && strings.TrimSpace(d.SourceURL) == "" {
		return errors.New("交易所公告来源必须保留 source_url")
	}
	if d.FetchedAt.IsZero() {
		return errors.New("fetched_at 不能为空")
	}
	return nil
}

// IsAuthoritative 返回能否覆盖当前日历记录的来源优先级。
// 交易所公告优先，其次人工覆盖，再其次 Tushare。
func IsAuthoritative(candidate, current Source) bool {
	rank := func(s Source) int {
		switch s {
		case SourceExchangeNotice:
			return 3
		case SourceManualOverride:
			return 2
		case SourceTushare:
			return 1
		default:
			return 0
		}
	}
	return rank(candidate) >= rank(current)
}
