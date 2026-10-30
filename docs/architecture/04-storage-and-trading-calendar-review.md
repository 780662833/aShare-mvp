# A股辅助平台 V1：数据存储与交易日历选型评审

> 评审范围：对照 go-stock、gotdx 当前公开代码，评估 SQLite、Parquet、DuckDB 组合是否适合 V1；明确 A 股交易日历的来源、模型和运行规则。
>
> 系统约束：只做选股、择时、买卖辅助、观察跟踪和复盘；V1 不做回测、券商连接、自动下单。

## 1. 结论摘要

### 1.1 存储结论

之前提出的“Parquet 保存全部历史行情，DuckDB 批量研究查询，SQLite 保存业务状态”在技术上成立，但对当前 V1 不是必须配置。尤其当前不做回测，过早引入三个持久化组件会增加数据复制、更新、修订、备份和部署复杂度。

**V1 建议：SQLite 作为唯一必需的持久化数据库，先同时承载业务数据和标准化 K 线；Parquet 与 DuckDB 通过仓储接口预留扩展，不在第一阶段强制引入。**

理由：
- 目前主要工作负载是按股票、周期读取最近 N 根 K 线，送入 CZSC 做结构分析；不是大型历史回测或复杂横截面 SQL 分析。
- go-stock 已证明 SQLite + GORM + WAL 在本地个人股票研究工作台中是可行的工程路线。
- gotdx 是协议客户端，不要求应用必须使用某一种数据库。
- 只用 SQLite 可减少部署和备份负担。V1 先测量全市场扫描耗时、K 线表规模、批量读写性能，再决定是否需要列式分析引擎。

**升级路线：** 当全市场批量特征筛选、历史数据规模或跨股票聚合查询成为实际瓶颈时，再采用“SQLite 业务库 + DuckDB 分析库 + Parquet 归档”。不要在没有基准数据前同时建设三套行情存储。

### 1.2 交易日历结论

- 不复制 go-stock 的 Timor.tech 普通节假日 API 作为最终交易日历。
- 不把 gotdx 内置的期货 SQLite 日历误当成 A 股股票日历。
- **A 股交易日的权威依据是交易所发布的休市/开市安排；Tushare `trade_cal` 可作为历史批量数据与结构化同步来源。**
- 本地必须保存交易日历，运行时只查本地，不应每次判断交易日都访问外部 API。
- 日期状态要允许 `UNKNOWN`；资料缺失或来源冲突时，不要靠“周一至周五”猜当天必然开市。

## 2. go-stock 当前做法

### 2.1 业务数据库

go-stock 使用 SQLite + GORM：
- `backend/db/db.go` 使用 `gorm.io/driver/sqlite`，底层注册 `modernc.org/sqlite`。
- 默认数据库文件为本地 `data/stock.db`（应用目录会解析实际路径）。
- 配置 WAL、busy_timeout、synchronous、cache_size、mmap_size、连接池等。
- `main.go` 和 `backend/db/chat_memory.go` 通过 GORM `AutoMigrate` 建立股票基础资料、自选、分组、资金流、资讯、AI 结果、交易记录、信号记录等业务表。

这对本项目有参考价值：个人本地研究工作台的股票主数据、观察池、策略状态、信号记录、手工交易日志和复盘任务，使用 SQLite 很自然。

### 2.2 K 线获取与持久化

go-stock 的 `KLineData` 是行情展示/接口 DTO，价格、成交量、成交额等字段是字符串；`FetchKLineWithFallback` 当前路径按优先级尝试：
1. TDX MAC；
2. 东方财富；
3. 新浪；
4. 腾讯；
5. 通达信主行情。

它会在结果中标明来源（例如 `tdx-mac`、`eastmoney`、`sina`、`tencent`、`tdx`）。

**在本次核对的 K 线主路径及 GORM 迁移模型中，没有发现一个作为全市场历史 K 线主存储的通用 K 线表。** K 线主要按请求获取并返回；项目确实有一些本地行情相关数据和缓存，例如实时 `StockInfo`、证券基础资料，以及分笔成交的 SQLite 缓存，但它们不等于完整历史 K 线库。

可借鉴：
- 多数据源接入、超时、降级和来源标识。
- 把证券基础资料、观察池、手工交易记录保存在本地。

不宜照搬：
- 用字符串字段作为内部标准 K 线模型。
- 每次研究请求时临时从多个来源取 K 线，却不建立可追溯的统一历史数据层。
- 不经校验就把不同来源、不同复权口径的 K 线拼在一起。

### 2.3 交易日历

`backend/data/holiday_cache.go` 当前采用：
- Timor.tech 年度节假日接口：`/api/holiday/year/{year}/`；
- 按年存入进程内存，TTL 24 小时；
- 年表失败时再请求单日 `/api/holiday/info/{date}`；
- 周六、周日直接作为休市日；
- 工作日再按年度节假日表判断是否休市。

这个实现比逐日请求好，适合一般节假日查询和简单工具场景；但它的数据语义是中国节假日/补班，不是交易所发布的证券市场开闭市记录。交易所休市安排才应成为 A 股交易日的权威依据。缓存也只存在进程内，不能替代可追溯的本地交易日历表。

相关代码：
- [go-stock db.go](https://github.com/ArvinLovegood/go-stock/blob/796e70dd902e629e6041511d24509882fd0ff7e2/backend/db/db.go)
- [go-stock main.go 的 AutoMigrate](https://github.com/ArvinLovegood/go-stock/blob/796e70dd902e629e6041511d24509882fd0ff7e2/main.go)
- [go-stock K 线来源选择](https://github.com/ArvinLovegood/go-stock/blob/796e70dd902e629e6041511d24509882fd0ff7e2/backend/data/sina_kline_api.go)
- [go-stock 交易日历缓存](https://github.com/ArvinLovegood/go-stock/blob/796e70dd902e629e6041511d24509882fd0ff7e2/backend/data/holiday_cache.go)

## 3. gotdx 当前做法

### 3.1 行情接口而非应用数据库

`gotdx` 是通达信协议 Go 客户端，提供 `StockAll`、`StockKLine`、快照、分时、逐笔、MAC 等接口。`examples/stock_all` 分别调用深圳、上海、北京市场的 `StockAll`，并用 `types.IsStock` 过滤证券类型。

它负责从协议端获取数据并返回 Go 结构，不负责替接入应用设计完整的股票数据库、业务库或行情归档体系。应用需要自行决定缓存、存储、去重、复权、数据质量和同步策略。

### 3.2 gotdx 的 SQLite 日历不是 A 股交易日历

gotdx 仓库有 `data/futures-trade-calendar.db` 和 `futures_calendar.go`：
- 表名 `futures_trade_calendar`；
- 关键字段包括 `exchange`、`cal_date`、`is_open`、`pretrade_date`；
- 用途是根据期货交易所、前一交易日和夜盘时间，把 MAC 期货 K 线的交易日字段转换为自然日期时间。

它是期货夜盘时间还原所需的数据，不是沪深北 A 股股票交易日历，也没有替应用提供证券市场日历管理服务。

参考：
- [gotdx README](https://github.com/bensema/gotdx/blob/d2b25b98e97e949ff64db4ac37d106635558e7d9/README.md)
- [gotdx futures_calendar.go](https://github.com/bensema/gotdx/blob/d2b25b98e97e949ff64db4ac37d106635558e7d9/futures_calendar.go)
- [gotdx stock_all example](https://github.com/bensema/gotdx/blob/d2b25b98e97e949ff64db4ac37d106635558e7d9/examples/stock_all/main.go)

## 4. 存储方案比较

| 方案 | 优势 | 代价/限制 | 对 V1 的判断 |
|---|---|---|---|
| SQLite 单库 | 部署简单；事务、唯一键、索引、业务数据和 K 线可统一管理；Go 生态成熟；适合单机应用 | 写入是单写者；海量全市场分析不如列式引擎；需要批量写入、索引和查询优化 | **V1 首选** |
| SQLite + Parquet + DuckDB | 业务事务、列式分析、压缩归档各司其职；DuckDB 可直接查询 Parquet，支持列裁剪和过滤下推 | 三套存储需同步；Parquet 修订/覆盖与小文件治理复杂；DuckDB 是分析型而非高频小事务数据库 | 数据增长或分析负载证明需要时再升级 |
| DuckDB 单库 | 列式分析和聚合能力强；SQL 易于做全市场横截面统计 | DuckDB 面向分析与批量操作，不以大量细碎事务为主要目标；单进程读写模型需明确 | 暂不作为业务主库 |
| SQLite 业务库 + DuckDB 行情库，Parquet 归档 | OLTP 与分析分开；行情扫描性能可独立扩展；归档可移植 | 比 SQLite 单库更复杂；DuckDB Go 驱动引入 CGO 和 Windows C 编译工具链要求 | V1 可预留，但暂不强制启用 |

### 推荐的 V1 存储边界

**一个 SQLite 数据库先作为唯一权威存储：**
- 证券主数据、证券状态、行业归属；
- A 股交易日历与交易时段；
- 标准化历史 K 线（至少日线、30 分钟、60 分钟）；
- 数据源、同步批次、数据质量状态；
- 候选池、观察池、策略信号状态、买卖辅助结论；
- 用户手工交易记录和复盘结果。

建议通过 `MarketDataRepository` / `BarRepository` 之类的边界隔离存储实现，策略和 CZSC 适配层不直接依赖 SQL。以后若行情数据迁移 DuckDB/Parquet，只替换数据仓储实现，不改策略规则。

### 为什么第一版暂不引入 DuckDB

V1 不是历史回测平台。主要读取模式是：对某股票、某周期获取最近几百根完成 K 线，送入 CZSC；同时做日线/分钟线的全市场候选筛选。SQLite 在有合适复合索引、批量预取、有限并发和单写者队列时足以作为第一阶段基线。

DuckDB 真正的优势在于大批量列式读取、过滤、聚合、横截面特征和研究型 SQL。若后续需要大量全市场滚动指标、跨年度统计、组合研究或回测，再引入就更有价值。DuckDB 官方说明也强调其适合批量分析，并指出多进程模式下可多进程只读、但只能由一个进程读写；Go 官方客户端使用 CGO，需要 C 编译器。对于 Windows 桌面部署，这是需要实际验证的额外成本。

### Parquet 应该放在哪里

Parquet 是列式文件格式，不是负责事务和更新的数据库。它适合：
- 历史行情的只读归档；
- 大批量导入/导出；
- DuckDB 的列式扫描来源；
- 数据快照、交换和离线研究。

不建议把每次分钟线更新都写成一个小 Parquet 文件。后续启用时，应按周期和日期分区、批量合并小文件，并保留数据版本/来源/复权版本。对已完成 K 线的更正需要有重写或覆盖分区的策略。

## 5. V1 K 线数据模型原则

### 5.1 数据类型与主键

- 内部 OHLC、成交量、成交额必须是数值类型，不沿用 go-stock DTO 的字符串字段。
- 使用统一证券标识（如 `600519.SH`），禁止仅依赖六位代码。
- 频率明确为 `1d`、`60m`、`30m`，不要用含义模糊的整数常量散落在各模块。
- 建议唯一键：`symbol + frequency + bar_end + price_basis`。
- 记录 `trade_date`、`bar_start`、`bar_end`、`is_final`、`source`、`source_timestamp`、`ingest_batch_id`、`quality_status`。
- 时间统一使用 `Asia/Shanghai` 语义；落库可用 UTC epoch/标准时间戳，但必须同时保留交易所本地交易日字段。

### 5.2 复权

- 保留不复权原始 OHLCV，绝不让前复权结果覆盖原始行情。
- 公司行动/复权因子独立存储，记录来源、有效日期、发布时间和版本。
- 复权序列作为派生视图或带版本的派生数据。
- 当前分析所用复权口径、因子版本必须随扫描结果保存，避免后续数据修订导致旧信号无法解释。

### 5.3 数据更新

- TDX 等上游来源只负责拉取；统一数据中心负责标准化、去重、校验和 upsert。
- 完成 K 线与未完成 K 线分开管理。策略正式信号只消费已完成 K 线。
- 同一证券/周期的 bar 按时间顺序更新；同一 bar 的重拉结果做幂等 upsert。
- 数据源切换时保留 source 标识，不直接把不同来源、不同复权口径的 bar 拼成连续序列。
- SQLite 采用单写者批量入库，分析任务通过有界并发读取；避免大量 worker 同时争抢写锁。

## 6. A 股交易日历方案

### 6.1 数据源优先级

1. **交易所官方公告/日历：最高优先级。** 上交所、深交所、北交所公布年度休市安排及特殊开市/休市通知。出现冲突时以交易所公告为准。
2. **Tushare `trade_cal`：结构化历史和同步来源。** 支持 `exchange`、`start_date`、`end_date`、`is_open`，返回 `cal_date`、`is_open`、`pretrade_date`；官方文档说明默认上交所，深交所可单独指定，北交所日历参考上交所和深交所；接口当前文档标注需 2000 积分。
3. Timor.tech 等普通节假日 API：只可作为辅助校验/展示，不作为交易日最终事实来源。

2026 年官方通知可作为设计校验样本：上交所、深交所、北交所各自公布的 2026 休市安排，日期安排一致；但数据库仍建议按交易所保存，不把“一致”硬编码成永久规则。

### 6.2 日历表

建议至少包括：

`exchange_trading_calendar`
- `exchange`：SSE / SZSE / BSE
- `calendar_date`：自然日
- `session_status`：OPEN / CLOSED / UNKNOWN
- `prev_trade_date`：上一交易日
- `next_trade_date`：下一交易日（可离线计算）
- `source_type`：EXCHANGE_NOTICE / TUSHARE / MANUAL
- `source_url`：公告或数据源地址
- `published_at`：来源发布时间
- `fetched_at`：抓取时间
- `verified_at`：人工或交叉核验时间
- `calendar_version`：日历版本

主键：`exchange + calendar_date`。

未知日期不要默认填为 OPEN。日历数据更新后保留变更版本，尤其要记录公告修订和特殊休市通知。

### 6.3 交易时段与交易日分开建模

交易日历回答“这一天市场开不开”；交易时段回答“这一天何时进行何种交易”。二者分开保存。

建议另建 `market_session`：
- `market` / `instrument_type`；
- `session_type`：CALL_AUCTION / CONTINUOUS / BREAK / CLOSE_AUCTION；
- `start_time` / `end_time`；
- `timezone`；
- `effective_from` / `effective_to`；
- `schedule_version`。

例如上交所股票交易时段包含开盘集合竞价、连续竞价、收盘集合竞价；不能简单把 9:30–15:00 视为无间断连续交易。分钟 K 线的时间边界要结合交易时段定义，并与 gotdx 实际返回的 bar 时间戳做样本验证。

### 6.4 交易日历运行规则

- 启动时加载本地日历；非交易时段定时同步；日历查询只读本地库。
- 同步顺序：抓取结构化日历 → 与交易所公告核验 → 事务性更新 → 记录来源和版本。
- 上游失效时保留最后一份已验证日历，同时报告数据陈旧状态。
- 当前日期或未来日期超出已确认日历范围时返回 UNKNOWN，而不是用工作日推断开市。
- 股票扫描若依赖某日是否为有效交易日，而状态为 UNKNOWN，应暂停该日正式扫描或标记结果不完整。
- `pretrade_date`、`next_trade_date` 用交易日记录计算，不用 `AddDate(0,0,1)` 代替。

### 6.5 和分钟线的关系

30m/60m bar 的结束时间必须落在相应市场交易时段的边界规则中。午间休市不是一段连续行情；集合竞价、连续竞价、收盘集合竞价的归属应单独验证。交易日历只提供交易日期，不能单独解决分钟线 bar 的切分和完整性判断。

## 7. 何时升级到 DuckDB + Parquet

以下任一条件被实测满足，再进入升级评审：
- SQLite 全市场扫描在目标硬件上不能满足预定的日常扫描时限；
- K 线数据增长使数据库备份、维护或索引效率明显恶化；
- 需要大规模跨股票、跨行业、跨日期的聚合/滚动特征 SQL；
- 后续启动系统化回测或大量参数研究。

升级目标建议：
- SQLite：证券主数据、日历、配置、观察池、信号状态、手工交易记录等 OLTP 数据。
- DuckDB：热的标准化行情与批量特征分析库，由 Go 主进程单一管理读写。
- Parquet：已完成历史行情的冷归档和离线交换；DuckDB 直接读取归档文件。

不要让 CZSC 服务自行写 DuckDB。由 Go 数据中心统一持有数据库写权限，通过标准化数据接口给 CZSC 输入，避免多进程写入冲突。

## 8. 本轮最终决策建议

| 决策项 | V1 决策 |
|---|---|
| 业务数据库 | SQLite |
| K 线持久化 | 先 SQLite，使用数值类型、复合主键和批量读写 |
| DuckDB | 暂缓；仓储接口预留 |
| Parquet | 暂缓实时写入；未来用于历史归档/交换 |
| A 股交易日历权威来源 | SSE / SZSE / BSE 官方公告 |
| 结构化日历同步 | Tushare `trade_cal` 可选，需 Token/积分；与官方公告核对 |
| Timor.tech | 普通假日日历辅助，不作为交易日权威源 |
| gotdx 日历 | 当前内置文件是期货夜盘转换日历，不用于 A 股日历 |
| 日历未知处理 | UNKNOWN，不默认开市 |

本设计维持 V1“不做回测、不做交易执行”的范围，同时保证后续扩展 DuckDB/Parquet 时不必重写策略层。