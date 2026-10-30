# D2：行情 K 线统一模型与时间语义设计

> 项目：个人 A 股研究与交易辅助平台 V1  
> 文档类型：架构与数据契约设计；本阶段不修改业务代码  
> 版本：D2.0（现有能力优先、最小统一模型）  
> 日期：2026-10-30  
> 依赖基线：D1.2 证券主数据与交易日历；Go + CloudWeGo Hertz；SQLite 优先；gotdx 行情接入；CZSC 缠论分析

---

## 0. 本次设计结论

D2 的目的不是重新实现一个行情系统，而是明确：**go-stock / gotdx 获取的 K 线，怎样以统一、可追溯的方式存入 SQLite，再交给 CZSC 分析。**

V1 冻结以下决定：

1. **优先复用 go-stock 的 `FetchKLineWithFallback()`** 作为已有多数据源 K 线获取入口；不再自行实现一套 MAC、东方财富、新浪、腾讯、通达信的降级链。
2. **直接复用 gotdx 的 `Client.StockKLine()`** 作为可控的单一数据源入口、数据校验入口及必要时的备用入口，不重写 TDX 二进制协议。
3. V1 主要使用日线、30 分钟、60 分钟三种周期。优先从数据源直接取相应周期，不强制下载 1 分钟全量数据后重新合成。
4. SQLite 只使用一张统一 `stock_kline` 表，不为每个周期建一张表，不建设 Parquet / DuckDB。
5. K 线统一以 **Asia/Shanghai** 为时区，内部时间字段统一表示 **K 线结束时间**。供应商原始时间先经过适配与校验，不能直接假定所有数据源时间戳含义相同。
6. 策略分析使用明确的复权口径；V1 技术分析优先使用前复权 `QFQ`，不允许将不复权、前复权、后复权序列混在同一分析序列。真实下单参考价来自实时行情快照，不使用前复权 K 线价格作为委托价格。
7. CZSC 负责 `RawBar`、缠论结构和信号计算。Go 只做字段转换、质量检查、数据持久化和结果编排，不重复实现缠论算法。
8. 未完成的日线或分钟线可以用于行情展示，但默认不进入策略信号计算。

**核心原则：现有接口能拿到数据就直接用；只有跨项目交换所必需的规范化、时区、复权和质量校验才补最薄的一层。**

---

## 1. 现有项目能力核对与复用方案

### 1.1 go-stock：优先复用现有 K 线获取及多源降级

已核对的现有接口和模型：

| 现有接口/模型 | 已有能力 | D2 的使用方式 |
|---|---|---|
| `FetchKLineWithFallback(stockCode, stockName, klt, limit, end, adjustFlag...)` | A 股多数据源获取与降级，返回 `KLineSourceResult` | V1 首选多源获取入口，不复制其回退链 |
| `KLineSourceResult` | `Data` + `Source` | 必须保留 `Source`，写入 K 线记录来源 |
| `KLineData` | 日期、OHLC、成交量、成交额、涨跌幅、振幅、换手率、量比、MA 等 | 仅取日期、OHLC、成交量、成交额；其余为衍生展示字段，不作为基础 K 线重复存储 |
| `GetStockKLinePageWithFallback()` | 应用层分页获取入口 | 需要历史分页时优先复用；V1 不另造分页协议 |

当前 go-stock A 股回退次序在 `FetchKLineWithFallback()` 中为：

`TDX MAC → 东方财富 → 新浪 → 腾讯 → TDX`

该函数已返回实际 `Source`，因此本平台不应再自行维护相同的源选择状态机。

**必须注意复权行为：**

- 函数支持 `qfq`、`hfq`、`none` / `0` 参数。
- MAC / TDX 按参数选择复权方式；东方财富按参数映射请求。
- 代码注释明确说明新浪 / 腾讯兜底固定使用前复权，传入的 `adjustFlag` 对它们无效。
- 未明确传复权参数时，不同源可能采用不同默认口径。

所以：技术分析调用必须显式传 `qfq`。如果未来需要 `none` 或 `hfq`，必须对回退结果的来源进行限制；当回退到不支持指定复权方式的数据源时，不能把结果谎称为指定口径。

go-stock 的 `KLineData` 把价格、成交量等保存为字符串，适合其 API / UI DTO，不建议直接作为本平台 SQLite 的基础存储类型。仅在接入边界严格解析成数值，解析失败则拒绝写入，不要把异常值默认为 0。

代码参考：

- [go-stock sina_kline_api.go](https://github.com/ArvinLovegood/go-stock/blob/796e70dd902e629e6041511d24509882fd0ff7e2/backend/data/sina_kline_api.go)
- [go-stock stock_data_api.go](https://github.com/ArvinLovegood/go-stock/blob/796e70dd902e629e6041511d24509882fd0ff7e2/backend/data/stock_data_api.go)
- [go-stock app.go](https://github.com/ArvinLovegood/go-stock/blob/796e70dd902e629e6041511d24509882fd0ff7e2/app.go)

### 1.2 gotdx：直接复用原生 K 线接口和结构体

gotdx 已提供：

```go
bars, err := client.StockKLine(
    category,
    market,
    code,
    start,
    count,
    times,
    adjust,
)
```

返回 `[]proto.SecurityBar`，字段包括：

- `DateTime`、`Year`、`Month`、`Day`、`Hour`、`Minute`
- `Open`、`High`、`Low`、`Close`
- `Vol`、`Amount`
- `PreClose`、`LastClose`、`Turnover`、`RisePrice`、`RiseRate`

频率映射：

| 本平台频率 | gotdx 常量 | 协议值 | 说明 |
|---|---|---:|---|
| 日线 | `types.KLINE_TYPE_DAILY` | 4 | 日 K |
| 30 分钟 | `types.KLINE_TYPE_30MIN` | 2 | 30 分钟 K |
| 60 分钟 | `types.KLINE_TYPE_1HOUR` | 3 | 1 小时 K |

复权常量：`types.AdjustNone = 0`、`types.AdjustQFQ = 1`、`types.AdjustHFQ = 2`。

`SecurityBar` 的价格、成交量、成交额为数值字段，比 go-stock 的字符串 DTO 更接近本平台内部数值模型。优先使用该接口，不再封装一套同义的 TDX Client。

**gotdx 时间边界注意事项：**当前协议时间解析代码 `proto.GetTime()` 使用 `time.Local` 构造 `DateTime`；日线解析为当地时间 15:00，分钟线则从协议中的日期和时分字段构造。若程序运行在 UTC 或美国时区，不能把 `DateTime.In(Asia/Shanghai)` 直接当作修复方式，因为这可能把原本表示的交易所墙上时间转换成另一个时刻。适配时应保留原始年月日时分字段，并以这些墙上时间分量在 `Asia/Shanghai` 时区重新构造时间。

另外，`SecurityBar.Vol` 在 K 线结构中是 `float64`，但它本身不携带量纲说明；证券列表的 `VolUnit` 不能被误认为每条 K 线都已统一成股。成交量单位必须经过样例核对后标注，不能根据字段名猜测。

代码参考：

- [gotdx client_unified.go](https://github.com/bensema/gotdx/blob/d2b25b98e97e949ff64db4ac37d106635558e7d9/client_unified.go)
- [gotdx proto/get_security_bars.go](https://github.com/bensema/gotdx/blob/d2b25b98e97e949ff64db4ac37d106635558e7d9/proto/get_security_bars.go)
- [gotdx proto/util.go](https://github.com/bensema/gotdx/blob/d2b25b98e97e949ff64db4ac37d106635558e7d9/proto/util.go)
- [gotdx types/constants.go](https://github.com/bensema/gotdx/blob/d2b25b98e97e949ff64db4ac37d106635558e7d9/types/constants.go)

### 1.3 CZSC：直接消费标准 K 线，不自建缠论数据结构

CZSC 1.0.x 现有核心接口包括：

| API | D2 用途 |
|---|---|
| `RawBar` | 标准原始 K 线对象，包含 `symbol`、`dt`、`freq`、OHLC、`vol`、`amount` |
| `Freq` | 周期枚举，使用 `F30`、`F60`、`D` 等现有枚举 |
| `CZSC` | 对单一周期 K 线序列进行缠论结构分析 |
| `BarGenerator` | 流式更新基础周期并生成其他周期 |
| `resample_bars` | 将同标的、同周期的标准 K 线批量重采样到目标周期 |
| `CzscSignals` | 多周期信号组织与更新 |
| `format_standard_kline()` | 将标准 DataFrame 的八个字段转换成 `RawBar` 列表 |

`format_standard_kline()` 要求列：`dt`、`symbol`、`open`、`close`、`high`、`low`、`vol`、`amount`。这与本平台最小 K 线字段直接对应，不需要再定义另一套 CZSC 专用 K 线模型。

**V1 默认策略：直接获取 D / F30 / F60 数据，分别建立周期分析输入。** 不强迫通过 `BarGenerator` 从分钟线合成日线，也不在 Go 中重新实现合成算法。

只有在确实需要从较低周期合成较高周期时，才调用 CZSC 的 `BarGenerator` 或 `resample_bars()`。`resample_bars()` 对输入要求严格递增、无重复时间、单标的、单频率和无 NaN；其当前实现对非分钟目标周期的 `drop_unfinished` 存在已知限制。因此不可在未验证的情况下，把它当作日线最终完成状态判断器。

代码参考：

- [CZSC RawBar 与公开 API](https://github.com/waditu/czsc/blob/5d350a943d6b1935561765fed5467c234973d121/docs/public_api.md)
- [CZSC format_standard_kline](https://github.com/waditu/czsc/blob/5d350a943d6b1935561765fed5467c234973d121/czsc/_format_standard_kline.py)
- [CZSC resample_bars 实现](https://github.com/waditu/czsc/blob/5d350a943d6b1935561765fed5467c234973d121/crates/czsc-utils/src/resample.rs)

---

## 2. V1 支持的周期与数据职责

| 周期 | 内部标识 | 主要用途 | 默认获取方式 |
|---|---|---|---|
| 日线 | `D1` | 趋势突破主判断、市场环境、MA20 / MA60 等日线状态 | 直接获取日 K |
| 30 分钟 | `M30` | 缠论三买短周期确认、突破后的时机确认 | 直接获取 30 分钟 K |
| 60 分钟 | `M60` | 缠论三买主分析周期、趋势结构确认 | 直接获取 60 分钟 K |

周期是数据的业务属性，不应通过 OHLC 时间间隔猜测。每根 K 线必须明确带有 `freq`。

V1 不建设 1 分钟全市场历史库，不存储逐笔成交，不引入独立的周期合成服务。未来需要更细周期时，再评估是否有必要增加 `M1` 或 `M5`。

### 2.1 周期获取参数映射

优先复用 go-stock `FetchKLineWithFallback()`：

| 本平台 | go-stock `klt` | 复权参数 |
|---|---:|---|
| `D1` | `101` | `qfq` |
| `M30` | `30` | `qfq` |
| `M60` | `60` | `qfq` |

若直接调用 gotdx，则分别传入 `KLINE_TYPE_DAILY`、`KLINE_TYPE_30MIN`、`KLINE_TYPE_1HOUR` 和 `AdjustQFQ`。

这些映射只负责请求参数，不代表不同供应商返回的时间戳语义、成交量单位和复权结果已经天然一致；仍需经过第 4 节的规范化和校验。

### 2.2 历史长度

V1 仅拉取足以支撑当前信号判断和常用均线的历史数据，不追求无限历史。

| 周期 | 初始建议 | 原因 |
|---|---:|---|
| `D1` | 最近 250 根 | 覆盖 MA60、较长趋势观察及结构初始化余量 |
| `M60` | 最近 250 根 | 为 60 分钟缠论结构和信号留出足够历史 |
| `M30` | 最近 250 根 | 为 30 分钟确认和局部结构留出余量 |

这些是 V1 的初始抓取配置，不是 CZSC 的硬性最低要求，也不意味着所有信号都必须使用固定 250 根。后续按信号函数所需最小历史长度调整即可。

---

## 3. 统一 K 线数据契约

### 3.1 统一字段

Go 内部只需要一份 K 线数据契约：

| 字段 | 类型 | 必填 | 语义 |
|---|---|---:|---|
| `ts_code` | string | 是 | 证券唯一代码，沿用 D1 的 `600519.SH` 格式 |
| `freq` | string | 是 | `D1` / `M30` / `M60` |
| `bar_time` | RFC3339 字符串 | 是 | K 线结束时间，固定使用 `+08:00` |
| `open` | REAL / float64 | 是 | 开盘价 |
| `high` | REAL / float64 | 是 | 最高价 |
| `low` | REAL / float64 | 是 | 最低价 |
| `close` | REAL / float64 | 是 | 收盘价或当前未完成 K 线的最新价 |
| `volume` | REAL / float64 | 是 | 成交量数值；单位由 `volume_unit` 说明 |
| `amount` | REAL / float64 | 是 | 成交额数值，单位统一为人民币元 |
| `adjust_mode` | string | 是 | `QFQ` / `HFQ` / `NONE` |
| `volume_unit` | string | 是 | `SHARE` / `LOT` / `SOURCE_NATIVE` / `UNKNOWN` |
| `source` | string | 是 | 实际提供本条 K 线的来源，如 `tdx-mac`、`eastmoney`、`sina`、`tencent`、`tdx` |
| `is_complete` | bool | 是 | 该周期 K 线是否已经完成 |
| `fetched_at` | RFC3339 字符串 | 是 | 本系统取得该条数据的时间，固定使用 `+08:00` |

`bar_time` 是统一后的结束时间；数据供应商原始日期/时间由接入适配过程解析，不另建一套复杂的原始行情 DTO。

### 3.2 明确不作为基础字段持久化的内容

以下字段可在 UI 或策略计算时临时生成，不放进基础 K 线表：

- 涨跌幅、涨跌额、振幅；
- MA5 / MA10 / MA20 / MA60 等均线；
- 量比、换手率等衍生值；
- CZSC 的分型、笔、中枢对象；
- 策略分数、信号状态、买卖建议。

这些数据有各自的计算时点和规则，不应和原始 OHLCV 混成一张不断扩张的 K 线表。若后续确实需要缓存指标，按实际性能瓶颈再增加缓存。

### 3.3 OHLCV 质量校验

写入 SQLite 或交给 CZSC 之前执行最小校验：

1. `ts_code`、`freq`、`bar_time` 必须存在；
2. OHLC 必须能严格解析为有限数值；价格必须大于 0；
3. `high >= max(open, close, low)`；`low <= min(open, close, high)`；
4. `volume`、`amount` 不能为负数；
5. 同一 `ts_code + freq + adjust_mode` 序列按 `bar_time` 升序，不能有重复时间；
6. 单次提交不得混入不同证券、不同周期或不同复权口径；
7. 来源切换时必须重新校验重叠时间段，不允许无检查地把两家供应商的数据拼成一条序列；
8. 缺失值、NaN、无穷值不得传给 CZSC。

若行情源将缺失值预先转成字符串 `"0"`，适配层还必须结合 OHLC 逻辑识别异常，不能把 0 价格作为有效 K 线保存。

---

## 4. 时间语义：所有内部时间统一按上海时区

这是 D2 最重要的数据契约之一。A 股时间必须按交易所所在地解释，不能由运行 Go 程序的服务器时区决定。

### 4.1 统一规则

- 时区：`Asia/Shanghai`，UTC+08:00。
- 内部主时间：`bar_time`，表示每根 K 线的结束时间。
- 日线时间：统一为对应交易日的 `15:00:00+08:00`。
- 分钟线时间：统一为该根 K 线的结束时刻。
- 不把日线时间统一存成 UTC 零点，也不把分钟线的 `HH:mm` 当作 UTC 时间。
- Go、SQLite 和 Python/CZSC 之间传输 RFC3339 时间时，必须保留 `+08:00` 偏移量。

日线用 15:00 作为统一时间锚点，便于和 CZSC 的 K 线时间、交易日历及分钟线末根进行比较。若某数据源只返回 `YYYY-MM-DD`，接入层补成当地交易日 15:00；不得在数据层把该日期解析为服务器时区的午夜。

### 4.2 gotdx 的时区处理

gotdx 的 `proto.GetTime()` 当前使用 `time.Local` 创建 `DateTime`：

- 分钟线：从协议年月日和时分字段构造；
- 日线：解析为当地时间 15:00。

部署机器可能在洛杉矶、UTC 或其他时区，因此适配时必须：

1. 读取 `SecurityBar.Year / Month / Day / Hour / Minute`，或从其原始墙上时间字段取得相同分量；
2. 用这些年月日时分在显式 `Asia/Shanghai` Location 中重新构造时间；
3. 再格式化为 `YYYY-MM-DDTHH:mm:ss+08:00`；
4. 不要先对 `DateTime` 调 `.In(shanghaiLoc)` 就认为完成了修正。

验收必须在至少两种不同系统时区下得到完全一致的 `bar_time`。

### 4.3 分钟 K 线结束时间与交易时段

A 股连续竞价时段：

- 上午：09:30–11:30；
- 下午：13:00–15:00。

若供应商的分钟 K 线时间戳已经表示区间结束时间，理论上的正常结束时间如下：

| 周期 | 上午结束时间 | 下午结束时间 |
|---|---|---|
| 30 分钟 | 10:00、10:30、11:00、11:30 | 13:30、14:00、14:30、15:00 |
| 60 分钟 | 10:30、11:30 | 14:00、15:00 |

这是本平台用于校验的交易时段边界，不是对供应商时间戳含义的未经验证假设。首次接入一个数据源时，必须用已知交易日的样例确认它的时间戳代表 K 线开始还是结束；只在适配层修正一次，内部统一按结束时间存储。

严禁使用 `09:30 + n * 30min` 或 `09:30 + n * 60min` 的简单连续加法跨越午休生成 A 股 K 线。若必须重采样，必须调用 CZSC 现有周期合成能力并通过 A 股交易时段用例验证。

### 4.4 日历与 K 线时间的关系

- 是否交易日，读取 D1 的 `trade_calendar`；
- 盘中时段边界使用固定的 A 股连续竞价时段配置；
- 是否完整，结合交易日历、bar 结束时间和数据源返回情况判断；
- 不因周末或节假日没有 K 线就生成零值 K 线；
- 不因缺少 K 线就把证券标记为停牌；停牌状态继续由 D1 的证券状态数据决定。

V1 不新增 `market_session_template` 表；A 股时段是固定的市场规则，作为小型代码常量维护即可。只有未来要支持多个市场或多种特殊交易时段时才考虑配置化。

---

## 5. 复权口径与来源一致性

### 5.1 V1 选择

**技术分析主序列统一使用前复权 `QFQ`。** 原因是缠论结构、趋势线和均线不应在除权除息日仅因价格口径断层而出现非市场性的结构跳变。

真实市场价仍从实时快照接口获得；前复权价格不用于实际委托价格、成本价或账户市值。

### 5.2 来源与复权规则

| 情况 | 处理 |
|---|---|
| go-stock 回退接口调用 | 必须显式传 `qfq` |
| MAC / TDX | 传入明确的前复权选项 |
| 东方财富 | 使用明确的前复权参数 |
| 新浪 / 腾讯 | 已知兜底实现固定前复权；只允许作为 QFQ 序列兜底 |
| 未知复权口径 | 标记 `UNKNOWN`，不进入 CZSC 正式信号计算 |
| 来源发生切换 | 记录来源，核对重叠日期 OHLC；必要时重取该证券该周期历史窗口，不允许静默拼接 |

### 5.3 前复权序列更新

前复权历史值可能因新的分红、送转等除权事件而变化。V1 不做 PIT 回测，但不能把这类变化误判为行情源错误：

- 每次拉取时按 `ts_code + freq + adjust_mode` 对历史窗口执行 upsert；
- 对近期重叠区间允许更新；
- 数据更新时间写入 `fetched_at`；
- 若来源切换导致大范围价格差异，先标记该序列待校验，不直接让策略读取混合结果。

V1 不单独建设复权因子服务，也不重复计算供应商已提供的前复权值。

---

## 6. 未完成 K 线与信号可用性

展示数据和策略数据必须区分。

### 6.1 状态定义

- `is_complete = true`：该周期已经结束，且已满足对应数据源的可用性要求；
- `is_complete = false`：当前仍在形成中的 K 线，价格和成交量会继续变化。

如果数据源没有提供明确的完成标识，适配层结合交易日历、交易时段和服务器上海时间判断。边界无法确认时按未完成处理，不得为了让信号尽早触发而乐观判定为完成。

### 6.2 V1 信号规则

- 未完成的 30 分钟或 60 分钟 K 线不进入正式缠论信号计算；
- 未收盘日线不进入正式日线趋势突破判断；
- 未完成 K 线可单独用于行情展示或“盘中观察”提示；
- 某根 K 线从未完成变成完成时，以同一个复合键更新，不插入第二根相同时间 K 线。

这样可以避免盘中临时高点、低点和收盘值变化引起信号反复出现、消失。

---

## 7. SQLite 最小存储设计

### 7.1 一张统一表

沿用 D1 的 SQLite 优先决策。V1 只增加一张 `stock_kline`，三个周期共用，不分三张表。

建议字段：

```sql
CREATE TABLE IF NOT EXISTS stock_kline (
    ts_code       TEXT    NOT NULL,
    freq          TEXT    NOT NULL CHECK (freq IN ('D1', 'M30', 'M60')),
    bar_time      TEXT    NOT NULL,
    open          REAL    NOT NULL,
    high          REAL    NOT NULL,
    low           REAL    NOT NULL,
    close         REAL    NOT NULL,
    volume        REAL    NOT NULL,
    amount        REAL    NOT NULL,
    adjust_mode   TEXT    NOT NULL CHECK (adjust_mode IN ('QFQ', 'HFQ', 'NONE', 'UNKNOWN')),
    volume_unit   TEXT    NOT NULL DEFAULT 'UNKNOWN',
    source        TEXT    NOT NULL,
    is_complete   INTEGER NOT NULL DEFAULT 1 CHECK (is_complete IN (0, 1)),
    fetched_at    TEXT    NOT NULL,
    PRIMARY KEY (ts_code, freq, adjust_mode, bar_time)
);

CREATE INDEX IF NOT EXISTS idx_stock_kline_freq_time
    ON stock_kline (freq, bar_time);
```

说明：

- 复合主键保证同一证券、周期、复权口径和结束时间只存在一根 K 线；
- `source` 记录本条数据最近一次写入时的来源；
- `fetched_at` 用于判断数据新鲜度；
- `volume_unit` 在完成量纲核验前保留 `UNKNOWN` 或 `SOURCE_NATIVE`，不得伪装为股数；
- `bar_time` 带有 `+08:00` 偏移，字符串格式固定，确保同一格式可按时间范围查询。

如果目标项目已有完全等价的 K 线表，优先扩展或复用已有表，不再创建第二张。表名与索引名称是建议，不要求为了名称一致而迁移已有数据。

### 7.2 不新增的表和系统

V1 明确不新增：

- `daily_kline`、`minute_kline` 等按频率拆开的表；
- `kline_source_registry`、`kline_provider_config` 等过度抽象配置表；
- `kline_adjust_factor` 独立服务；
- `kline_indicator`、`kline_ma` 等指标基础表；
- Parquet / DuckDB 数据仓库；
- 历史回测专用行情快照表。

### 7.3 写入和更新规则

1. 单次写入按同一证券、同一周期、同一复权口径组织；
2. 严格解析和校验完成后再开启事务写入；
3. 采用复合主键 upsert；
4. 定期重取最近若干根 K 线，修正供应商对最近数据的延迟补齐或更正；
5. 源发生切换时先比对重叠区间，不允许仅凭时间不重复就把另一来源的数据无缝追加；
6. 对复权历史有变更的窗口执行更新；
7. 行情请求失败时保留原有数据，不将空响应写成“成功清空”；
8. 只有当本轮数据完整且校验通过，才更新该证券、该周期的数据新鲜度状态。

V1 不需要单独设计复杂的 K 线任务事件表；可复用现有同步任务日志或记录本次同步结果。

---

## 8. Go → CZSC 的最小接口契约

### 8.1 职责边界

Go 侧负责：

- 按 D1 证券范围筛选标的；
- 调用 go-stock / gotdx 获取 K 线；
- 规范化时间、周期、复权、成交量单位和来源；
- 做 OHLCV 质量校验；
- 从 SQLite 取出有序且完整的 K 线；
- 向 CZSC 分析入口传递标准化数据。

CZSC 侧负责：

- 把标准 K 线转换为 `RawBar`；
- 执行 `CZSC` 结构分析；
- 按配置执行现有信号函数；
- 返回分型、笔、中枢或信号结果。

不得在 Go 中再次实现 `RawBar`、分型、笔、中枢或 CZSC 信号函数。

### 8.2 JSON 输入字段

CZSC 服务入口只需要稳定、简单的请求结构：

```json
{
  "schema_version": "v1",
  "ts_code": "600519.SH",
  "freq": "60分钟",
  "adjust_mode": "QFQ",
  "bars": [
    {
      "dt": "2026-10-30T10:30:00+08:00",
      "symbol": "600519.SH",
      "open": 1500.0,
      "close": 1505.0,
      "high": 1508.0,
      "low": 1498.0,
      "vol": 12345.0,
      "amount": 18500000.0
    }
  ]
}
```

这是接口契约示例，不代表真实行情值。只传已完成 K 线，按 `dt` 严格升序排列。`freq` 使用 CZSC 可识别的频率字符串；`D1` 映射成 `日线`、`M30` 映射成 `30分钟`、`M60` 映射成 `60分钟`。

CZSC Python 侧可把 `bars` 组装成标准 DataFrame，再调用现有 `format_standard_kline(df, freq)`；或直接构造其公开 `RawBar`，不必另写一套缠论对象模型。

### 8.3 周期分析策略

V1 三个周期不是同一根基础线合成出的三层数据：

- `D1` 独立分析，作为大级别环境和趋势突破依据；
- `M60` 独立分析，作为缠论三买的主结构周期；
- `M30` 独立分析，作为短周期时机确认。

Go 决策层组合三份 CZSC 输出，并保留每个策略自己的信号、状态和理由。不要把三种周期的指标简单相加为一个总分。

### 8.4 分析结果

CZSC 输出至少要带回：

- `ts_code`；
- `freq`；
- `as_of`（最后一根实际使用的完整 K 线结束时间）；
- `adjust_mode`；
- `source` 或来源摘要；
- 信号 key/value 或结构摘要；
- 分析是否成功及错误原因。

结果必须能追溯到使用了哪一根最新 K 线，不允许只返回一个无法核对时点的 BUY / SELL 标签。

---

## 9. 数据获取与刷新流程

### 9.1 首次初始化

1. 从 D1 取得有效普通 A 股证券列表；
2. 按标的分别获取 D1、M60、M30 历史窗口；
3. 默认通过 go-stock 的既有回退入口请求 QFQ；需要固定源或验证字段时可直接调用 gotdx；
4. 规范化字段、时间和量纲；
5. 校验后 upsert 到 `stock_kline`；
6. 同一证券的三个周期分别执行质量检查；
7. 仅把完整且满足历史长度要求的序列交给 CZSC。

### 9.2 日常更新

- 日线：交易日收盘后刷新，确认数据源日 K 已完整后更新；
- 60 分钟：每个 60 分钟结束后更新对应周期；
- 30 分钟：每个 30 分钟结束后更新对应周期；
- 盘中未完成 K 线可用于 UI，但不触发正式结构信号；
- 非交易日不执行常规 K 线拉取；
- 是否运行任务由 D1 `trade_calendar` 和 A 股交易时段控制，不用固定 cron 假设每个工作日都开市。

V1 不建设 WebSocket 实时行情推送系统，不建立每秒更新 K 线的高频任务。

### 9.3 扫描范围优化

为避免无效请求和数据量膨胀，建议按策略职责使用已有周期：

- D1：全量普通 A 股扫描，供趋势突破和市场环境判断；
- M60：用于全市场缠论主周期筛查，或按已明确的扫描能力分批更新；
- M30：优先用于 M60 初筛命中、观察池和已有候选股票的时机确认。

若产品要求 M30 信号必须覆盖全部普通 A 股，则再扩大为全市场 M30 更新；不要在没有需求验证前默认把所有周期、所有历史长度都按最高成本全量抓取。

---

## 10. 异常处理边界

| 异常 | 处理 |
|---|---|
| 一个来源失败，go-stock 降级成功 | 使用回退结果，保留实际 `Source` |
| 所有来源均失败 | 不覆盖旧数据，标记本轮获取失败 |
| 来源返回空列表 | 不当作证券退市或停牌，不清理旧历史 |
| 来源切换 | 比对重叠时间和 OHLC；无法确认时标记待校验 |
| 时间戳无法解析 | 拒绝写入该批数据，记录错误 |
| 时区无法确认 | 不传 CZSC，不猜测时差 |
| 复权方式未知 | 不传正式 CZSC 分析 |
| 成交量单位未知 | 保留来源原始量纲标识；不得计算需要跨源可比量纲的绝对量指标 |
| OHLC 不合法 | 拒绝该根或整批，并记录可定位错误 |
| 重复 / 乱序时间 | 接入层去重并排序；批量重采样前必须严格校验 |
| 当前 K 线未完成 | 可展示，不进入正式信号分析 |
| 某个周期历史不足 | 标记该周期数据不足，不伪造缺失 K 线 |

---

## 11. 验收标准

D2 完成后，后续实现至少通过以下验收：

### A. 复用优先

- [ ] Go 侧能够调用 go-stock 现有 K 线入口，不复制五个数据源的回退逻辑；
- [ ] 能直接调用 gotdx `StockKLine()` 获取 D1 / M30 / M60；
- [ ] 能在结果中记录真实数据源 `Source`；
- [ ] CZSC 输入通过 `RawBar` / `format_standard_kline()` 现有接口转换，不自写缠论算法。

### B. 时间与周期

- [ ] 日线统一为交易日 15:00:00 +08:00；
- [ ] 30 分钟、60 分钟统一按经样例确认的 K 线结束时间存储；
- [ ] 在 UTC、America/Los_Angeles 和 Asia/Shanghai 系统时区下，同一根 TDX K 线规范化结果一致；
- [ ] 午休期间不生成合成 K 线；
- [ ] 日历闭市日不生成零值 K 线。

### C. 数值和来源

- [ ] 所有 OHLCV 数值可严格解析；
- [ ] 成交量量纲有明确标识，不猜测股 / 手；
- [ ] 同一分析序列复权口径一致；
- [ ] 来源切换有重叠区间校验，不静默拼接；
- [ ] 重复时间、乱序、NaN、异常 OHLC 不进入 CZSC。

### D. 完成状态与 CZSC

- [ ] 未完成 K 线不会进入正式信号输入；
- [ ] 每个 CZSC 结果包含 `as_of` 和周期；
- [ ] D1、M30、M60 分别使用各自的完整有序序列；
- [ ] CZSC 输入只含同一个证券、同一个周期；
- [ ] 分析失败或数据不足时明确返回失败原因，不伪造中性信号。

### E. 范围控制

- [ ] 只使用 SQLite；
- [ ] 只维护统一 `stock_kline` 表（如已有等价表则复用）；
- [ ] 没有为每个周期建立重复表；
- [ ] 没有在 Go 中复制 CZSC 算法；
- [ ] 没有引入回测、Parquet / DuckDB、WebSocket 或微服务拆分。

---

## 12. 本阶段明确不做

D2 是数据模型与时间语义设计，不是代码实现。当前不做：

- 修改 Go 代码、数据库迁移或 Git 操作；
- 为了“统一”而重写 go-stock / gotdx / CZSC 已有能力；
- 新增行情微服务、数据总线、消息队列；
- 下载全市场 1 分钟 K 线；
- 构建全量复权因子计算系统；
- 构建回测、策略优化或自动交易；
- 引入 Parquet / DuckDB；
- 将所有周期强行用 `BarGenerator` 重采样。

---

## 13. D2 后续工作顺序

D2 文档完成后，后续建议按下列顺序进行，不跨步堆功能：

1. 只读核对目标 Go 仓库是否已有 K 线表、数据源接口及同步任务；
2. 对照本 D2 找出真实缺口，尽量直接复用现有实现；
3. 用少量代表证券（沪、深、北各至少一只）核验 D1 / M30 / M60 时间戳、复权和成交量单位；
4. 通过 UTC / 美国时区测试 gotdx `DateTime` 规范化；
5. 确定最小接入补丁和验收用例后，再单独提出代码实施方案。

**最终原则：优先使用已有函数和类型，只有在现有接口的真实边界上补缺；任何新增接口、表和服务都必须能对应一条明确验收要求。**