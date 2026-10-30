# 架构设计文档索引

本目录保存 aShare-mvp V1 已确认的产品范围、策略边界、模块架构和数据契约。它们是代码生成与验收的依据；若代码与文档不一致，应先明确修订决策，再改实现。

## 产品与总体架构

1. [产品范围与双策略规范](01-product-scope-and-dual-strategy.md)：V1 范围冻结、缠论三买和趋势突破两套策略边界、明确不做事项。
2. [模块架构与接口设计](02-module-architecture-and-interfaces.md)：Go 模块化单体、CZSC 独立服务、模块职责与接口草案。
3. [数据中心详细设计](03-data-center-design.md)：证券主数据、行情数据、数据治理与数据中心边界。
4. [数据存储与交易日历选型评审](04-storage-and-trading-calendar-review.md)：SQLite V1 选型及交易所日历来源策略。

## 数据契约

5. [D1 证券主数据与交易日历](05-d1-security-master-and-calendar.md)：复用优先的证券资料、每日状态与交易日历最小模型。
6. [D2 行情 K 线统一模型与时间语义](06-d2-kline-model-and-time-semantics.md)：D1/M30/M60、复权、来源、完成状态、Asia/Shanghai 时间和 CZSC 输入契约。

## 文档治理

- 本目录存放设计文档；具体实施状态见 docs/implementation。
- 不因文档中出现接口草案就默认它已经实现。
- 任何新字段、新表、策略或接口都必须先核对仓库中已有实现，优先复用并保持最小改动。
