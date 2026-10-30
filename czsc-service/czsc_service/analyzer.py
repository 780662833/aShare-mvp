"""CZSC 1.0.x 分析适配；所有缠论结构由 CZSC 原生实现。"""

from __future__ import annotations

from datetime import datetime, timedelta, timezone
import math
from typing import Any

FREQ_MAP = {"日线": "D", "30分钟": "F30", "60分钟": "F60"}
VALID_ADJUST = {"QFQ", "HFQ", "NONE"}
MAX_BARS = 5000
SHANGHAI_TZ = timezone(timedelta(hours=8))


def _finite_number(value: Any, field: str, *, positive: bool = False) -> float:
    if isinstance(value, bool):
        raise ValueError(f"{field} 必须是数值")
    try:
        number = float(value)
    except (TypeError, ValueError) as exc:
        raise ValueError(f"{field} 必须是数值") from exc
    if not math.isfinite(number):
        raise ValueError(f"{field} 必须是有限数值")
    if positive and number <= 0:
        raise ValueError(f"{field} 必须大于 0")
    if not positive and number < 0:
        raise ValueError(f"{field} 不能小于 0")
    return number


def _czsc_wall_time(value: datetime) -> datetime:
    """保留上海市场墙上时间数值，转换为 CZSC 要求的 naive datetime。

    CZSC 1.0.x RawBar 内部以 UTC DateTime 存储市场时间数值，构造器拒绝
    tz-aware 入参。因此这里只移除 tzinfo，不调用 astimezone/utc，也不改变
    hour/minute。否则 09:30+08 会被换算为 01:30，破坏 A 股周期桶定位。
    """
    if value.tzinfo is None or value.utcoffset() != timedelta(hours=8):
        raise ValueError("市场时间必须显式带 +08:00")
    return value.replace(tzinfo=None)


def _market_iso(value: Any) -> str:
    """CZSC 返回 naive datetime；重新附加 +08:00 标签，不做时间数值换算。"""
    if hasattr(value, "to_pydatetime"):
        value = value.to_pydatetime()
    if isinstance(value, datetime) and value.tzinfo is None:
        value = value.replace(tzinfo=SHANGHAI_TZ)
    if hasattr(value, "isoformat"):
        return value.isoformat()
    return str(value)


def validate_payload(payload: Any) -> dict[str, Any]:
    if not isinstance(payload, dict):
        raise ValueError("请求体必须是 JSON object")
    if payload.get("schema_version") != "v1":
        raise ValueError("schema_version 必须为 v1")

    ts_code = str(payload.get("ts_code", "")).strip().upper()
    if not ts_code or "." not in ts_code:
        raise ValueError("ts_code 必须包含证券代码和交易所后缀")
    freq = str(payload.get("freq", "")).strip()
    if freq not in FREQ_MAP:
        raise ValueError(f"不支持的 CZSC 周期: {freq!r}")
    adjust_mode = str(payload.get("adjust_mode", "")).strip().upper()
    if adjust_mode not in VALID_ADJUST:
        raise ValueError("adjust_mode 必须是明确的 QFQ / HFQ / NONE")
    source = str(payload.get("source", "")).strip()
    if not source:
        raise ValueError("source 不能为空")

    raw_bars = payload.get("bars")
    if not isinstance(raw_bars, list) or not raw_bars:
        raise ValueError("bars 必须是非空数组")
    if len(raw_bars) > MAX_BARS:
        raise ValueError(f"bars 数量不能超过 {MAX_BARS}")

    bars: list[dict[str, Any]] = []
    previous_dt: datetime | None = None
    for index, raw in enumerate(raw_bars):
        if not isinstance(raw, dict):
            raise ValueError(f"bars[{index}] 必须是 object")
        symbol = str(raw.get("symbol", "")).strip().upper()
        if symbol != ts_code:
            raise ValueError(f"bars[{index}].symbol 与 ts_code 不一致")
        dt_text = str(raw.get("dt", "")).strip()
        try:
            dt = datetime.fromisoformat(dt_text.replace("Z", "+00:00"))
        except ValueError as exc:
            raise ValueError(f"bars[{index}].dt 不是有效 ISO-8601 时间") from exc
        if dt.tzinfo is None or dt.utcoffset() != timedelta(hours=8):
            raise ValueError(f"bars[{index}].dt 必须显式带 +08:00")
        if previous_dt is not None and dt <= previous_dt:
            raise ValueError("bars 必须按 dt 严格升序且不能重复")
        previous_dt = dt

        bar = {
            "dt": dt,
            "czsc_dt": _czsc_wall_time(dt),
            "symbol": symbol,
            "open": _finite_number(raw.get("open"), f"bars[{index}].open", positive=True),
            "close": _finite_number(raw.get("close"), f"bars[{index}].close", positive=True),
            "high": _finite_number(raw.get("high"), f"bars[{index}].high", positive=True),
            "low": _finite_number(raw.get("low"), f"bars[{index}].low", positive=True),
            "vol": _finite_number(raw.get("vol"), f"bars[{index}].vol"),
            "amount": _finite_number(raw.get("amount"), f"bars[{index}].amount"),
        }
        if bar["high"] < max(bar["open"], bar["close"], bar["low"]):
            raise ValueError(f"bars[{index}] high 小于 OHLC 其他值")
        if bar["low"] > min(bar["open"], bar["close"], bar["high"]):
            raise ValueError(f"bars[{index}] low 大于 OHLC 其他值")
        bars.append(bar)

    return {
        "schema_version": "v1",
        "ts_code": ts_code,
        "freq": freq,
        "freq_native": FREQ_MAP[freq],
        "adjust_mode": adjust_mode,
        "source": source,
        "bars": bars,
    }


def analyze_payload(payload: Any) -> dict[str, Any]:
    request = validate_payload(payload)
    try:
        from czsc import CZSC, Freq, RawBar
    except ImportError as exc:
        raise RuntimeError("CZSC 未安装或 Rust/PyO3 扩展不可用") from exc

    freq = {"D": Freq.D, "F30": Freq.F30, "F60": Freq.F60}[request["freq_native"]]
    raw_bars = [
        RawBar(
            item["symbol"], item["czsc_dt"], freq,
            item["open"], item["close"], item["high"], item["low"],
            item["vol"], item["amount"], index,
        )
        for index, item in enumerate(request["bars"])
    ]
    analyzer = CZSC(raw_bars)

    recent_fx = [
        {
            "dt": _market_iso(item.dt),
            "mark": item.mark.name,
            "fx": float(item.fx),
            "high": float(item.high),
            "low": float(item.low),
        }
        for item in analyzer.fx_list[-5:]
    ]
    recent_bi = [
        {
            "start_time": _market_iso(item.sdt),
            "end_time": _market_iso(item.edt),
            "direction": item.direction.name,
            "high": float(item.high),
            "low": float(item.low),
            "power": float(item.power),
            "length": int(item.length),
        }
        for item in analyzer.bi_list[-5:]
    ]
    recent_zs = [
        {
            "start_time": _market_iso(item.sdt),
            "end_time": _market_iso(item.edt),
            "zg": float(item.zg),
            "zd": float(item.zd),
            "zz": float(item.zz),
        }
        for item in analyzer.zs_list[-5:]
    ]

    return {
        "schema_version": request["schema_version"],
        "ts_code": request["ts_code"],
        "freq": request["freq"],
        "adjust_mode": request["adjust_mode"],
        "source": request["source"],
        "as_of": request["bars"][-1]["dt"].isoformat(),
        "input_bar_count": len(raw_bars),
        "fx_count": len(analyzer.fx_list),
        "bi_count": len(analyzer.bi_list),
        "zs_count": len(analyzer.zs_list),
        "recent_fx": recent_fx,
        "recent_bi": recent_bi,
        "recent_zs": recent_zs,
        "last_bi_extend": bool(analyzer.last_bi_extend),
    }
