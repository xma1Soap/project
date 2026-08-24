from __future__ import annotations

import argparse
import unittest

from channel_quota_controller.gensoukyou_cli import (
    _effective_dry_run,
    _is_loopback_url,
)


class GensoukyouCliTestCase(unittest.TestCase):
    def test_insecure_http_is_identified_as_loopback_only(self) -> None:
        self.assertTrue(_is_loopback_url("http://127.0.0.1:3001"))
        self.assertFalse(_is_loopback_url("http://gensoukyou.xyz"))

    def test_production_requires_third_confirmation_gate(self) -> None:
        args = argparse.Namespace(
            confirm_live_actions=True,
            environment="production",
            confirm_production_host=None,
        )
        self.assertTrue(_effective_dry_run(False, args))
        args.confirm_production_host = "gensoukyou.xyz"
        self.assertFalse(_effective_dry_run(False, args))


if __name__ == "__main__":
    unittest.main()
