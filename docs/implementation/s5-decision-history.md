# S5：历史策略决策查询实施记录

- 阶段：S5
- 分支：`feature/s5-decision-history`
- 基线：S4.4 `f0f1387f6a0e7430676d343f7ff7254bf1e163e8`
- 范围：复用既有 `strategy_decisions` 表，提供历史查询和分页；不新增表、不改变决策写入、不重算历史策略。

## 实施内容

1. 新增 `Store.ListStrategyDecisions`，支持证券代码、策略 ID、状态、起止决策时间过滤。
2. 使用 `limit/offset` 分页，限制每页 1–200 条；返回过滤后的总数。
3. 固定排序为 `as_of DESC, decision_id DESC`，在同一只读事务中读取总数和当前页。
4. 返回数据库中保存的完整 `contract.StrategyDecision` payload。
5. 保留 `SaveStrategyDecision` 的幂等与冲突保护，以及 S4.3 扫描结果按原始 DecisionID 读取的行为。

## 验收范围

- 多条件组合过滤及包含式时间边界；
- 稳定排序、分页和总数；
- 完整决策 payload 往返；
- 无效分页、状态和时间范围参数拒绝；
- Go 全量测试与对应 CI。

## 明确不包含

不新增决策表或迁移，不修改策略规则、策略执行、扫描流程、S4.3 read model，不加入自动交易、回测或其他历史分析功能。S5 完成不代表合并到 `main`。
