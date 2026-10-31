package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/780662833/aShare-mvp/internal/market/universe"
	"github.com/780662833/aShare-mvp/internal/scanner"
)

func (s *Store) CreateScanRun(ctx context.Context, run scanner.Run, items []scanner.Item) error {
	if run.ID == "" || run.AsOf.IsZero() || run.StartedAt.IsZero() ||
		run.Status != scanner.RunRunning || run.TotalCount != len(items) {
		return fmt.Errorf("扫描运行初始化参数无效")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO scan_runs (
		scan_id, as_of, started_at, status, total_count
	) VALUES (?, ?, ?, ?, ?)`,
		run.ID, run.AsOf.Format(time.RFC3339Nano),
		run.StartedAt.Format(time.RFC3339Nano), string(run.Status), run.TotalCount)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("创建扫描运行记录失败: %w", err)
	}
	for _, item := range items {
		if item.ScanID != run.ID || item.TsCode == "" ||
			item.Status != scanner.ItemPending {
			_ = tx.Rollback()
			return fmt.Errorf("扫描证券初始化记录无效: %s", item.TsCode)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO scan_run_items (
			scan_id, ts_code, security_name, exchange, list_date, delist_date,
			status, decision_ids, reason_codes
		) VALUES (?, ?, ?, ?, ?, ?, ?, '[]', '[]')`,
			item.ScanID, item.TsCode, item.SecurityName, string(item.Exchange),
			item.ListDate, item.DelistDate, string(item.Status))
		if err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("创建证券 %s 扫描记录失败: %w", item.TsCode, err)
		}
	}
	return tx.Commit()
}

func (s *Store) UpdateScanRunItem(ctx context.Context, item scanner.Item) error {
	if item.ScanID == "" || item.TsCode == "" || item.Status == scanner.ItemPending ||
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
	result, err := s.db.ExecContext(ctx, `UPDATE scan_run_items SET
		status = ?, data_version = ?, decision_ids = ?, reason_codes = ?,
		error_message = ?, started_at = ?, completed_at = ?
		WHERE scan_id = ? AND ts_code = ? AND status = ?`,
		string(item.Status), item.DataVersion, string(decisionIDs), string(reasonCodes),
		item.ErrorMessage, item.StartedAt.Format(time.RFC3339Nano),
		item.CompletedAt.Format(time.RFC3339Nano), item.ScanID, item.TsCode,
		string(scanner.ItemPending))
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

func (s *Store) CompleteScanRun(ctx context.Context, run scanner.Run) error {
	if run.ID == "" || run.CompletedAt.IsZero() ||
		(run.Status != scanner.RunCompleted && run.Status != scanner.RunPartial &&
			run.Status != scanner.RunFailed && run.Status != scanner.RunCancelled) {
		return fmt.Errorf("扫描运行终态参数无效")
	}
	processed := run.SuccessCount + run.DataIncompleteCount + run.AnalysisErrorCount + run.FailedCount
	if processed > run.TotalCount {
		return fmt.Errorf("扫描运行汇总计数超过总数")
	}
	switch run.Status {
	case scanner.RunCompleted:
		if run.SuccessCount != run.TotalCount || run.DataIncompleteCount != 0 ||
			run.AnalysisErrorCount != 0 || run.FailedCount != 0 {
			return fmt.Errorf("COMPLETED 扫描必须全部成功")
		}
	case scanner.RunPartial:
		if run.SuccessCount == 0 || processed != run.TotalCount {
			return fmt.Errorf("PARTIAL_SUCCESS 扫描必须有成功项且所有证券均已处理")
		}
	case scanner.RunFailed:
		if run.SuccessCount != 0 || processed != run.TotalCount {
			return fmt.Errorf("FAILED 扫描必须无成功项且所有证券均已处理")
		}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	var total, success, incomplete, analysisError, failed, pending int
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
	if total != run.TotalCount || success != run.SuccessCount ||
		incomplete != run.DataIncompleteCount || analysisError != run.AnalysisErrorCount ||
		failed != run.FailedCount {
		_ = tx.Rollback()
		return fmt.Errorf("扫描汇总与逐股明细不一致")
	}
	if run.Status != scanner.RunCancelled && pending != 0 {
		_ = tx.Rollback()
		return fmt.Errorf("非取消扫描仍存在未处理证券")
	}
	result, err := tx.ExecContext(ctx, `UPDATE scan_runs SET
		completed_at = ?, status = ?, success_count = ?, data_incomplete_count = ?,
		analysis_error_count = ?, failed_count = ?, error_message = ?
		WHERE scan_id = ? AND status = ? AND total_count = ?`,
		run.CompletedAt.Format(time.RFC3339Nano), string(run.Status), run.SuccessCount,
		run.DataIncompleteCount, run.AnalysisErrorCount, run.FailedCount,
		run.ErrorMessage, run.ID, string(scanner.RunRunning), run.TotalCount)
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

func (s *Store) GetScanRun(ctx context.Context, scanID string) (scanner.Run, error) {
	var run scanner.Run
	var asOf, startedAt, completedAt, status string
	err := s.db.QueryRowContext(ctx, `SELECT scan_id, as_of, started_at, completed_at, status,
		total_count, success_count, data_incomplete_count, analysis_error_count, failed_count,
		error_message FROM scan_runs WHERE scan_id = ?`, scanID).Scan(
		&run.ID, &asOf, &startedAt, &completedAt, &status,
		&run.TotalCount, &run.SuccessCount, &run.DataIncompleteCount, &run.AnalysisErrorCount,
		&run.FailedCount, &run.ErrorMessage)
	if err != nil {
		return scanner.Run{}, err
	}
	run.AsOf, err = time.Parse(time.RFC3339Nano, asOf)
	if err != nil {
		return scanner.Run{}, fmt.Errorf("解析扫描 as_of 失败: %w", err)
	}
	run.StartedAt, err = time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return scanner.Run{}, fmt.Errorf("解析扫描 started_at 失败: %w", err)
	}
	if completedAt != "" {
		run.CompletedAt, err = time.Parse(time.RFC3339Nano, completedAt)
		if err != nil {
			return scanner.Run{}, fmt.Errorf("解析扫描 completed_at 失败: %w", err)
		}
	}
	run.Status = scanner.RunStatus(status)
	return run, nil
}

func (s *Store) ListScanRunItems(ctx context.Context, scanID string) ([]scanner.Item, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT scan_id, ts_code, security_name, exchange,
		list_date, delist_date, status, data_version, decision_ids, reason_codes,
		error_message, started_at, completed_at
		FROM scan_run_items WHERE scan_id = ? ORDER BY ts_code ASC`, scanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]scanner.Item, 0)
	for rows.Next() {
		var item scanner.Item
		var exchange, status, decisionIDs, reasonCodes, startedAt, completedAt string
		if err := rows.Scan(&item.ScanID, &item.TsCode, &item.SecurityName, &exchange,
			&item.ListDate, &item.DelistDate, &status, &item.DataVersion,
			&decisionIDs, &reasonCodes, &item.ErrorMessage, &startedAt, &completedAt); err != nil {
			return nil, err
		}
		item.Exchange = universe.Exchange(exchange)
		item.Status = scanner.ItemStatus(status)
		if err := json.Unmarshal([]byte(decisionIDs), &item.DecisionIDs); err != nil {
			return nil, fmt.Errorf("解析扫描决策标识失败: %w", err)
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
