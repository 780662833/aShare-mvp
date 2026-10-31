"""Native CZSC signal-engine integration tests for the pinned dependency."""

import math
import unittest
from datetime import datetime, timedelta, timezone

from czsc_service.analyzer import analyze_payload


SHANGHAI = timezone(timedelta(hours=8))


def make_payload(freq: str, profile: str, count: int = 220) -> dict:
    if freq == "日线":
        start = datetime(2025, 1, 2, 15, 0, tzinfo=SHANGHAI)
        step = timedelta(days=1)
    elif freq == "60分钟":
        start = datetime(2025, 1, 2, 10, 30, tzinfo=SHANGHAI)
        step = timedelta(hours=1)
    else:
        start = datetime(2025, 1, 2, 9, 30, tzinfo=SHANGHAI)
        step = timedelta(minutes=30)

    bars = []
    for i in range(count):
        close = 100.0 + 0.018 * i + 7.0 * math.sin(i * 0.31)
        open_price = close + 0.45 * math.sin(i * 0.83)
        high = max(open_price, close) + 0.8
        low = min(open_price, close) - 0.8
        volume = 1200.0 + 250.0 * (1.0 + math.sin(i * 0.17))
        bars.append({
            "dt": (start + i * step).isoformat(),
            "symbol": "600519.SH",
            "open": open_price,
            "close": close,
            "high": high,
            "low": low,
            "vol": volume,
            "amount": volume * close,
        })

    return {
        "schema_version": "v1",
        "ts_code": "600519.SH",
        "freq": freq,
        "adjust_mode": "QFQ",
        "source": "native-test-fixture",
        "signal_profile": profile,
        "bars": bars,
    }


class NativeSignalIntegrationTests(unittest.TestCase):
    def test_trend_breakout_profile_runs_all_native_signal_families(self):
        result = analyze_payload(make_payload("日线", "trend_breakout_d1_v1", 160))

        self.assertEqual(result["signal_profile"], "trend_breakout_d1_v1")
        self.assertEqual(result["signal_profile_version"], "trend-breakout-d1-v1")
        self.assertEqual(
            result["czsc_source_revision"],
            "5d350a943d6b1935561765fed5467c234973d121",
        )
        self.assertEqual(result["signal_data_status"], "READY")

        signals = result["signals"]
        self.assertIn("日线_D1W21_事件V240428", signals)
        self.assertIn("日线_D1W31_事件V240428", signals)
        self.assertIn("日线_D1W56_事件V240428", signals)
        self.assertIn("日线_D1SMA#20_分类V221101", signals)
        self.assertIn("日线_D1SMA#60_分类V221101", signals)
        self.assertIn("日线_D1VOL#SMA#20_分类V230214", signals)
        self.assertTrue(all(isinstance(value, str) for value in signals.values()))
        self.assertIn("日线_D1W21_事件V240428", result["signal_lifecycles"])
        self.assertGreaterEqual(result["signal_lifecycles"]["日线_D1W21_事件V240428"]["age_bars"], 0)
        self.assertTrue(result["signal_lifecycles"]["日线_D1W21_事件V240428"]["started_at"].endswith("+08:00"))

    def test_chan3buy_profile_executes_primary_and_support_signals(self):
        result = analyze_payload(make_payload("60分钟", "chan3buy_m60_v1", 360))

        self.assertEqual(result["signal_profile_version"], "chan3buy-m60-v1")
        self.assertIn("60分钟_D1_三买辅助V230228", result["signals"])
        self.assertTrue(any("V230318" in key for key in result["signals"]))
        self.assertTrue(any("V230319" in key for key in result["signals"]))
        self.assertIn("60分钟_D1_三买辅助V230228", result["signal_lifecycles"])
        self.assertIn(result["signal_data_status"], {"READY", "INSUFFICIENT_DATA"})

    def test_m30_timing_profile_executes_native_bi_status_signal(self):
        result = analyze_payload(make_payload("30分钟", "chan3buy_m30_timing_v1", 180))

        self.assertEqual(result["signal_profile_version"], "chan3buy-m30-timing-v1")
        self.assertIn("30分钟_D1_表里关系V230102", result["signals"])
        self.assertIn("30分钟_D1_表里关系V230102", result["signal_lifecycles"])
        self.assertIn(result["signal_data_status"], {"READY", "INSUFFICIENT_DATA"})


if __name__ == "__main__":
    unittest.main()
