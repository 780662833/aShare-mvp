# 代码生成第 03 段：CZSC HTTP/JSON 分析边界

## 本段目标

把 Go 标准 K 线交给独立 Python/CZSC 服务，复用 CZSC Rust/PyO3 算法；Go 只做输入校验、DTO 转换、HTTP 调用和响应版本检查。

## 已生成

- internal/czscclient：Go 请求 DTO、完成 K 线筛选、HTTP 客户端、响应 DTO。
- czsc-service：锁定 CZSC 上游 commit 的 Python 服务，使用 RawBar / CZSC 公开 API。
- 服务默认只监听本机回环地址；远程监听必须配置 token。

## 数据边界

- 不允许未完成 K 线进入正式分析。
- 单请求必须是单证券、单周期、单复权、单来源序列。
- 日期时间使用显式 +08:00；服务端不把 naive datetime 猜成上海时间。
- 只返回结构摘要，不输出买卖指令。
- as_of 等于最后一根传入 K 线时间，便于 Go 策略层对账。

## 本段未做

- 没有在 Python 中重复计算三买、趋势突破等策略。
- 没有使用 CZSC 回测、参数优化或 Position 执行逻辑。
- 没有硬编码第三买信号注册名；策略阶段按锁定 CZSC 版本实际公开信号进行核对。
- 当前容器无法连接 GitHub 下载 Go 工具链或 CZSC 依赖，本段尚未完成真实 Go/Python 端到端测试。

## 验收命令

Go：
```bash
go test ./...
```

Python：
```bash
cd czsc-service
python -m unittest discover -s czsc_service -p "test_*.py"
```
