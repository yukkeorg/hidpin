import json
import sys
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(REPO_ROOT / "host" / "src"))


@pytest.fixture(scope="session")
def vectors() -> dict:
    """The test vectors shared with the firmware unit tests."""
    return json.loads((REPO_ROOT / "protocol" / "vectors.json").read_text())
