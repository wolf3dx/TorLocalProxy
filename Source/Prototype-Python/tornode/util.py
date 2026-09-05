# -*- coding: utf-8 -*-
"""Общие мелочи: пути, порты, журнал, кросс-платформенные особенности."""

from __future__ import annotations

import ctypes
import os
import socket
import sys
import threading
import time

APP_ID = "TorLocalProxy"
IS_WINDOWS = sys.platform == "win32"
IS_MACOS = sys.platform == "darwin"

# ---------------------------------------------------------------- журнал ----

_sinks: list = []
_lock = threading.Lock()


def add_log_sink(fn) -> None:
    with _lock:
        _sinks.append(fn)


def clear_log_sinks() -> None:
    with _lock:
        _sinks.clear()


def log(msg: str) -> None:
    line = time.strftime("[%H:%M:%S] ") + str(msg)
    with _lock:
        sinks = list(_sinks)
    if not sinks:
        print(line, flush=True)
        return
    for fn in sinks:
        try:
            fn(line)
        except Exception:
            pass


# ------------------------------------------------------------------ пути ----

def data_dir() -> str:
    """Каталог приложения. Намеренно без пробелов в пути на UNIX —
    tor не умеет кавычить путь в ClientTransportPlugin exec."""
    if IS_WINDOWS:
        base = os.environ.get("LOCALAPPDATA") or os.path.expanduser("~")
        path = os.path.join(base, APP_ID)
    else:
        path = os.path.expanduser("~/.torlocalproxy")
    os.makedirs(path, exist_ok=True)
    if not IS_WINDOWS:
        try:
            os.chmod(path, 0o700)   # tor требует 0700 на DataDirectory
        except OSError:
            pass
    return path


def app_root() -> str:
    """Каталог, где лежит само приложение (работает и внутри PyInstaller)."""
    if getattr(sys, "frozen", False):
        return os.path.dirname(os.path.abspath(sys.executable))
    return os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def short_path(path: str) -> str:
    """Windows: превращает путь с пробелами в короткую форму 8.3.

    Нужно потому, что строка `ClientTransportPlugin ... exec <путь>`
    разбивается tor'ом по пробелам и кавычки там не работают.
    """
    if not IS_WINDOWS or " " not in path:
        return path
    try:
        GetShortPathNameW = ctypes.windll.kernel32.GetShortPathNameW
        GetShortPathNameW.argtypes = [ctypes.c_wchar_p, ctypes.c_wchar_p,
                                      ctypes.c_uint]
        GetShortPathNameW.restype = ctypes.c_uint
        size = GetShortPathNameW(path, None, 0)
        if size:
            buf = ctypes.create_unicode_buffer(size)
            if GetShortPathNameW(path, buf, size):
                return buf.value
    except Exception:
        pass
    return path


def quote_torrc(value: str) -> str:
    """Значение torrc в кавычках (для путей с пробелами)."""
    return '"' + value.replace("\\", "\\\\").replace('"', '\\"') + '"'


# ----------------------------------------------------------------- порты ----

def port_open(host: str, port: int, timeout: float = 1.5) -> bool:
    try:
        with socket.create_connection((host, int(port)), timeout):
            return True
    except OSError:
        return False


def port_free(host: str, port: int) -> bool:
    s = socket.socket()
    try:
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s.bind((host, int(port)))
        return True
    except OSError:
        return False
    finally:
        s.close()


def free_port(host: str = "127.0.0.1") -> int:
    with socket.socket() as s:
        s.bind((host, 0))
        return s.getsockname()[1]
