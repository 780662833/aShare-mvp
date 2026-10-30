// Package czscclient 调用独立 CZSC HTTP/JSON 服务，不在 Go 中实现缠论算法。
package czscclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/780662833/aShare-mvp/internal/marketdata"
)

const (
	SchemaVersion    = "v1"
	MaxResponseBytes = 4 << 20
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
	SchemaVersion string      `json:"schema_version"`
	TsCode        string      `json:"ts_code"`
	Freq          string      `json:"freq"`
	AdjustMode    string      `json:"adjust_mode"`
	Source        string      `json:"source"`
	AsOf          string      `json:"as_of"`
	InputBarCount int         `json:"input_bar_count"`
	FXCount       int         `json:"fx_count"`
	BICount       int         `json:"bi_count"`
	ZSCount       int         `json:"zs_count"`
	RecentFX      []FXSummary `json:"recent_fx"`
	RecentBI      []BISummary `json:"recent_bi"`
	RecentZS      []ZSSummary `json:"recent_zs"`
	LastBIExtend  bool        `json:"last_bi_extend"`
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
		endpoint: strings.TrimRight(parsed.String(), "/") + "/api/v1/analyze",
		httpClient: &http.Client{Timeout: timeout},
		token: token,
	}, nil
}

// BuildRequest 只允许把单一证券、周期、复权口径、来源的已完成 K 线交给 CZSC。
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
		TsCode: first.TsCode,
		Freq: freq,
		AdjustMode: string(first.AdjustMode),
		Source: first.Source,
		Bars: make([]BarDTO, 0, len(bars)),
	}
	for i, bar := range bars {
		if !bar.IsComplete {
			return AnalyzeRequest{}, fmt.Errorf("bars[%d] 尚未完成，不可进入正式分析", i)
		}
		request.Bars = append(request.Bars, BarDTO{
			DT: bar.BarTime.Format(time.RFC3339),
			Symbol: bar.TsCode,
			Open: bar.Open, Close: bar.Close, High: bar.High, Low: bar.Low,
			Vol: bar.Volume, Amount: bar.Amount,
		})
	}
	return request, nil
}

func (c *Client) Analyze(ctx context.Context, request AnalyzeRequest) (AnalyzeResponse, error) {
	var result AnalyzeResponse
	if request.SchemaVersion != SchemaVersion || len(request.Bars) == 0 {
		return result, fmt.Errorf("CZSC 请求契约无效")
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
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes))
	if err != nil {
		return result, fmt.Errorf("读取 CZSC 响应失败: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("CZSC 服务返回 HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	if err := json.Unmarshal(responseBody, &result); err != nil {
		return result, fmt.Errorf("解析 CZSC 响应失败: %w", err)
	}
	if result.SchemaVersion != SchemaVersion || result.TsCode != request.TsCode || result.Freq != request.Freq {
		return result, fmt.Errorf("CZSC 响应与请求的 schema/证券/周期不匹配")
	}
	return result, nil
}
