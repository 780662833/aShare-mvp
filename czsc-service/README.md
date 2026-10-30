# CZSC 独立分析服务

本服务使用 CZSC 1.0.x 现有 RawBar / CZSC API，不在 Go 中重写缠论算法。

## 安装与启动

需要 Python 3.10+。按 CZSC 上游说明准备带 Rust/PyO3 兼容环境。

```bash
python -m venv .venv
# Windows: .venv\Scripts\activate
# Linux/macOS: source .venv/bin/activate
pip install -r requirements.txt
python app.py
```

默认只监听 127.0.0.1:8091。跨主机监听时必须配置 CZSC_SERVICE_TOKEN。

## API

- GET /healthz
- POST /api/v1/analyze

请求只能包含同一证券、同一周期、同一复权口径、同一来源的已完成 K 线，按时间严格升序排列。时间必须带 +08:00。V1 返回 CZSC 原生分型、笔、中枢结构摘要，不在服务里直接生成 BUY/SELL 交易结论。

策略阶段将按锁定版本核对三买信号注册名，不在本阶段猜测或硬编码。
