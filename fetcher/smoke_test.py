"""Build-time check: can the stealth browser actually launch?

Uses the same engine as Scrapling's StealthyFetcher (Patchright Chromium)
and only opens about:blank, so it needs no network access.
"""

import sys
import tempfile

from patchright.sync_api import sync_playwright


def main() -> int:
    with tempfile.TemporaryDirectory() as profile, sync_playwright() as p:
        # same launch path StealthyFetcher uses (persistent context)
        ctx = p.chromium.launch_persistent_context(profile, headless=True)
        page = ctx.new_page()
        page.goto("about:blank")
        ctx.close()
    print("smoke test ok: patchright chromium launches")
    return 0


if __name__ == "__main__":
    sys.exit(main())
