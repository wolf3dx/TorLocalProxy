# -*- coding: utf-8 -*-
"""Точка входа: GUI по умолчанию, консольный режим по --no-gui."""

from __future__ import annotations

import argparse
import sys
import time

from .app import (TorProxyService, load_bridges_text, load_config,
                  save_bridges_text, save_config)
from .bridges import parse_bridges
from .tormgr import find_tor
from .util import log


def build_parser() -> argparse.ArgumentParser:
    p = argparse.ArgumentParser(
        prog="tor-local-proxy",
        description="Локальный прокси через сеть Tor с поддержкой мостов.")
    p.add_argument("--no-gui", action="store_true", help="работать без окна")
    p.add_argument("--bridges-file", metavar="ФАЙЛ",
                   help="файл со строками мостов (по умолчанию — сохранённые)")
    p.add_argument("--no-bridges", action="store_true",
                   help="подключаться напрямую, без мостов")
    p.add_argument("--http-port", type=int, help="порт локального HTTP-прокси")
    p.add_argument("--socks-port", type=int, help="порт SOCKS5 (0 — выбрать сам)")
    p.add_argument("--tor-binary", help="путь к исполняемому файлу tor")
    p.add_argument("--timeout", type=int, help="таймаут подключения, секунд")
    p.add_argument("-v", "--verbose", action="store_true",
                   help="логировать каждое соединение")
    p.add_argument("--check", action="store_true",
                   help="после подключения проверить выходной IP")
    p.add_argument("--print-torrc", action="store_true",
                   help="показать сгенерированный torrc и выйти")
    p.add_argument("--find-tor", action="store_true",
                   help="показать найденный путь к tor и выйти")
    return p


def main(argv=None) -> int:
    args = build_parser().parse_args(argv)
    cfg = load_config()

    if args.http_port is not None:
        cfg["http_port"] = args.http_port
    if args.socks_port is not None:
        cfg["socks_port"] = args.socks_port or "auto"
    if args.tor_binary:
        cfg["tor_binary"] = args.tor_binary
    if args.timeout:
        cfg["connect_timeout"] = args.timeout
    if args.verbose:
        cfg["verbose"] = True

    if args.find_tor:
        path = find_tor(cfg.get("tor_binary", ""))
        print(path or "tor не найден")
        return 0 if path else 1

    text = ""
    if not args.no_bridges:
        if args.bridges_file:
            with open(args.bridges_file, "r", encoding="utf-8") as f:
                text = f.read()
        else:
            text = load_bridges_text()

    if args.print_torrc:
        from .tormgr import TorManager, build_torrc
        mgr = TorManager()
        bridges, _ = parse_bridges(text)
        plugins, missing = mgr._plan_plugins(bridges, find_tor(cfg.get("tor_binary", "")))
        if missing:
            print("# ВНИМАНИЕ: не найдены программы для транспортов: "
                  + ", ".join(missing), file=sys.stderr)
        print(build_torrc(datadir=mgr.datadir, control_file=mgr.control_file,
                          log_file=mgr.log_file,
                          socks_port=cfg.get("socks_port", 9052),
                          bridges=bridges, pt_plugins=plugins,
                          extra=cfg.get("extra_torrc", "")))
        return 0

    if not args.no_gui:
        try:
            from .gui import run as run_gui
            return run_gui(cfg)
        except ImportError:
            log("Tkinter недоступен — перехожу в консольный режим.")

    # ------------------------------------------------------ консольный режим --
    save_config(cfg)
    if args.bridges_file and text.strip():
        save_bridges_text(text)

    service = TorProxyService(cfg)

    def on_progress(percent, phase):
        sys.stderr.write(f"\r  подключение: {percent:3d}%  {phase:<40}")
        sys.stderr.flush()
        if percent >= 100:
            sys.stderr.write("\n")

    try:
        result = service.connect(text, on_progress=on_progress)
    except Exception as e:
        sys.stderr.write("\n")
        log(f"Не подключилось: {e}")
        return 1

    log(f"HTTP-прокси : {result['http'] or '(не запущен)'}")
    log(f"SOCKS5      : {result['socks']}")

    if args.check:
        try:
            log(service.check_ip())
        except Exception as e:
            log(f"Проверка не удалась: {e}")

    log("Ctrl+C — выход.")
    try:
        while service.running:
            time.sleep(1)
    except KeyboardInterrupt:
        pass
    finally:
        service.disconnect()
    return 0
