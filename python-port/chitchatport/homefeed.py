"""Read-only chat transcript feed, ported from ``home_chat.go`` (core rules).

Kept: session per (run, conversation), bot/user roles from own/partner names,
dedup by message id with text updates, DOM order when the page reports it,
bounded rows (500) and sessions (12), late-receipt guard and inactive marking.

Simplified for Phase 1 (documented, not silent): the Go feed's extra display
edge-cases (lobby replay suppression nuances) are reduced to the rules above.
"""

from __future__ import annotations

import threading

from .domchat import DOMSnapshot

HOME_CHAT_ROWS = 500
HOME_CHAT_SESSIONS = 12


class HomeChatFeed:
    def __init__(self):
        self._lock = threading.Lock()
        self.version = 0
        self.next_run = 0
        self.runs: dict[int, dict] = {}
        self.sessions: dict[str, dict] = {}
        self.order: list[str] = []
        self.evicted = 0

    # -- run lifecycle -----------------------------------------------------

    def start(self, label: str) -> int:
        with self._lock:
            self.next_run += 1
            self.runs[self.next_run] = {
                "label": label,
                "current": "",
                "suppressed": "",
                "chats": 0,
                "ended": False,
            }
            return self.next_run

    def end(self, run: int) -> None:
        with self._lock:
            record = self.runs.get(run)
            if record is None:
                return
            record["ended"] = True
            for session in self.sessions.values():
                if session["run"] == run and session["info"]["active"]:
                    session["info"]["active"] = False
                    self.version += 1
            self._trim()

    def version_value(self) -> int:
        with self._lock:
            return self.version

    # -- observing ---------------------------------------------------------

    def observe(self, run: int, snapshot: DOMSnapshot, receipt: bool = False) -> None:
        with self._lock:
            record = self.runs.get(run)
            if record is None or record["ended"]:
                return
            controls = snapshot.controls
            if controls.phase != "chatting":
                if not receipt:
                    session = self.sessions.get(record["current"])
                    if session is not None and session["info"]["active"]:
                        session["info"]["active"] = False
                        self.version += 1
                return  # never replace the last real transcript with a lobby snapshot
            if (
                controls.conversation == ""
                or not controls.own_name.strip()
                or not controls.partner.strip()
                or controls.own_name.casefold() == controls.partner.casefold()
            ):
                return

            key = "{}/{}".format(run, controls.conversation)
            if key == record["suppressed"]:
                return
            session = self.sessions.get(key)
            if session is None:
                # A late receipt can finish an old conversation but never switch
                # the view.
                if receipt and record["current"] != "":
                    return
                old = self.sessions.get(record["current"])
                if old is not None:
                    old["info"]["active"] = False
                record["chats"] += 1
                session = {
                    "run": run,
                    "info": {
                        "id": key,
                        "label": "{} · Chat {}".format(record["label"], record["chats"]),
                        "active": True,
                        "dropped": 0,
                    },
                    "rows": {},
                    "floor": 0,
                    "next_order": 0,
                }
                self.sessions[key] = session
                self.order.append(key)
                record["current"] = key
                self.version += 1

            if key == record["current"] and not receipt and not session["info"]["active"]:
                session["info"]["active"] = True
                self.version += 1

            for row in snapshot.messages:
                if not row.id or not row.text.strip():
                    continue
                if row.author.casefold() == controls.own_name.casefold():
                    role = "bot"
                elif row.author.casefold() == controls.partner.casefold():
                    role = "user"
                else:
                    continue
                order = row.order
                if order <= 0:
                    old_row = session["rows"].get(row.id)
                    if old_row is not None:
                        order = old_row["order"]
                    else:
                        session["next_order"] += 1
                        order = session["next_order"]
                if order > session["next_order"]:
                    session["next_order"] = order
                if order <= session["floor"]:
                    continue
                message = {"id": row.id, "role": role, "text": row.text, "order": order}
                old_row = session["rows"].get(row.id)
                if old_row is None or old_row != message:
                    session["rows"][row.id] = message
                    self.version += 1

            if len(session["rows"]) > HOME_CHAT_ROWS:
                rows = self._ordered(session)
                for row in rows[: len(rows) - HOME_CHAT_ROWS]:
                    session["rows"].pop(row["id"], None)
                    if row["order"] > session["floor"]:
                        session["floor"] = row["order"]
                    session["info"]["dropped"] += 1
                    self.version += 1
            self._trim()

    @staticmethod
    def _ordered(session: dict) -> list[dict]:
        return sorted(session["rows"].values(), key=lambda row: (row["order"], row["id"]))

    def _trim(self) -> None:
        while len(self.order) > HOME_CHAT_SESSIONS:
            index = -1
            for position, key in enumerate(self.order):
                if not self.sessions[key]["info"]["active"]:
                    index = position
                    break
            if index < 0:
                index = 0  # display-only bound, even across multiple launch batches
            key = self.order[index]
            session = self.sessions[key]
            record = self.runs.get(session["run"])
            if record is not None and record["current"] == key:
                record["suppressed"] = key
            del self.sessions[key]
            self.order.pop(index)
            self.evicted += 1
            self.version += 1
        for run, record in list(self.runs.items()):
            if not record["ended"]:
                continue
            if not any(session["run"] == run for session in self.sessions.values()):
                del self.runs[run]

    # -- reading -----------------------------------------------------------

    def observed_rows(self) -> int:
        """How many chat rows were observed in total (used by the dashboard flow)."""
        with self._lock:
            return sum(len(session["rows"]) for session in self.sessions.values())

    def snapshot(self, selected: str = "") -> dict:
        with self._lock:
            out = {
                "version": self.version,
                "selected": "",
                "sessions": [self.sessions[key]["info"] for key in self.order],
                "messages": [],
                "evicted": self.evicted,
            }
            if selected not in self.sessions:
                selected = ""
                for key in reversed(self.order):
                    if selected == "":
                        selected = key
                    if self.sessions[key]["info"]["active"]:
                        selected = key
                        break
            out["selected"] = selected
            session = self.sessions.get(selected)
            if session is not None:
                out["messages"] = self._ordered(session)
            return out
