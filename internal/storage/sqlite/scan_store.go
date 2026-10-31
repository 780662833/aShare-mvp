package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/780662833/aShare-mvp/internal/market/universe"
	scanmodel "github.com/780662833/aShare-mvp/internal/scanner/model"
)

func (s *Store) CreateScanRun(ctx context.Context, run scanmodel.Run, items []scanmodel.Item) error {
	return s.CreateScanRunWithExclusions(ctx, run, items, nil)
}

func (s *Store) CreateScanRunWithExclusions(ctx context.Context, run scanmodel.Run, items []scanmodel.Item, exclusions []scanmodel.Exclusion) error {
	if run.ID == "" || run.AsOf.IsZero() || run.StartedAt.IsZero() || run.Status != scanmodel.RunRunning || run.TotalCount != len(items)+len(exclusions) || run.ExcludedCount != len(exclusions) { return fmt.Errorf("扫描运行初始化参数无效") }
	tx, err := s.db.BeginTx(ctx, nil); if err != nil { return err }
	_, err = tx.ExecContext(ctx, "INSERT INTO scan_runs (scan_id, as_of, started_at, status, total_count, excluded_count) VALUES (?, ?, ?, ?, ?, ?)", run.ID, run.AsOf.Format(time.RFC3339Nano), run.StartedAt.Format(time.RFC3339Nano), string(run.Status), run.TotalCount, run.ExcludedCount)
	if err != nil { _ = tx.Rollback(); return fmt.Errorf("创建扫描运行记录失败: %w", err) }
	for _, item := range items {
		if item.ScanID != run.ID || item.TsCode == "" || item.Status != scanmodel.ItemPending { _ = tx.Rollback(); return fmt.Errorf("扫描证券初始化记录无效: %s", item.TsCode) }
		_, err = tx.ExecContext(ctx, "INSERT INTO scan_run_items (scan_id, ts_code, security_name, exchange, list_date, delist_date, status, decision_ids, reason_codes) VALUES (?, ?, ?, ?, ?, ?, ?, '[]', '[]')", item.ScanID, item.TsCode, item.SecurityName, string(item.Exchange), item.ListDate, item.DelistDate, string(item.Status))
		if err != nil { _ = tx.Rollback(); return fmt.Errorf("创建证券 %s 扫描记录失败: %w", item.TsCode, err) }
	}
	for _, exclusion := range exclusions {
		if exclusion.ScanID != run.ID || exclusion.TsCode == "" || exclusion.TradeDate == "" || len(exclusion.ReasonCodes) == 0 { _ = tx.Rollback(); return fmt.Errorf("扫描排除记录无效: %s", exclusion.TsCode) }
		reasons, marshalErr := json.Marshal(exclusion.ReasonCodes); if marshalErr != nil { _ = tx.Rollback(); return fmt.Errorf("序列化扫描排除原因失败: %w", marshalErr) }
		_, err = tx.ExecContext(ctx, "INSERT INTO scan_run_exclusions (scan_id, ts_code, security_name, exchange, trade_date, reason_codes) VALUES (?, ?, ?, ?, ?, ?)", exclusion.ScanID, exclusion.TsCode, exclusion.SecurityName, string(exclusion.Exchange), exclusion.TradeDate, string(reasons))
		if err != nil { _ = tx.Rollback(); return fmt.Errorf("保存证券 %s 扫描排除记录失败: %w", exclusion.TsCode, err) }
	}
	return tx.Commit()
}

func (s *Store) UpdateScanRunItem(ctx context.Context, item scanmodel.Item) error {
	if item.ScanID == "" || item.TsCode == "" || item.Status == scanmodel.ItemPending ||
		item.StartedAt.IsZero() || item.CompletedAt.IsZero() {
		return fmt.Errorf("扫描证券结果参数无效")
	}
	decisionIDs, err := json.Marshal(item.DecisionIDs)
	if err != nil {
		return fmt.Errorf("序列化策略决策标识失败: %w", err)
	}
	reasonCodes, err := json.Marshal(item.ReasonCodes)
	if err != nil {
		return fmt.Errorf("序列化策略原因失败: %w", err)
	}
	strategyResults, err := json.Marshal(item.StrategyResults)
	if err != nil {
		return fmt.Errorf("序列化独立策略结果失败: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE scan_run_items SET
		status = ?, data_version = ?, decision_ids = ?, strategy_results = ?, reason_codes = ?,
		error_message = ?, started_at = ?, completed_at = ?
		WHERE scan_id = ? AND ts_code = ? AND status = ?`,
		string(item.Status), item.DataVersion, string(decisionIDs), string(strategyResults), string(reasonCodes),
		item.ErrorMessage, item.StartedAt.Format(time.RFC3339Nano),
		item.CompletedAt.Format(time.RFC3339Nano), item.ScanID, item.TsCode,
		string(scanmodel.ItemPending))
	if err != nil {
		return fmt.Errorf("更新证券扫描结果失败: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("证券 %s 扫描记录不存在或已完成，拒绝覆盖", item.TsCode)
	}
	return nil
}

func (s *Store) ListScanRunExclusions(ctx context.Context, scanID string) ([]scanmodel.Exclusion, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT scan_id, ts_code, security_name, exchange, trade_date, reason_codes FROM scan_run_exclusions WHERE scan_id = ? ORDER BY ts_code ASC", scanID)
	if err != nil { return nil, err }; defer rows.Close()
	items := make([]scanmodel.Exclusion, 0)
	for rows.Next() { var item scanmodel.Exclusion; var exchange, reasons string
		if err := rows.Scan(&item.ScanID, &item.TsCode, &item.SecurityName, &exchange, &item.TradeDate, &reasons); err != nil { return nil, err }
		item.Exchange = universe.Exchange(exchange); if err := json.Unmarshal([]byte(reasons), &item.ReasonCodes); err != nil { return nil, fmt.Errorf("解析扫描排除原因失败: %w", err) }; items = append(items, item)
	}; if err := rows.Err(); err != nil { return nil, err }; return items, nil
}

func (s *Store) CompleteScanRun(ctx context.Context, run scanmodel.Run) error {
	if run.ID == "" || run.CompletedAt.IsZero() ||
		(run.Status != scanmodel.RunCompleted && run.Status != scanmodel.RunPartial &&
			run.Status != scanmodel.RunFailed && run.Status != scanmodel.RunCancelled) {
		return fmt.Errorf("扫描运行终态参数无效")
	}
	processed := run.SuccessCount + run.DataIncompleteCount + run.AnalysisErrorCount + run.FailedCount
	eligibleCount := run.TotalCount - run.ExcludedCount
	if run.ExcludedCount < 0 || eligibleCount < 0 || processed > eligibleCount { return fmt.Errorf("扫描运行汇总计数超过有效证券范围") }
	switch run.Status {
	case scanmodel.RunCompleted:
		if run.SuccessCount != eligibleCount || run.DataIncompleteCount != 0 || run.AnalysisErrorCount != 0 || run.FailedCount != 0 { return fmt.Errorf("COMPLETED 扫描必须所有合格证券成功，排除项单独计数") }
	case scanmodel.RunPartial:
		if run.SuccessCount == 0 || processed != eligibleCount { return fmt.Errorf("PARTIAL_SUCCESS 扫描必须有成功项且所有合格证券均已处理") }
	case scanmodel.RunFailed:
		if run.SuccessCount != 0 || (eligibleCount == 0 && run.TotalCount != 0) || processed != eligibleCount { return fmt.Errorf("FAILED 扫描必须存在合格证券、无成功项且所有合格证券均已处理") }
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	var total, success, incomplete, analysisError, failed, pending, excluded int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*),
		COALESCE(SUM(CASE WHEN status = 'SUCCESS' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'DATA_INCOMPLETE' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'ANALYSIS_ERROR' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'FAILED' THEN 1 ELSE 0 END), 0),
		COALESCE(SUM(CASE WHEN status = 'PENDING' THEN 1 ELSE 0 END), 0)
		FROM scan_run_items WHERE scan_id = ?`, run.ID).Scan(
		&total, &success, &incomplete, &analysisError, &failed, &pending)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("核对扫描证券明细失败: %w", err)
	}
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM scan_run_exclusions WHERE scan_id = ?", run.ID).Scan(&excluded); err != nil { _ = tx.Rollback(); return fmt.Errorf("核对扫描排除明细失败: %w", err) }
	if total+excluded != run.TotalCount || excluded != run.ExcludedCount || success != run.SuccessCount ||
		incomplete != run.DataIncompleteCount || analysisError != run.AnalysisErrorCount ||
		failed != run.FailedCount {
		_ = tx.Rollback()
		return fmt.Errorf("扫描汇总与逐股明细不一致")
	}
	if run.Status != scanmodel.RunCancelled && pending != 0 {
		_ = tx.Rollback()
		return fmt.Errorf("非取消扫描仍存在未处理证券")
	}
	result, err := tx.ExecContext(ctx, `UPDATE scan_runs SET
		completed_at = ?, status = ?, success_count = ?, data_incomplete_count = ?,
		analysis_error_count = ?, failed_count = ?, excluded_count = ?, error_message = ?
		WHERE scan_id = ? AND status = ? AND total_count = ?`,
		run.CompletedAt.Format(time.RFC3339Nano), string(run.Status), run.SuccessCount,
		run.DataIncompleteCount, run.AnalysisErrorCount, run.FailedCount, run.ExcludedCount,
		run.ErrorMessage, run.ID, string(scanmodel.RunRunning), run.TotalCount)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("完成扫描运行记录失败: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if affected != 1 {
		_ = tx.Rollback()
		return fmt.Errorf("扫描运行记录不存在、总数不匹配或已终结: %s", run.ID)
	}
	return tx.Commit()
}

func (s *Store) GetScanRun(ctx context.Context, scanID string) (scanmodel.Run, error) {
	var run scanmodel.Run
	var asOf, startedAt, completedAt, status string
	err := s.db.QueryRowContext(ctx, `SELECT scan_id, as_of, started_at, completed_at, status,
		total_count, success_count, data_incomplete_count, analysis_error_count, failed_count, excluded_count,
		error_message FROM scan_runs WHERE scan_id = ?`, scanID).Scan(
		&run.ID, &asOf, &startedAt, &completedAt, &status,
		&run.TotalCount, &run.SuccessCount, &run.DataIncompleteCount, &run.AnalysisErrorCount,
		&run.FailedCount, &run.ExcludedCount, &run.ErrorMessage)
	if err != nil {
		return scanmodel.Run{}, err
	}
	run.AsOf, err = time.Parse(time.RFC3339Nano, asOf)
	if err != nil {
		return scanmodel.Run{}, fmt.Errorf("解析扫描 as_of 失败: %w", err)
	}
	run.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return scanmodel.Run{}, fmt.Errorf("解析扫描 started_at 失败: %w", err)
	}
	if completedAt != "" {
		run.CompletedAt, err = time.Parse(time.RFC3339Nano, completedAt)
		if err != nil {
			return scanmodel.Run{}, fmt.Errorf("解析扫描 completed_at 失败: %w", err)
		}
	}
	run.Status = scanmodel.RunStatus(status)
	return run, nil
}


// ListScanRunItemsPage 按证券代码稳定排序分页读取扫描项。
func (s *Store) ListScanRunItemsPage(ctx context.Context, scanID string, limit, offset int) ([]scanmodel.Item, int64, error) {
	if scanID == "" || limit < 1 || limit > 200 || offset < 0 { return nil, 0, fmt.Errorf("扫描项分页参数无效") }
	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM scan_run_items WHERE scan_id = ?", scanID).Scan(&total); err != nil { return nil, 0, err }
	rows, err := s.db.QueryContext(ctx, `SELECT scan_id, ts_code, security_name, exchange, list_date, delist_date, status, data_version, decision_ids, strategy_results, reason_codes, error_message, started_at, completed_at FROM scan_run_items WHERE scan_id = ? ORDER BY ts_code ASC LIMIT ? OFFSET ?`, scanID, limit, offset)
	if err != nil { return nil, 0, err }; defer rows.Close()
	items := make([]scanmodel.Item, 0, limit)
	for rows.Next() {
		var item scanmodel.Item; var exchange, status, ids, strategies, reasons, startedAt, completedAt string
		if err := rows.Scan(&item.ScanID, &item.TsCode, &item.SecurityName, &exchange, &item.ListDate, &item.DelistDate, &status, &item.DataVersion, &ids, &strategies, &reasons, &item.ErrorMessage, &startedAt, &completedAt); err != nil { return nil, 0, err }
		item.Exchange = universe.Exchange(exchange); item.Status = scanmodel.ItemStatus(status)
		if err := json.Unmarshal([]byte(ids), &item.DecisionIDs); err != nil { return nil, 0, fmt.Errorf("解析扫描决策标识失败: %w", err) }
		if err := json.Unmarshal([]byte(strategies), &item.StrategyResults); err != nil { return nil, 0, fmt.Errorf("解析扫描策略结果失败: %w", err) }
		if err := json.Unmarshal([]byte(reasons), &item.ReasonCodes); err != nil { return nil, 0, fmt.Errorf("解析扫描原因失败: %w", err) }
		if startedAt != "" { item.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt); if err != nil { return nil, 0, err } }
		if completedAt != "" { item.CompletedAt, err = time.Parse(time.RFC3339Nano, completedAt); if err != nil { return nil, 0, err } }
		items = append(items, item)
	}
	if err := rows.Err(); err != nil { return nil, 0, err }
	return items, total, nil
}

// ListScanRunExclusionsPage 按证券代码稳定排序分页读取资格门排除记录。
func (s *Store) ListScanRunExclusionsPage(ctx context.Context, scanID string, limit, offset int) ([]scanmodel.Exclusion, int64, error) {
	if scanID == "" || limit < 1 || limit > 200 || offset < 0 { return nil, 0, fmt.Errorf("扫描排除分页参数无效") }
	var total int64
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM scan_run_exclusions WHERE scan_id = ?", scanID).Scan(&total); err != nil { return nil, 0, err }
	rows, err := s.db.QueryContext(ctx, "SELECT scan_id, ts_code, security_name, exchange, trade_date, reason_codes FROM scan_run_exclusions WHERE scan_id = ? ORDER BY ts_code ASC LIMIT ? OFFSET ?", scanID, limit, offset)
	if err != nil { return nil, 0, err }; defer rows.Close()
	items := make([]scanmodel.Exclusion, 0, limit)
	for rows.Next() { var item scanmodel.Exclusion; var exchange, reasons string
		if err := rows.Scan(&item.ScanID, &item.TsCode, &item.SecurityName, &exchange, &item.TradeDate, &reasons); err != nil { return nil, 0, err }
		item.Exchange = universe.Exchange(exchange); if err := json.Unmarshal([]byte(reasons), &item.ReasonCodes); err != nil { return nil, 0, fmt.Errorf("解析扫描排除原因失败: %w", err) }; items = append(items, item)
	}
	if err := rows.Err(); err != nil { return nil, 0, err }; return items, total, nil
}

// GetScanRunProgress 从逐股明细计算实时进度，适用于 RUNNING 扫描查询。
func (s *Store) GetScanRunProgress(ctx context.Context, scanID string) (scanmodel.Run, int, error) {
	run, err := s.GetScanRun(ctx, scanID); if err != nil { return scanmodel.Run{}, 0, err }
	var total, pending, success, incomplete, analysisError, failed int
	err = s.db.QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(CASE WHEN status='PENDING' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN status='SUCCESS' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN status='DATA_INCOMPLETE' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN status='ANALYSIS_ERROR' THEN 1 ELSE 0 END),0), COALESCE(SUM(CASE WHEN status='FAILED' THEN 1 ELSE 0 END),0) FROM scan_run_items WHERE scan_id = ?`, scanID).Scan(&total, &pending, &success, &incomplete, &analysisError, &failed)
	if err != nil { return scanmodel.Run{}, 0, err }
	if run.Status == scanmodel.RunRunning {
		run.SuccessCount, run.DataIncompleteCount, run.AnalysisErrorCount, run.FailedCount = success, incomplete, analysisError, failed
	}
	return run, pending, nil
}

func (s *Store) ListScanRunItems(ctx context.Context, scanID string) ([]scanmodel.Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT scan_id, ts_code, security_name, exchange,
		list_date, delist_date, status, data_version, decision_ids, strategy_results, reason_codes,
		error_message, started_at, completed_at
		FROM scan_run_items WHERE scan_id = ? ORDER BY ts_code ASC`, scanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]scanmodel.Item, 0)
	for rows.Next() {
		var item scanmodel.Item
		var exchange, status, decisionIDs, strategyResults, reasonCodes, startedAt, completedAt string
		if err := rows.Scan(&item.ScanID, &item.TsCode, &item.SecurityName, &exchange,
			&item.ListDate, &item.DelistDate, &status, &item.DataVersion,
			&decisionIDs, &strategyResults, &reasonCodes, &item.ErrorMessage, &startedAt, &completedAt); err != nil {
			return nil, err
		}
		item.Exchange = universe.Exchange(exchange)
		item.Status = scanmodel.ItemStatus(status)
		if err := json.Unmarshal([]byte(decisionIDs), &item.DecisionIDs); err != nil {
			return nil, fmt.Errorf("解析扫描决策标识失败: %w", err)
		}
		if err := json.Unmarshal([]byte(strategyResults), &item.StrategyResults); err != nil {
			return nil, fmt.Errorf("解析独立策略结果失败: %w", err)
		}
		if err := json.Unmarshal([]byte(reasonCodes), &item.ReasonCodes); err != nil {
			return nil, fmt.Errorf("解析扫描原因失败: %w", err)
		}
		if startedAt != "" {
			item.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt)
			if err != nil {
				return nil, fmt.Errorf("解析扫描证券 started_at 失败: %w", err)
			}
		}
		if completedAt != "" {
			item.CompletedAt, err = time.Parse(time.RFC3339Nano, completedAt)
			if err != nil {
				return nil, fmt.Errorf("解析扫描证券 completed_at 失败: %w", err)
			}
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}
