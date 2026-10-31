package sqlite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	scanmodel "github.com/780662833/aShare-mvp/internal/scanner/model"
	"github.com/780662833/aShare-mvp/internal/strategy/contract"
)

// ScanResult 将扫描快照、逐股结果与原始完整策略决策组合为只读结果。
// Decisions 保留数据库中保存的完整 payload，不重新运行策略。
type ScanResult struct {
	Run       scanmodel.Run
	Items     []scanmodel.Item
	Decisions []contract.StrategyDecision
}

// GetScanResult 按原始 DecisionID 批量读取并校验完整决策。
// 任一引用缺失、重复或与扫描快照不匹配时整体返回错误，不返回部分拼接结果。
func (s *Store) GetScanResult(ctx context.Context, scanID string) (ScanResult, error) {
	if strings.TrimSpace(scanID) == "" {
		return ScanResult{}, fmt.Errorf("scan_id 不能为空")
	}
	run, err := s.GetScanRun(ctx, scanID)
	if err != nil {
		return ScanResult{}, fmt.Errorf("读取扫描运行失败: %w", err)
	}
	items, err := s.ListScanRunItems(ctx, scanID)
	if err != nil {
		return ScanResult{}, fmt.Errorf("读取扫描证券明细失败: %w", err)
	}

	type expectedDecision struct {
		tsCode      string
		strategyID  string
		dataVersion string
	}
	expected := make(map[string]expectedDecision)
	decisionIDs := make([]string, 0)
	for _, item := range items {
		itemIDs := make(map[string]struct{}, len(item.DecisionIDs))
		for _, id := range item.DecisionIDs {
			if strings.TrimSpace(id) == "" {
				return ScanResult{}, fmt.Errorf("证券 %s 包含空 DecisionID", item.TsCode)
			}
			if _, exists := itemIDs[id]; exists {
				return ScanResult{}, fmt.Errorf("证券 %s 包含重复 DecisionID: %s", item.TsCode, id)
			}
			itemIDs[id] = struct{}{}
			if _, exists := expected[id]; exists {
				return ScanResult{}, fmt.Errorf("扫描运行包含重复 DecisionID: %s", id)
			}
			expected[id] = expectedDecision{
				tsCode: item.TsCode, dataVersion: item.DataVersion,
			}
			decisionIDs = append(decisionIDs, id)
		}

		savedByStrategy := make(map[string]string)
		for _, sr := range item.StrategyResults {
			if sr.PersistenceStatus != "SAVED" {
				continue
			}
			if strings.TrimSpace(sr.DecisionID) == "" {
				return ScanResult{}, fmt.Errorf("证券 %s 的 SAVED 策略结果缺少 DecisionID", item.TsCode)
			}
			if prior, exists := savedByStrategy[sr.StrategyID]; exists {
				return ScanResult{}, fmt.Errorf("证券 %s 的策略 %s 存在重复 SAVED 结果: %s / %s", item.TsCode, sr.StrategyID, prior, sr.DecisionID)
			}
			savedByStrategy[sr.StrategyID] = sr.DecisionID
			if sr.AsOf.IsZero() || !sr.AsOf.Equal(run.AsOf) || sr.DataVersion != item.DataVersion {
				return ScanResult{}, fmt.Errorf("证券 %s 的策略结果快照与扫描不一致: %s", item.TsCode, sr.StrategyID)
			}
			if _, exists := itemIDs[sr.DecisionID]; !exists {
				return ScanResult{}, fmt.Errorf("证券 %s 的策略 %s DecisionID 未包含在决策索引中: %s", item.TsCode, sr.StrategyID, sr.DecisionID)
			}
			e := expected[sr.DecisionID]
			e.strategyID = sr.StrategyID
			expected[sr.DecisionID] = e
		}
		if len(item.StrategyResults) > 0 && len(savedByStrategy) != len(itemIDs) {
			return ScanResult{}, fmt.Errorf("证券 %s 的 SAVED 策略结果与 DecisionIDs 数量不一致", item.TsCode)
		}
	}

	decisions := make(map[string]contract.StrategyDecision, len(decisionIDs))
	// 分批查询，避免大股票池触及 SQLite bind 参数上限。
	const batchSize = 400
	for start := 0; start < len(decisionIDs); start += batchSize {
		end := start + batchSize
		if end > len(decisionIDs) {
			end = len(decisionIDs)
		}
		batch := decisionIDs[start:end]
		marks := strings.TrimRight(strings.Repeat("?,", len(batch)), ",")
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		rows, queryErr := s.db.QueryContext(ctx,
			"SELECT decision_id, ts_code, strategy_id, as_of, data_version, payload FROM strategy_decisions WHERE decision_id IN ("+marks+")",
			args...,
		)
		if queryErr != nil {
			return ScanResult{}, fmt.Errorf("批量读取策略决策失败: %w", queryErr)
		}
		for rows.Next() {
			var id, tsCode, strategyID, asOf, dataVersion, payload string
			if scanErr := rows.Scan(&id, &tsCode, &strategyID, &asOf, &dataVersion, &payload); scanErr != nil {
				_ = rows.Close()
				return ScanResult{}, fmt.Errorf("读取策略决策行失败: %w", scanErr)
			}
			var decision contract.StrategyDecision
			if decodeErr := json.Unmarshal([]byte(payload), &decision); decodeErr != nil {
				_ = rows.Close()
				return ScanResult{}, fmt.Errorf("解析策略决策 %s payload 失败: %w", id, decodeErr)
			}
			if decision.DecisionID != id || decision.TsCode != tsCode ||
				decision.StrategyID != strategyID || decision.DataVersion != dataVersion {
				_ = rows.Close()
				return ScanResult{}, fmt.Errorf("策略决策 %s 的索引字段与 payload 不一致", id)
			}
			want, ok := expected[id]
			if !ok {
				_ = rows.Close()
				return ScanResult{}, fmt.Errorf("数据库返回未请求的策略决策: %s", id)
			}
			parsedAsOf, parseErr := time.Parse(time.RFC3339Nano, asOf)
			if parseErr != nil {
				_ = rows.Close()
				return ScanResult{}, fmt.Errorf("解析策略决策 %s as_of 失败: %w", id, parseErr)
			}
			if want.tsCode != tsCode || !parsedAsOf.Equal(run.AsOf) ||
				!decision.AsOf.Equal(run.AsOf) || want.dataVersion != dataVersion ||
				(want.strategyID != "" && want.strategyID != strategyID) {
				_ = rows.Close()
				return ScanResult{}, fmt.Errorf("策略决策 %s 与扫描证券、as_of、策略或 data_version 不匹配", id)
			}
			decisions[id] = decision
		}
		if rowsErr := rows.Err(); rowsErr != nil {
			_ = rows.Close()
			return ScanResult{}, fmt.Errorf("遍历策略决策结果失败: %w", rowsErr)
		}
		if closeErr := rows.Close(); closeErr != nil {
			return ScanResult{}, fmt.Errorf("关闭策略决策查询失败: %w", closeErr)
		}
	}

	ordered := make([]contract.StrategyDecision, 0, len(decisionIDs))
	for _, id := range decisionIDs {
		decision, ok := decisions[id]
		if !ok {
			return ScanResult{}, fmt.Errorf("扫描结果引用的完整策略决策不存在: %s", id)
		}
		ordered = append(ordered, decision)
	}
	return ScanResult{Run: run, Items: items, Decisions: ordered}, nil
}
