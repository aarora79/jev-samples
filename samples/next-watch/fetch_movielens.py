"""Download the MovieLens small dataset and check it against its published MD5.

GroupLens publishes ml-latest-small with an MD5 beside it. This script fetches
both over https, refuses a zip whose hash does not match, and extracts the three
files the sample reads into data/ml-latest-small/. It needs no Jev key.

The dataset carries its own license: research use, no commercial use, cite
GroupLens, and redistribute only under the same terms. The README beside the CSVs
holds the full text, and the extract keeps it.

Usage:
    uv run fetch_movielens.py            # download once, reuse after
    uv run fetch_movielens.py --force    # download again
"""

import argparse
import hashlib
import logging
import pathlib
import sys
import urllib.request
import zipfile

# Configure logging with basicConfig
logging.basicConfig(
    level=logging.INFO,  # Set the log level to INFO
    # Define log message format
    format="%(asctime)s,p%(process)s,{%(filename)s:%(lineno)d},%(levelname)s,%(message)s",
)
logger = logging.getLogger(__name__)

ZIP_URL: str = "https://files.grouplens.org/datasets/movielens/ml-latest-small.zip"
MD5_URL: str = f"{ZIP_URL}.md5"

FETCH_TIMEOUT_SECONDS: int = 60

# Everything this writes lands under data/, which git ignores apart from the
# committed reports.
DATA_DIR: pathlib.Path = pathlib.Path(__file__).parent / "data"
DATASET_DIR: pathlib.Path = DATA_DIR / "ml-latest-small"

# The only members the sample reads, plus the license. Extracting by name keeps
# a hostile archive from writing outside DATASET_DIR.
MEMBERS: tuple[str, ...] = (
    "ml-latest-small/movies.csv",
    "ml-latest-small/ratings.csv",
    "ml-latest-small/README.txt",
)


def _download(url: str) -> bytes:
    """Fetch one https URL into memory.

    Args:
        url: An https URL.

    Returns:
        The response body.

    Raises:
        SystemExit: If the URL is anything other than https.
    """
    if not url.startswith("https://"):
        sys.exit(f"https only: refusing {url}")
    logger.info(f"Fetching: {url}")
    with urllib.request.urlopen(url, timeout=FETCH_TIMEOUT_SECONDS) as response:  # nosec B310 - https-only, enforced above
        return response.read()


def _published_md5() -> str:
    """Read the MD5 GroupLens publishes beside the zip.

    The file reads `MD5 (ml-latest-small.zip) = <hex>`, so the hash is the last
    word on the line.

    Returns:
        The hex digest, lowercase.
    """
    return _download(MD5_URL).decode("ascii").strip().split()[-1].lower()


def _extract(archive: pathlib.Path) -> None:
    """Pull the named members out of the zip into DATA_DIR.

    Args:
        archive: The verified zip on disk.

    Raises:
        SystemExit: If the zip lacks a member the sample needs.
    """
    with zipfile.ZipFile(archive) as bundle:
        names = set(bundle.namelist())
        missing = [member for member in MEMBERS if member not in names]
        if missing:
            sys.exit(f"{archive.name} lacks {', '.join(missing)}")
        for member in MEMBERS:
            target = DATA_DIR / member
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(bundle.read(member))
            logger.info(f"Wrote {target}")


def ensure_dataset(force: bool = False) -> pathlib.Path:
    """Make sure the MovieLens CSVs sit on disk, downloading them when absent.

    Args:
        force: Download and extract again even when the files exist.

    Returns:
        The folder holding movies.csv and ratings.csv.

    Raises:
        SystemExit: If the download does not match the published MD5.
    """
    if not force and all((DATA_DIR / member).exists() for member in MEMBERS):
        logger.info(f"Using the MovieLens files already in {DATASET_DIR}")
        return DATASET_DIR

    DATA_DIR.mkdir(exist_ok=True)
    body = _download(ZIP_URL)
    expected = _published_md5()
    actual = hashlib.md5(body, usedforsecurity=False).hexdigest()
    if actual != expected:
        sys.exit(f"MD5 mismatch: GroupLens publishes {expected}, the download hashed {actual}")
    logger.info(f"MD5 matches the published {expected}")

    archive = DATA_DIR / "ml-latest-small.zip"
    archive.write_bytes(body)
    _extract(archive)
    archive.unlink()
    return DATASET_DIR


def main() -> None:
    """Parse arguments and fetch the dataset."""
    parser = argparse.ArgumentParser(
        description="Download MovieLens ml-latest-small and verify it against its MD5.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog="""
Example usage:
    # Download once; later runs reuse the files
    uv run fetch_movielens.py

    # Throw the local copy away and download it again
    uv run fetch_movielens.py --force
""",
    )
    parser.add_argument(
        "--force",
        action="store_true",
        help="Download and extract again even when the files exist",
    )
    parser.add_argument(
        "--debug",
        action="store_true",
        help="Turn on debug logging",
    )
    args = parser.parse_args()

    if args.debug:
        logging.getLogger().setLevel(logging.DEBUG)

    print(f"MovieLens files in {ensure_dataset(args.force)}")


if __name__ == "__main__":
    main()
