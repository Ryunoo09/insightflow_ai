"""Server-side storage path resolution for dataset files.

Callers never supply filesystem paths: only opaque IDs, which are validated
before being joined under the resolved root.
"""

from __future__ import annotations

import re
from pathlib import Path

_SEGMENT = re.compile(r"^[A-Za-z0-9._-]+$")


class UnsafePathError(ValueError):
    """Raised when a path segment could escape the storage root."""


class StorageResolver:
    """Builds all dataset paths under a single root directory."""

    def __init__(self, root: str | Path) -> None:
        if not root:
            raise ValueError("root must not be empty")
        self._root = Path(root).resolve()

    @property
    def root(self) -> Path:
        return self._root

    def source_path(
        self, workspace_id: str, dataset_id: str, version_no: int, ext: str
    ) -> Path:
        """Original upload: sources/{ws}/{dataset}/{version}/source.{ext}."""
        suffix = ext.lower().lstrip(".")
        if not suffix:
            raise UnsafePathError("extension must not be empty")
        return self._build(
            "sources", workspace_id, dataset_id, str(version_no), f"source.{suffix}"
        )

    def parquet_path(self, workspace_id: str, dataset_id: str, version_no: int) -> Path:
        """Normalized table: parquet/{ws}/{dataset}/{version}/data.parquet."""
        return self._build(
            "parquet", workspace_id, dataset_id, str(version_no), "data.parquet"
        )

    def temp_path(self, name: str) -> Path:
        """Scratch file: temp/{name}."""
        return self._build("temp", name)

    def _build(self, *parts: str) -> Path:
        for part in parts:
            if not _is_safe_segment(part):
                raise UnsafePathError(f"unsafe path segment: {part!r}")

        candidate = self._root.joinpath(*parts).resolve()
        if candidate != self._root and self._root not in candidate.parents:
            raise UnsafePathError(f"path escapes root: {candidate}")
        return candidate


def _is_safe_segment(segment: str) -> bool:
    if segment in ("", ".", ".."):
        return False
    if "/" in segment or "\\" in segment:
        return False
    return bool(_SEGMENT.match(segment))
