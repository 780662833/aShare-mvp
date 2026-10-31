# 策略规则与决策契约（V1）

- 文档类型：规范性架构与策略规则
- 文档版本：1.0
- 适用范围：Go A 股研究辅助平台 V1
- 当前实现基线：S4.3；S4.4 在本分支补强趋势突破 M30 回踩确认
- 规则优先级：本文与对应策略版本、配置版本、测试共同构成规则依据

> 本文区分“已实现规则”和“本阶段新增规则”。示例配置值仅用于测试，不代表已验证的实盘参数或收益优势。策略输出是研究观察结论，不是交易指令。

## 1. 产品定位与总体架构

系统定位为个人 A 股研究与交易辅助工具：市场数据进入标准化数据层，经数据质量门后，由 Go 应用服务编排 CZSC 技术分析和两套独立 Go 策略，结果持久化并供扫描结果读取、观察和复盘使用。用户自行决定是否交易，系统不连接券商、不自动下单。

### 1.1 架构职责

| 层/模块 | 职责 | 禁止越界 |
|---|---|---|
| market/universe、calendar | 证券身份、上市/风险/停牌状态、交易日历 | 不判断策略买卖 |
| marketdata、storage/sqlite | D1/M60/M30 标准 K 线、质量校验和业务状态持久化 | 不生成策略信号 |
| feature | 可复算的均线、前序区间高点、量比、收盘位置等通用特征 | 不实现 CZSC 缠论算法 |
| czscclient + 独立 czsc-service | 请求/响应协议、版本检查；CZSC 计算分型、笔、中枢及信号 | 不直接写 SQLite 业务表，不作最终策略结论 |
| strategy/chan3buy | 只负责缠论三买确定性规则 | 不调用趋势突破策略 |
| strategy/trendbreakout | 只负责趋势突破确定性规则 | 不调用三买策略 |
| app/strategy | 读取输入、质量预检、调用 CZSC profiles、分别执行并保存两套策略 | 不合并两套决策或跨策略回滚 |
| scanner | 批量调度、记录扫描运行与逐股结果 | 不复制策略规则 |
| storage/sqlite scan read model | 读取扫描运行、扫描项和原始策略决策 | 不重新运行策略来重建历史结果 |
| AI / UI（后续消费方） | 解释和展示已经保存的证据与状态 | 不伪造信号、不改写规则计算结果 |

V1 采用 Go 模块化单体承载业务规则与编排，CZSC 作为独立分析服务；SQLite 是业务状态存储。不要为了架构整洁重复建设已有能力，也不要让 CZSC 服务承担 Go 业务职责。

### 1.2 核心数据流

```text
行情源
  -> 标准化 D1 / M60 / M30 Bar
  -> 数据质量与 as_of 预检
  -> CZSC 按周期分析（D1 / M60 / M30 profiles）
  -> Go app/strategy
       -> chan3buy 独立 Evaluate -> 独立 Decision -> 独立保存
       -> trendbreakout 独立 Evaluate -> 独立 Decision -> 独立保存
  -> scanner 记录运行与逐股结果
  -> scan read model 按原始 DecisionID 读取完整决策
  -> 展示 / 人工观察 / 复盘
```

## 2. 双策略隔离规则

1. `chan3buy` 与 `trend_breakout` 是两套独立策略，各有 StrategyID、StrategyVersion、ConfigVersion、输入要求、状态、证据和失效条件。
2. 两套策略可以共享同一份标准行情和 M30 CZSC 技术快照，但不能共享/覆盖彼此的策略状态。
3. 同一股票同时运行两套策略，必须分别产生结果、分别持久化。一个策略返回 WAIT_ENTRY、DATA_INCOMPLETE 或执行错误，不得抹掉另一策略已成功产生的结果。
4. 禁止把两套策略分数直接相加或以一个统一状态替代两份决策。UI 可以合并股票卡片，但必须保留策略来源和独立证据。
5. 跨策略共同的数据预检失败可以使两份决策分别进入 DATA_INCOMPLETE；这不等于某一策略失败后回滚另一策略。
6. 历史结果读取必须使用扫描项保存的原始 DecisionID，不得为展示历史而重新运行策略。

## 3. 统一决策契约

每条策略决策至少保留：

- `decision_id`、`ts_code`、`strategy_id`；
- `strategy_version`、`config_version`；
- `as_of`、`confirmed_at`、状态；
- `data_version`、CZSC profile / profile version / CZSC revision；
- EntryConditions、MissingConditions、Invalidation、RiskWarnings；
- ReferenceLevels、ReasonCodes。

状态含义以 Go contract 为准：WATCHING、CONFIRMED、WAIT_ENTRY、BUY_OBSERVATION、REDUCE_OBSERVATION、INVALIDATED、EXPIRED、DATA_INCOMPLETE、ANALYSIS_ERROR 等。BUY_OBSERVATION 不是买入指令；REDUCE_OBSERVATION 不是自动减仓命令。

决策 ID 必须由证券、策略 ID、策略版本、配置版本、as_of 和数据版本确定性生成。规则或参数变化必须提升策略版本或配置版本，避免新旧语义被误认为同一决策。

## 4. 数据与时间语义（PIT）

### 4.1 K 线契约

正式策略周期为 D1、M60、M30。Bar 的 `BarTime` 表示 K 线结束时间，必须使用显式 UTC+08:00；`FetchedAt` 表示系统采集时间。Bar 至少包含证券、周期、OHLC、成交量、成交额、复权方式、成交量单位、来源、完成状态和采集时间。

同一批次必须证券、周期、复权、来源、成交量单位一致，时间严格递增。不同周期也必须保持来源和复权口径一致。不得在策略输入中混拼供应商或复权口径。

### 4.2 PIT / fail-closed

- `as_of` 是决策时点可获得信息的截止时间，不等同于 K 线结束时间。
- 所有参与策略的 K 线必须已完成，且 `BarTime <= as_of`、`FetchedAt <= as_of`。
- 不完整 K 线只能用于展示，不能参与确认。
- 历史数据不得因后来采集而伪装成当时已知数据。
- 缺失、乱序、重复、跨周期来源/复权冲突、未来采集、技术快照不匹配均应显式失败或 DATA_INCOMPLETE；不得自动修复后继续给出正常信号。
- 交易日历、证券状态和 CZSC 快照也必须满足 as_of 一致性。

### 4.3 CZSC 边界

Go 负责数据规范化、质量门、请求编排和策略规则；CZSC 负责缠论结构与信号。Go 只解析本策略白名单中的 signal key，并校验 profile、profile version、CZSC source revision、输入 bar 数量和 AsOf。信号生命周期的 age_bars / started_at 应使用 CZSC 返回的逐信号生命周期，不以自然日差代替 K 线数量。

## 5. 缠论三买规则（StrategyID: chan3buy）

### 5.1 周期分工

- D1：日线趋势环境，当前通过 Go `ClassifyDailyTrend` 提供上下文。
- M60：主识别周期，CZSC 主信号 `60分钟_D1_三买辅助V230228`。
- M30：短周期择时辅助，不要求 M30 独立出现三买。

### 5.2 主信号与辅助证据

1. M60 主三买信号必须解析为“三买”，才进入主确认路径。
2. `V230318` / `V230319` 是辅助证据；当前实现要求至少一个辅助三买标签支持防守位重建，若辅助信号出现三卖冲突则 WAIT_ENTRY。
3. 防守位从 CZSC 最近五笔中第一笔与第三笔重叠区间计算，使用其上沿；不足五笔或不能形成有效重叠区间时，不得猜测防守价。
4. 主信号生命周期未知时 WAIT_ENTRY；超过配置的已完成 M60 bar 年龄时 EXPIRED。
5. 连续配置数量的 M60 收盘跌破可追溯防守位时 INVALIDATED。

### 5.3 M30 入场辅助

M30 CZSC `30分钟_D1_表里关系V230102` 必须给出预定义的短周期转强标签（底分或向上延伸）；M30 快照必须晚于 M60 主信号快照，最新完整 M30 收盘不得低于防守位，并且不得超过版本化配置的防守位偏离上限。条件不满足时 WAIT_ENTRY。

本节记录当前 Go 实现契约；任何调整应同步更新策略版本、配置版本和测试。

## 6. 趋势突破规则（StrategyID: trend_breakout）

### 6.1 日线突破确认

1. 前 N 日区间只取当前交易日之前已完成的 N 根日线。当前日线不参与自己的区间高点计算；不足 N+1 根总数据时数据不完整。
2. 当前已完成日线收盘价必须高于 `rangeHigh * (1 + BreakoutBufferPct/100))，并且 CZSC 对应 D1W(N+1) 事件信号返回“收盘新高”。
3. 盘中冲高、未完成日线或只有最高价越过区间，不得确认为突破。
4. 支持的 LookbackDays 为 20、30、55；对应 CZSC 信号 key 为 D1W21 / D1W31 / D1W56。
5. 若已有活动突破决策，日线收盘重新回到此前突破位下方，标记 INVALIDATED；生命周期超过配置有效期则 EXPIRED。

### 6.2 日线趋势与量价过滤

- Go `ClassifyDailyTrend` 使用 MA20 / MA60 特征分类；它是趋势环境，不单独构成买入信号。
- 市场状态 UNFAVORABLE 时不升级；UNKNOWN / NEUTRAL 是否允许继续观察由显式配置决定。
- 成交量比率使用当前量除以前 20 根已完成日线均量；当前 bar 不进入基准。
- 成交额、量比、收盘位置、CZSC 量能分类都必须达到配置门槛，否则 WAIT_ENTRY。
- 突破信号年龄来自 CZSC 生命周期；未知年龄 WAIT_ENTRY，超龄 EXPIRED。

### 6.3 M60 突破站稳 / 失败

只使用日线突破确认时间之后的完整 M60 bar：
- 最近连续 `MinM60ClosesAboveBreakout` 根 M60 收盘均须高于前序区间高点，才算站稳；
- 最近连续 `M60FailureConfirmBars` 根 M60 收盘均低于该位时，标记 REDUCE_OBSERVATION 风险；
- M60 尚未站稳时 WAIT_ENTRY。

### 6.4 M30 回踩与转强确认（S4.4 冻结规则）

**趋势突破的 BUY_OBSERVATION 必须走完 M30 回踩确认路径，不再以单独 CZSC 标签或直接延续替代真实回踩。**

1. **时间范围**：仅使用 M60 站稳确认时间之后、且已完成并通过 PIT 检查的 M30 K 线。M30 必须晚于 M60 确认。
2. **回踩窗口**：在最近 `M30RetestLookbackBars` 根 post-M60 完整 M30 K 线中查找回踩；窗口至少容纳两根 bar，最后一根保留给转强确认。
3. **回踩区间**：以 D1 前序区间高点 `rangeHigh` 为基准，容忍区间为：
   - 下沿 = `rangeHigh * (1 - M30RetestTolerancePct / 100)`
   - 上沿 = `rangeHigh * (1 + M30RetestTolerancePct / 100)`
   至少一根非最新 M30 bar 的 Low 必须落在闭区间内。
4. **收复确认**：最新完整 M30 bar 的 Close 必须严格高于 `rangeHigh`。
5. **转强确认**：最新完整 M30 Close 必须严格高于前一根完整 M30 bar 的 High；并且最新 bar 的收盘位置 `(Close-Low)/(High-Low)` 不低于 `M30MinRetestClosePosition`。
6. **CZSC 辅助标签**：M30 profile 仍须通过快照质量检查，且短周期信号为预定义底分或向上延伸。CZSC 标签是附加证据，不能代替第 3–5 条价格行为确认。
7. **追高限制**：最新 M30 Close 相对 rangeHigh 的偏离不得超过 `MaxChaseAboveBreakoutPct`。
8. **失败结果**：无回踩、M30 未收复、未突破前一根高点、收盘位置不足、数据不完整或时序错误时，保持 WAIT_ENTRY 或 DATA_INCOMPLETE；不得升级 BUY_OBSERVATION。

以下配置必须由调用方显式提供，禁止在策略代码中暗设默认值：
- `M30RetestTolerancePct`：回踩区间百分比容忍度，有限正数；
- `M30RetestLookbackBars`：回踩观察窗口，至少 2 根完整 M30 bar；
- `M30MinRetestClosePosition`：转强 bar 收盘位置阈值，范围 [0,1]。

上述字段属于 `trendbreakout.Config`，必须纳入 `Config.Version`。任一阈值改变，调用方必须变更配置版本。S4.4 将策略实现版本提升至 1.2.0。文档和测试中的数值仅是夹具，不构成推荐交易参数。

### 6.5 趋势突破失效和状态

- 日线收盘回到上一轮突破位下方：INVALIDATED。
- M60 连续收盘跌回突破位下方：REDUCE_OBSERVATION 风险提示，不代表真实持仓减仓。
- 突破信号超过最大已完成日线年龄：EXPIRED。
- 所有数据质量或技术快照不满足要求：DATA_INCOMPLETE。
- 仅当日线突破、趋势/量价过滤、M60 站稳、M30 回踩转强和追高限制全部通过，才输出 BUY_OBSERVATION。

## 7. 扫描与历史结果读取

扫描器负责批量编排和记录每次运行，不负责策略规则。扫描运行、扫描项、策略结果分别保存；双策略结果分别保留其 StrategyID 和 DecisionID。

SQLite scan result read model 应：
1. 按 scan ID 读取 run 和 items；
2. 按 items 中保存的原始 DecisionID 批量读取完整策略决策 payload；
3. 校验 DecisionID、证券代码、策略 ID、AsOf、DataVersion，以及策略结果与 DecisionIDs 的一一映射；
4. 校验 item 总数与 run 汇总状态一致，拒绝未知状态或汇总不一致；
5. 不重新运行 CZSC 或策略，不从当前行情重建历史结论。

## 8. 文档、版本和变更治理

涉及核心逻辑的修改必须遵循：

1. 先修改本规范，明确输入、周期、条件、阈值、状态转换、失效和失败语义。
2. 由用户确认规则；不得以“代码更方便”为由自行改策略定义。
3. 实施时先核对现有接口和实现，做最小差异。
4. 更新策略实现和单元测试；关键行为必须有正向、反向、边界和数据质量测试。
5. 规则语义变化提升 StrategyVersion；参数变化提升 Config.Version。决策历史必须可区分版本。
6. 更新本规范和对应实施记录，保证文档、代码、测试一致。
7. 未通过 Go 全量测试和相关 CZSC 合约 CI 前，不得宣称阶段完成。
8. 本项目当前不做回测、券商连接和自动交易；不得把观察结论描述为已验证收益策略。

## 9. 当前阶段边界

S4.4 仅补强 Go 趋势突破规则和对应测试/文档，不扩展到自动交易、回测、CZSC 算法重写、候选池或扫描调度改造。S4.4 的提交和 CI 成功不代表已经合并 main。
