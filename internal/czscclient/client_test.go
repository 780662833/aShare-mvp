package czscclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/780662833/aShare-mvp/internal/marketdata"
)

func completeBar(at time.Time) marketdata.Bar {
	return marketdata.Bar{
		TsCode: "600519.SH", Frequency: marketdata.FrequencyM30, BarTime: at,
		Open: 100, High: 103, Low: 99, Close: 102, Volume: 1000, Amount: 102000,
		AdjustMode: marketdata.AdjustQFQ, VolumeUnit: marketdata.VolumeSourceNative,
		Source: "gotdx", IsComplete: true,
		FetchedAt: time.Date(2026, 10, 30, 16, 0, 0, 0, marketdata.ShanghaiLocation()),
	}
}

func TestBuildRequestMapsD2BarContract(t *testing.T) {
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, marketdata.ShanghaiLocation())
	request, err := BuildRequest([]marketdata.Bar{completeBar(at)})
	if err != nil {
		t.Fatal(err)
	}
	if request.Freq != "30分钟" || request.Bars[0].DT != "2026-10-30T10:00:00+08:00" {
		t.Fatalf("CZSC 周期/时间映射错误: %+v", request)
	}
}

func TestBuildRequestRejectsFormingBars(t *testing.T) {
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, marketdata.ShanghaiLocation())
	bar := completeBar(at)
	bar.IsComplete = false
	if _, err := BuildRequest([]marketdata.Bar{bar}); err == nil {
		t.Fatal("未完成 K 线必须被拒绝")
	}
}

func TestAnalyzeHTTPRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/analyze" {
			t.Errorf("路径错误: %s", r.URL.Path)
		}
		var input AnalyzeRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Errorf("解析请求体失败: %v", err)
			http.Error(w, "invalid request", http.StatusBadRequest)
			return
		}
		if input.TsCode != "600519.SH" || len(input.Bars) != 1 {
			t.Errorf("请求体错误: %+v", input)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AnalyzeResponse{
			SchemaVersion: SchemaVersion, TsCode: input.TsCode, Freq: input.Freq,
			AdjustMode: input.AdjustMode, Source: input.Source,
			AsOf: input.Bars[0].DT, InputBarCount: 1, FXCount: 2,
		})
	}))
	defer server.Close()

	client, err := New(server.URL, time.Second, "")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, marketdata.ShanghaiLocation())
	request, err := BuildRequest([]marketdata.Bar{completeBar(at)})
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Analyze(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.FXCount != 2 || response.AsOf != request.Bars[0].DT {
		t.Fatalf("响应不匹配: %+v", response)
	}
}


func TestValidateAnalyzeRequestRejectsWrongShanghaiOffset(t *testing.T) {
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, marketdata.ShanghaiLocation())
	request, err := BuildRequest([]marketdata.Bar{completeBar(at)})
	if err != nil {
		t.Fatal(err)
	}
	request.Bars[0].DT = at.UTC().Format(time.RFC3339)
	if err := validateAnalyzeRequest(request); err == nil {
		t.Fatal("CZSC 请求必须拒绝 UTC 时间戳")
	}
}

func TestAnalyzeRejectsResponseFromDifferentSource(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input AnalyzeRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(AnalyzeResponse{
			SchemaVersion: SchemaVersion, TsCode: input.TsCode, Freq: input.Freq,
			AdjustMode: input.AdjustMode, Source: "another-provider",
			AsOf: input.Bars[len(input.Bars)-1].DT,
			InputBarCount: len(input.Bars),
		})
	}))
	defer server.Close()

	client, err := New(server.URL, time.Second, "")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, marketdata.ShanghaiLocation())
	request, err := BuildRequest([]marketdata.Bar{completeBar(at)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Analyze(context.Background(), request); err == nil {
		t.Fatal("CZSC 响应的数据来源不匹配时必须拒绝")
	}
}
