"""Activity log with the Go dashboard's levels and a UI ring buffer.

Levels are upper-cased like ``internal/applog`` (ACTIVITY / INFO / WARN / ERROR)
and every entry is appended to ``<data_dir>/logs/port.log`` as JSON lines, so a
Python run can be compared with the Go ``logs.txt`` line by line.
"""

from __future__ import annotations

import json
import os
import threading
import time
from collections import deque
from pathlib import Path

LEVELS = ("ACTIVITY", "INFO", "WARN", "ERROR")


class ActivityLog:
    def __init__(self, log_dir: str | os.PathLike[str], capacity: int = 2000):
        self.log_dir = Path(log_dir)
        self.path = self.log_dir / "port.log"
        self._entries: deque[dict] = deque(maxlen=capacity)
        self._lock = threading.Lock()
        self._lines_written = 0

    def append(self, level: str, message: str) -> dict:
        entry = {
            "timestamp": time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime()) + "Z",
            "level": str(level or "info").strip().upper(),
            "message": str(message or ""),
        }
        with self._lock:
            self._entries.append(entry)
            self._lines_written += 1
            try:
                self.log_dir.mkdir(parents=True, exist_ok=True)
                with open(self.path, "a", encoding="utf-8") as handle:
                    handle.write(json.dumps(entry, ensure_ascii=False) + "\n")
            except OSError:
                # Logging must never break the chat loop.
                pass
        return entry

    def entries(self, limit: int = 200) -> list[dict]:
        with self._lock:
            items = list(self._entries)
        if limit and len(items) > limit:
            items = items[-limit:]
        return items

    def clear(self) -> None:
        with self._lock:
            self._entries.clear()

    def contains(self, text: str) -> bool:
        with self._lock:
            return any(text in entry["message"] for entry in self._entries)

    def count(self, text: str) -> int:
        with self._lock:
            return sum(1 for entry in self._entries if text in entry["message"])
