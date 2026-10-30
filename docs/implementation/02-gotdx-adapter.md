# 代码生成第 02 段：gotdx 名单与 K 线适配

## 本段目标

直接调用 github.com/bensema/gotdx 的公开高阶 API，将 D1 / D2 约束转成可用的数据接入代码，不复制通达信二进制协议。

## 接口复用

- 名单：按 SZ / SH / BJ 分别调用 Client.StockAll()。
- K 线：调用 Client.StockKLine()，直接获取日线、30 分钟和 60 分钟。
- 初筛：复用 types.IsStock()、types.IsETF()、types.IsIndex()。
- 复权：显式使用 types.AdjustQFQ。
- K 线协议结构：直接读取 proto.SecurityBar，不创建第二套 TDX 结构体。

## 完整性约束

FetchUniverse 返回每个市场的结果状态。任一市场失败时，仍返回已取得的部分数据用于诊断，但同时返回错误且 Complete() == false。调用方不得把部分结果当全市场名单覆盖数据库。

gotdx 名单不包含完整上市状态、行业与 ST 历史，因此转换出来的 ListStatus 标记为 UNKNOWN；后续必须由基础资料来源补齐，不能从代码或名称推断。

## 时间与数据量纲

- 从 SecurityBar.Year/Month/Day/Hour/Minute 取墙上时间字段，以显式 UTC+08:00 重建 bar_time。
- 不对 gotdx 返回的 DateTime 直接调用 .In(Asia/Shanghai)，避免把服务器本地时间误转换成错误时刻。
- 日线统一锚定在交易日 15:00。
- gotdx 未提供每根 K 线的明确完成标志，当前按结束时间加 1 分钟宽限做时钟判断；这不能代替交易所日历和数据源最终性核验。
- 成交量单位暂标记 SOURCE_NATIVE，没有猜测股/手；成交额按 gotdx 数值返回。量纲仍须用真实证券样本与源端数据核对。

## go-stock 复用边界说明

go-stock 的 FetchKLineWithFallback() 与 GetAllStockList() 位于其 Wails 应用 backend/data 包，该包依赖应用内数据库、logger 和配置。其 go.mod 模块名为 go-stock，不是为外部项目发布的独立 SDK。当前阶段不导入整个 Wails 应用，也不复制其 MAC→东财→新浪→腾讯→TDX 降级链；本段先实现 gotdx 直接可用入口。若后续需要 go-stock 多源回退，应先取得可独立调用的函数/包边界，再实现 Provider 适配器。

## 本段验收

```bash
go test ./...
```
