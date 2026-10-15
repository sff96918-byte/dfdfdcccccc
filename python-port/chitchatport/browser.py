"""Playwright backend driving the system Chrome/Edge (no bundled Chromium).

Ported from ``internal/browser/rod.go`` + ``domchat.go`` + ``session.go`` +
``scripts.go``. The page evaluates the same shared DOM script, sends are done
with native key events (CDP input), and login data is captured with the same
JSON schema, so a ``login_info_*.json`` written here is readable by the Go
worker writer and vice versa.
"""

from __future__ import annotations

import base64
import json
import time
from datetime import datetime, timezone
from pathlib import Path

from .domchat import DOMSnapshot, PageGoneError, confirmed_dom_send, same_dom_conversation
from .proxyrelay import Relay
from .domscript import click_expression, probe_expression, snapshot_expression

APP_URL = "https://app.chitchat.gg/"
API_BASE_URL = "https://api.chitchat.gg"
COOKIE_URLS = ["https://app.chitchat.gg/", "https://api.chitchat.gg/"]
CHANNEL_CANDIDATES = ("chrome", "msedge")

DEFAULT_TIMEOUT_MS = 15000
ACTION_TIMEOUT_MS = 4000
SEND_CONFIRM_TIMEOUT_SECONDS = 3.0
SEND_CONFIRM_POLL_SECONDS = 0.15
COMPOSER_SELECTOR = '[data-chat-composer="true"] textarea[aria-label="Send a message"]'

# Verbatim from internal/browser/scripts.go.
STORAGE_FN = """() => {
  const copy = (storage) => {
    const data = {};
    for (let index = 0; index < storage.length; index += 1) {
      const key = storage.key(index);
      data[key] = storage.getItem(key);
    }
    return data;
  };

  return {
    local_storage: copy(window.localStorage),
    session_storage: copy(window.sessionStorage),
  };
}"""

FINGERPRINT_FN = """() => ({
  userAgent: navigator.userAgent,
  language: navigator.language,
  colorDepth: String(screen.colorDepth),
  screenResolution: String(screen.width) + "x" + String(screen.height),
  timeZone: Intl.DateTimeFormat().resolvedOptions().timeZone,
  addBehavior: String("addBehavior" in document.documentElement),
  openDatabase: String("openDatabase" in window),
  plugins: Array.from(navigator.plugins).map((plugin) => plugin.name).join(","),
  canvas: (() => {
    const canvas = document.createElement("canvas");
    const context = canvas.getContext("2d");
    if (context) {
      context.fillText("BF", 2, 2);
    }
    return canvas.toDataURL().slice(-50);
  })(),
  webgl: (() => {
    const context = document.createElement("canvas").getContext("webgl");
    if (!context) {
      return "~";
    }
    return String(context.getParameter(context.VENDOR)) + "~" + String(context.getParameter(context.RENDERER));
  })(),
  touchSupport: String("ontouchstart" in window),
  hardwareConcurrency: String(navigator.hardwareConcurrency || "unknown"),
})"""

FETCH_FN = """async (request) => {
  const options = {
    method: request.method,
    credentials: "include",
    headers: request.headers || {},
  };

  if (Object.prototype.hasOwnProperty.call(request, "body")) {
    options.body = request.body;
  }

  try {
    const response = await fetch(request.url, options);
    const text = await response.text();
    return {
      ok: response.ok,
      status: response.status,
      statusText: response.statusText,
      text,
    };
  } catch (error) {
    return {
      ok: false,
      status: 0,
      statusText: "FETCH_ERROR",
      text: String(error),
    };
  }
}"""


def _is_target_closed(error: BaseException) -> bool:
    """True when Playwright reports the page/context/browser as gone.

    ``TargetClosedError`` is not exported from ``playwright.sync_api``, so the
    class hierarchy is checked by name instead of by import.
    """
    if any(base.__name__ == "TargetClosedError" for base in type(error).__mro__):
        return True
    message = str(error).lower()
    return "has been closed" in message or "target closed" in message


def token_from_login(login: dict) -> str:
    """Ported from ``LoginInfo.Token()``: the cookie named ``token``."""
    for cookie in login.get("cookies") or []:
        if cookie.get("name") == "token":
            return str(cookie.get("value") or "")
    return ""


def current_user_id_from_token(token: str) -> str:
    """Ported from ``CurrentUserIDFromToken`` (userId -> sub -> id)."""
    parts = str(token or "").split(".")
    if len(parts) < 2:
        return ""
    payload = parts[1]
    payload += "=" * (-len(payload) % 4)
    try:
        claims = json.loads(base64.urlsafe_b64decode(payload.encode("ascii")))
    except Exception:  # noqa: BLE001 - a malformed token simply yields no id
        return ""
    if not isinstance(claims, dict):
        return ""
    for key in ("userId", "sub", "id"):
        value = str(claims.get(key) or "").strip()
        if value:
            return value
    return ""


def current_user_id(login: dict) -> str:
    """Ported from ``LoginInfo.CurrentUserID()``."""
    user_id = str(login.get("user_id") or "").strip()
    if user_id:
        return user_id
    from_token = current_user_id_from_token(token_from_login(login))
    if from_token:
        return from_token
    for storage_key in ("local_storage", "session_storage"):
        storage = login.get(storage_key) or {}
        value = str(storage.get("cachedUserId") or "").strip()
        if value:
            return value
    return ""


def sanitize_fresh_file_segment(value: str) -> str:
    """Ported from ``sanitizeFreshFileSegment``."""
    value = str(value or "").strip()
    if not value:
        return ""
    value = value.replace("..", "_")
    for char in ("\\", "/", ":", "*", "?", '"', "<", ">", "|", " "):
        value = value.replace(char, "_")
    return value.strip("._")


def _utc_stamp(moment: datetime) -> str:
    # Go: 20060102-150405 + "-" + %09d nanoseconds.
    return moment.strftime("%Y%m%d-%H%M%S") + "-{:09d}".format(moment.microsecond * 1000)


def fresh_login_info_path(login_dir: Path, login: dict, index: int) -> Path:
    """Ported from ``freshLoginInfoPath`` (same file naming as the Go app)."""
    user_id = sanitize_fresh_file_segment(current_user_id(login))
    if not user_id:
        user_id = "browser-{:02d}".format(index)
    saved_at = login.get("saved_at")
    try:
        moment = datetime.fromisoformat(str(saved_at).replace("Z", "+00:00"))
    except (TypeError, ValueError):
        moment = datetime.now(timezone.utc)
    if moment.tzinfo is None:
        moment = moment.replace(tzinfo=timezone.utc)
    return Path(login_dir) / "login_info_{}_{}_{:02d}.json".format(
        user_id, _utc_stamp(moment.astimezone(timezone.utc)), index
    )


class PlaywrightDOMBackend:
    """Implements the DOMChatBackend protocol with the system browser."""

    name = "playwright"

    def __init__(
        self,
        user_data_dir: str | Path,
        channel: str = "auto",
        headless: bool = False,
        start_url: str = APP_URL,
        proxy=None,
    ):
        self.user_data_dir = Path(user_data_dir)
        self.requested_channel = channel or "auto"
        self.headless = headless
        self.start_url = start_url
        # One proxy entry per browser; the credentials never reach the browser
        # flags - the local relay adds them (see proxyrelay.py).
        self.proxy = proxy
        self.channel = ""
        self._playwright = None
        self._context = None
        self._page = None
        self._relay = None

    # -- lifecycle ---------------------------------------------------------

    def _candidates(self) -> list[str]:
        if self.requested_channel in CHANNEL_CANDIDATES:
            return [self.requested_channel]
        return list(CHANNEL_CANDIDATES)

    def start(self) -> "PlaywrightDOMBackend":
        from playwright.sync_api import Error as PlaywrightError
        from playwright.sync_api import sync_playwright

        self.user_data_dir.mkdir(parents=True, exist_ok=True)
        self._playwright = sync_playwright().start()
        launch_options: dict = {}
        if self.proxy is not None:
            self._relay = Relay(self.proxy).start()
            # Chrome sees a plain local proxy; the relay adds the real
            # credentials upstream. "<-loopback>" keeps localhost traffic
            # inside the proxy path, exactly like the Go relay.
            launch_options["proxy"] = {"server": self._relay.browser_url, "bypass": "<-loopback>"}
        failures: list[str] = []
        for candidate in self._candidates():
            try:
                self._context = self._playwright.chromium.launch_persistent_context(
                    user_data_dir=str(self.user_data_dir),
                    channel=candidate,
                    headless=self.headless,
                    no_viewport=True,
                    args=["--start-maximized", "--no-default-browser-check"],
                    **launch_options,
                )
            except PlaywrightError as error:
                failures.append("{}: {}".format(candidate, error))
                self._context = None
                continue
            self.channel = candidate
            break
        if self._context is None:
            self._shutdown_driver()
            raise RuntimeError(
                "Could not launch a system browser (install Google Chrome or Microsoft Edge): "
                + " | ".join(failures)
            )
        self._context.set_default_timeout(DEFAULT_TIMEOUT_MS)
        self._page = self._context.pages[0] if self._context.pages else self._context.new_page()
        try:
            self._page.goto(self.start_url, wait_until="domcontentloaded")
        except Exception as error:  # noqa: BLE001 - a slow first paint is not fatal
            if _is_target_closed(error):
                raise PageGoneError(str(error)) from error
        return self

    def _shutdown_driver(self) -> None:
        if self._relay is not None:
            self._relay.close()
            self._relay = None
        if self._playwright is not None:
            try:
                self._playwright.stop()
            except Exception:  # noqa: BLE001
                pass
            self._playwright = None

    def close(self) -> None:
        if self._context is not None:
            try:
                self._context.close()
            except Exception:  # noqa: BLE001
                pass
            self._context = None
        self._page = None
        self._shutdown_driver()

    # -- helpers -----------------------------------------------------------

    def _require_page(self):
        page = self._page
        if page is None:
            raise PageGoneError("browser page is not available")
        try:
            if page.is_closed():
                raise PageGoneError("browser page is closed")
        except PageGoneError:
            raise
        except Exception as error:  # noqa: BLE001
            if _is_target_closed(error):
                raise PageGoneError(str(error)) from error
        return page

    def _evaluate(self, expression: str, arg=None):
        page = self._require_page()
        try:
            if arg is None:
                return page.evaluate(expression)
            return page.evaluate(expression, arg)
        except Exception as error:  # noqa: BLE001
            if _is_target_closed(error):
                raise PageGoneError(str(error)) from error
            raise

    def page_alive(self) -> bool:
        try:
            self._require_page()
            return True
        except PageGoneError:
            return False

    # -- DOMChatBackend ----------------------------------------------------

    def read_dom_snapshot(self) -> DOMSnapshot:
        return DOMSnapshot.from_json(self._evaluate(snapshot_expression()))

    def dom_probe(self) -> str:
        return str(self._evaluate(probe_expression()) or "")

    def click_dom_button(self, labels: list[str]) -> str:
        result = self._evaluate(click_expression(), list(labels or []))
        return str(result or "")

    def send_dom_message(self, text: str) -> bool:
        return self.send_dom_message_observed(text, None)

    def send_dom_message_observed(self, text: str, observe=None) -> bool:
        """Send with native input; success requires a NEW own row (like Go).

        ``observe`` receives every actual DOM read, so the Home transcript can
        show confirmed rows only - never a draft.
        """
        text = str(text or "").strip()
        before = self.read_dom_snapshot()
        if observe is not None:
            observe(before)
        if (
            not before.controls.can_send
            or before.controls.overflow
            or before.controls.own_name == ""
        ):
            raise RuntimeError("Show Mode composer/own identity unavailable; no send attempted")

        page = self._require_page()
        locator = page.locator(COMPOSER_SELECTOR).first
        locator.wait_for(state="visible", timeout=ACTION_TIMEOUT_MS)
        if (locator.input_value() or "") != "":
            raise RuntimeError("Show Mode composer has a draft; refusing to overwrite it")

        # Native CDP key events, like rod's Element.Input.
        _type_text(locator, text, ACTION_TIMEOUT_MS)

        check = self.read_dom_snapshot()
        if observe is not None:
            observe(check)
        if not same_dom_conversation(before.controls, check.controls) or not check.controls.can_send:
            # A navigation/rematch occurred while typing. Never press Enter here.
            raise RuntimeError("Show Mode conversation changed while typing; inspect the draft")

        locator.press("Enter", timeout=ACTION_TIMEOUT_MS)
        deadline = time.monotonic() + SEND_CONFIRM_TIMEOUT_SECONDS
        while True:
            after = self.read_dom_snapshot()
            if observe is not None:
                observe(after)
            if not same_dom_conversation(before.controls, after.controls):
                raise RuntimeError("Show Mode conversation changed before send confirmation")
            if confirmed_dom_send(before, after, text):
                return True
            if time.monotonic() >= deadline:
                return False
            time.sleep(SEND_CONFIRM_POLL_SECONDS)

    # -- session -----------------------------------------------------------

    def capture_session(self) -> dict:
        """Ported from ``CaptureSession`` (same JSON schema as the Go app)."""
        cookies = self._context.cookies(COOKIE_URLS) if self._context is not None else []
        storage = self._evaluate(STORAGE_FN) or {}
        fingerprint = self._evaluate(FINGERPRINT_FN) or {}
        login = {
            "profile": "",
            "backend": self.name,
            "cookies": [
                {
                    "name": str(cookie.get("name") or ""),
                    "value": str(cookie.get("value") or ""),
                    "domain": str(cookie.get("domain") or ""),
                    "path": str(cookie.get("path") or ""),
                    "expires": float(cookie.get("expires") or 0),
                    "secure": bool(cookie.get("secure")),
                    "httpOnly": bool(cookie.get("httpOnly")),
                }
                for cookie in cookies
            ],
            "user_agent": str(fingerprint.get("userAgent") or ""),
            "local_storage": {str(k): str(v) for k, v in (storage.get("local_storage") or {}).items()},
            "session_storage": {
                str(k): str(v) for k, v in (storage.get("session_storage") or {}).items()
            },
            "transport_fingerprint": {str(k): str(v) for k, v in fingerprint.items()},
            "saved_at": datetime.now(timezone.utc).isoformat(timespec="microseconds").replace("+00:00", "Z"),
        }
        return login

    def session_authenticated(self) -> bool:
        """Ported from ``SessionAuthenticated``: in-page GET /users/me/relationships."""
        result = self._evaluate(
            FETCH_FN,
            {
                "method": "GET",
                "url": API_BASE_URL + "/users/me/relationships",
                "headers": {"accept": "*/*"},
            },
        ) or {}
        try:
            status = int(result.get("status") or 0)
        except (TypeError, ValueError):
            status = 0
        return 200 <= status < 400


def _type_text(locator, text: str, timeout_ms: int) -> None:
    """Prefer ``press_sequentially`` (real key events), fall back to ``type``."""
    for method_name in ("press_sequentially", "type"):
        method = getattr(locator, method_name, None)
        if callable(method):
            method(text, timeout=timeout_ms)
            return
    raise RuntimeError("Playwright locator cannot type text")
