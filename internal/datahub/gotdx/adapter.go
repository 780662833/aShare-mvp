// Package gotdxadapter 将 gotdx 现有高阶 API 映射到本项目 D1/D2 数据契约。
package gotdxadapter

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	tdx "github.com/bensema/gotdx"
	"github.com/bensema/gotdx/proto"
	"github.com/bensema/gotdx/types"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
)

// Client 只覆盖本适配器实际调用的 gotdx 公开方法，便于测试注入。
type Client interface {
	StockAll(market uint8) ([]proto.Security, error)
	StockKLine(category uint16, market uint8, code string, start uint16, count uint16, times uint16, adjust uint16) ([]proto.SecurityBar, error)
	Disconnect()
}

type Config struct {
	TimeoutSeconds int
	SettleDelay     time.Duration
}

type Adapter struct {
	client      Client
	mu          sync.Mutex
	now         func() time.Time
	settleDelay time.Duration
}

func New(cfg Config) *Adapter {
	if cfg.TimeoutSeconds <= 0 {
		cfg.TimeoutSeconds = 8
	}
	if cfg.SettleDelay <= 0 {
		cfg.SettleDelay = time.Minute
	}
	return &Adapter{
		client:      tdx.New(tdx.WithTimeoutSec(cfg.TimeoutSeconds)),
		now:         time.Now,
		settleDelay: cfg.SettleDelay,
	}
}

func NewWithClient(client Client, cfg Config) (*Adapter, error) {
	if client == nil {
		return nil, errors.New("gotdx client 不能为空")
	}
	if cfg.SettleDelay <= 0 {
		cfg.SettleDelay = time.Minute
	}
	return &Adapter{client: client, now: time.Now, settleDelay: cfg.SettleDelay}, nil
}

func (a *Adapter) Close() {
	if a == nil || a.client == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.client.Disconnect()
}

type MarketFetchStatus struct {
	Exchange  universe.Exchange `json:"exchange"`
	RawCount  int                `json:"raw_count"`
	StockCount int               `json:"stock_count"`
	Success   bool               `json:"success"`
	Error     string             `json:"error,omitempty"`
}

type UniverseResult struct {
	Securities []universe.StockBasic `json:"securities"`
	Markets    []MarketFetchStatus   `json:"markets"`
}

func (r UniverseResult) Complete() bool {
	if len(r.Markets) != 3 {
		return false
	}
	for _, status := range r.Markets {
		if !status.Success {
			return false
		}
	}
	return true
}

// FetchUniverse 分市场调用 StockAll，任何一个市场失败都返回不完整结果和 error。
// 调用方必须检查 Complete()，不能把部分名单覆盖为全市场完整名单。
func (a *Adapter) FetchUniverse(ctx context.Context) (UniverseResult, error) {
	result := UniverseResult{Securities: make([]universe.StockBasic, 0), Markets: make([]MarketFetchStatus, 0, 3)}
	markets := []types.Market{types.MarketSZ, types.MarketSH, types.MarketBJ}
	seen := make(map[string]struct{})
	var failures []string

	for _, market := range markets {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		items, err := a.fetchAll(market.Uint8())
		exchange := exchangeForMarket(market)
		status := MarketFetchStatus{Exchange: exchange}
		if err != nil {
			status.Error = err.Error()
			result.Markets = append(result.Markets, status)
			failures = append(failures, market.String()+": "+err.Error())
			continue
		}

		status.Success = true
		status.RawCount = len(items)
		for _, item := range items {
			code := strings.TrimSpace(item.Code)
			name := strings.TrimSpace(item.Name)
			if code == "" || name == "" {
				continue
			}
			symbol := code + "." + market.String()
			if !types.IsStock(symbol) || types.IsETF(symbol) || types.IsIndex(symbol) {
				continue
			}
			tsCode := code + "." + market.String()
			if _, exists := seen[tsCode]; exists {
				continue
			}
			seen[tsCode] = struct{}{}
			result.Securities = append(result.Securities, universe.StockBasic{
				TsCode: tsCode, Symbol: code, Name: name,
				Market: market.String(), Exchange: exchange,
				ListStatus: universe.ListingUnknown,
			})
			status.StockCount++
		}
		result.Markets = append(result.Markets, status)
	}

	sort.Slice(result.Securities, func(i, j int) bool {
		return result.Securities[i].TsCode < result.Securities[j].TsCode
	})
	if len(failures) > 0 {
		return result, fmt.Errorf("gotdx 沪深北证券名单不完整: %s", strings.Join(failures, "; "))
	}
	return result, nil
}

func (a *Adapter) fetchAll(market uint8) ([]proto.Security, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client.StockAll(market)
}

// FetchBars 直接调用 gotdx StockKLine 获取日线、30 分钟或 60 分钟数据。
// gotdx 不提供显式 is_complete 字段；这里仅按 K 线结束时间 + settle delay 做保守时钟判断。
func (a *Adapter) FetchBars(ctx context.Context, tsCode string, freq marketdata.Frequency, count uint16) ([]marketdata.Bar, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	market, code, err := parseTsCode(tsCode)
	if err != nil {
		return nil, err
	}
	category, err := categoryForFrequency(freq)
	if err != nil {
		return nil, err
	}
	if count == 0 {
		count = 250
	}

	a.mu.Lock()
	raw, err := a.client.StockKLine(category, market, code, 0, count, 1, types.AdjustQFQ)
	a.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("gotdx StockKLine %s %s 失败: %w", tsCode, freq, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	now := a.now().In(marketdata.ShanghaiLocation())
	bars := make([]marketdata.Bar, 0, len(raw))
	for i, item := range raw {
		barTime, err := normalizeBarTime(item, freq)
		if err != nil {
			return nil, fmt.Errorf("gotdx K 线第 %d 根时间转换失败: %w", i, err)
		}
		bars = append(bars, marketdata.Bar{
			TsCode: tsCode, Frequency: freq, BarTime: barTime,
			Open: item.Open, High: item.High, Low: item.Low, Close: item.Close,
			Volume: item.Vol, Amount: item.Amount,
			AdjustMode: marketdata.AdjustQFQ,
			VolumeUnit: marketdata.VolumeSourceNative,
			Source: "gotdx",
			IsComplete: !barTime.Add(a.settleDelay).After(now),
			FetchedAt: now,
		})
	}
	sort.Slice(bars, func(i, j int) bool { return bars[i].BarTime.Before(bars[j].BarTime) })
	if len(bars) == 0 {
		return bars, nil
	}
	if err := marketdata.ValidateBatch(bars); err != nil {
		return nil, fmt.Errorf("gotdx K 线质量检查失败: %w", err)
	}
	return bars, nil
}

func normalizeBarTime(item proto.SecurityBar, freq marketdata.Frequency) (time.Time, error) {
	if item.Year < 1990 || item.Month < 1 || item.Month > 12 || item.Day < 1 || item.Day > 31 {
		return time.Time{}, fmt.Errorf("非法日期字段 %04d-%02d-%02d", item.Year, item.Month, item.Day)
	}
	hour, minute := item.Hour, item.Minute
	if freq == marketdata.FrequencyD1 {
		hour, minute = 15, 0
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return time.Time{}, fmt.Errorf("非法时间字段 %02d:%02d", hour, minute)
	}
	result := marketdata.ShanghaiWallTime(item.Year, time.Month(item.Month), item.Day, hour, minute)
	if result.Year() != item.Year || int(result.Month()) != item.Month || result.Day() != item.Day {
		return time.Time{}, fmt.Errorf("日期字段无效")
	}
	return result, nil
}

func categoryForFrequency(freq marketdata.Frequency) (uint16, error) {
	switch freq {
	case marketdata.FrequencyD1:
		return types.KLINE_TYPE_DAILY, nil
	case marketdata.FrequencyM30:
		return types.KLINE_TYPE_30MIN, nil
	case marketdata.FrequencyM60:
		return types.KLINE_TYPE_1HOUR, nil
	default:
		return 0, fmt.Errorf("gotdx 不支持本平台周期 %q", freq)
	}
}

func parseTsCode(tsCode string) (uint8, string, error) {
	parts := strings.Split(strings.TrimSpace(strings.ToUpper(tsCode)), ".")
	if len(parts) != 2 || len(parts[0]) != 6 {
		return 0, "", fmt.Errorf("ts_code 格式错误: %q，期望 6 位代码加交易所后缀", tsCode)
	}
	switch parts[1] {
	case "SH":
		return types.MarketSH.Uint8(), parts[0], nil
	case "SZ":
		return types.MarketSZ.Uint8(), parts[0], nil
	case "BJ":
		return types.MarketBJ.Uint8(), parts[0], nil
	default:
		return 0, "", fmt.Errorf("不支持的交易所后缀: %q", parts[1])
	}
}

func exchangeForMarket(market types.Market) universe.Exchange {
	switch market {
	case types.MarketSZ:
		return universe.ExchangeSZSE
	case types.MarketSH:
		return universe.ExchangeSSE
	case types.MarketBJ:
		return universe.ExchangeBSE
	default:
		return ""
	}
}
