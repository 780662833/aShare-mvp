// Package httpapi exposes the existing application use cases through a local REST API.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	appstrategy "github.com/780662833/aShare-mvp/internal/app/strategy"
	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/scanner"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
	sqlitestore "github.com/780662833/aShare-mvp/internal/storage/sqlite"
)

const maxRequestBody = 1 << 20

type Evaluator interface {
	EvaluateSymbol(context.Context, string, time.Time) (appstrategy.Result, error)
}

type Server struct {
	store       *sqlitestore.Store
	evaluator   Evaluator
	scanner     *scanner.Scanner
	scanTimeout time.Duration
	rootCtx     context.Context
	cancel      context.CancelFunc
	scanMu      sync.Mutex
	mux         *http.ServeMux
}

func New(store *sqlitestore.Store, evaluator Evaluator, scanTimeout time.Duration) (*Server, error) {
	if store == nil || evaluator == nil {
		return nil, fmt.Errorf("HTTP API 必须提供 SQLite Store 和 Evaluator")
	}
	if scanTimeout <= 0 {
		scanTimeout = 30 * time.Minute
	}
	scan, err := scanner.New(store, evaluator)
	if err != nil {
		return nil, err
	}
	rootCtx, cancel := context.WithCancel(context.Background())
	s := &Server{store: store, evaluator: evaluator, scanner: scan, scanTimeout: scanTimeout, rootCtx: rootCtx, cancel: cancel, mux: http.NewServeMux()}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

// Close cancels background scan jobs. The scanner persists their CANCELLED terminal state.
func (s *Server) Close() { s.cancel() }

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.handleHealth)
	s.mux.HandleFunc("POST /api/v1/evaluations", s.handleEvaluation)
	s.mux.HandleFunc("POST /api/v1/scans", s.handleCreateScan)
	s.mux.HandleFunc("GET /api/v1/scans/{scan_id}", s.handleGetScan)
	s.mux.HandleFunc("GET /api/v1/scans/{scan_id}/items", s.handleScanItems)
	s.mux.HandleFunc("GET /api/v1/scans/{scan_id}/exclusions", s.handleScanExclusions)
	s.mux.HandleFunc("GET /api/v1/decisions", s.handleDecisions)
	s.mux.HandleFunc("GET /api/v1/decisions/{decision_id}", s.handleDecision)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet { methodNotAllowed(w); return }
	if err := s.store.DB().PingContext(r.Context()); err != nil {
		writeError(w, http.StatusServiceUnavailable, "DATABASE_UNAVAILABLE", "SQLite 不可用")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type evaluationRequest struct {
	TsCode string `json:"ts_code"`
	AsOf   string `json:"as_of"`
}

func (s *Server) handleEvaluation(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { methodNotAllowed(w); return }
	var request evaluationRequest
	if err := decodeJSON(w, r, &request); err != nil { writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error()); return }
	request.TsCode = strings.TrimSpace(request.TsCode)
	if request.TsCode == "" { writeError(w, http.StatusBadRequest, "INVALID_ARGUMENT", "ts_code 必填"); return }
	asOf, err := parseAsOf(request.AsOf)
	if err != nil { writeError(w, http.StatusBadRequest, "INVALID_AS_OF", err.Error()); return }
	result, err := s.evaluator.EvaluateSymbol(r.Context(), request.TsCode, asOf)
	if err != nil {
		status, code := http.StatusInternalServerError, "EVALUATION_FAILED"
		if strings.Contains(err.Error(), "SECURITY_INELIGIBLE") { status, code = http.StatusUnprocessableEntity, "SECURITY_INELIGIBLE" }
		if errors.Is(err, context.DeadlineExceeded) { status, code = http.StatusGatewayTimeout, "DEADLINE_EXCEEDED" }
		if errors.Is(err, context.Canceled) { status, code = 499, "REQUEST_CANCELLED" }
		writeError(w, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type createScanRequest struct {
	AsOf string `json:"as_of"`
}

type scanCompletion struct {
	run scanner.Run
	err error
}

func (s *Server) handleCreateScan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost { methodNotAllowed(w); return }
	var request createScanRequest
	if err := decodeJSON(w, r, &request); err != nil { writeError(w, http.StatusBadRequest, "INVALID_REQUEST", err.Error()); return }
	asOf, err := parseAsOf(request.AsOf)
	if err != nil { writeError(w, http.StatusBadRequest, "INVALID_AS_OF", err.Error()); return }
	if !s.scanMu.TryLock() { writeError(w, http.StatusConflict, "SCAN_ALREADY_RUNNING", "当前已有扫描任务运行"); return }
	id, err := scanner.NewScanID()
	if err != nil { s.scanMu.Unlock(); writeError(w, http.StatusInternalServerError, "SCAN_ID_FAILED", err.Error()); return }
	jobCtx, cancel := context.WithTimeout(s.rootCtx, s.scanTimeout)
	started := make(chan scanner.Run, 1)
	finished := make(chan scanCompletion, 1)
	go func() {
		defer cancel()
		defer s.scanMu.Unlock()
		run, runErr := s.scanner.RunWithID(jobCtx, asOf, id, func(run scanner.Run) error {
			started <- run
			return nil
		})
		finished <- scanCompletion{run: run, err: runErr}
	}()
	select {
	case run := <-started:
		w.Header().Set("Location", "/api/v1/scans/"+id)
		writeJSON(w, http.StatusAccepted, map[string]any{"scan_id": id, "status": run.Status, "as_of": run.AsOf})
	case result := <-finished:
		if result.err != nil { writeError(w, http.StatusUnprocessableEntity, "SCAN_START_FAILED", result.err.Error()); return }
		writeJSON(w, http.StatusOK, result.run)
	}
}

func (s *Server) handleGetScan(w http.ResponseWriter, r *http.Request) {
	run, pending, err := s.store.GetScanRunProgress(r.Context(), r.PathValue("scan_id"))
	if err != nil { writeStoreError(w, err); return }
	processed := run.TotalCount - run.ExcludedCount - pending
	writeJSON(w, http.StatusOK, map[string]any{"run": run, "processed_count": processed, "pending_count": pending})
}

func (s *Server) handleScanItems(w http.ResponseWriter, r *http.Request) {
	if _, _, err := s.store.GetScanRunProgress(r.Context(), r.PathValue("scan_id")); err != nil { writeStoreError(w, err); return }
	limit, offset, err := pagination(r)
	if err != nil { writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error()); return }
	items, total, err := s.store.ListScanRunItemsPage(r.Context(), r.PathValue("scan_id"), limit, offset)
	if err != nil { writeStoreError(w, err); return }
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleScanExclusions(w http.ResponseWriter, r *http.Request) {
	if _, _, err := s.store.GetScanRunProgress(r.Context(), r.PathValue("scan_id")); err != nil { writeStoreError(w, err); return }
	limit, offset, err := pagination(r)
	if err != nil { writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error()); return }
	items, total, err := s.store.ListScanRunExclusionsPage(r.Context(), r.PathValue("scan_id"), limit, offset)
	if err != nil { writeStoreError(w, err); return }
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "limit": limit, "offset": offset})
}

func (s *Server) handleDecisions(w http.ResponseWriter, r *http.Request) {
	limit, offset, err := pagination(r)
	if err != nil { writeError(w, http.StatusBadRequest, "INVALID_PAGINATION", err.Error()); return }
	query := sqlitestore.StrategyDecisionQuery{TsCode: r.URL.Query().Get("ts_code"), StrategyID: r.URL.Query().Get("strategy_id"), Status: contractStatus(r.URL.Query().Get("status")), Limit: limit, Offset: offset}
	if value := r.URL.Query().Get("from"); value != "" { parsed, parseErr := parseAsOf(value); if parseErr != nil { writeError(w, http.StatusBadRequest, "INVALID_FROM", parseErr.Error()); return }; query.FromAsOf = &parsed }
	if value := r.URL.Query().Get("to"); value != "" { parsed, parseErr := parseAsOf(value); if parseErr != nil { writeError(w, http.StatusBadRequest, "INVALID_TO", parseErr.Error()); return }; query.ToAsOf = &parsed }
	page, err := s.store.ListStrategyDecisions(r.Context(), query)
	if err != nil { writeError(w, http.StatusBadRequest, "INVALID_QUERY", err.Error()); return }
	writeJSON(w, http.StatusOK, page)
}

func (s *Server) handleDecision(w http.ResponseWriter, r *http.Request) {
	decision, err := s.store.GetStrategyDecision(r.Context(), r.PathValue("decision_id"))
	if err != nil { writeStoreError(w, err); return }
	writeJSON(w, http.StatusOK, decision)
}

func contractStatus(value string) contract.DecisionStatus { return contract.DecisionStatus(strings.TrimSpace(value)) }

func pagination(r *http.Request) (int, int, error) {
	limit, offset := 50, 0
	var err error
	if value := r.URL.Query().Get("limit"); value != "" { limit, err = strconv.Atoi(value); if err != nil { return 0, 0, fmt.Errorf("limit 必须是整数") } }
	if value := r.URL.Query().Get("offset"); value != "" { offset, err = strconv.Atoi(value); if err != nil { return 0, 0, fmt.Errorf("offset 必须是整数") } }
	if limit < 1 || limit > 200 || offset < 0 { return 0, 0, fmt.Errorf("limit 必须在 1-200，offset 不能小于 0") }
	return limit, offset, nil
}

func parseAsOf(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" { return time.Time{}, fmt.Errorf("as_of 必须使用带时区的 RFC3339 格式") }
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil { return time.Time{}, fmt.Errorf("as_of 必须使用带时区的 RFC3339 格式: %w", err) }
	return parsed.In(marketdata.ShanghaiLocation()), nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBody)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil { return fmt.Errorf("JSON 请求体无效: %w", err) }
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF { return fmt.Errorf("请求体只能包含一个 JSON 对象") }
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeStoreError(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) { writeError(w, http.StatusNotFound, "NOT_FOUND", "请求的记录不存在"); return }
	writeError(w, http.StatusInternalServerError, "STORAGE_ERROR", err.Error())
}

func methodNotAllowed(w http.ResponseWriter) { writeError(w, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "HTTP 方法不支持") }
