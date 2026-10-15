"""Entry point: a pywebview desktop window (same WebView2 as the Go app).

Run:  python app.py       (after: pip install -r requirements.txt)
"""

from __future__ import annotations

import sys
from pathlib import Path

BASE_DIR = Path(__file__).resolve().parent
if str(BASE_DIR) not in sys.path:
    sys.path.insert(0, str(BASE_DIR))

from chitchatport.service import ShowModeService  # noqa: E402


class Api:
    """Methods exposed to the frontend as ``window.pywebview.api.<name>``."""

    def __init__(self, service: ShowModeService):
        self.service = service

    def get_state(self, selected: str = "") -> dict:
        return self.service.state(str(selected or ""))

    def save_browser_settings(self, payload: dict | None = None) -> dict:
        return self.service.save_browser_settings(payload or {})

    def save_general_settings(self, payload: dict | None = None) -> dict:
        return self.service.save_general_settings(payload or {})

    def save_message_settings(self, payload: dict | None = None) -> dict:
        return self.service.save_message_settings(payload or {})

    def start_show_mode(self, count: int = 1) -> dict:
        return self.service.start_show_mode(count)

    def stop_show_mode(self) -> dict:
        return self.service.stop_show_mode()

    def clear_logs(self) -> dict:
        return self.service.clear_logs()

    def load_message_folder(self) -> dict:
        import webview  # lazy: keeps the engine importable without a GUI stack

        window = webview.windows[0] if webview.windows else None
        if window is None:
            return {"ok": False, "error": "no window available"}
        selected = window.create_file_dialog(webview.FOLDER_DIALOG)
        if not selected:
            return {"ok": False, "error": "no folder selected"}
        directory = selected[0] if isinstance(selected, (list, tuple)) else selected
        return self.service.load_message_folder(str(directory))


def main() -> int:
    import webview  # imported lazily so the engine modules stay dependency-free

    data_dir = BASE_DIR / "data"
    service = ShowModeService(data_dir)
    ui_index = BASE_DIR / "chitchatport" / "ui" / "index.html"
    webview.create_window(
        "ChitChat Bot — Python Show Mode",
        url=str(ui_index),
        js_api=Api(service),
        width=1180,
        height=820,
        min_size=(980, 640),
    )
    webview.start()
    service.shutdown()
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
