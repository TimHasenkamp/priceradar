# Fetcher sidecar: Scrapling stealth browser (Patchright/Chromium). Large image.
FROM python:3.12-slim-bookworm

ENV PYTHONUNBUFFERED=1 \
    PIP_NO_CACHE_DIR=1 \
    XDG_CACHE_HOME=/opt/cache \
    PLAYWRIGHT_BROWSERS_PATH=/opt/ms-playwright

WORKDIR /app
COPY pyproject.toml ./
# - libatomic1: needed by Playwright's bundled Node driver, missing in -slim.
# - `scrapling install`: system dependencies + Scrapling's own resources.
# - `patchright install chromium`: StealthyFetcher runs on Patchright, which
#   pins its OWN Chromium revision (different from plain Playwright's), so it
#   must be downloaded explicitly or launching fails at runtime.
RUN apt-get update \
 && apt-get install -y --no-install-recommends libatomic1 \
 && pip install "scrapling[fetchers]==0.5.1" \
 && scrapling install \
 && patchright install --with-deps chromium \
 && mkdir -p /opt/cache /opt/ms-playwright \
 && chmod -R a+rX /opt/cache /opt/ms-playwright \
 && rm -rf /var/lib/apt/lists/*

COPY main.py smoke_test.py ./
RUN useradd --create-home --uid 10001 app
USER app

# Fail the image build if the stealth browser cannot start as the runtime user
# (no network access needed: it only opens about:blank).
RUN python smoke_test.py

ENV FETCHER_ADDR=0.0.0.0 FETCHER_PORT=8000
EXPOSE 8000
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s \
  CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:8000/healthz',timeout=4).status==200 else 1)"
CMD ["python", "main.py"]
