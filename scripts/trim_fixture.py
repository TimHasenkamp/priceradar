"""Trim a saved shop product page down to what the parser needs.

Keeps: <title>, all JSON-LD blocks (with "description" texts emptied),
the first main price block (div.js_pdp_price ... </ofc-pricing-item-v1>),
and a marker that the Kasada SDK was present (real pages load it too).
Everything else (page chrome, product texts, tracking) is dropped, so the
fixtures stay small and contain as little third-party content as possible.

Usage: python scripts/trim_fixture.py <in.html> <out.html>
"""

from __future__ import annotations

import re
import sys

LD_RE = re.compile(r'<script[^>]*application/ld\+json[^>]*>.*?</script>', re.S)
TITLE_RE = re.compile(r'<title[^>]*>.*?</title>', re.S)
DESC_RE = re.compile(r'("description"\s*:\s*)"(?:[^"\\]|\\.)*"')


def trim(html: str) -> str:
    title = TITLE_RE.search(html)
    ld_blocks = [DESC_RE.sub(r'\1""', m.group(0)) for m in LD_RE.finditer(html)]

    price = ""
    start = html.find('class="pdp_price js_pdp_price')
    if start >= 0:
        start = html.rfind("<div", 0, start)
        end = html.find("</ofc-pricing-item-v1>", start)
        if end >= 0:
            # close the two wrapping divs (price block + price tag)
            price = html[start:end + len("</ofc-pricing-item-v1>")] + "</div></div></div>"

    kasada = "<script>window.KPSDK={};</script>" if "KPSDK" in html else ""
    parts = [
        "<!DOCTYPE html><html><head>",
        title.group(0) if title else "",
        *ld_blocks,
        kasada,
        "</head><body>",
        price,
        "</body></html>\n",
    ]
    return "\n".join(p for p in parts if p)


def main() -> None:
    src, dst = sys.argv[1], sys.argv[2]
    with open(src, encoding="utf-8") as f:
        html = f.read()
    out = trim(html)
    with open(dst, "w", encoding="utf-8", newline="\n") as f:
        f.write(out)
    print(f"{src}: {len(html):,} -> {len(out):,} bytes")


if __name__ == "__main__":
    main()
