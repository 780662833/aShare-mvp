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

func responseForRequest(input AnalyzeRequest) AnalyzeResponse {
	profile := normalizeSignalProfile(input.SignalProfile)
	version, _ := expectedSignalProfileVersion(profile)
	status := "NOT_REQUESTED"
	if profile != SignalProfileStructureOnly {
		status = "READY"
	}
	return AnalyzeResponse{
		SchemaVersion:        SchemaVersion,
		TsCode:               input.TsCode,
		Freq:                 input.Freq,
		AdjustMode:           input.AdjustMode,
		Source:               input.Source,
		SignalProfile:        profile,
		SignalProfileVersion: version,
		CZSCSourceRevision:   CZSCSourceRevision,
		SignalDataStatus:     status,
		Signals:              map[string]string{},
		AsOf:                 input.Bars[len(input.Bars)-1].DT,
		InputBarCount:        len(input.Bars),
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
	if request.SignalProfile != SignalProfileStructureOnly {
		t.Fatalf("旧 BuildRequest 应默认 structure_only，得到 %q", request.SignalProfile)
	}
}

func TestBuildRequestWithProfile(t *testing.T) {
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, marketdata.ShanghaiLocation())
	request, err := BuildRequestWithProfile(
		[]marketdata.Bar{completeBar(at)},
		SignalProfileChan3BuyM30,
	)
	if err != nil {
		t.Fatal(err)
	}
	if request.SignalProfile != SignalProfileChan3BuyM30 {
		t.Fatalf("信号 profile 未写入请求: %+v", request)
	}

	if _, err := BuildRequestWithProfile(
		[]marketdata.Bar{completeBar(at)},
		SignalProfileTrendD1,
	); err == nil {
		t.Fatal("D1 profile 不应接受 30 分钟 K 线")
	}
	if _, err := BuildRequestWithProfile(
		[]marketdata.Bar{completeBar(at)},
		"arbitrary_signal",
	); err == nil {
		t.Fatal("未知 profile 必须被拒绝")
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

func TestAnalyzeHTTPRoundTripPreservesSignalsAndVersions(t *testing.T) {
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
		response := responseForRequest(input)
		response.FXCount = 2
		response.Signals = map[string]string{
			"30分钟_D1_表里关系V230102": "向上_底分_任意_0",
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
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
	if response.Signals["30分钟_D1_表里关系V230102"] != "向上_底分_任意_0" {
		t.Fatalf("CZSC 原始信号 key/value 未完整保留: %+v", response.Signals)
	}
	if response.CZSCSourceRevision != CZSCSourceRevision ||
		response.SignalProfileVersion != "structure-only-v1" {
		t.Fatalf("CZSC 信号版本信息错误: %+v", response)
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
		response := responseForRequest(input)
		response.Source = "another-provider"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
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

func TestAnalyzeRejectsMismatchedSignalRevision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input AnalyzeRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		response := responseForRequest(input)
		response.CZSCSourceRevision = "different-revision"
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
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
		t.Fatal("CZSC 响应源码 revision 不匹配时必须拒绝")
	}
}

func TestValidateAnalyzeRequestRejectsProfileFrequencyMismatch(t *testing.T) {
	at := time.Date(2026, 10, 30, 10, 0, 0, 0, marketdata.ShanghaiLocation())
	request, err := BuildRequest([]marketdata.Bar{completeBar(at)})
	if err != nil {
		t.Fatal(err)
	}
	request.SignalProfile = SignalProfileTrendD1
	if err := validateAnalyzeRequest(request); err == nil {
		t.Fatal("趋势突破日线 profile 不应接受 30 分钟请求")
	}
}


func TestAnalyzePreservesAndValidatesPerSignalLifecycle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input AnalyzeRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		response := responseForRequest(input)
		key := "30分钟_D1_表里关系V230102"
		response.Signals[key] = "向上_底分_任意_0"
		response.SignalLifecycles = map[string]SignalLifecycle{
			key: {AgeBars: 2, StartedAt: "2026-10-30T10:00:00+08:00"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	client, err := New(server.URL, time.Second, "")
	if err != nil {
		t.Fatal(err)
	}
	bars := []marketdata.Bar{
		completeBar(marketdata.ShanghaiWallTime(2026, 10, 30, 9, 0)),
		completeBar(marketdata.ShanghaiWallTime(2026, 10, 30, 10, 0)),
		completeBar(marketdata.ShanghaiWallTime(2026, 10, 30, 11, 0)),
	}
	request, err := BuildRequestWithProfile(bars, SignalProfileChan3BuyM30)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Analyze(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := response.SignalLifecycles["30分钟_D1_表里关系V230102"]
	if lifecycle.AgeBars != 2 || lifecycle.StartedAt != "2026-10-30T10:00:00+08:00" {
		t.Fatalf("信号生命周期未正确透传: %+v", lifecycle)
	}
}

func TestValidateAnalyzeResponseRejectsLifecycleForUnknownSignal(t *testing.T) {
	at := marketdata.ShanghaiWallTime(2026, 10, 30, 11, 0)
	request, err := BuildRequestWithProfile([]marketdata.Bar{completeBar(at)}, SignalProfileChan3BuyM30)
	if err != nil {
		t.Fatal(err)
	}
	response := responseForRequest(request)
	response.SignalLifecycles = map[string]SignalLifecycle{
		"unexpected_signal": {AgeBars: 0, StartedAt: "2026-10-30T11:00:00+08:00"},
	}
	if err := validateAnalyzeResponse(request, response); err == nil {
		t.Fatal("不存在于 signals 的生命周期 key 必须被拒绝")
	}
}

func TestValidateAnalyzeResponseRejectsLifecycleOutsideRequestBars(t *testing.T) {
	at := marketdata.ShanghaiWallTime(2026, 10, 30, 11, 0)
	request, err := BuildRequestWithProfile([]marketdata.Bar{completeBar(at)}, SignalProfileChan3BuyM30)
	if err != nil {
		t.Fatal(err)
	}
	key := "30分钟_D1_表里关系V230102"
	response := responseForRequest(request)
	response.Signals[key] = "向上_底分_任意_0"
	response.SignalLifecycles = map[string]SignalLifecycle{
		key: {AgeBars: 1, StartedAt: "2026-10-30T10:00:00+08:00"},
	}
	if err := validateAnalyzeResponse(request, response); err == nil {
		t.Fatal("生命周期起始时间必须属于本次请求 K 线序列")
	}
}

func TestValidateAnalyzeResponseRejectsLifecycleAgeMismatch(t *testing.T) {
	at := marketdata.ShanghaiWallTime(2026, 10, 30, 11, 0)
	bars := []marketdata.Bar{
		completeBar(marketdata.ShanghaiWallTime(2026, 10, 30, 10, 0)),
		completeBar(at),
	}
	request, err := BuildRequestWithProfile(bars, SignalProfileChan3BuyM30)
	if err != nil {
		t.Fatal(err)
	}
	key := "30分钟_D1_表里关系V230102"
	response := responseForRequest(request)
	response.Signals[key] = "向上_底分_任意_0"
	response.SignalLifecycles = map[string]SignalLifecycle{
		key: {AgeBars: 0, StartedAt: "2026-10-30T10:00:00+08:00"},
	}
	if err := validateAnalyzeResponse(request, response); err == nil {
		t.Fatal("age_bars 必须与请求内起始 K 线和最新 K 线的位置一致")
	}
}

func TestValidateAnalyzeResponseRejectsFutureLifecycleStart(t *testing.T) {
	at := marketdata.ShanghaiWallTime(2026, 10, 30, 11, 0)
	request, err := BuildRequestWithProfile([]marketdata.Bar{completeBar(at)}, SignalProfileChan3BuyM30)
	if err != nil {
		t.Fatal(err)
	}
	key := "30分钟_D1_表里关系V230102"
	response := responseForRequest(request)
	response.Signals[key] = "向上_底分_任意_0"
	response.SignalLifecycles = map[string]SignalLifecycle{
		key: {AgeBars: 0, StartedAt: "2026-10-30T11:30:00+08:00"},
	}
	if err := validateAnalyzeResponse(request, response); err == nil {
		t.Fatal("生命周期起始时间不能晚于 as_of")
	}
}
