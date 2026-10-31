import unittest
from datetime import datetime, timezone

from czsc_service.analyzer import (
    SIGNAL_PROFILES,
    _czsc_wall_time,
    _czsc_signal_market_iso,
    _market_iso,
    _signal_lifecycles,
    validate_payload,
)


def sample_payload():
    return {
        "schema_version": "v1",
        "ts_code": "600519.SH",
        "freq": "30分钟",
        "adjust_mode": "QFQ",
        "source": "gotdx",
        "bars": [{
            "dt": "2026-10-30T09:30:00+08:00",
            "symbol": "600519.SH",
            "open": 100,
            "close": 102,
            "high": 103,
            "low": 99,
            "vol": 1000,
            "amount": 102000,
        }],
    }


class ValidatePayloadTests(unittest.TestCase):
    def test_accepts_standard_bar_payload_and_defaults_to_structure_only(self):
        result = validate_payload(sample_payload())
        self.assertEqual(result["freq_native"], "F30")
        self.assertEqual(len(result["bars"]), 1)
        self.assertEqual(result["bars"][0]["czsc_dt"].isoformat(), "2026-10-30T09:30:00")
        self.assertEqual(result["signal_profile"], "structure_only")
        self.assertEqual(result["signal_profile_version"], "structure-only-v1")
        self.assertEqual(result["signals_config"], [])

    def test_wall_time_conversion_does_not_shift_eight_hours(self):
        result = validate_payload(sample_payload())
        czsc_dt = result["bars"][0]["czsc_dt"]
        self.assertIsNone(czsc_dt.tzinfo)
        self.assertEqual(czsc_dt.hour, 9)
        self.assertEqual(czsc_dt.minute, 30)
        self.assertEqual(_market_iso(czsc_dt), "2026-10-30T09:30:00+08:00")

    def test_market_time_helper_rejects_wrong_offset(self):
        wrong = datetime(2026, 10, 30, 9, 30, tzinfo=timezone.utc)
        with self.assertRaises(ValueError):
            _czsc_wall_time(wrong)

    def test_rejects_wrong_timezone(self):
        payload = sample_payload()
        payload["bars"][0]["dt"] = "2026-10-30T09:30:00Z"
        with self.assertRaises(ValueError):
            validate_payload(payload)

    def test_rejects_unsorted_or_duplicate_time(self):
        payload = sample_payload()
        second = dict(payload["bars"][0])
        second["dt"] = payload["bars"][0]["dt"]
        payload["bars"].append(second)
        with self.assertRaises(ValueError):
            validate_payload(payload)

    def test_rejects_invalid_ohlc(self):
        payload = sample_payload()
        payload["bars"][0]["high"] = 98
        with self.assertRaises(ValueError):
            validate_payload(payload)

    def test_czsc_signal_market_iso_preserves_naive_market_wall_time(self):
        value = datetime(2026, 10, 30, 9, 30)
        self.assertEqual(
            _czsc_signal_market_iso(value),
            "2026-10-30T09:30:00+08:00",
        )

    def test_czsc_signal_market_iso_relabels_utc_encoded_time_without_shifting(self):
        value = datetime(2026, 10, 30, 9, 30, tzinfo=timezone.utc)
        self.assertEqual(
            _czsc_signal_market_iso(value),
            "2026-10-30T09:30:00+08:00",
        )
        self.assertEqual(
            _czsc_signal_market_iso("2026-10-30T09:30:00Z"),
            "2026-10-30T09:30:00+08:00",
        )

    def test_czsc_signal_market_iso_keeps_shanghai_offset(self):
        value = datetime.fromisoformat("2026-10-30T09:30:00+08:00")
        self.assertEqual(
            _czsc_signal_market_iso(value),
            "2026-10-30T09:30:00+08:00",
        )

    def test_czsc_signal_market_iso_rejects_other_nonzero_offsets(self):
        value = datetime.fromisoformat("2026-10-30T09:30:00+05:30")
        with self.assertRaises(ValueError):
            _czsc_signal_market_iso(value)
        with self.assertRaises(ValueError):
            _czsc_signal_market_iso("not-a-timestamp")

    def test_market_iso_rejects_non_shanghai_aware_datetime(self):
        wrong = datetime(2026, 10, 30, 1, 30, tzinfo=timezone.utc)
        with self.assertRaises(ValueError):
            _market_iso(wrong)

    def test_market_iso_normalizes_naive_datetime_strings(self):
        self.assertEqual(
            _market_iso("2026-10-30 15:00:00"),
            "2026-10-30T15:00:00+08:00",
        )
        self.assertEqual(
            _market_iso("2026-10-30T15:00:00+08:00"),
            "2026-10-30T15:00:00+08:00",
        )

    def test_market_iso_rejects_utc_timestamp_strings(self):
        with self.assertRaises(ValueError):
            _market_iso("2026-10-30T07:00:00Z")

    def test_accepts_m30_timing_profile_and_uses_allowlisted_config(self):
        payload = sample_payload()
        payload["signal_profile"] = "chan3buy_m30_timing_v1"
        result = validate_payload(payload)
        self.assertEqual(result["signal_profile_version"], "chan3buy-m30-timing-v1")
        self.assertEqual(
            [item["name"] for item in result["signals_config"]],
            ["cxt_bi_status_V230102"],
        )

    def test_rejects_unknown_profile(self):
        payload = sample_payload()
        payload["signal_profile"] = "run_any_function"
        with self.assertRaisesRegex(ValueError, "不支持的 signal_profile"):
            validate_payload(payload)

    def test_rejects_profile_frequency_mismatch(self):
        payload = sample_payload()
        payload["signal_profile"] = "chan3buy_m60_v1"
        with self.assertRaisesRegex(ValueError, "只支持 60分钟"):
            validate_payload(payload)

    def test_trend_profile_window_and_indicator_configuration_is_pinned(self):
        spec = SIGNAL_PROFILES["trend_breakout_d1_v1"]
        self.assertEqual(spec["version"], "trend-breakout-d1-v1")
        self.assertEqual(
            [item["w"] for item in spec["signals"] if item["name"] == "bar_break_V240428"],
            [21, 31, 56],
        )
        self.assertEqual(
            [
                item["timeperiod"]
                for item in spec["signals"]
                if item["name"] == "tas_ma_base_V221101"
            ],
            [20, 60],
        )


class SignalLifecycleTests(unittest.TestCase):
    def test_signal_age_counts_consecutive_same_values_and_tracks_start_time(self):
        rows = [
            {"dt": datetime(2026, 10, 30, 10, 0), "60分钟_D1_三买辅助V230228": "其他_任意_任意_0"},
            {"dt": datetime(2026, 10, 30, 11, 0), "60分钟_D1_三买辅助V230228": "三买_任意_任意_0"},
            {"dt": datetime(2026, 10, 30, 14, 0), "60分钟_D1_三买辅助V230228": "三买_任意_任意_0"},
            {"dt": datetime(2026, 10, 30, 15, 0), "60分钟_D1_三买辅助V230228": "三买_任意_任意_0"},
        ]
        lifecycle = _signal_lifecycles(rows, "60分钟")
        item = lifecycle["60分钟_D1_三买辅助V230228"]
        self.assertEqual(item["age_bars"], 2)
        self.assertEqual(item["started_at"], "2026-10-30T11:00:00+08:00")

    def test_signal_age_is_zero_for_signal_first_seen_on_latest_bar(self):
        rows = [
            {"dt": datetime(2026, 10, 30, 14, 0), "日线_D1W21_事件V240428": "其他_任意_任意_0"},
            {"dt": datetime(2026, 10, 30, 15, 0), "日线_D1W21_事件V240428": "收盘新高_任意_任意_0"},
        ]
        lifecycle = _signal_lifecycles(rows, "日线")
        item = lifecycle["日线_D1W21_事件V240428"]
        self.assertEqual(item["age_bars"], 0)
        self.assertEqual(item["started_at"], "2026-10-30T15:00:00+08:00")


if __name__ == "__main__":
    unittest.main()
