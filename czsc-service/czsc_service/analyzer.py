"""CZSC 1.0.x 分析适配；所有缠论结构和原生信号由 CZSC 实现。"""

from __future__ import annotations

from copy import deepcopy
from datetime import datetime, timedelta, timezone
import math
from typing import Any

FREQ_MAP = {"日线": "D", "30分钟": "F30", "60分钟": "F60"}
VALID_ADJUST = {"QFQ", "HFQ", "NONE"}
MAX_BARS = 5000
SHANGHAI_TZ = timezone(timedelta(hours=8))

# 与 requirements.txt 中精确锁定的 CZSC commit 保持一致。
CZSC_SOURCE_REVISION = "5d350a943d6b1935561765fed5467c234973d121"

# 信号配置是服务端白名单；请求方只能选择 profile，不能传入任意 Rust 信号函数。
SIGNAL_PROFILES: dict[str, dict[str, Any]] = {
    "structure_only": {
        "version": "structure-only-v1",
        "freq": None,
        "signals": [],
        "min_bars": 1,
        "min_bi_count": 0,
    },
    "chan3buy_m60_v1": {
        "version": "chan3buy-m60-v1",
        "freq": "60分钟",
        "signals": [
            {"name": "cxt_third_buy_V230228", "freq": "60分钟", "di": 1},
            {
                "name": "cxt_third_bs_V230318",
                "freq": "60分钟",
                "di": 1,
                "ma_type": "SMA",
                "timeperiod": 34,
            },
            {
                "name": "cxt_third_bs_V230319",
                "freq": "60分钟",
                "di": 1,
                "ma_type": "SMA",
                "timeperiod": 34,
            },
        ],
        "min_bars": 1,
        "min_bi_count": 12,
    },
    "chan3buy_m30_timing_v1": {
        "version": "chan3buy-m30-timing-v1",
        "freq": "30分钟",
        "signals": [
            {"name": "cxt_bi_status_V230102", "freq": "30分钟"},
        ],
        "min_bars": 1,
        "min_bi_count": 3,
    },
    "trend_breakout_d1_v1": {
        "version": "trend-breakout-d1-v1",
        "freq": "日线",
        "signals": [
            {"name": "bar_break_V240428", "freq": "日线", "di": 1, "w": 21},
            {"name": "bar_break_V240428", "freq": "日线", "di": 1, "w": 31},
            {"name": "bar_break_V240428", "freq": "日线", "di": 1, "w": 56},
            {
                "name": "tas_ma_base_V221101",
                "freq": "日线",
                "di": 1,
                "ma_type": "SMA",
                "timeperiod": 20,
            },
            {
                "name": "tas_ma_base_V221101",
                "freq": "日线",
                "di": 1,
                "ma_type": "SMA",
                "timeperiod": 60,
            },
            {
                "name": "vol_single_ma_V230214",
                "freq": "日线",
                "di": 1,
                "ma_type": "SMA",
                "timeperiod": 20,
            },
        ],
        # bar_break_V240428 对 w=56 要求至少 7+w 根原始 K 线。
        "min_bars": 63,
        "min_bi_count": 0,
    },
}


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
    if isinstance(value, datetime):
        if value.tzinfo is None:
            value = value.replace(tzinfo=SHANGHAI_TZ)
        elif value.utcoffset() != timedelta(hours=8):
            raise ValueError("CZSC 输出时间必须保留 Asia/Shanghai +08:00 语义")
        return value.isoformat()
    if hasattr(value, "isoformat"):
        return value.isoformat()
    return str(value)


def _resolve_signal_profile(profile: Any, freq: str) -> tuple[str, dict[str, Any]]:
    name = str(profile or "structure_only").strip() or "structure_only"
    spec = SIGNAL_PROFILES.get(name)
    if spec is None:
        raise ValueError(f"不支持的 signal_profile: {name!r}")
    expected_freq = spec["freq"]
    if expected_freq is not None and freq != expected_freq:
        raise ValueError(
            f"signal_profile {name!r} 只支持 {expected_freq}，当前请求周期为 {freq}"
        )
    return name, deepcopy(spec)


def _signal_data_status(profile: str, analyzer: Any, bar_count: int) -> str:
    """标记该 profile 的最低计算数据条件，不代表策略信号是否成立。"""
    spec = SIGNAL_PROFILES[profile]
    if bar_count < spec["min_bars"]:
        return "INSUFFICIENT_DATA"
    if len(analyzer.bi_list) < spec["min_bi_count"]:
        return "INSUFFICIENT_DATA"
    if profile == "chan3buy_m30_timing_v1" and not analyzer.ubi_fxs:
        return "INSUFFICIENT_DATA"
    return "READY" if profile != "structure_only" else "NOT_REQUESTED"


def _latest_signal_values(rows: Any, freq: str) -> dict[str, str]:
    if not isinstance(rows, list) or not rows:
        raise RuntimeError("CZSC 信号引擎未返回最新计算记录")
    latest = rows[-1]
    if not isinstance(latest, dict):
        raise RuntimeError("CZSC 信号引擎最新记录格式无效")

    # CZSC 1.0.x 信号 key 采用「周期_参数_信号名」三段结构；
    # 过滤掉 dt、symbol 等元信息，保留原始 key/value，不在此处解释含义。
    prefix = f"{freq}_"
    result: dict[str, str] = {}
    for key, value in latest.items():
        if (
            isinstance(key, str)
            and key.startswith(prefix)
            and len(key.split("_")) == 3
            and isinstance(value, str)
        ):
            result[key] = value
    return result


def _signal_lifecycles(rows: Any, freq: str) -> dict[str, dict[str, Any]]:
    """为最新信号计算连续状态年龄及起始 K 线时间。

    年龄按连续相同信号值的已完成 K 线计数；最新 K 线为 0。
    仅使用本次 CZSC 实际生成的历史行，不从自然时间差猜测交易 bar 数。
    """
    latest_signals = _latest_signal_values(rows, freq)
    result: dict[str, dict[str, Any]] = {}
    for key, value in latest_signals.items():
        age_bars = 0
        start_row = rows[-1]
        for row in reversed(rows[:-1]):
            if not isinstance(row, dict) or row.get(key) != value:
                break
            age_bars += 1
            start_row = row
        started_at = start_row.get("dt")
        if started_at is None:
            raise RuntimeError(f"CZSC 信号历史缺少 dt，无法确定 {key!r} 的起始时间")
        result[key] = {
            "age_bars": age_bars,
            "started_at": _market_iso(started_at),
        }
    return result


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
    signal_profile, profile_spec = _resolve_signal_profile(
        payload.get("signal_profile", "structure_only"), freq
    )
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
        "signal_profile": signal_profile,
        "signal_profile_version": profile_spec["version"],
        "signals_config": profile_spec["signals"],
        "min_signal_bars": profile_spec["min_bars"],
        "min_signal_bi_count": profile_spec["min_bi_count"],
        "bars": bars,
    }


def analyze_payload(payload: Any) -> dict[str, Any]:
    request = validate_payload(payload)
    try:
        from czsc import CZSC, Freq, RawBar, generate_czsc_signals
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

    signals: dict[str, str] = {}
    signal_lifecycles: dict[str, dict[str, Any]] = {}
    if request["signals_config"]:
        rows = generate_czsc_signals(
            raw_bars,
            signals_config=request["signals_config"],
            sdt="20170101",
            init_n=min(500, max(len(raw_bars) - 1, 0)),
            df=False,
        )
        signals = _latest_signal_values(rows, request["freq"])
        signal_lifecycles = _signal_lifecycles(rows, request["freq"])

    signal_data_status = _signal_data_status(
        request["signal_profile"], analyzer, len(raw_bars)
    )

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
        "signal_profile": request["signal_profile"],
        "signal_profile_version": request["signal_profile_version"],
        "czsc_source_revision": CZSC_SOURCE_REVISION,
        "signal_data_status": signal_data_status,
        "signals": signals,
        "signal_lifecycles": signal_lifecycles,
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
