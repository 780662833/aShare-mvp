# CLI 实际运行说明

## 1. 命令范围

本 CLI 只把已存在的 Provider、单股评估服务、扫描器、SQLite 仓储和历史查询服务连接起来；不新增策略逻辑，不连接券商，不下单。

| 命令 | 作用 |
|---|---|
| `init` | 初始化 SQLite 和 migrations |
| `sync-universe` | 从 gotdx 获取证券名单；证券上市状态保持 UNKNOWN |
| `sync-bars` | 获取指定证券 D1/M60/M30 前复权 K 线并入库 |
| `import-reference` | 导入经核验的证券主数据、每日状态和交易日历 |
| `evaluate` | 调用现有单股双策略服务并分别保存决策 |
| `scan` | 调用现有扫描器，扫描明确标记为 ACTIVE 的普通 A 股 |
| `scan-result` | 根据原始 DecisionID 读取完整扫描结果 |
| `history` | 调用 S5 历史决策分页查询 |

## 2. 环境

- Go 1.27.2+
- CZSC Python 服务按 `czsc-service/README.md` 安装并运行，默认 `http://127.0.0.1:8091`
- 可访问 gotdx 行情服务
- Windows PowerShell 示例命令在仓库根目录执行

## 3. 首次初始化与行情同步

```powershell
go run ./cmd/ashare-mvp init
go run ./cmd/ashare-mvp sync-universe
go run ./cmd/ashare-mvp sync-bars --ts-code 600519.SH --count 250 --freq all
```

`sync-universe` 不会把 gotdx 返回的证券名单擅自标记为 ACTIVE，因为该名单不提供本项目要求的完整上市状态、ST 和停牌事实。它只写入 `ListingStatus=UNKNOWN` 的资料；未知状态不会进入扫描股票池。

## 4. 导入经核验的参考数据

gotdx adapter 当前没有提供本项目所需的交易所交易日历和逐日 ST/停牌状态 Provider。因此，策略评估前必须通过可信来源准备 `reference.json`，至少包含目标证券资料、as_of 所需的每日状态和交易日历。

```json
{
  "securities": [
    {
      "ts_code": "600519.SH",
      "symbol": "600519",
      "name": "贵州茅台",
      "exchange": "SSE",
      "list_status": "L"
    }
  ],
  "daily_states": [
    {
      "trade_date": "2026-10-30",
      "ts_code": "600519.SH",
      "st_type": "NONE",
      "suspend_type": "NONE",
      "source": "manual-verified-source",
      "fetched_at": "2026-10-30T10:30:00+08:00"
    }
  ],
  "trade_calendar": [
    {
      "exchange": "SSE",
      "cal_date": "2026-10-29",
      "is_open": true,
      "source": "EXCHANGE_NOTICE",
      "source_url": "https://www.sse.com.cn/",
      "fetched_at": "2026-10-01T09:00:00+08:00"
    },
    {
      "exchange": "SSE",
      "cal_date": "2026-10-30",
      "is_open": true,
      "source": "EXCHANGE_NOTICE",
      "source_url": "https://www.sse.com.cn/",
      "fetched_at": "2026-10-01T09:00:00+08:00"
    }
  ]
}
```

字段枚举和时间必须符合领域模型校验；上面只是格式示例，不能把示例日期/证券状态当作真实市场数据。批次按证券资料、每日状态、交易日历三个已有仓储事务依次写入，不是跨三类数据的单一原子事务。

```powershell
go run ./cmd/ashare-mvp import-reference --file .\\data\\reference.json
```

只有数据来源核验确认上市状态后，才可将 `ListStatus` 设为 `L`。未知、ST/停牌历史缺失或交易日历缺失时，不应为了让扫描启动而伪造状态。

## 5. 配置策略与运行评估

复制 `configs/runtime.example.json` 为本地 `configs/runtime.json`。示例阈值仅为配置格式演示/测试夹具，不代表经过回测或实盘验证的推荐参数。运行前必须由使用者确认所有阈值并修改两个 `Version`，不得把示例值直接当作交易建议。

```powershell
# 先启动另一个终端中的 CZSC 服务
go run ./cmd/ashare-mvp evaluate --ts-code 600519.SH --config .\\configs\\runtime.json
```

省略 `--as-of` 时以本机当前时间作为决策时点；仅在对应历史数据的 `FetchedAt` 不晚于该时点时才可指定历史 `--as-of`。gotdx 实时抓取的历史 K 线，其采集时间是当前时刻，不能拿来伪造过去时点的 PIT 输入。单股评估会调用现有应用服务：读取 PIT 行情/证券状态/交易日历、按 profile 请求 CZSC、独立评估两套策略、分别保存决策，并输出两套策略结果和 DecisionID。CZSC URL、超时和可选 token 由配置提供；token 也可通过环境变量 `ASHARE_CZSC_TOKEN` 注入。

## 6. 扫描与历史查询

```powershell
go run ./cmd/ashare-mvp scan --config .\\configs\\runtime.json
go run ./cmd/ashare-mvp scan-result --scan-id scan-<实际返回的ID>
go run ./cmd/ashare-mvp history --ts-code 600519.SH --limit 50 --offset 0
```

扫描仅读取 SQLite 中明确标记为 ACTIVE 的普通 A 股股票池，并按代码排序；每只证券复用单股评估服务，扫描运行/逐股状态/DecisionID 由现有 scanner 和 SQLite 仓储留痕。扫描结果读取不会重新计算策略。历史查询支持 `--strategy-id`、`--status`、`--from`、`--to`、`--limit`、`--offset`。

## 7. 待开发事项（当前未实现）

以下三项是现有运行链路的明确缺口，**状态均为待开发**。本次 CLI 接入没有修改这些业务逻辑，也不能将其视为已通过验收。

### 7.1 证券状态数据接入与扫描前 ST/停牌排除

<badge>待开发</badge>

- **现状**：gotdx adapter 当前没有本项目需要的交易日历、逐日 ST/停牌状态 Provider；参考数据只能通过可信来源导入。扫描器当前尚未证明会在扫描前按目标交易日的逐日状态自动排除 ST/*ST 和停牌证券。
- **待开发内容**：
  1. 接入或实现可信的交易日历、逐日 ST/停牌状态数据 Provider，并保留来源、采集时间和 PIT 可追溯信息。
  2. 在扫描入口/股票池资格判断处，基于本次扫描的 `as_of` 对应交易日状态，明确排除 ST/*ST、停牌及状态缺失/过期的证券。
  3. 保证单股评估与批量扫描采用一致的证券状态资格规则；未知状态不得默认视为正常。
- **验收要求**：覆盖正常证券、ST、*ST、停牌、状态缺失、状态过期及历史 `as_of` 场景；证明不合格证券不会进入策略评估，并验证状态数据来源和时点可审计。
- **运行限制**：在此项完成并验收前，不应将当前 CLI 扫描视为可直接用于全市场扫描；只能对已人工核验的少量 ACTIVE 股票池进行受控验证。

### 7.2 Context 取消与超时传播

<badge>待开发</badge>

- **现状**：自动审查指出，调用链对 `context.Canceled` 和 `context.DeadlineExceeded` 的传播行为仍需核对；当前不能宣称取消/超时语义已完整贯通。
- **待开发内容**：核对 CLI、扫描编排、单股评估、行情/CZSC Provider 和仓储调用之间的 Context 传递；取消或超时时保留原始错误语义，不将其误报为普通策略失败或继续执行后续工作。
- **验收要求**：分别覆盖主动取消、deadline 超时、扫描中途取消及 Provider 超时；验证调用及时结束、错误可识别、扫描运行与逐股状态留痕一致。

### 7.3 策略成功但决策持久化失败的错误分类

<badge>待开发</badge>

- **现状**：自动审查指出，策略分析成功但决策保存失败时，错误分类可能被归为分析失败；该分类尚未完成核实和修正。
- **待开发内容**：明确区分行情/策略分析错误与决策持久化错误；当策略计算已成功而保存失败时，向上层返回可识别的存储错误，不得把它记录成策略分析失败或伪装为成功。
- **验收要求**：模拟决策仓储写入失败，验证策略计算结果与持久化结果分别表达、错误类型准确、扫描运行/逐股状态可审计，且不会生成误导性的成功记录。

## 8. 运行验证边界

运行命令不等于实盘验证。应先用少量明确证券和可信历史快照验证数据质量，再扩大扫描范围。上述三项待开发事项完成并通过对应验收前，不应将 CLI 当作具备完整全市场风险过滤和稳定取消/错误分类能力的生产扫描系统。
