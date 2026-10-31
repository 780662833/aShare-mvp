# 产品 HTTP API V1

## 1. 定位与安全边界

V1 是现有 Go 模块化单体的 HTTP/JSON 入口，复用同一组应用服务、Scanner 和 SQLite 仓储，不复制策略逻辑。

- 使用 Go 标准库 `net/http`，不增加 Web 框架依赖。
- API 仅监听 `localhost` / loopback 地址；V1 没有远程访问认证能力，不得将监听地址暴露到公网或局域网。
- 时间参数必须为带时区 RFC3339 / RFC3339Nano；服务内部转换为 Asia/Shanghai。
- 策略结果是研究观察，不是交易指令。
- 不包含券商接口、订单执行、自动交易、前端 UI、回测或 AI Agent。

## 2. 启动

先初始化数据库、导入可信证券主数据/逐日状态/交易日历，并启动 CZSC Python 服务。

```powershell
go run ./cmd/ashare-mvp init
go run ./cmd/ashare-mvp serve --config .\configs\runtime.json --addr 127.0.0.1:8080 --scan-timeout 30m
```

参数：
- `--db`：SQLite 文件路径，默认 `data/ashare.db`。
- `--config`：运行时策略与 CZSC 服务配置，必填。
- `--addr`：监听地址，默认 `127.0.0.1:8080`；只允许 localhost/loopback。
- `--scan-timeout`：单个后台扫描任务最大运行时间，默认 30m。
- `--timeout`：通用 CLI 命令超时；用于 serve 时，到期会触发服务关闭。
- Ctrl+C 会取消服务生命周期 Context，触发后台扫描取消，并在 10 秒 HTTP graceful shutdown 窗口内退出。

API 启动时会将数据库中上次进程遗留的 `RUNNING` 扫描任务恢复标记为 `CANCELLED`，保留已完成逐股结果和未处理的 `PENDING` 项；不会自动续跑。

## 3. 端点

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/healthz` | SQLite 连通性检查 |
| POST | `/api/v1/evaluations` | 单股双策略评估 |
| POST | `/api/v1/scans` | 启动后台扫描 |
| GET | `/api/v1/scans/{scan_id}` | 扫描状态和进度 |
| GET | `/api/v1/scans/{scan_id}/items` | 分页读取逐股结果 |
| GET | `/api/v1/scans/{scan_id}/exclusions` | 分页读取资格门排除项 |
| GET | `/api/v1/decisions` | 历史决策筛选与分页 |
| GET | `/api/v1/decisions/{decision_id}` | 单条决策详情 |

所有错误采用统一 JSON 外壳：

```json
{
  "error": {
    "code": "INVALID_REQUEST",
    "message": "具体错误说明"
  }
}
```

请求体最大 1 MiB；拒绝未知 JSON 字段和尾随的第二个 JSON 值。

## 4. 单股评估

`POST /api/v1/evaluations`

请求：

```json
{
  "ts_code": "600519.SH",
  "as_of": "2026-10-30T15:00:00+08:00"
}
```

PowerShell：

```powershell
$body = @{
  ts_code = "600519.SH"
  as_of = "2026-10-30T15:00:00+08:00"
} | ConvertTo-Json
Invoke-RestMethod -Method Post -Uri "http://127.0.0.1:8080/api/v1/evaluations" -ContentType "application/json" -Body $body
```

成功返回现有评估服务结果，包含双策略各自状态、DecisionID、数据版本和时点。证券状态不符合资格要求时返回 422；无效请求/时点返回 400；Provider deadline 返回 504；请求取消使用 499（非标准 HTTP 状态码，供本地客户端识别）。

## 5. 启动扫描

`POST /api/v1/scans`

请求：

```json
{
  "as_of": "2026-10-30T15:00:00+08:00"
}
```

启动成功返回 HTTP 202 和 `Location`：

```json
{
  "scan_id": "scan-...",
  "status": "RUNNING",
  "as_of": "2026-10-30T15:00:00+08:00"
}
```

如果扫描在响应前已经结束，可能返回 HTTP 200 和终态扫描记录。当前已有扫描时返回 409 `SCAN_ALREADY_RUNNING`。扫描运行和逐股明细写入 SQLite；进度查询不重新计算策略。

扫描资格门和边界：

1. 从 SQLite 冻结当前 `ACTIVE` 普通 A 股股票池，并按证券代码排序。
2. 对每个交易所读取 `as_of` 对应日历；若该自然日休市，回退至不晚于目标日期的最近已知开市日。
3. 交易日历必须通过校验，且 `fetched_at <= as_of`。
4. 要求证券逐日状态与目标交易日一致、状态记录采集时间不晚于 `as_of`，且 ST 状态和停牌状态均明确为 NONE。
5. ST、*ST、停牌、状态未知/缺失/过期、目标日尚未上市或已退市的证券不进入策略评估，写入独立排除记录和原因码。
6. 合格证券分别执行缠论三买和趋势突破策略；两套策略状态和决策分别留痕。

交易日历缺失/不可信时扫描 fail closed。证券每日状态和交易日历通过 CLI `import-reference` 导入；gotdx 不提供这些逐日事实数据，本版本不伪造自动 Provider。

**重要边界：** 扫描使用数据库中当前 `ACTIVE` 股票池快照，不重建历史日期完整上市证券池。不能把它描述为可重建任意历史时点的全市场成分。

## 6. 查询扫描

`GET /api/v1/scans/{scan_id}`

返回 `run`、`processed_count` 和 `pending_count`。run 包含总数、成功、数据不完整、分析错误、失败和排除数量。

`GET /api/v1/scans/{scan_id}/items?limit=50&offset=0`

返回逐股 `items`、`total`、`limit`、`offset`。每项包含证券代码、状态、数据版本、DecisionID、双策略结果、原因码和错误信息。

`GET /api/v1/scans/{scan_id}/exclusions?limit=50&offset=0`

返回资格门排除项，包含证券代码、交易所、交易日和原因码。

分页参数：`limit` 默认 50，范围 1–200；`offset` 默认 0，不能为负数。列表按证券代码稳定排序。扫描 ID 不存在时返回 404。

扫描状态：
- `RUNNING`：运行中。
- `COMPLETED`：全部合格证券成功。
- `PARTIAL_SUCCESS`：有成功项，同时存在数据不完整、分析错误或失败项。
- `FAILED`：没有成功项且扫描已结束。
- `CANCELLED`：主动取消、服务退出或任务超时；未处理项保留为 PENDING。

## 7. 历史决策

`GET /api/v1/decisions` 支持：
- `ts_code`
- `strategy_id`
- `status`
- `from`、`to`：带时区 RFC3339 时间
- `limit`：默认 50，范围 1–200
- `offset`：默认 0

示例：

```powershell
Invoke-RestMethod "http://127.0.0.1:8080/api/v1/decisions?ts_code=600519.SH&limit=50&offset=0"
Invoke-RestMethod "http://127.0.0.1:8080/api/v1/decisions/decision-..."
```

列表复用 S5 仓储查询；详情通过 `GET /api/v1/decisions/{decision_id}` 读取。记录不存在返回 404。

## 8. 错误码

| HTTP | code | 含义 |
|---:|---|---|
| 400 | `INVALID_REQUEST` | JSON 格式错误、未知字段或请求体超限 |
| 400 | `INVALID_ARGUMENT` | 缺少必填参数 |
| 400 | `INVALID_AS_OF` | 时点不是带时区 RFC3339 |
| 400 | `INVALID_PAGINATION` | 分页参数越界 |
| 404 | `NOT_FOUND` | 扫描或决策记录不存在 |
| 409 | `SCAN_ALREADY_RUNNING` | 已有后台扫描运行 |
| 422 | `SECURITY_INELIGIBLE` | 证券资格不满足 |
| 422 | `SCAN_START_FAILED` | 扫描预检或启动失败 |
| 500 | `EVALUATION_FAILED` / `STORAGE_ERROR` | 评估或存储失败 |
| 503 | `DATABASE_UNAVAILABLE` | SQLite 不可用 |
| 504 | `DEADLINE_EXCEEDED` | 评估 deadline 超时 |
| 499 | `REQUEST_CANCELLED` | 请求取消（非标准状态码） |

## 9. V1 运行约束

- 单进程内最多一个后台扫描任务；不支持多实例协调。
- 服务进程异常退出后，下一次启动会把遗留 RUNNING 任务标记为 CANCELLED，不会自动续跑。
- 本机 API 没有身份认证和授权；只能绑定 loopback。
- 本版本没有提供 OpenAPI 机器可读 schema；本文件是接口契约说明。
- CI 自动化测试不等于 Windows 本机端到端验收。首次运行仍需使用实际 gotdx 网络、经核验 reference 数据和 CZSC 服务完成联调。
