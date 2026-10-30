# 代码生成第 01 段：D1/D2 领域契约与 SQLite 基础层

## 本段目标

将 D1 / D2 中真正需要跨模块共用的事实模型落入代码：证券基础资料、证券每日状态、交易日历、标准 K 线，以及最小 SQLite 持久化。

## 复用决策

- go-stock 的 `StockBasic` 字段作为模型基线。
- 不把 go-stock 整个 Wails 应用作为依赖：其 `backend/data` 与全局 DB、logger、配置和应用包存在耦合。
- 后续行情接入直接调用 gotdx 公开 API；本段不复制通达信协议。
- CZSC 的结构和信号不在 Go 中实现；下一阶段通过 HTTP/JSON 复用 CZSC。

## 新增内容

- `internal/market/universe`：StockBasic 与最小每日 ST / 停牌状态。
- `internal/market/calendar`：交易所日历行及来源优先级。
- `internal/marketdata`：D1 / M30 / M60 标准 K 线、上海时区、OHLCV 质量门。
- `internal/storage/sqlite`：SQLite WAL、嵌入式 migration、基础 upsert / query。
- `cmd/ashare-mvp`：数据库初始化入口。

## 数据约束

- `ts_code` 是证券唯一键，纯 `symbol` 不是跨市场唯一键。
- 日历没有记录时视为 UNKNOWN，不允许按周末/节假日推断后自动写开市。
- K 线时间必须有 +08:00 偏移并表示 K 线结束时间。
- 同次 K 线写入只允许单证券、单周期、单复权口径，时间严格递增。
- 未确认成交量量纲时存 `SOURCE_NATIVE` / `UNKNOWN`，不伪装成股数。
- 复权方式、来源、是否完成都随每根 K 线存储。

## 暂不包含

本段没有实现交易所公告抓取、Tushare API 调用、全市场扫描、行情 Provider、CZSC 客户端、策略、Hertz Handler 或 UI。这些分别在后续段落实现，避免将尚未实测的外部接口误包装成已可用功能。

## 本段验收

```bash
go test ./...
go run ./cmd/ashare-mvp -db ./data/ashare.db
```
