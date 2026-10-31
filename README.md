# aShare-mvp

个人 A 股研究与交易辅助平台 V1。

**当前状态：S1–S5 阶段性交付完成，进入实际使用反馈期。** 当前不预设 S6，不因阶段结束而自动增加新功能。

## 产品范围

- 选股、择时、买卖观察、扫描结果留痕、历史决策查询与复盘。
- 首发策略：缠论三买、趋势突破；两套策略独立运行、独立保存决策。
- 策略输出是研究观察，不是交易指令；交易决定由用户作出。
- 不做回测、不接券商、不自动下单、不做订单执行。
- Go 模块化单体承载业务规则与编排；CZSC 通过独立 HTTP/JSON 服务复用。
- SQLite 优先；Parquet / DuckDB 延后。
- 行情优先复用 gotdx 公开 API；保留 Provider 边界。

## 架构与阶段总结

- [V1 架构文档索引](docs/architecture/README.md)：产品范围、模块架构、数据中心、D1/D2 数据契约和策略规则。
- [S1–S5 最终交付总结与项目阶段性状态](docs/implementation/07-s1-s5-stage-closeout.md)：阶段交付清单、当前架构、数据流、策略规则边界、未交付能力和使用反馈期事项。

## 当前阶段（截至 2026-10-31）

| 阶段 | 交付内容 | 状态 |
|---|---|---|
| S1 | D1/D2 数据模型、SQLite 基础层、gotdx 适配、CZSC HTTP/JSON 服务、数据质量加固 | 已完成 |
| S2 | 缠论三买与趋势突破两套独立 Go 策略 | 已完成 |
| S3 | 单股双策略评估应用服务、PIT/新鲜度检查、决策持久化与生命周期校验 | 已完成 |
| S4.3 | 扫描编排、扫描运行/逐股结果留痕、原始 DecisionID 关联读取 | 已完成 |
| S4.4 | 趋势突破 M30 真实回踩与转强确认规则 | 已完成 |
| S5 | 历史策略决策筛选、稳定排序、分页、总数与原始 payload 查询 | 已完成 |

S5 通过 PR [#6](https://github.com/780662833/aShare-mvp/pull/6) 合并到 `main`，merge commit 为 `6847d9237ddb2e1b2ead8e0671ee18500dd8e59c`。合并后 Go tests 已通过；CZSC contract/native workflow 的最终状态请查看 [GitHub Actions](https://github.com/780662833/aShare-mvp/actions)。

## 当前不包含

- Hertz HTTP API 产品入口和完整研究工作台/UI。
- 自动行情同步、完整 ST/停牌历史、交易所公告日历自动更新。
- 回测、Walk-forward、参数优化和收益验证。
- 组合管理、资金分配和持仓风险预算。
- 券商 API、订单执行和自动交易。
- AI 自主决策或自动交易代理。

以上项目不构成当前阶段的待办。是否规划下一阶段，等待实际使用反馈后再决定。

## 本地运行

需要 Go 1.27.2 或更高版本。

```bash
go test ./...
go run ./cmd/ashare-mvp -db ./data/ashare.db
```

CZSC 服务：

```bash
cd czsc-service
python -m venv .venv
pip install -r requirements.txt
python app.py
```

默认 CZSC 监听 127.0.0.1:8091。

## 实施原则

1. 先检查项目/依赖已有能力，再决定是否补代码。
2. gotdx / CZSC 使用其公开 API，不复制协议解析或缠论算法。
3. go-stock 是 Wails 应用，其 backend/data 与应用 DB、logger、配置等存在耦合；当前仓库不导入整个应用，也不复制其多源降级实现。先用 gotdx 作为可运行数据入口，保留 Provider 边界，待 go-stock 数据能力可独立调用时再接入。
4. 不完整的行情名单、未知交易日、未知证券状态和质量不合格 K 线不得伪装成成功结果。
5. 任何策略输出都只是研究辅助，不是交易指令。
