# aShare-mvp

个人 A 股研究与交易辅助平台 V1。

**当前状态：S1–S5 基础能力已完成；完整扫描 CLI 与本机 HTTP API 的代码和 CI 自动化测试已完成，位于 `feature/full-scan-product-api` 分支。** 尚未合入 `main`，Windows 本机端到端验收待执行；本次范围完成后停止扩展。

## 产品范围

- 选股、择时、买卖观察、扫描结果留痕、历史决策查询与复盘。
- 首发策略：缠论三买、趋势突破；两套策略独立运行、独立保存决策。
- 策略输出是研究观察，不是交易指令；交易决定由用户作出。
- 不做回测、不接券商、不自动下单、不做订单执行。
- Go 模块化单体承载业务规则与编排；CZSC 通过独立 HTTP/JSON 服务复用；产品 API 复用 Go 应用服务。
- SQLite 优先；Parquet / DuckDB 延后。
- 行情优先复用 gotdx 公开 API；保留 Provider 边界。

## 架构与阶段总结

- [V1 架构文档索引](docs/architecture/README.md)：产品范围、模块架构、数据中心、D1/D2 数据契约和策略规则。
- [S1–S5 最终交付总结与项目阶段性状态](docs/implementation/07-s1-s5-stage-closeout.md)：阶段交付清单、当前架构、数据流、策略规则边界、未交付能力和使用反馈期事项。

## 当前阶段（截至 2026-10-31）

| 阶段 | 交付内容 | 状态 |
|---|---|---|
| S1 | D1/D2 数据模型、SQLite 基础层、gotdx 适配、CZSC HTTP/JSON 服务、数据质量加固 | 已完成 |
| S2 | 缠论三买与趋势突破两套独立 Go 策略 | 已完成 |
| S3 | 单股双策略评估应用服务、PIT/新鲜度检查、决策持久化与生命周期校验 | 已完成 |
| S4.3 | 扫描编排、扫描运行/逐股结果留痕、原始 DecisionID 关联读取 | 已完成 |
| S4.4 | 趋势突破 M30 真实回踩与转强确认规则 | 已完成 |
| S5 | 历史策略决策筛选、稳定排序、分页、总数与原始 payload 查询 | 已完成 |
| Runtime V1 | 全 ACTIVE 普通 A 股批量行情同步、全市场扫描资格门、CLI 和本机 HTTP API | 代码与 CI 已完成；待 Windows 本机验收 |

S5 通过 PR [#6](https://github.com/780662833/aShare-mvp/pull/6) 合并到 `main`，merge commit 为 `6847d9237ddb2e1b2ead8e0671ee18500dd8e59c`。合并后 Go tests 已通过；CZSC contract/native workflow 的最终状态请查看 [GitHub Actions](https://github.com/780662833/aShare-mvp/actions)。

## 当前不包含

- 完整研究工作台/UI。
- 自动行情同步、完整 ST/停牌历史、交易所公告日历自动更新。
- 回测、Walk-forward、参数优化和收益验证。
- 组合管理、资金分配和持仓风险预算。
- 券商 API、订单执行和自动交易。
- AI 自主决策或自动交易代理。

以上项目不在本次交付范围内。完成当前 V1 产品交付后停止扩展，是否规划下一阶段等待实际使用反馈。

## 本地运行

需要 Go 1.27.2 或更高版本。当前交付分支提供完整 CLI（含全 ACTIVE 普通 A 股批量行情同步）和版本化本机 HTTP/JSON API；OpenAPI 3.1 契约见 `docs/implementation/openapi-v1.yaml`。完整步骤与参考数据格式见 [CLI 实际运行说明](docs/implementation/08-runtime-cli.md)。

```powershell
go test ./... -count=1
go run ./cmd/ashare-mvp init
go run ./cmd/ashare-mvp help
# 本机 API：go run ./cmd/ashare-mvp serve --config configs/runtime.json
```

策略配置模板：复制 `configs/runtime.example.json` 为本地配置文件。模板阈值只是配置格式演示，不是经过回测/实盘验证的推荐参数，运行前必须由使用者确认并修改配置版本。gotdx 不提供本项目所需的交易日历和逐日 ST/停牌状态，这些参考数据必须从可信来源导入；证券状态为 UNKNOWN 的记录不会进入扫描池。

**扫描与 API 交付边界：**

- 扫描资格门已按 as_of 对应交易日校验交易日历和逐日状态；ST/*ST、停牌、状态未知/缺失/过期及非上市时点证券会被排除，并在扫描记录中留存原因。
- 可信证券状态和交易日历仍通过 import-reference 导入；gotdx 不提供这些事实数据，本版本不伪造自动 Provider。参考数据不完整时扫描 fail closed。
- Context 取消/超时在 CLI、扫描、评估和 CZSC Provider 调用链传播；策略成功但决策持久化失败会独立分类。
- API 仅监听 localhost/loopback，支持单股评估、后台扫描、扫描进度/逐股结果/排除项分页和历史决策查询；不提供远程开放访问、前端 UI 或自动交易。

使用和验收细节见 [CLI 实际运行说明](docs/implementation/08-runtime-cli.md) 与 [产品 API 说明](docs/implementation/09-product-api.md)。CI 通过不等于用户 Windows 本机端到端验收，首次实际运行仍需按手册验证。

CZSC 服务：

```bash
cd czsc-service
python -m venv .venv
pip install -r requirements.txt
python app.py
```

默认 CZSC 监听 127.0.0.1:8091。

## 实施原则

1. 先检查项目/依赖已有能力，再决定是否补代码。
2. gotdx / CZSC 使用其公开 API，不复制协议解析或缠论算法。
3. go-stock 是 Wails 应用，其 backend/data 与应用 DB、logger、配置等存在耦合；当前仓库不导入整个应用，也不复制其多源降级实现。先用 gotdx 作为可运行数据入口，保留 Provider 边界，待 go-stock 数据能力可独立调用时再接入。
4. 不完整的行情名单、未知交易日、未知证券状态和质量不合格 K 线不得伪装成成功结果。
5. 任何策略输出都只是研究辅助，不是交易指令。
