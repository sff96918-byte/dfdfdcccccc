"""ChitChat Bot Show Mode — Python port (Phase 1).

This package is a faithful port of the Go Show Mode path only:

* the same in-page DOM observer script (``assets/showmode_dom.js``, byte-for-byte
  copy of ``root-project/internal/browser/showmode_dom.js``),
* the same runner rules (single-consumer queue, seen/queued row bookkeeping,
  Skip/START rotation, send confirmation by a new own row),
* the same first-message setting (``bot`` / ``user`` / ``random``, read once per
  conversation, never answering older history),
* the same activity log strings, so logs from both apps can be compared line by
  line.

Nothing in ``root-project/`` is imported or modified at runtime; the only shared
file is the verbatim DOM script asset.
"""

__all__ = ["__version__"]

__version__ = "0.1.0"
