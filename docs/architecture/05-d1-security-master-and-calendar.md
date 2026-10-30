# D1：证券主数据与交易日历设计（复用优先版）

> 项目：个人 A 股研究与交易辅助平台 V1  
> 文档类型：架构与数据契约设计；本阶段不修改业务代码  
> 版本：D1.2（复用优先、最小补缺）  
> 日期：2026-10-30  
> 技术基线：Go + CloudWeGo Hertz；SQLite 优先；gotdx 负责行情接入；CZSC 负责缠论分析

---

## 0. 本次优化结论

D1 不应重新实现 go-stock、gotdx 已经具备的能力，也不应为了“架构完整”提前建立大量事件表和抽象接口。

**采用原则：优先调用现有接口；优先复用现有数据模型；只有现有接口无法满足 V1 的明确验收要求时，才增加最小适配。**

本版收敛为：

1. 证券名单、证券基础资料优先复用 go-stock 的实现和 `StockBasic` 字段模型。
2. 行情协议直接使用 gotdx 的高阶 Go API，不自行实现通达信协议，不重复包装现成的列表、快照、K 线方法。
3. 普通 A 股范围通过 gotdx 初筛 + 证券基础资料核对得到，不另建一套庞大的证券分类引擎。
4. ST / 停牌只保留 V1 需要的当前状态及必要的按日快照，不建设复杂的多类事件溯源系统。
5. 交易日历保留“交易所公告优先、Tushare 辅助”的既定原则，只需一张本地日历表，不建设公告数据库、日历版本服务等复杂模块。
6. CZSC 只消费标准化 K 线并完成缠论分析；D1 不重复实现 CZSC 的结构、信号或时段分析能力。

V1 不做回测，因此不需要为未来可能发生的 PIT 回测预先建设全套 `known_at` 事件溯源、每日全市场状态物化和复杂历史快照系统。但从上线开始，应保留每日实际使用的证券状态快照，避免复盘时只能看到当前状态。

---

## 1. 三个项目的复用边界

### 1.1 go-stock：优先复用证券名单与基础资料逻辑

已核对到的现有能力：

| 现有代码/模型 | 已有功能 | D1 处理 |
|---|---|---|
| `TdxKLineApi.GetAllStockList()` | 通过 gotdx 拉取 SZ、SH、BJ 市场证券列表；返回代码、名称、市场、昨收、小数位和成交量单位 | 优先复用名单获取逻辑 |
| `TdxKLineApi.SyncStockBasicToDB()` | 将 TDX 名单同步到 `StockBasic` | 复用 upsert 思路，不照搬其纯 `Symbol` 去重键 |
| `StockBasic` | 已有代码、名称、市场、交易所、币种、上市状态、上市日期、退市日期、行业、地区等字段 | 作为证券基础资料模型基线，不再重复设计同类 `security_master` |
| `StockDataApi.GetStockBaseInfo()` | 调用 Tushare `stock_basic` 补齐证券基础信息 | 复用字段映射和数据同步逻辑 |
| SQLite + GORM 初始化 | 已有 SQLite、WAL、busy timeout 等实践 | 沿用 SQLite 技术选型；不要求引入另一套数据库 |

`StockBasic` 已有字段包括：`TsCode`、`Symbol`、`Name`、`Area`、`Industry`、`Fullname`、`Ename`、`Cnspell`、`Market`、`Exchange`、`CurrType`、`ListStatus`、`ListDate`、`DelistDate`、`IsHs` 等。V1 不需要重新发明同义字段。

**需要保留的两个小修正点：**

- `GetAllStockList()` 的返回值是证券列表指针，不包含逐市场成功/失败状态；内部单市场失败会记录日志并继续，因此调用方无法单凭返回值确认沪深北三地全部拉取成功。全市场扫描需要完整性判断时，应补一个轻量的逐市场结果状态，或直接使用 gotdx 的 `StockAll()` 分市场拉取。不能因部分市场失败而把部分名单当成全市场完整名单。
- `SyncStockBasicToDB()` 目前按纯 `Symbol` 查询已有记录。新系统应优先以带市场后缀的 `TsCode`（例如 `600519.SH`）作为唯一识别键，避免跨市场代码歧义；不需要因此新建复杂的证券身份服务。

go-stock 是 Wails 应用，其数据包与应用内 DB、logger、配置及全局 `Dao` 存在耦合。**复用优先指复用现有模型、函数逻辑和字段契约，不要求把整个 Wails 应用作为本系统的运行时依赖。** 只有在确认依赖边界可控后，才直接导入其数据包；否则保留一层很薄的本地适配即可。

### 1.2 gotdx：直接调用现有 Go API

gotdx 已提供高阶统一入口，不需要本系统再次实现协议解析或创建一套同功能 API。

| 需求 | 优先使用的现有接口 | 用途 |
|---|---|---|
| 单市场证券总数 | `Client.StockCount(market)` | 名单数量核验 |
| 分页证券列表 | `Client.StockList(market, start, count)` | 需要自行掌握分页和逐页状态时使用 |
| 单市场完整证券列表 | `Client.StockAll(market)` | 默认名单获取入口；内部已分页读取 |
| 批量行情快照 | `Client.StockQuotesDetail(markets, codes)` | 获取当前报价详情 |
| K 线 | `Client.StockKLine(...)` | 直接获取日线、分钟线等协议返回数据 |
| 逐笔/分时等 | 对应 `StockTickChart`、`StockHistoryTransaction` 等现有方法 | 按 V1 实际需要调用 |

名单获取建议：按 `SZ`、`SH`、`BJ` 分别调用 `StockAll()`，分别记录请求结果、返回数量和错误；仅在三个市场都成功且数量检查通过后，才把本轮名单标记为完整。需要更细控制时使用 `StockCount + StockList`，否则不必自行分页。

`types.IsStock()`、`types.IsETF()`、`types.IsIndex()` 可用于初步分类和过滤。它们是代码/市场规则辅助函数，不应被当成完整的交易所证券主数据。gotdx 名单中仍可能存在场内基金等目标策略范围之外的证券，必须结合 Tushare `stock_basic` 的 `exchange`、`market`、`list_status` 等字段过滤。

### 1.3 CZSC：D1 不重做，D2 直接接入

CZSC 不是证券名单、ST 数据或年度交易日历来源。它的职责是接收标准 K 线并完成缠论结构和信号分析。

后续直接复用 CZSC 已有能力：

- `RawBar`：CZSC 的标准原始 K 线对象；
- `format_standard_kline()`：将包含 `dt / symbol / open / close / high / low / vol / amount` 的标准数据转换为 `RawBar`；
- `CZSC`：单周期缠论结构分析；
- `BarGenerator`：基础周期向多个目标周期合成；
- `CzscSignals` / `generate_czsc_signals`：多周期信号计算。

D1 不新增缠论分型、笔、中枢、三买识别算法，也不重新定义 CZSC 已经提供的结构对象。D2 再明确 Go 行情数据如何转成 CZSC 所需的标准 K 线输入。

CZSC 的 `is_trading_time` / 频率时间工具属于交易时段或 K 线时间判断，不等同于某交易所某自然日是否开市；不能用它替代交易所年度交易日历。

---

## 2. V1 最小证券范围

### 2.1 扫描对象

V1 普通 A 股策略扫描范围：

- 上交所普通 A 股；
- 深交所普通 A 股；
- 北交所普通 A 股；
- 包括主板、创业板、科创板和北交所普通股。

默认不纳入普通策略扫描：B 股、ETF、LOF、REITs、债券/可转债、指数、CDR、期货、期权、权证及新三板挂牌证券。排除只影响扫描范围，不要求删除其行情或基础资料。

### 2.2 证券识别规则

1. 证券唯一代码使用带交易所后缀的 `TsCode`，如 `600519.SH`、`000001.SZ`、北交所代码带 `.BJ`。
2. `Symbol` 仅作为纯代码展示和行情接口参数，不能单独承担跨市场唯一键。
3. 先用 gotdx `types.IsStock()` 做快速判断，再用 `IsETF()` / `IsIndex()` 等现有辅助判断排除明显非普通股。
4. 再结合 go-stock `StockBasic` / Tushare `stock_basic` 中的 `Exchange`、`Market`、`ListStatus`、`ListDate`、`DelistDate` 判断。
5. 分类冲突或资料不足时标记为待核验，不进入普通 A 股候选扫描；不为此建设复杂的通用资产分类框架。

特别说明：不要仅凭名称前缀 `ST`、代码前缀或单个来源的列表判断证券类型。

### 2.3 上市状态与扫描资格

V1 只需要明确回答“当前是否上市、是否进入普通扫描”：

| 状态 | 默认处理 |
|---|---|
| `ListStatus=L` 且属于普通 A 股 | 可以进入基础扫描池 |
| ST / *ST | 保留证券资料，默认排除普通候选扫描 |
| 全天停牌 | 保留证券资料，当日不生成新的正常候选信号 |
| `ListStatus=P` 或上市日期未到 | 暂不进入扫描 |
| `ListStatus=D` 或已退市 | 保留资料，退出当前扫描 |
| 状态未知或来源冲突 | 不进入普通候选扫描，显示待核验 |

V1 不建设退市整理期的全量历史事件模型，也不把上市、ST、停牌和交易状态全部塞进一个复杂状态机。只保留影响当前扫描和日常复盘的必要字段。

---

## 3. 最小数据模型

### 3.1 证券基础资料：复用 `StockBasic`

优先复用 go-stock `StockBasic` 或目标项目中已经存在的同类表。若目标项目已存在证券基础表，不再创建第二张主表。

V1 必须可用的字段：

| 字段 | 用途 |
|---|---|
| `ts_code` | 带市场后缀的唯一代码，建议唯一索引 |
| `symbol` | 纯代码，供行情接口使用 |
| `name` | 当前简称 |
| `exchange` | SSE / SZSE / BSE |
| `market` | 板块/市场类别，按来源原值保存 |
| `list_status` | 当前上市状态 |
| `list_date` / `delist_date` | 上市、退市日期 |
| `industry` / `area` | 展示和筛选需要时使用，可为空 |

其余 go-stock 已有字段可以继续保留，但不要求为了 D1 再扩展一整套字段。TDX 返回的 `PreClose`、`DecimalPoint`、`VolUnit` 属于行情/代码元数据；优先在接入层使用，确有跨请求缓存需求再决定是否持久化。

**V1 不新增：** `security_id`、`security_alias_history`、`security_lifecycle_event`、复杂的来源原始记录表。证券代码本身作为稳定业务键已足够支撑当前功能；只有确实遇到代码变更、历史简称回放等需求时再扩展。

### 3.2 ST / 停牌状态：一个简化的按日状态表（按需启用）

如果目标项目已有状态历史或数据源快照表，直接复用。否则，只在需要保存历史扫描状态时增加一张简表：`security_daily_state`。

建议字段：

| 字段 | 用途 |
|---|---|
| `trade_date` | 状态对应交易日 |
| `ts_code` | 证券代码 |
| `st_type` | `NONE` / `ST` / `ST_STAR` / `UNKNOWN` |
| `suspend_type` | `NONE` / `FULL` / `INTRADAY` / `UNKNOWN` |
| `suspend_timing` | 日内停牌时间段，可空 |
| `source` | Tushare / 交易所公告 / 其他来源 |
| `fetched_at` | 本系统取得该状态的时间 |

主键建议 `(trade_date, ts_code)`。只记录每日需要的状态，不拆成 ST 事件、停牌事件、生命周期事件三套表。

来源使用：

- `stock_st`：需要历史/当日 ST 名单时使用；
- `suspend_d`：需要停复牌信息时使用；
- 交易所公告：对关键状态冲突进行核验。

V1 暂不要求每个状态都具备完整 `announced_at / known_at / effective_to` 事件链；不做回测，不为尚未进入范围的 PIT 历史回放预建设复杂审计模型。上线以后保存本系统每日实际抓取的状态快照即可。

**数据缺失规则：没有 K 线不等于停牌。** 交易日已开市、没有行情返回且没有可靠停牌证据时，应标记行情缺失，不允许自动写成停牌。

### 3.3 交易日历：只新增一张表

保留既定数据源原则：交易所官方休市/临时开市公告优先，Tushare `trade_cal` 用于结构化导入和交叉核验。

建议表：`trade_calendar`

| 字段 | 用途 |
|---|---|
| `exchange` | SSE / SZSE / BSE |
| `cal_date` | 自然日期 `YYYY-MM-DD` |
| `is_open` | 1 开市 / 0 休市 |
| `pretrade_date` | 上一交易日 |
| `source` | `EXCHANGE_NOTICE` / `TUSHARE` / `MANUAL_OVERRIDE` |
| `source_url` | 对应公告或数据来源链接，可空 |
| `fetched_at` | 本系统同步时间 |

主键 `(exchange, cal_date)`。不用额外创建 `exchange_calendar_notice`、`market_session_template`、`calendar_version` 等表；公告链接和来源足以满足 V1 日常核对。若公告修订，只更新受影响日期并留下来源及同步时间即可。

同步规则：

1. 交易所年度和临时公告是最终依据；
2. Tushare `trade_cal` 负责批量提供 `is_open`、`pretrade_date`；
3. BSE 日历以北交所公告核验，不能只假定 Tushare 参数能直接返回 BSE；
4. 交易所公告与 Tushare 不一致时，采用官方公告，并保留差异日志；
5. 未确认日期不允许默认当作开市日，扫描任务应停止或明确标记日历未知。

go-stock 的 `holiday_cache.go` 使用 Timor.tech 民用节假日年表和 24 小时缓存，其 `IsTradingDayInfo()` 按周末/民用节假日推断工作日。可以借鉴其“按年缓存、减少逐日请求”的性能思路，但不能把民用节假日 API 作为交易所日历权威来源，也不应把该函数直接作为本平台最终开市判定。

---

## 4. 最小同步和扫描流程

### 4.1 证券名单同步

1. 通过 go-stock 已有 `GetAllStockList()` 获取名单；需要完整性审计时，使用它的轻量增强版本，或直接逐市场调用 gotdx `StockAll()`。
2. 每个市场分别记录成功/失败和返回数量；任何一个市场失败，本轮结果为 `PARTIAL`，不覆盖完整名单。
3. 用 `TsCode` 做 upsert 唯一键；名称变化更新当前名称，不把纯 `Symbol` 当全局键。
4. 通过 `StockBasic` / Tushare `stock_basic` 补充上市状态、日期、交易所和板块信息。
5. 应用普通 A 股过滤规则，得到扫描范围。

不需要构建独立证券发现微服务、通用证券注册中心或复杂的消息总线。

### 4.2 状态同步

- ST：按交易日同步 `stock_st` 或可靠公告数据；没有拿到时标记 `UNKNOWN`，不要沿用过期状态冒充最新状态。
- 停牌：按需同步 `suspend_d`；盘中临停以可靠公告/行情状态补充。
- 上市状态：优先使用 `StockBasic` 已有 `ListStatus / ListDate / DelistDate`；历史事件表暂缓。

### 4.3 扫描前检查

全市场扫描只需要以下几项必要检查：

- 沪深北名单拉取均成功，或清楚标记 `PARTIAL`；
- 证券代码和交易所映射无重复/冲突；
- 普通 A 股范围已过滤 ETF、指数等非目标类型；
- 上市状态可用；
- 当日交易日历已确认；
- ST / 停牌状态已取得，或明确标记未知；
- 不允许把行情缺失误当作停牌。

扫描任务的完成状态、观察池和候选状态优先复用现有项目逻辑，不额外建设 `universe_snapshot` / `universe_snapshot_member` 两张重复表。若现有扫描记录已经保存日期、股票代码和排除原因，直接使用现有记录。

---

## 5. 明确删除或延后的原设计

| 原设计内容 | D1.2 处理 | 原因 |
|---|---|---|
| 独立 `security_master` | 取消；复用 `StockBasic` / 现有证券表 | 字段重叠，避免双主表 |
| `security_alias_history` | 延后 | V1 不需要完整历史简称回放 |
| `security_lifecycle_event` | 延后 | 当前上市状态与日期字段足够支持 V1 扫描 |
| 独立 ST 事件表、停牌事件表 | 合并为可选 `security_daily_state` | 当前只需用于扫描和日常复盘 |
| `source_raw_record` | 取消 | 不做通用原始数据仓库 |
| `exchange_calendar_notice` | 取消 | 保留来源链接和同步记录即可 |
| `market_session_template` | 移至 D2 评估 | D1 只判定自然日是否开市；分钟线边界在 CZSC/K 线接入时复用现有规则 |
| `universe_snapshot` / `universe_snapshot_member` | 优先复用现有扫描记录/候选池 | 不重复建设股票池生命周期 |
| 全量 `known_at` / PIT 事件链 | 延后 | V1 不做回测；先保存上线后的每日实际状态 |
| Parquet / DuckDB | 延后 | 已确定 SQLite 优先 |

---

## 6. D1 验收标准

D1 不以表数量或抽象接口数量作为验收目标，而以现有能力是否被正确复用为准：

1. 能使用 go-stock/gotdx 现有代码获取沪深北证券名单，无重复实现行情协议。
2. 名单同步以 `TsCode` 区分市场；单市场失败能被识别，部分数据不会覆盖完整名单。
3. 能使用 `StockBasic` / Tushare 基础资料识别交易所、上市状态和上市日期。
4. ETF、指数、债券等非普通 A 股不会混入普通策略扫描池。
5. ST、*ST、全天停牌和未知状态能从普通候选扫描中区分；未知状态不按正常股票处理。
6. 交易日历以交易所公告为准，Tushare `trade_cal` 作结构化辅助；沪深北交易所分别可查询。
7. 不因 D1 新增与现有项目重复的证券表、候选池表或通用数据源框架。
8. CZSC 缠论算法和 K 线对象不在 D1 重做，后续按 CZSC 现有 API 接入。

---

## 7. D1 后续实施顺序

代码实施前仍按既定工作流程执行：

1. 只读检查目标项目现有证券表、数据库模型、gotdx 调用、候选池和交易日历；
2. 对照本文件确认哪些 go-stock/gotdx 代码可以直接复用，哪些已有功能已经存在；
3. 只列出缺失的最小差异，不预先创建新表；
4. 给出最小改动清单；
5. 等待用户明确批准后再实施。

下一阶段 D2：日线、30 分钟、60 分钟 K 线统一模型与时间语义。D2 优先使用 gotdx 现有行情 API 和 CZSC `RawBar` / `format_standard_kline` / `BarGenerator`；本系统只负责必要的数据标准化、存储和接口衔接，不重写 CZSC 算法。

---

## 8. 核对过的代码与文档

### go-stock

- [TDX 行情及股票列表实现（当前 dev 代码）](https://github.com/ArvinLovegood/go-stock/blob/796e70dd902e629e6041511d24509882fd0ff7e2/backend/data/tdx_kline_api.go)
- [股票基础资料与 `StockBasic` 模型（当前 dev 代码）](https://github.com/ArvinLovegood/go-stock/blob/796e70dd902e629e6041511d24509882fd0ff7e2/backend/data/stock_data_api.go)
- [交易日/节假日缓存（当前 dev 代码）](https://github.com/ArvinLovegood/go-stock/blob/796e70dd902e629e6041511d24509882fd0ff7e2/backend/data/holiday_cache.go)
- [SQLite 初始化（当前 dev 代码）](https://github.com/ArvinLovegood/go-stock/blob/796e70dd902e629e6041511d24509882fd0ff7e2/backend/db/db.go)
- [go-stock 仓库](https://github.com/ArvinLovegood/go-stock)

### gotdx

- [gotdx 仓库及高阶 API 说明](https://github.com/bensema/gotdx)
- [统一 API 实现 `client_unified.go`](https://github.com/bensema/gotdx/blob/master/client_unified.go)
- [证券列表示例](https://github.com/bensema/gotdx/tree/master/examples/stock_list)
- [K 线示例](https://github.com/bensema/gotdx/tree/master/examples/stock_kline)

### CZSC

- [CZSC 仓库与 1.0.X 架构说明](https://github.com/waditu/czsc)
- [CZSC 公共 API 清单](https://github.com/waditu/czsc/blob/master/docs/public_api.md)
- [标准 K 线格式转换](https://github.com/waditu/czsc/blob/master/czsc/_format_standard_kline.py)

### Tushare

- [`stock_basic`：股票列表和基础资料](https://tushare.pro/document/2?doc_id=25)
- [`trade_cal`：交易日历](https://tushare.pro/document/2?doc_id=26)
- [`stock_st`：ST 股票列表](https://tushare.pro/document/2?doc_id=397)
- [`suspend_d`：停复牌信息](https://tushare.pro/document/2?doc_id=214)

### 交易所官方日历

- [上交所交易日历/休市安排](https://www.sse.com.cn/disclosure/dealinstruc/closed/)
- [深交所通知公告](https://www.szse.cn/www/disclosure/notice/general/)
- [北交所市场日历](https://www.bse.cn/disclosure/tradingtips.html)