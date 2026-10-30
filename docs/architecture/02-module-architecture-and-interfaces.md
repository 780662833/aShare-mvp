# A 股研究与交易辅助平台 V1：模块架构与接口设计

- 文档版本：V1.0-draft
- 前置文档：《A 股辅助平台 V1：范围冻结与双策略规范》
- 架构定位：Go 模块化单体 + 独立 CZSC 分析进程 + 本地研究数据存储
- 当前阶段：架构与接口设计，不包含代码实现

> 设计约束：V1 不做回测、不接券商、不自动交易。系统仅负责选股、择时、买卖辅助、候选跟踪和复盘。本文中的 Go interface / DTO 是接口契约草案，不是可直接编译的实现代码。

---

## 1. 总体架构决策

### 1.1 采用模块化单体，不拆微服务集群

Go 主程序以一个可部署应用承载主要业务，通过明确的包边界隔离职责。CZSC 单独作为 Python 进程运行，因为 CZSC 1.0.x 的缠论核心在 Rust，通过 PyO3 暴露给 Python。

```text
┌──────────────────────────────────────────────────────────┐
│                    Research Workbench                    │
│  市场看板 / 候选股票 / 个股分析 / 观察池 / 每日复盘 / AI 助手 │
└────────────────────────────┬─────────────────────────────┘
                             │ Application API
┌────────────────────────────▼─────────────────────────────┐
│                    Go Application Core                   │
│                                                          │
│  Universe │ Market Context │ Scanner │ Opportunity       │
│  DataHub  │ Feature Engine │ Strategy │ Decision Support  │
│  Watchlist│ Alert          │ Journal  │ Review             │
│  AI Read-only Tools        │ Scheduler                    │
└───────────┬────────────────────────────┬─────────────────┘
            │                            │
    ┌───────▼────────┐           ┌───────▼────────────┐
    │ Data Adapters  │           │ CZSC Analysis Svc │
    │ gotdx          │           │ Python + Rust     │
    │ Eastmoney      │           │ CZSC / signals    │
    │ Tushare/others │           │ Multi-frequency   │
    └───────┬────────┘           └────────────────────┘
            │
    ┌───────▼──────────────────────────────────────────────┐
    │ Storage                                               │
    │ Parquet: 历史行情与分析快照                            │
    │ SQLite: 股票主数据、策略状态、观察池、提醒、手工日志   │
    │ DuckDB: 本地批量研究查询（可选，不做业务事实账本）     │
    └──────────────────────────────────────────────────────┘
```

### 1.2 为什么不把一切都放进 Go，也不把一切都交给 CZSC

- Go：掌握股票池、数据治理、任务调度、市场环境、策略编排、决策状态、观察池和复盘。
- gotdx：仅是数据适配器，负责与通达信协议通信。
- CZSC：只负责缠论技术结构/信号计算，不负责证券范围和交易辅助系统的最终结论。
- AI：只读查询并解释系统已产生的事实和规则，不越权改写策略结果。

### 1.3 模块边界的硬规则

1. Provider 不能直接被策略包调用；所有策略只能读取标准化数据接口。
2. 三买策略不能调用趋势突破策略，趋势突破策略也不能调用三买策略。
3. 两策略共享市场环境、行情和基础特征，但独立配置、状态、证据和版本。
4. CZSC 返回的结构/信号是分析证据，不是最终策略决策。
5. Watchlist 负责跟踪，不负责生成策略信号。
6. Review 负责总结事实，不反向修改历史信号。
7. AI 不直接写策略状态、股票池或用户交易记录；需要用户确认的操作交给显式 UI 流程。

---

## 2. Go 主项目模块划分

建议 Go 业务代码按以下边界组织。名称可随现有仓库风格调整，但职责不应混合。

```text
cmd/
  research-app/             # 应用入口与依赖装配

internal/
  app/                      # 用例编排、任务入口、应用服务
  market/
    universe/               # 股票池、证券分类、上市状态
    calendar/               # A股交易日历、交易时段、周期边界
    context/                # 大盘环境、市场宽度、行业强弱
  datahub/
    provider/               # 数据源接口和来源元信息
    adapter/gotdx/           # gotdx 适配器
    adapter/eastmoney/       # 东方财富补充/交叉核验
    adapter/tushare/         # 财务/基础资料适配器
    normalize/              # 代码、时间、周期、价格口径标准化
    quality/                # 缺失、重复、异常、陈旧检查
    repository/              # 标准数据查询与存储
  feature/                   # 技术指标、量价特征、相对强弱
  czscclient/                # CZSC 服务客户端与 DTO 转换
  scanner/                   # 全市场扫描任务、分批、并发和续跑
  strategy/
    contract/                # 策略接口、输入输出 DTO、状态定义
    chan3buy/                # 缠论三买策略规则
    trendbreakout/           # 趋势突破策略规则
  opportunity/               # 股票候选聚合、去重展示、策略冲突标记
  decision/                  # 买入/持有/减仓/卖出观察结论
  watchlist/                 # 自选、候选跟踪、状态历史
  alert/                     # 价格/信号/结构变化提醒
  journal/                   # 用户手工交易与持仓备注
  review/                    # 市场、个股、策略及个人交易复盘
  ai/                        # 只读工具、证据解释、报告生成
  storage/                   # SQLite/Parquet/DuckDB 基础设施
  config/                    # 策略参数、数据源和系统配置
  observability/             # 日志、指标、扫描运行记录

czsc-service/                # 独立 Python/CZSC 服务（独立目录或独立仓库）
  api/
  engine/
  dto/
  tests/
```

### 2.1 模块职责与禁止事项

| 模块 | 负责 | 不负责 |
|---|---|---|
| `universe` | A股普通股票范围、证券身份、状态、股票池快照 | 行情 K 线、策略评分 |
| `calendar` | 交易日、交易时段、日线/分钟线边界 | 技术指标 |
| `datahub` | 多数据源接入、标准化、数据质量、存储 | 买卖规则 |
| `context` | 大盘、宽度、行业、市场状态 | 某只股票的最终买卖结论 |
| `feature` | 均线、成交额、量比、波动、相对强弱等通用特征 | 缠论中枢与笔的重复实现 |
| `czscclient` | 调用 CZSC、协议转换、超时/版本检查 | 自己实现 CZSC 算法 |
| `scanner` | 选取股票和周期、分批调度、结果运行记录 | 具体策略判断 |
| `strategy/*` | 单策略确定性规则和状态更新 | 行情抓取、写 UI、调用其他策略 |
| `opportunity` | 多策略候选合并呈现、去重、冲突标签 | 合并成一个无来源总分 |
| `decision` | 把策略证据转成买卖观察状态 | 实际下单、账户执行 |
| `watchlist` | 人工观察、分组、跟踪与提醒订阅 | 重新计算策略信号 |
| `journal` | 用户手工交易/持仓备注 | 券商自动同步和执行 |
| `review` | 生成可追溯复盘快照与文本 | 回测、改写历史决策 |
| `ai` | 解释、问答、摘要、研究辅助 | 独立制造未验证信号或改写系统事实 |

---

## 3. 数据管线与存储边界

### 3.1 数据管线

```text
Provider Raw Response
        │
        ▼
Raw Capture / Source Metadata
        │
        ▼
Normalize
  代码 / 时间 / 交易日 / 周期 / 价格单位 / 复权口径
        │
        ▼
Quality Gate
  缺失 / 重复 / 乱序 / 异常价格 / 陈旧 / 不完整K线
        │
        ├── 不合格：记录质量状态，不进入正式策略分析
        │
        ▼
Canonical Market Data
        │
        ├── Daily / 60m / 30m Bars
        ├── Quote Snapshot
        ├── Fundamentals & Announcements
        └── Market / Sector Data
```

### 3.2 数据存储建议

V1 可以采用低复杂度本地存储：

| 存储 | 内容 | 定位 |
|---|---|---|
| Parquet | 历史 K 线、行情快照、批量分析输入输出 | 研究数据文件层 |
| SQLite | 股票主数据、扫描任务、策略决策、观察池、提醒、手工日志、复盘索引 | 业务状态主存储 |
| DuckDB（可选） | 跨股票、跨日期批量查询和临时研究 | 查询加速，不作为业务状态权威 |

SQLite 保存业务状态，Parquet 保存大量时序行情。不要把每一根历史 K 线都塞进业务表，也不要让 CZSC 服务直接修改 SQLite 业务表。

### 3.3 数据版本与时间语义

每份标准化 K 线至少需要：
- `symbol`、`freq`、`bar_start`、`bar_end`；
- `open`、`high`、`low`、`close`、`volume`、`amount`；
- `is_complete`；
- `source`、`source_timestamp`、`ingested_at`；
- `adjustment`、`adjustment_version`；
- `quality_status`、`data_version`。

所有业务时间统一采用 `Asia/Shanghai` 作为交易所时间语义。数据库中可用 UTC 存储时间戳，但接口必须显式携带时区/交易日语义，不可将本地时区默认值当作交易所时间。

`as_of` 表示系统作出判断时可获得的信息截止时点；`bar_end` 表示这根 K 线对应的区间结束时间。两者不可混用。

### 3.4 复权策略

- 研究 K 线必须显式声明 `qfq/hfq/none` 及版本。
- 不同复权口径的数据不得混在同一段 CZSC 序列中。
- 实际现价和用户手工成交价保持未复权口径。
- 公司行动及复权因子变更后，受影响的结构分析状态需要标记为需重算，不能悄悄覆盖旧结果。

---

## 4. 关键领域对象（概念 DTO）

以下为跨模块概念结构，实施时可映射为 Go struct。字段可根据真实接口适当增减。

### 4.1 标准 K 线 `Bar`

```go
type Bar struct {
    Symbol            string
    Frequency         string // 1d / 60m / 30m
    StartAt           time.Time
    EndAt             time.Time
    Open              float64
    High              float64
    Low               float64
    Close             float64
    Volume            float64
    Amount            float64
    Complete          bool
    Source            string
    Adjustment        string // qfq / hfq / none
    AdjustmentVersion string
    DataVersion       string
    QualityStatus     string
}
```

### 4.2 市场环境 `MarketContext`

```go
type MarketContext struct {
    AsOf          time.Time
    MarketStatus  string // FAVORABLE / NEUTRAL / UNFAVORABLE / UNKNOWN
    IndexFacts    []Evidence
    BreadthFacts  []Evidence
    SectorFacts   []Evidence
    RiskWarnings  []Evidence
    DataVersion   string
}
```

### 4.3 分析证据 `Evidence`

```go
type Evidence struct {
    Code        string
    Description string
    Value       any
    Frequency  string
    ObservedAt  time.Time
    Source      string
    Confidence  string // HIGH / MEDIUM / LOW / UNKNOWN
}
```

`Evidence` 是解释和审计的基础。重要结论不能只保存一段自然语言，应有结构化证据和来源。

### 4.4 CZSC 分析结果 `TechnicalSnapshot`

```go
type TechnicalSnapshot struct {
    AnalysisID     string
    Symbol         string
    AsOf           time.Time
    DataVersion    string
    CZSCVersion    string
    Frequencies    []FrequencySnapshot
    Signals        []TechnicalSignal
    Structures     []StructureEvidence
    AnalysisStatus string // OK / DATA_INCOMPLETE / ERROR
}
```

`FrequencySnapshot`、`TechnicalSignal`、`StructureEvidence` 应由 Go 与 CZSC 服务共同确定稳定的 JSON 契约；不能将 CZSC 内部 Rust/Python 对象直接透传为长期业务数据库格式。

### 4.5 策略决策 `StrategyDecision`

```go
type StrategyDecision struct {
    DecisionID       string
    Symbol           string
    StrategyID       string
    StrategyVersion  string
    ConfigVersion    string
    AsOf             time.Time
    ConfirmedAt      *time.Time
    Status           string
    InternalGrade    string
    EntryConditions  []Evidence
    Invalidation     []Evidence
    RiskWarnings     []Evidence
    ReferenceLevels  []PriceLevel
    ReasonCodes      []string
    DataVersion      string
    AnalysisID       string
}
```

不建议 V1 就定义跨策略总分。`InternalGrade` 只在策略内部使用；跨策略工作台展示策略名称、各自等级及证据。

---

## 5. 数据层接口草案

### 5.1 行情 Provider

```go
type QuoteProvider interface {
    Name() string
    FetchSecurityList(ctx context.Context, market Market) ([]RawSecurity, error)
    FetchQuotes(ctx context.Context, symbols []string) ([]RawQuote, error)
    FetchBars(ctx context.Context, req BarRequest) ([]RawBar, error)
}
```

数据适配器只返回原始/来源明确的数据，不在 Provider 内部直接执行策略过滤。gotdx 是 `QuoteProvider` 的一种实现；东方财富等作为其他实现。

### 5.2 标准行情查询

```go
type MarketDataRepository interface {
    UpsertBars(ctx context.Context, bars []Bar) error
    GetBars(ctx context.Context, req BarQuery) ([]Bar, error)
    GetLatestBar(ctx context.Context, symbol, frequency string) (Bar, error)
    GetDataVersion(ctx context.Context, req BarQuery) (string, error)
}
```

### 5.3 股票池

```go
type UniverseService interface {
    Sync(ctx context.Context) (UniverseSyncReport, error)
    Snapshot(ctx context.Context, asOf time.Time, filter UniverseFilter) (UniverseSnapshot, error)
    GetSecurity(ctx context.Context, symbol string, asOf time.Time) (Security, error)
}
```

必须生成扫描时使用的 `UniverseSnapshot`，防止扫描期间股票池变化导致结果无法解释。

---

## 6. CZSC 分析服务接口草案

### 6.1 服务边界

Go 将已标准化、已通过质量门的 OHLCV 序列提交给 CZSC。CZSC 不自行连接 gotdx、东方财富或问财，不直接查询 Go 的业务数据库。

### 6.2 请求契约

```json
{
  "request_id": "analysis-request-id",
  "symbol": "600519.SH",
  "as_of": "2026-10-30T15:00:00+08:00",
  "data_version": "market-data-snapshot-v1",
  "czsc_version": "pinned-version",
  "frequencies": ["日线", "60分钟", "30分钟"],
  "bars": {
    "日线": [],
    "60分钟": [],
    "30分钟": []
  },
  "signal_config": []
}
```

真实传输时允许按频率分开发送或使用压缩载荷；契约必须保持含义不变。只接受 `Complete=true` 的 K 线用于确认状态。盘中观察数据应走独立字段或独立请求，不可混入已完成 bars。

### 6.3 响应契约

```json
{
  "request_id": "analysis-request-id",
  "analysis_id": "analysis-id",
  "symbol": "600519.SH",
  "as_of": "2026-10-30T15:00:00+08:00",
  "data_version": "market-data-snapshot-v1",
  "czsc_version": "pinned-version",
  "status": "OK",
  "frequency_results": [],
  "signals": [],
  "structures": [],
  "warnings": []
}
```

响应应输出业务需要的结构化 DTO，例如分型、笔、中枢的必要坐标/时间范围和信号 key/value；不要求将 CZSC 所有内部字段都暴露给 Go。

### 6.4 调用和状态管理

- V1 可以使用 localhost HTTP/JSON；若未来批量数据量大，再考虑 gRPC 或文件批处理。
- 请求必须带 `request_id`、`analysis_id`、`as_of`、`data_version` 和 CZSC 版本。
- 一个股票的增量更新按时间顺序串行；不同股票并行。
- CZSC 服务崩溃后，Go 可以用历史标准 K 线重新构建分析状态。
- 数据修订、复权变化或信号配置变化时，产生新分析版本，不覆盖旧分析快照。
- 必须有健康检查、超时、请求大小限制、并发上限和错误状态。

### 6.5 版本锁定

CZSC 更新可能带来信号名称、参数模板或 API 变化。实施时必须锁定具体 release/tag/commit，保存一份信号注册清单和兼容性测试。当前公开文档列出的 `CzscSignals`、`CzscTrader`、`generate_czsc_signals` 等入口属于 CZSC 1.0 的接口体系；V1 只需要使用与本系统相符的信号分析能力，不需要让 CZSC `Position` 管理用户真实持仓。

---

## 7. 策略接口与双策略独立性

### 7.1 策略接口

```go
type Strategy interface {
    ID() string
    Version() string
    RequiredFrequencies() []string
    Evaluate(ctx context.Context, input StrategyInput) (StrategyDecision, error)
}
```

策略输入：

```go
type StrategyInput struct {
    Security       Security
    AsOf           time.Time
    Bars           map[string][]Bar
    Features       FeatureSet
    MarketContext  MarketContext
    Technical      *TechnicalSnapshot
    ConfigVersion  string
}
```

策略实现必须是确定性的：相同输入版本、相同配置版本应产生相同决策。策略不得自行抓取行情、读写用户观察池或调用另一套策略。

### 7.2 双策略注册

```go
registry.Register(chan3buy.New())
registry.Register(trendbreakout.New())
```

以上仅表达注册概念，不是当前代码。每次扫描由策略调度器选择策略、周期和股票范围，分别执行并保存结果。

### 7.3 策略结果持久化

建议唯一性基于：

`symbol + strategy_id + strategy_version + config_version + signal_instance_id`

同一策略的新状态变化写入状态历史，不直接覆盖事件轨迹。不同策略的结果必须分开存储。

### 7.4 机会聚合接口

```go
type OpportunityService interface {
    BuildDailyBoard(ctx context.Context, asOf time.Time) (OpportunityBoard, error)
    GetBySymbol(ctx context.Context, symbol string, asOf time.Time) (OpportunityDetail, error)
}
```

聚合层只负责按股票合并展示，并标识 `SINGLE_STRATEGY`、`DUAL_STRATEGY`、`STRATEGY_CONFLICT` 等关系。它不能改变单策略原始决策。

---

## 8. 扫描调度与并发模型

### 8.1 扫描流程

```text
Create Scan Run
   │
   ▼
Freeze Universe Snapshot
   │
   ▼
Fetch / Validate Required Bars
   │
   ▼
Calculate Common Features + Market Context
   │
   ├── Chan 3-buy strategy batch
   │
   └── Trend breakout strategy batch
   │
   ▼
Persist Independent Decisions
   │
   ▼
Aggregate Opportunity Board
   │
   ▼
Update Watchlist / Emit Alerts / Prepare Review
```

### 8.2 并发原则

- 股票之间并行；同一股票的多周期增量状态按时间串行。
- 全市场任务分批执行，记录批次、进度、失败和重试。
- 单只股票分析失败不能让整个全市场任务失败。
- 部分市场/数据源失败必须标记为部分完成，不允许静默地将不完整结果显示为“全市场扫描完成”。
- CZSC 服务并发量应可配置，并通过压测确定，不根据理论线程数无限并行。

### 8.3 扫描运行记录

每次扫描至少保存：
- `scan_id`、开始/结束时间；
- `universe_snapshot_id`；
- 股票总数、已完成、跳过、失败、数据不足数量；
- 策略版本和配置版本；
- 行情数据版本及 CZSC 版本；
- 最终运行状态：`SUCCESS`、`PARTIAL`、`FAILED`。

---

## 9. 决策辅助、观察池与复盘接口

### 9.1 Decision Support

策略先输出 `StrategyDecision`，Decision 模块再结合用户配置的风险偏好、市场风险提示和手工标注生成面向 UI 的摘要。V1 不实现真实账户仓位风险计算，不假定系统知道用户账户余额。

### 9.2 Watchlist

```go
type WatchlistService interface {
    Add(ctx context.Context, req AddWatchRequest) error
    Remove(ctx context.Context, req RemoveWatchRequest) error
    List(ctx context.Context, query WatchlistQuery) (WatchlistBoard, error)
    AppendNote(ctx context.Context, note WatchNote) error
}
```

观察池允许记录用户自选、策略候选、手工持有标记、计划关注价格和备注。策略状态由策略模块生成，Watchlist 只引用策略决策 ID。

### 9.3 Alert

提醒由策略状态变化、价格触及关键位、信号过期和数据质量异常触发。提醒必须包含触发类型、观察/确认属性、对应 K 线时间、来源策略和证据链接。

### 9.4 Journal 与 Review

手工交易记录至少保存：股票、买/卖方向、成交日期时间、数量、实际成交价、费用（可选）、关联策略/决策 ID（可选）、个人理由和备注。

复盘流程：
- 汇总当天市场环境；
- 汇总候选新增、维持、确认、失效、过期；
- 汇总两套策略各自触发与证据；
- 将用户手工交易和当时已存在的策略结论做时间对照；
- 生成 AI 复盘草稿，用户可修订和确认。

复盘应保留 `review_snapshot_id` 和生成时使用的数据/决策版本，不得回写策略历史状态。

---

## 10. UI 工作台建议

V1 建议以任务流为主，而不是堆很多独立指标菜单。

1. **市场总览**：大盘、市场宽度、行业强弱、当日风险状态、扫描覆盖情况。
2. **机会中心**：全部候选、三买候选、趋势突破候选、双策略共振、策略冲突。
3. **个股研究页**：日线/60分钟/30分钟 K 线、CZSC 分型/笔/中枢、信号时间、入场观察和失效条件。
4. **策略详情页**：规则版本、参数、当前结果、命中证据、状态变化历史。
5. **观察池**：自选、待观察、等待入场、手工持有、已失效/已过期。
6. **复盘中心**：市场复盘、策略复盘、个股复盘、个人交易日记。
7. **数据与配置**：数据源健康、数据质量、CZSC 版本、策略参数和提醒设置。

AI 助手应嵌入页面上下文，解释当前选中的股票、策略和证据；不能用一个通用聊天窗口替代明确的策略状态页面。

---

## 11. 测试与验收策略（不含回测）

V1 不建设回测，但仍然需要单元测试和历史数据样本验证，二者不等于回测。

### 11.1 数据层测试
- 证券代码标准化和市场识别；
- 交易日历、日线/分钟线边界；
- 未完成 K 线过滤；
- 重复、乱序、缺失和异常数据检测；
- 复权版本变化后的缓存失效和重算标记。

### 11.2 CZSC 集成测试
- 固定一组已审阅 K 线样本，验证输入输出契约；
- 验证 60m 三买信号与当前 CZSC 锁定版本一致；
- 验证数据不足、信号 `其他`、服务超时等场景；
- 验证同一股票按时间更新与从历史重建得到一致结果；
- 验证 30m/60m 时间对齐和未完成 K 线门控。

### 11.3 策略规则测试
- 每个策略针对“应命中、应等待、应失效、应过期、数据不足”建立固定样本；
- 对边界价格、刚好突破、盘中突破但收盘回落等情况建立测试；
- 验证两个策略互不调用、互不覆盖；
- 验证双策略命中和策略冲突的聚合展示。

### 11.4 运行质量测试
- 部分数据源失败；
- 个别股票分析失败；
- CZSC 服务重启；
- 扫描任务中断后续跑；
- 扫描结果重复执行幂等；
- 扫描结束时准确报告覆盖率及未完成原因。

---

## 12. 推荐实施顺序

仍然遵循先数据、再策略、再工作台的依赖关系：

1. **架构基线确认**：冻结本模块边界、数据口径、策略输入输出和服务边界。
2. **数据中心最小实现**：股票池、交易日历、gotdx 适配、标准 K 线、质量门、历史存储。
3. **CZSC 适配最小实现**：版本锁定、请求响应、60m/30m/日线结构结果、故障恢复。
4. **三买策略实现**：只实现已确认的策略规范，不扩展全量 CZSC 信号。
5. **趋势突破策略实现**：使用相同的公共数据和特征层，独立实现规则。
6. **机会中心和观察池**：统一展示、策略来源、冲突、提醒及状态历史。
7. **复盘和 AI 助手**：最后接入结构化证据，生成可追溯复盘。

在用户明确批准具体实施阶段前，本文件不授权修改现有项目代码、创建分支、提交或推送。

---

## 13. 关键技术风险与控制

| 风险 | 控制方式 |
|---|---|
| CZSC 版本升级造成信号/API 变化 | 固定 tag/commit、版本化协议、集成测试 |
| TDX 行情不完整或短暂不可用 | Provider 健康状态、重试、缓存、质量状态，不静默降级 |
| 多周期 K 线错位 | 统一交易日历和时间边界，加入周期对齐测试 |
| 复权数据变化导致结构漂移 | adjustment_version、历史快照和重算标记 |
| 全市场扫描部分失败 | scan run 记录覆盖率，PARTIAL 不冒充 SUCCESS |
| 策略结果不可解释 | Evidence 和 ReasonCode 必填 |
| AI 产生虚假判断 | 只基于现有结构化证据回答，缺数据时明确未知 |
| 两策略分数不可比 | 不做简单总分，分别显示策略状态和证据 |
| 盘中未收 K 线误触发 | 完成状态门控，区分 FORMING 与 CONFIRMED |

---

## 14. 参考资料

- go-stock：<https://github.com/ArvinLovegood/go-stock>
- gotdx：<https://github.com/bensema/gotdx>
- CZSC：<https://github.com/waditu/czsc>
- CZSC README：<https://github.com/waditu/czsc/blob/master/README.md>
- CZSC public API：<https://github.com/waditu/czsc/blob/master/docs/public_api.md>
- CZSC changelog：<https://github.com/waditu/czsc/blob/master/CHANGELOG.md>

说明：CZSC 当前文档说明核心分析 API 位于 Rust 扩展并通过 Python 门面暴露；实际开发必须以锁定的具体版本和集成测试为准。