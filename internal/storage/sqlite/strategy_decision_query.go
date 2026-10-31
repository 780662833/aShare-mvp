package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/780662833/aShare-mvp/internal/marketdata"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
)

// StrategyDecisionQuery 是历史策略决策查询条件。
// FromAsOf 和 ToAsOf 均为包含边界；Limit 必须在 1..200，Offset 从 0 开始。
type StrategyDecisionQuery struct {
	TsCode     string
	StrategyID string
	Status     contract.DecisionStatus
	FromAsOf   *time.Time
	ToAsOf     *time.Time
	Limit      int
	Offset     int
}

// StrategyDecisionPage 是按决策时间倒序返回的历史决策分页。
type StrategyDecisionPage struct {
	Items  []contract.StrategyDecision `json:"items"`
	Total  int64                       `json:"total"`
	Limit  int                         `json:"limit"`
	Offset int                         `json:"offset"`
}

// ListStrategyDecisions 按可选条件查询原始历史决策，不重新执行策略。
// 排序键为 as_of DESC、decision_id DESC，保证相同决策时点下顺序稳定。
func (s *Store) ListStrategyDecisions(ctx context.Context, query StrategyDecisionQuery) (StrategyDecisionPage, error) {
	if query.Limit < 1 || query.Limit > 200 {
		return StrategyDecisionPage{}, fmt.Errorf("历史决策 Limit 必须在 1 到 200 之间")
	}
	if query.Offset < 0 {
		return StrategyDecisionPage{}, fmt.Errorf("历史决策 Offset 不能小于 0")
	}
	if query.Status != "" && !validDecisionStatus(query.Status) {
		return StrategyDecisionPage{}, fmt.Errorf("历史决策 Status 无效: %q", query.Status)
	}
	if query.FromAsOf != nil && query.FromAsOf.IsZero() {
		return StrategyDecisionPage{}, fmt.Errorf("历史决策 FromAsOf 不能为零值")
	}
	if query.ToAsOf != nil && query.ToAsOf.IsZero() {
		return StrategyDecisionPage{}, fmt.Errorf("历史决策 ToAsOf 不能为零值")
	}
	if query.FromAsOf != nil && query.ToAsOf != nil && query.FromAsOf.After(*query.ToAsOf) {
		return StrategyDecisionPage{}, fmt.Errorf("历史决策 FromAsOf 不能晚于 ToAsOf")
	}

	conditions := make([]string, 0, 5)
	args := make([]any, 0, 5)
	if value := strings.TrimSpace(query.TsCode); value != "" {
		conditions = append(conditions, "ts_code = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(query.StrategyID); value != "" {
		conditions = append(conditions, "strategy_id = ?")
		args = append(args, value)
	}
	if query.Status != "" {
		conditions = append(conditions, "status = ?")
		args = append(args, string(query.Status))
	}
	if query.FromAsOf != nil {
		conditions = append(conditions, "as_of >= ?")
		args = append(args, query.FromAsOf.In(marketdata.ShanghaiLocation()).Format(time.RFC3339Nano))
	}
	if query.ToAsOf != nil {
		conditions = append(conditions, "as_of <= ?")
		args = append(args, query.ToAsOf.In(marketdata.ShanghaiLocation()).Format(time.RFC3339Nano))
	}

	where := ""
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}

	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return StrategyDecisionPage{}, fmt.Errorf("开启历史决策只读事务失败: %w", err)
	}
	defer tx.Rollback()

	var total int64
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM strategy_decisions"+where, args...).Scan(&total); err != nil {
		return StrategyDecisionPage{}, fmt.Errorf("统计历史策略决策失败: %w", err)
	}

	pageArgs := append(append([]any(nil), args...), query.Limit, query.Offset)
	rows, err := tx.QueryContext(ctx, "SELECT payload FROM strategy_decisions"+where+
		" ORDER BY as_of DESC, decision_id DESC LIMIT ? OFFSET ?", pageArgs...)
	if err != nil {
		return StrategyDecisionPage{}, fmt.Errorf("查询历史策略决策失败: %w", err)
	}
	page := StrategyDecisionPage{
		Items: make([]contract.StrategyDecision, 0, query.Limit),
		Total: total, Limit: query.Limit, Offset: query.Offset,
	}
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return StrategyDecisionPage{}, fmt.Errorf("读取历史策略决策 payload 失败: %w", err)
		}
		var decision contract.StrategyDecision
		if err := json.Unmarshal([]byte(payload), &decision); err != nil {
			return StrategyDecisionPage{}, fmt.Errorf("解析历史策略决策 payload 失败: %w", err)
		}
		page.Items = append(page.Items, decision)
	}
	if err := rows.Err(); err != nil {
		return StrategyDecisionPage{}, fmt.Errorf("遍历历史策略决策失败: %w", err)
	}
	if err := rows.Close(); err != nil {
		return StrategyDecisionPage{}, fmt.Errorf("关闭历史策略决策结果集失败: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return StrategyDecisionPage{}, fmt.Errorf("完成历史决策只读事务失败: %w", err)
	}
	return page, nil
}

func validDecisionStatus(status contract.DecisionStatus) bool {
	switch status {
	case contract.StatusWatching,
		contract.StatusSetupReady,
		contract.StatusConfirmed,
		contract.StatusWaitEntry,
		contract.StatusBuyObservation,
		contract.StatusHoldObservation,
		contract.StatusReduceObservation,
		contract.StatusSellObservation,
		contract.StatusInvalidated,
		contract.StatusExpired,
		contract.StatusDataIncomplete,
		contract.StatusAnalysisError:
		return true
	default:
		return false
	}
}
