# -*- coding: utf-8 -*-
"""Связка всего вместе: настройки, мосты, процесс Tor, локальный прокси."""

from __future__ import annotations

import json
import os
import urllib.request

from .bridges import parse_bridges
from .httpbridge import HttpBridge
from .tormgr import TorManager, TorStartError, find_tor
from .util import data_dir, log

CONFIG_PATH = os.path.join(data_dir(), "config.json")
BRIDGES_PATH = os.path.join(data_dir(), "bridges.txt")

DEFAULT_CONFIG = {
    "http_host": "127.0.0.1",
    "http_port": 8080,
    "socks_port": 9052,
    "tor_binary": "",
    "extra_torrc": "",
    "verbose": False,
    "connect_timeout": 180,
}


def load_config() -> dict:
    cfg = dict(DEFAULT_CONFIG)
    try:
        with open(CONFIG_PATH, "r", encoding="utf-8") as f:
            cfg.update(json.load(f))
    except FileNotFoundError:
        pass
    except Exception as e:
        log(f"Не удалось прочитать настройки: {e}")
    return cfg


def save_config(cfg: dict) -> None:
    try:
        with open(CONFIG_PATH, "w", encoding="utf-8") as f:
            json.dump(cfg, f, ensure_ascii=False, indent=2)
    except Exception as e:
        log(f"Не удалось сохранить настройки: {e}")


def load_bridges_text() -> str:
    try:
        with open(BRIDGES_PATH, "r", encoding="utf-8") as f:
            return f.read()
    except OSError:
        return ""


def save_bridges_text(text: str) -> None:
    try:
        with open(BRIDGES_PATH, "w", encoding="utf-8") as f:
            f.write(text)
    except OSError as e:
        log(f"Не удалось сохранить мосты: {e}")


class TorProxyService:
    """Поднимает Tor с указанными мостами и локальный HTTP-прокси над ним."""

    def __init__(self, cfg: dict | None = None):
        self.cfg = cfg or load_config()
        self.tor = TorManager()
        self.http = HttpBridge()

    # --------------------------------------------------------- состояние --
    @property
    def running(self) -> bool:
        return self.tor.running

    @property
    def socks_address(self) -> str:
        return self.tor.socks_address

    @property
    def http_address(self) -> str:
        return self.http.address

    # ------------------------------------------------------------- пуск --
    def connect(self, bridges_text: str, on_progress=None) -> dict:
        bridges, problems = parse_bridges(bridges_text)
        for p in problems:
            log("Пропущена строка: " + p)

        if bridges:
            kinds = {}
            for b in bridges:
                kinds[b.transport or "vanilla"] = kinds.get(b.transport or "vanilla", 0) + 1
            log("Мостов принято: " + ", ".join(f"{k} × {v}" for k, v in kinds.items()))
        else:
            log("Мосты не заданы — подключаюсь к сети Tor напрямую.")

        self.tor.on_progress = on_progress
        socks = self.tor.start(
            bridges,
            tor_binary=self.cfg.get("tor_binary", ""),
            socks_port=self.cfg.get("socks_port", 9052),
            extra_torrc=self.cfg.get("extra_torrc", ""),
            timeout=float(self.cfg.get("connect_timeout", 180)),
        )

        http_addr = ""
        try:
            http_addr = self.http.start(self.cfg.get("http_host", "127.0.0.1"),
                                        int(self.cfg.get("http_port", 8080)),
                                        socks,
                                        bool(self.cfg.get("verbose")))
            log(f"HTTP-прокси: {http_addr}")
        except OSError as e:
            log(f"HTTP-мост не поднялся (порт занят?): {e}. "
                f"SOCKS5 всё равно доступен на {socks}.")

        return {"socks": socks, "http": http_addr,
                "bridges": len(bridges), "problems": problems}

    def disconnect(self) -> None:
        self.http.stop()
        self.tor.stop()
        log("Отключено.")

    def new_identity(self) -> None:
        self.tor.new_identity()
        log("Запрошена новая цепочка. Выходной IP сменится через несколько секунд.")

    # ------------------------------------------------------------ проверка --
    def check_ip(self, timeout: float = 60.0) -> str:
        """Прогоняет запрос через локальный прокси и смотрит, что видит сайт."""
        if self.http.running:
            proxy = "http://" + self.http.address
            opener = urllib.request.build_opener(
                urllib.request.ProxyHandler({"http": proxy, "https": proxy}))
        elif self.socks_address:
            raise RuntimeError("HTTP-мост не запущен — проверьте порт "
                               f"{self.cfg.get('http_port')}")
        else:
            raise RuntimeError("Tor не подключён")

        opener.addheaders = [("User-Agent", "TorLocalProxy/2.0")]
        with opener.open("https://check.torproject.org/api/ip", timeout=timeout) as r:
            data = json.loads(r.read().decode("utf-8", "replace"))
        ip = data.get("IP", "?")
        if data.get("IsTor"):
            return f"Tor работает. Выходной IP: {ip}"
        return f"ВНИМАНИЕ: трафик идёт НЕ через Tor. Виден IP: {ip}"


def tor_status_line(cfg: dict) -> str:
    binary = find_tor(cfg.get("tor_binary", ""))
    return binary or ""
