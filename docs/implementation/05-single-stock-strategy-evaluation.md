# 第 05 段：单股双策略评估应用服务（S3）

## 目标与范围

将已完成的 D1/D2 数据层、CZSC HTTP 客户端和 S2 两套 Go 策略评估器连接成单股评估用例。

本段只提供 Go application service，可由后续 CLI、HTTP API 或工作台调用；本段不创建 Hertz Handler，不接入扫描器或定时任务。

**明确不做：**
- 全市场扫描、扫描调度、候选池批量生成；
- 行情自动抓取和证券每日状态自动同步；
- AI 自主决策、跨策略评分或合并策略状态；
- 回测、券商 API、自动下单和订单执行。

## 评估流程

```text
EvaluateSymbol(ts_code, as_of)
    |
    +-- 读取 StockBasic / SecurityDailyState / TradeCalendar
    |
    +-- 读取截至 as_of 的 D1 / M60 / M30 完整 K 线
    |
    +-- 根据交易日历和交易时段检查各周期最新 bar 是否陈旧
    |
    +-- 数据不完整：两套策略分别输出 DATA_INCOMPLETE
    |
    +-- CZSC M60: chan3buy_m60_v1
    +-- CZSC M30: chan3buy_m30_timing_v1
    +-- CZSC D1 : trend_breakout_d1_v1
    |
    +-- 分别加载各自上一条 StrategyDecision
    |
    +-- 独立执行 chan3buy / trendbreakout
    |
    +-- 分别持久化两条 StrategyDecision
```

M30 profile 为两套策略共享的短周期择时分析，因此一次评估只请求 CZSC 三次，不重复分析 M30。

## 数据仓储接口

SQLite 增加只读方法：
- `GetStockBasic`
- `GetSecurityDailyState`：读取不晚于指定交易日的最近状态，并由应用层确认状态日期符合预期；
- `GetTradeDay` / `GetLatestOpenTradeDay`
- `ListRecentBars`：过滤 `bar_time <= as_of`、只读取 `is_complete=1`，按时间倒序限量查询后再正序返回。

`ListRecentBars` 限制读取上限为 5000 根；应用配置显式声明复权口径和所需历史长度。

策略决策通过 migration `002_strategy_decisions.sql` 持久化：
- 决策标识、证券、策略/实现版本、配置版本；
- as_of、首次确认时间、状态、数据版本；
- 完整 JSON payload（包括证据、缺失条件、风险提示、防守/突破参考位和 CZSC profile 引用）。

`decision_id` 幂等；相同 ID 若对应不同 payload 则返回冲突，不静默覆盖。读取上一条决策使用严格早于当前 `as_of` 的记录，避免同一快照重放时把本次记录读作上一状态。

## CZSC 生命周期契约

CZSC 响应增加 `signal_lifecycles`，按 CZSC 原始 signal key 返回：
- `age_bars`：当前连续相同信号值相对起始 bar 的已完成 bar 数，最新 bar 为 0；
- `started_at`：该连续信号值首次出现的 bar 时间。

生命周期直接由本次 `generate_czsc_signals` 输出的历史行计算，不以自然日差推导 K 线数量。

Go 客户端校验：
- 生命周期 key 必须存在于 `signals`；
- age 不得为负数；
- `started_at` 必须是 +08:00，且不能晚于响应 `as_of`；还必须精确匹配本次请求中的一根 K 线，`age_bars` 必须与该 bar 到最新 bar 的序列距离一致。

策略只读取本策略所需的主信号 key。三买读取 M60 主三买信号；趋势突破读取当前配置窗口对应的 D1 突破信号。决策的 `ConfirmedAt` 取该信号 `started_at`，而不是每次评估都重置成最新快照时间。

## 按周期进行新鲜度检查

D1/M60/M30 最新完整 bar 的时间并不总相同，不能强制三个周期的 bar 同时结束。

应用服务根据本地交易日历和固定交易时段，分别计算 `as_of` 时点各周期应有的最新完整 bar：
- D1：日线收盘宽限期后使用当日 15:00 bar，否则使用上一个开市日；
- M60：按 10:30、11:30、14:00、15:00 的结束时刻；
- M30：按 10:00、10:30、11:00、11:30、13:30、14:00、14:30、15:00 的结束时刻。

当前阶段采用已完成 bar 结束后 1 分钟的宽限规则，与 D2 gotdx 适配器当前契约一致。若交易日历缺失、日历记录在 `as_of` 之后才采集、证券状态缺失/过期、任一所需周期的最新 bar 不符合预期，策略输入带入明确质量问题。CZSC 调用之前还会检查每一根历史 bar 的 `fetched_at` 不晚于 `as_of`、所有 bar 均已完成，以及跨周期数据源和复权口径一致；两套策略都不能把不完整数据转成正常信号。

这个检查不代替真实供应商分钟线时间标签验收；若 gotdx 实际返回时间与契约不一致，应先核验数据样本，不得为了通过测试而放宽新鲜度门。

## 当前限制

- 市场宽度、指数趋势和行业环境数据尚未接入，`MarketContext` 保持 `UNKNOWN`，由策略配置决定未知环境是否允许观察。
- gotdx 证券名单不能补足可靠的历史 ST/停牌状态；服务只消费已核验并入库的每日状态，缺失时 fail-closed。
- S3 不自动获取缺失 K 线。调用方须先完成既有数据同步流程。
- 当前服务是 Go 包级用例，没有 CLI 命令、Hertz API 或 UI 调用入口。

## 验收

- 仓储测试：as_of、完整状态、limit、幂等决策读写。
- 生命周期测试：CZSC 返回按 key 的年龄和首次出现时间；Go 客户端拒绝不存在的 key、负年龄和未来起始时间。
- 策略测试：两套策略分别使用对应主信号生命周期，不重置首次确认时间。
- 应用服务测试：三次 CZSC profile 调用、两套策略独立输出与持久化、M30 晚于 M60 的盘中时间关系、质量预检失败时不调用 CZSC 且 fail-closed。
- CI 命令：`go test ./...`；Python/CZSC contract and native signal workflow。

本文件记录 S3 分支实现边界和验收方式；只有分支 CI 与代码审查通过、PR 合并后，才能将 S3 标记为 main 已完成。
