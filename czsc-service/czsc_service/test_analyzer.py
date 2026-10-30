import unittest
from datetime import datetime, timezone

from czsc_service.analyzer import (
    SIGNAL_PROFILES,
    _czsc_wall_time,
    _market_iso,
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

    def test_market_iso_rejects_non_shanghai_aware_datetime(self):
        wrong = datetime(2026, 10, 30, 1, 30, tzinfo=timezone.utc)
        with self.assertRaises(ValueError):
            _market_iso(wrong)

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


if __name__ == "__main__":
    unittest.main()
