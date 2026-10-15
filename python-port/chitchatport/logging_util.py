"""Activity log with the Go dashboard's levels and a UI ring buffer.

Levels are upper-cased like ``internal/applog`` (ACTIVITY / INFO / WARN / ERROR)
and every entry is appended to ``<data_dir>/logs/port.log`` as JSON lines, so a
Python run can be compared with the Go ``logs.txt`` line by line.

Like the Go ``applog.Redact`` sink, known credential forms (Authorization /
Cookie headers, token/api-key/password fields, Bearer values and
``scheme://user:pass@`` URLs) are masked before a line is stored or written.
"""

from __future__ import annotations

import json
import os
import re
import threading
import time
from collections import deque
from pathlib import Path

LEVELS = ("ACTIVITY", "INFO", "WARN", "ERROR")

# Verbatim ports of the applog patterns.
_HEADER_SECRET = re.compile(
    r"(?im)\b(authorization|proxy-authorization|cookie|set-cookie)\s*[:=]\s*[^\r\n]+"
)
_FIELD_SECRET = re.compile(
    r"(?i)([\"']?\b(?:access[_-]?token|refresh[_-]?token|id[_-]?token|token|"
    r"ai[_-]?api[_-]?key|api[_-]?key|license[_-]?key|password|passwd|secret|"
    r"session[_-]?id|authorization|proxy-authorization|cookie|set-cookie)\b[\"']?"
    r"\s*[:=]\s*)(\"[^\"\r\n]*\"|'[^'\r\n]*'|[^\s,;&]+)"
)
_BEARER_SECRET = re.compile(r"(?i)\bBearer\s+[a-z0-9._~+/-]+=*")
_PROXY_SECRET = re.compile(r"(?i)\b(https?|socks5h?|socks4a?)://[^\s/@]+@")


def redact(message: str) -> str:
    """The Go ``applog.Redact`` safeguard, ported pattern by pattern.

    It is a safeguard, not a promise: it recognises the credential shapes the
    Go sink recognises, and never touches ordinary chat text.
    """
    text = _HEADER_SECRET.sub(r"\1: [REDACTED]", str(message or ""))
    text = _FIELD_SECRET.sub(r"\1[REDACTED]", text)
    text = _BEARER_SECRET.sub("Bearer [REDACTED]", text)
    return _PROXY_SECRET.sub(r"\1://[REDACTED]@", text)


class ActivityLog:
    def __init__(self, log_dir: str | os.PathLike[str], capacity: int = 2000, echo: bool = False):
        self.log_dir = Path(log_dir)
        self.path = self.log_dir / "port.log"
        self._entries: deque[dict] = deque(maxlen=capacity)
        self._lock = threading.Lock()
        self._lines_written = 0
        # --debug: mirror every line to the console window so a problem is
        # visible even when the dashboard tab is closed or not reachable.
        self.echo = bool(echo)

    def append(self, level: str, message: str) -> dict:
        entry = {
            "timestamp": time.strftime("%Y-%m-%dT%H:%M:%S", time.gmtime()) + "Z",
            "level": str(level or "info").strip().upper(),
            "message": redact(message),
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
        if self.echo:
            try:
                print("[{}] {:<8} {}".format(entry["timestamp"][11:19], entry["level"], entry["message"]), flush=True)
            except (OSError, ValueError):
                pass  # a closed console must never stop the bot
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
