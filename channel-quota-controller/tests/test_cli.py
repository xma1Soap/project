from __future__ import annotations

import argparse
import unittest
from datetime import UTC, datetime

from channel_quota_controller.cli import parse_datetime


class CliTestCase(unittest.TestCase):
    def test_parse_datetime_normalizes_to_utc(self) -> None:
        parsed = parse_datetime("2099-01-01T08:00:00+08:00")
        self.assertEqual(parsed, datetime(2099, 1, 1, 0, 0, tzinfo=UTC))

    def test_parse_datetime_rejects_naive_time(self) -> None:
        with self.assertRaises(argparse.ArgumentTypeError):
            parse_datetime("2099-01-01T00:00:00")


if __name__ == "__main__":
    unittest.main()
