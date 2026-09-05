# -*- coding: utf-8 -*-
"""Разбор строк мостов Tor.

Принимает то, что приходит письмом от bridges@torproject.org или со
страницы bridges.torproject.org, вместе с окружающей прозой — лишние
строки просто отсеиваются.

Поддерживаемые форматы:
    obfs4 192.0.2.1:1234 <FINGERPRINT> cert=... iat-mode=0
    webtunnel [2001:db8::1]:443 <FINGERPRINT> url=https://... ver=0.0.1
    snowflake 192.0.2.3:80 <FINGERPRINT> fingerprint=... url=... front=...
    meek_lite 192.0.2.2:80 <FINGERPRINT> url=... front=...
    192.0.2.4:9001 <FINGERPRINT>                       (обычный мост)
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field

# Транспорты, которые умеет запускать приложение
KNOWN_TRANSPORTS = {
    "obfs4", "obfs3", "scramblesuit",
    "meek", "meek_lite",
    "webtunnel",
    "snowflake",
    "conjure",
}

_ADDR_RE = re.compile(r"^(?:\[[0-9A-Fa-f:.]+\]|[0-9A-Za-z.\-]+):(\d{1,5})$")
_FP_RE = re.compile(r"^[0-9A-Fa-f]{40}$")
_TRANSPORT_RE = re.compile(r"^[A-Za-z][A-Za-z0-9_]{1,30}$")


class BridgeError(ValueError):
    pass


@dataclass(frozen=True)
class Bridge:
    transport: str                       # "" — обычный мост без транспорта
    address: str                         # host:port или [v6]:port
    fingerprint: str = ""                # 40 hex, может отсутствовать
    args: tuple = field(default_factory=tuple)   # оставшиеся key=value

    @property
    def host(self) -> str:
        if self.address.startswith("["):
            return self.address[1:self.address.index("]")]
        return self.address.rsplit(":", 1)[0]

    @property
    def port(self) -> int:
        return int(self.address.rsplit(":", 1)[1])

    def torrc_line(self) -> str:
        parts = []
        if self.transport:
            parts.append(self.transport)
        parts.append(self.address)
        if self.fingerprint:
            parts.append(self.fingerprint)
        parts.extend(self.args)
        return "Bridge " + " ".join(parts)

    def short(self) -> str:
        label = self.transport or "vanilla"
        return f"{label} {self.address}"

    def key(self) -> str:
        return f"{self.transport}|{self.address.lower()}"


def parse_bridge_line(line: str) -> Bridge:
    """Разбирает одну строку. Бросает BridgeError, если строка не мост."""
    text = line.strip()
    if "#" in text:
        text = text.split("#", 1)[0].strip()
    if not text:
        raise BridgeError("пустая строка")

    tokens = text.split()
    # письма и веб-страница иногда отдают строки с префиксом "Bridge"
    if tokens and tokens[0].lower() == "bridge":
        tokens = tokens[1:]
    if not tokens:
        raise BridgeError("пустая строка")

    transport = ""
    if not _ADDR_RE.match(tokens[0]):
        candidate = tokens[0].lower()
        if not _TRANSPORT_RE.match(candidate):
            raise BridgeError(f"не похоже на мост: «{tokens[0]}»")
        if candidate not in KNOWN_TRANSPORTS:
            raise BridgeError(f"неизвестный транспорт «{tokens[0]}»")
        transport = candidate
        tokens = tokens[1:]

    if not tokens:
        raise BridgeError("после транспорта нет адреса")

    address = tokens[0]
    m = _ADDR_RE.match(address)
    if not m:
        raise BridgeError(f"некорректный адрес «{address}» (нужно host:port)")
    if not 1 <= int(m.group(1)) <= 65535:
        raise BridgeError(f"порт вне диапазона в «{address}»")
    tokens = tokens[1:]

    fingerprint = ""
    if tokens and _FP_RE.match(tokens[0]):
        fingerprint = tokens[0].upper()
        tokens = tokens[1:]

    for tok in tokens:
        if "=" not in tok:
            raise BridgeError(f"лишний параметр «{tok}» (ожидалось key=value)")

    return Bridge(transport, address, fingerprint, tuple(tokens))


def _looks_like_bridge(line: str) -> bool:
    """Была ли строка попыткой задать мост (в отличие от прозы письма)."""
    tokens = line.split()
    if tokens and tokens[0].lower() == "bridge":
        tokens = tokens[1:]
    if not tokens:
        return False
    if tokens[0].lower() in KNOWN_TRANSPORTS:
        return True
    if _ADDR_RE.match(tokens[0]):
        return True
    if any(_FP_RE.match(t) for t in tokens):
        return True
    return bool(re.search(r"[\w\]]:\d{1,5}\b", line))


def parse_bridges(text: str):
    """Разбирает текст целиком.

    Возвращает (мосты, проблемы). В «проблемы» попадают только строки,
    которые явно задумывались как мосты (содержат host:port), — проза из
    письма молча игнорируется.
    """
    bridges: list[Bridge] = []
    problems: list[str] = []
    seen: set[str] = set()

    for raw in text.splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        try:
            bridge = parse_bridge_line(line)
        except BridgeError as e:
            if _looks_like_bridge(line):
                short = line if len(line) <= 70 else line[:67] + "…"
                problems.append(f"{short} — {e}")
            continue
        if bridge.key() in seen:
            continue
        seen.add(bridge.key())
        bridges.append(bridge)

    return bridges, problems


def transports_used(bridges) -> list:
    """Уникальные транспорты (без обычных мостов), в порядке появления."""
    out = []
    for b in bridges:
        if b.transport and b.transport not in out:
            out.append(b.transport)
    return out
