// Package czscclient 调用独立 CZSC HTTP/JSON 服务，不在 Go 中实现缠论算法。
package czscclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/780662833/aShare-mvp/internal/marketdata"
)

const (
	SchemaVersion    = "v1"
	MaxResponseBytes = 4 << 20

	// CZSCSourceRevision 必须与 czsc-service/requirements.txt 精确锁定的 commit 一致。
	CZSCSourceRevision = "5d350a943d6b1935561765fed5467c234973d121"

	SignalProfileStructureOnly = "structure_only"
	SignalProfileChan3BuyM60   = "chan3buy_m60_v1"
	SignalProfileChan3BuyM30   = "chan3buy_m30_timing_v1"
	SignalProfileTrendD1       = "trend_breakout_d1_v1"
)

type BarDTO struct {
	DT     string  `json:"dt"`
	Symbol string  `json:"symbol"`
	Open   float64 `json:"open"`
	Close  float64 `json:"close"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Vol    float64 `json:"vol"`
	Amount float64 `json:"amount"`
}

type AnalyzeRequest struct {
	SchemaVersion string   `json:"schema_version"`
	TsCode        string   `json:"ts_code"`
	Freq          string   `json:"freq"`
	AdjustMode    string   `json:"adjust_mode"`
	Source        string   `json:"source"`
	SignalProfile string   `json:"signal_profile,omitempty"`
	Bars          []BarDTO `json:"bars"`
}

type FXSummary struct {
	DT   string  `json:"dt"`
	Mark string  `json:"mark"`
	FX   float64 `json:"fx"`
	High float64 `json:"high"`
	Low  float64 `json:"low"`
}

type BISummary struct {
	StartTime string  `json:"start_time"`
	EndTime   string  `json:"end_time"`
	Direction string  `json:"direction"`
	High      float64 `json:"high"`
	Low       float64 `json:"low"`
	Power     float64 `json:"power"`
	Length    int     `json:"length"`
}

type ZSSummary struct {
	StartTime string  `json:"start_time"`
	EndTime   string  `json:"end_time"`
	ZG        float64 `json:"zg"`
	ZD        float64 `json:"zd"`
	ZZ        float64 `json:"zz"`
}

type AnalyzeResponse struct {
	SchemaVersion        string            `json:"schema_version"`
	TsCode               string            `json:"ts_code"`
	Freq                 string            `json:"freq"`
	AdjustMode           string            `json:"adjust_mode"`
	Source               string            `json:"source"`
	SignalProfile        string            `json:"signal_profile"`
	SignalProfileVersion string            `json:"signal_profile_version"`
	CZSCSourceRevision   string            `json:"czsc_source_revision"`
	SignalDataStatus     string            `json:"signal_data_status"`
	Signals              map[string]string `json:"signals"`
	AsOf                 string            `json:"as_of"`
	InputBarCount        int               `json:"input_bar_count"`
	FXCount              int               `json:"fx_count"`
	BICount              int               `json:"bi_count"`
	ZSCount              int               `json:"zs_count"`
	RecentFX             []FXSummary       `json:"recent_fx"`
	RecentBI             []BISummary       `json:"recent_bi"`
	RecentZS             []ZSSummary       `json:"recent_zs"`
	LastBIExtend         bool              `json:"last_bi_extend"`
}

type Client struct {
	endpoint   string
	httpClient *http.Client
	token      string
}

func New(baseURL string, timeout time.Duration, token string) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, fmt.Errorf("CZSC base URL 必须是有效的 http(s) URL")
	}
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	return &Client{
		endpoint:   strings.TrimRight(parsed.String(), "/") + "/api/v1/analyze",
		httpClient: &http.Client{Timeout: timeout},
		token:      token,
	}, nil
}

func normalizeSignalProfile(profile string) string {
	profile = strings.TrimSpace(profile)
	if profile == "" {
		return SignalProfileStructureOnly
	}
	return profile
}

func expectedSignalProfileVersion(profile string) (string, bool) {
	switch normalizeSignalProfile(profile) {
	case SignalProfileStructureOnly:
		return "structure-only-v1", true
	case SignalProfileChan3BuyM60:
		return "chan3buy-m60-v1", true
	case SignalProfileChan3BuyM30:
		return "chan3buy-m30-timing-v1", true
	case SignalProfileTrendD1:
		return "trend-breakout-d1-v1", true
	default:
		return "", false
	}
}

func validateSignalProfile(profile, freq string) error {
	switch normalizeSignalProfile(profile) {
	case SignalProfileStructureOnly:
		return nil
	case SignalProfileChan3BuyM60:
		if freq != "60分钟" {
			return fmt.Errorf("signal_profile %q 只支持 60分钟", SignalProfileChan3BuyM60)
		}
	case SignalProfileChan3BuyM30:
		if freq != "30分钟" {
			return fmt.Errorf("signal_profile %q 只支持 30分钟", SignalProfileChan3BuyM30)
		}
	case SignalProfileTrendD1:
		if freq != "日线" {
			return fmt.Errorf("signal_profile %q 只支持 日线", SignalProfileTrendD1)
		}
	default:
		return fmt.Errorf("不支持的 CZSC signal_profile: %q", profile)
	}
	return nil
}

// BuildRequest 保持旧调用兼容，默认只计算 CZSC 结构，不加载额外策略信号。
func BuildRequest(bars []marketdata.Bar) (AnalyzeRequest, error) {
	if err := marketdata.ValidateBatch(bars); err != nil {
		return AnalyzeRequest{}, err
	}
	first := bars[0]
	if first.AdjustMode == marketdata.AdjustUnknown {
		return AnalyzeRequest{}, fmt.Errorf("CZSC 正式分析不接受未知复权口径")
	}
	freq, err := first.Frequency.CZSCName()
	if err != nil {
		return AnalyzeRequest{}, err
	}
	request := AnalyzeRequest{
		SchemaVersion: SchemaVersion,
		TsCode:        first.TsCode,
		Freq:          freq,
		AdjustMode:    string(first.AdjustMode),
		Source:        first.Source,
		SignalProfile: SignalProfileStructureOnly,
		Bars:          make([]BarDTO, 0, len(bars)),
	}
	for i, bar := range bars {
		if !bar.IsComplete {
			return AnalyzeRequest{}, fmt.Errorf("bars[%d] 尚未完成，不可进入正式分析", i)
		}
		request.Bars = append(request.Bars, BarDTO{
			DT:     bar.BarTime.Format(time.RFC3339),
			Symbol: bar.TsCode,
			Open:   bar.Open,
			Close:  bar.Close,
			High:   bar.High,
			Low:    bar.Low,
			Vol:    bar.Volume,
			Amount: bar.Amount,
		})
	}
	return request, nil
}

// BuildRequestWithProfile 在构建标准请求后选择一个服务端白名单信号配置。
func BuildRequestWithProfile(bars []marketdata.Bar, signalProfile string) (AnalyzeRequest, error) {
	request, err := BuildRequest(bars)
	if err != nil {
		return AnalyzeRequest{}, err
	}
	request.SignalProfile = normalizeSignalProfile(signalProfile)
	if err := validateSignalProfile(request.SignalProfile, request.Freq); err != nil {
		return AnalyzeRequest{}, err
	}
	return request, nil
}

func validateAnalyzeRequest(request AnalyzeRequest) error {
	if request.SchemaVersion != SchemaVersion {
		return fmt.Errorf("CZSC schema_version 必须为 %s", SchemaVersion)
	}
	if strings.TrimSpace(request.TsCode) == "" || !strings.Contains(request.TsCode, ".") {
		return fmt.Errorf("CZSC ts_code 必须包含证券代码和交易所后缀")
	}
	switch request.Freq {
	case "日线", "30分钟", "60分钟":
	default:
		return fmt.Errorf("CZSC 周期无效: %q", request.Freq)
	}
	if err := validateSignalProfile(request.SignalProfile, request.Freq); err != nil {
		return err
	}
	switch request.AdjustMode {
	case string(marketdata.AdjustQFQ), string(marketdata.AdjustHFQ), string(marketdata.AdjustNone):
	default:
		return fmt.Errorf("CZSC 复权口径无效: %q", request.AdjustMode)
	}
	if strings.TrimSpace(request.Source) == "" {
		return fmt.Errorf("CZSC 数据来源不能为空")
	}
	if len(request.Bars) == 0 || len(request.Bars) > 5000 {
		return fmt.Errorf("CZSC K 线数量必须在 1 到 5000 之间")
	}

	var previous time.Time
	for i, bar := range request.Bars {
		if !strings.EqualFold(bar.Symbol, request.TsCode) {
			return fmt.Errorf("bars[%d].symbol 与 ts_code 不一致", i)
		}
		dt, err := time.Parse(time.RFC3339Nano, bar.DT)
		if err != nil {
			return fmt.Errorf("bars[%d].dt 不是有效 RFC3339 时间: %w", i, err)
		}
		_, offset := dt.Zone()
		if offset != marketdata.ShanghaiOffsetSeconds {
			return fmt.Errorf("bars[%d].dt 必须显式使用 +08:00", i)
		}
		if i > 0 && !dt.After(previous) {
			return fmt.Errorf("bars[%d].dt 必须严格递增", i)
		}
		previous = dt
		if !finitePositive(bar.Open) || !finitePositive(bar.Close) ||
			!finitePositive(bar.High) || !finitePositive(bar.Low) {
			return fmt.Errorf("bars[%d] OHLC 必须为有限正数", i)
		}
		if bar.High < math.Max(bar.Open, math.Max(bar.Close, bar.Low)) ||
			bar.Low > math.Min(bar.Open, math.Min(bar.Close, bar.High)) {
			return fmt.Errorf("bars[%d] OHLC 关系无效", i)
		}
		if !finiteNonNegative(bar.Vol) || !finiteNonNegative(bar.Amount) {
			return fmt.Errorf("bars[%d] 成交量和成交额必须为有限非负数", i)
		}
	}
	return nil
}

func finitePositive(value float64) bool {
	return value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func finiteNonNegative(value float64) bool {
	return value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func validateAnalyzeResponse(request AnalyzeRequest, result AnalyzeResponse) error {
	profile := normalizeSignalProfile(request.SignalProfile)
	profileVersion, ok := expectedSignalProfileVersion(profile)
	if !ok {
		return fmt.Errorf("不支持的 CZSC signal_profile: %q", profile)
	}
	lastBar := request.Bars[len(request.Bars)-1]
	if result.SchemaVersion != request.SchemaVersion ||
		result.TsCode != request.TsCode ||
		result.Freq != request.Freq ||
		result.AdjustMode != request.AdjustMode ||
		result.Source != request.Source ||
		result.InputBarCount != len(request.Bars) ||
		result.AsOf != lastBar.DT {
		return fmt.Errorf("CZSC 响应与请求的 schema/证券/周期/复权/来源/数据范围不匹配")
	}
	if result.SignalProfile != profile ||
		result.SignalProfileVersion != profileVersion ||
		result.CZSCSourceRevision != CZSCSourceRevision {
		return fmt.Errorf("CZSC 响应的信号 profile 或源码版本与请求契约不匹配")
	}
	switch result.SignalDataStatus {
	case "READY", "INSUFFICIENT_DATA":
		if profile == SignalProfileStructureOnly {
			return fmt.Errorf("structure_only 响应的 signal_data_status 必须为 NOT_REQUESTED")
		}
	case "NOT_REQUESTED":
		if profile != SignalProfileStructureOnly {
			return fmt.Errorf("策略信号 profile 响应不能标记为 NOT_REQUESTED")
		}
	default:
		return fmt.Errorf("CZSC 响应的 signal_data_status 无效: %q", result.SignalDataStatus)
	}
	if result.Signals == nil {
		result.Signals = map[string]string{}
	}
	return nil
}

func (c *Client) Analyze(ctx context.Context, request AnalyzeRequest) (AnalyzeResponse, error) {
	var result AnalyzeResponse
	if err := validateAnalyzeRequest(request); err != nil {
		return result, fmt.Errorf("CZSC 请求契约无效: %w", err)
	}
	body, err := json.Marshal(request)
	if err != nil {
		return result, fmt.Errorf("序列化 CZSC 请求失败: %w", err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return result, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	if c.token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+c.token)
	}

	response, err := c.httpClient.Do(httpRequest)
	if err != nil {
		return result, fmt.Errorf("调用 CZSC 服务失败: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return result, fmt.Errorf("读取 CZSC 响应失败: %w", err)
	}
	if len(responseBody) > MaxResponseBytes {
		return result, fmt.Errorf("CZSC 响应超过 %d 字节上限", MaxResponseBytes)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("CZSC 服务返回 HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return result, fmt.Errorf("解析 CZSC 响应失败: %w", err)
	}
	if err := validateAnalyzeResponse(request, result); err != nil {
		return result, err
	}
	if result.Signals == nil {
		result.Signals = map[string]string{}
	}
	return result, nil
}
