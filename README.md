# aShare-mvp

个人 A 股研究与交易辅助平台 V1。

当前实施边界：

- 选股、择时、买卖观察、观察池与复盘。
- 首发策略：缠论三买、趋势突破；两套策略独立运行。
- 不做回测、不接券商、不自动下单、不做订单执行。
- Go 模块化单体；CloudWeGo Hertz 作为后续 HTTP API 入口。
- SQLite 优先；Parquet / DuckDB 延后。
- 行情优先复用 gotdx 公开 API；CZSC 通过独立 HTTP/JSON 服务复用。

## 当前代码阶段

| 阶段 | 内容 | 状态 |
|---|---|---|
| 01 | D1/D2 领域契约、SQLite 基础表与仓储 | 已生成 |
| 02 | gotdx 证券名单、日线/30m/60m 接入适配 | 已生成 |
| 03 | CZSC HTTP/JSON 客户端与独立分析服务 | 下一段 |
| 04 | 两套策略规则与状态跟踪 | 待生成 |
| 05 | Hertz API、扫描调度与工作台接口 | 待生成 |

## 本地运行

需要 Go 1.27.2 或更高版本。

```bash
go test ./...
go run ./cmd/ashare-mvp -db ./data/ashare.db
```

首次运行会创建 SQLite 文件并执行嵌入式 SQL migration。

## 实施原则

1. 先检查项目/依赖已有能力，再决定是否补代码。
2. gotdx / CZSC 使用其公开 API，不复制协议解析或缠论算法。
3. go-stock 是 Wails 应用，其 backend/data 与应用 DB、logger、配置等存在耦合；当前仓库不导入整个应用，也不复制其多源降级实现。先用 gotdx 作为可运行数据入口，保留 Provider 边界，待 go-stock 数据能力可独立调用时再接入。
4. 不完整的行情名单、未知交易日、未知证券状态和质量不合格 K 线不得伪装成成功结果。
5. 任何策略输出都只是研究辅助，不是交易指令。
