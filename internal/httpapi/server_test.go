package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	appstrategy "github.com/780662833/aShare-mvp/internal/app/strategy"
	"github.com/780662833/aShare-mvp/internal/market/calendar"
	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/scanner"
	sqlitestore "github.com/780662833/aShare-mvp/internal/storage/sqlite"
	"github.com/780662833/aShare-mvp/internal/strategy/chan3buy"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
	"github.com/780662833/aShare-mvp/internal/strategy/trendbreakout"
)

type fakeEvaluator struct{}

func (fakeEvaluator) EvaluateSymbol(_ context.Context, code string, asOf time.Time) (appstrategy.Result, error) {
	return appstrategy.Result{TsCode: code, AsOf: asOf, DataVersion: "data-api-test", Decisions: []contract.StrategyDecision{
		{DecisionID: "decision-chan-" + code, TsCode: code, StrategyID: chan3buy.StrategyID, AsOf: asOf, Status: contract.StatusWatching},
		{DecisionID: "decision-trend-" + code, TsCode: code, StrategyID: trendbreakout.StrategyID, AsOf: asOf, Status: contract.StatusWatching},
	}}, nil
}

func newTestAPI(t *testing.T) (*sqlitestore.Store, *Server) {
	t.Helper()
	store, err := sqlitestore.Open(context.Background(), filepath.Join(t.TempDir(), "api.db"))
	if err != nil { t.Fatal(err) }
	api, err := New(store, fakeEvaluator{}, time.Minute)
	if err != nil { store.Close(); t.Fatal(err) }
	t.Cleanup(func() { api.Close(); _ = store.Close() })
	return store, api
}

func TestHealthz(t *testing.T) {
	_, api := newTestAPI(t)
	recorder := httptest.NewRecorder()
	api.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if recorder.Code != http.StatusOK { t.Fatalf("healthz status=%d body=%s", recorder.Code, recorder.Body.String()) }
}

func TestEvaluationRequiresExplicitPointInTime(t *testing.T) {
	_, api := newTestAPI(t)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/evaluations", bytes.NewBufferString(`{"ts_code":"600519.SH"}`))
	api.Handler().ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest { t.Fatalf("missing as_of must be rejected: status=%d body=%s", recorder.Code, recorder.Body.String()) }
}

func TestScanAPIStartsJobAndPagesItems(t *testing.T) {
	store, api := newTestAPI(t)
	ctx := context.Background()
	security := universe.StockBasic{TsCode:"000001.SZ", Symbol:"000001", Name:"平安银行", Exchange:universe.ExchangeSZSE, ListStatus:universe.ListingActive}
	if err := store.UpsertStockBasics(ctx, []universe.StockBasic{security}); err != nil { t.Fatal(err) }
	asOf := marketdata.ShanghaiWallTime(2026, 10, 30, 15, 1)
	fetchedAt := marketdata.ShanghaiWallTime(2026, 10, 30, 14, 59)
	if err := store.UpsertDailyStates(ctx, []universe.SecurityDailyState{{TradeDate:"2026-10-30", TsCode:security.TsCode, STType:universe.STNone, SuspendType:universe.SuspendNone, Source:"api-test", FetchedAt:fetchedAt}}); err != nil { t.Fatal(err) }
	if err := store.UpsertTradeCalendar(ctx, []calendar.TradeDay{{Exchange:calendar.SZSE, CalDate:"2026-10-30", IsOpen:true, Source:calendar.SourceTushare, FetchedAt:fetchedAt}}); err != nil { t.Fatal(err) }

	body, _ := json.Marshal(createScanRequest{AsOf:asOf.Format(time.RFC3339Nano)})
	created := httptest.NewRecorder()
	api.Handler().ServeHTTP(created, httptest.NewRequest(http.MethodPost, "/api/v1/scans", bytes.NewReader(body)))
	if created.Code != http.StatusAccepted && created.Code != http.StatusOK { t.Fatalf("create scan status=%d body=%s", created.Code, created.Body.String()) }
	var response struct { ScanID string `json:"scan_id"` }
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil { t.Fatal(err) }
	if response.ScanID == "" { t.Fatalf("scan_id missing: %s", created.Body.String()) }

	deadline := time.Now().Add(3 * time.Second)
	for {
		run, _, err := store.GetScanRunProgress(ctx, response.ScanID)
		if err == nil && run.Status != scanner.RunRunning { break }
		if time.Now().After(deadline) { t.Fatal("scan job did not finish before test deadline") }
		time.Sleep(10 * time.Millisecond)
	}
	items := httptest.NewRecorder()
	api.Handler().ServeHTTP(items, httptest.NewRequest(http.MethodGet, "/api/v1/scans/"+response.ScanID+"/items?limit=10&offset=0", nil))
	if items.Code != http.StatusOK { t.Fatalf("items status=%d body=%s", items.Code, items.Body.String()) }
	var page struct { Items []struct { TsCode string `json:"ts_code"`; Status string `json:"status"` } `json:"items"`; Total int64 `json:"total"` }
	if err := json.Unmarshal(items.Body.Bytes(), &page); err != nil { t.Fatal(err) }
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].TsCode != security.TsCode || page.Items[0].Status != "SUCCESS" { t.Fatalf("unexpected scan item page: %+v", page) }
}
