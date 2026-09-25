"""Render pipeline.html into the PNG the sample README embeds.

The HTML is the source. Edit it, run this, and commit both files.

Usage:
    python3 assets/render.py

Needs playwright and a chromium it can launch. Install the browser once with
`python3 -m playwright install chromium`, because `uv run --with playwright`
brings the library without the browser and the launch then fails.
"""

import asyncio
import logging
import pathlib

from playwright.async_api import async_playwright

# Configure logging with basicConfig
logging.basicConfig(
    level=logging.INFO,  # Set the log level to INFO
    # Define log message format
    format="%(asctime)s,p%(process)s,{%(filename)s:%(lineno)d},%(levelname)s,%(message)s",
)
logger = logging.getLogger(__name__)

ASSETS: pathlib.Path = pathlib.Path(__file__).parent
SOURCE: pathlib.Path = ASSETS / "pipeline.html"
TARGET: pathlib.Path = ASSETS / "pipeline.png"

# The body sets its own width, so the viewport only has to be wide enough not to
# wrap it. Two device pixels per CSS pixel keeps the text crisp on a retina screen
# without doubling the file size the way three would.
VIEWPORT: dict[str, int] = {"width": 1180, "height": 1400}
DEVICE_SCALE_FACTOR: float = 2.0
SETTLE_MS: int = 400


async def _render() -> None:
    """Shoot the body element, so the PNG crops to the diagram."""
    async with async_playwright() as playwright:
        browser = await playwright.chromium.launch()
        page = await browser.new_page(
            viewport=VIEWPORT,
            device_scale_factor=DEVICE_SCALE_FACTOR,
        )
        await page.goto(SOURCE.resolve().as_uri())
        await page.wait_for_timeout(SETTLE_MS)
        await page.locator("body").screenshot(path=str(TARGET))
        await browser.close()

    logger.info(f"wrote {TARGET} ({TARGET.stat().st_size:,} bytes)")


if __name__ == "__main__":
    asyncio.run(_render())
