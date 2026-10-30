import unittest

from czsc_service.analyzer import validate_payload


def sample_payload():
    return {
        "schema_version": "v1",
        "ts_code": "600519.SH",
        "freq": "30分钟",
        "adjust_mode": "QFQ",
        "source": "gotdx",
        "bars": [
            {
                "dt": "2026-10-30T10:00:00+08:00",
                "symbol": "600519.SH",
                "open": 100,
                "close": 102,
                "high": 103,
                "low": 99,
                "vol": 1000,
                "amount": 102000,
            }
        ],
    }


class ValidatePayloadTests(unittest.TestCase):
    def test_accepts_standard_bar_payload(self):
        result = validate_payload(sample_payload())
        self.assertEqual(result["freq_native"], "F30")
        self.assertEqual(len(result["bars"]), 1)

    def test_rejects_wrong_timezone(self):
        payload = sample_payload()
        payload["bars"][0]["dt"] = "2026-10-30T10:00:00Z"
        with self.assertRaises(ValueError):
            validate_payload(payload)

    def test_rejects_unsorted_or_duplicate_time(self):
        payload = sample_payload()
        payload["bars"].append(dict(payload["bars"][0]))
        with self.assertRaises(ValueError):
            validate_payload(payload)

    def test_rejects_invalid_ohlc(self):
        payload = sample_payload()
        payload["bars"][0]["high"] = 98
        with self.assertRaises(ValueError):
            validate_payload(payload)


if __name__ == "__main__":
    unittest.main()
