# -*- coding: utf-8 -*-
"""Управление процессом tor: поиск бинарников, torrc, запуск, bootstrap."""

from __future__ import annotations

import os
import shutil
import subprocess
import sys
import threading
import time

from .bridges import Bridge, transports_used
from .control import ControlClient, ControlError
from .util import (IS_MACOS, IS_WINDOWS, app_root, data_dir, log, port_free,
                   quote_torrc, short_path)

# Какой исполняемый файл обслуживает какой транспорт.
# lyrebird — современное имя obfs4proxy, включает obfs4/meek_lite/webtunnel.
PT_BINARIES = {
    "obfs4":        ["lyrebird", "obfs4proxy"],
    "obfs3":        ["lyrebird", "obfs4proxy"],
    "scramblesuit": ["lyrebird", "obfs4proxy"],
    "meek":         ["lyrebird", "obfs4proxy", "meek-client"],
    "meek_lite":    ["lyrebird", "obfs4proxy"],
    "webtunnel":    ["webtunnel-client", "lyrebird"],
    "snowflake":    ["snowflake-client"],
    "conjure":      ["conjure-client"],
}

BOOTSTRAP_TAGS = {
    "starting": "Запуск",
    "conn_pt": "Подключение к мосту",
    "conn_done_pt": "Мост ответил",
    "conn_proxy": "Подключение через прокси",
    "conn_done_proxy": "Прокси ответил",
    "conn": "Подключение к сети Tor",
    "conn_done": "Соединение установлено",
    "handshake": "Согласование шифрования",
    "handshake_done": "Шифрование согласовано",
    "onehop_create": "Создание служебной цепочки",
    "requesting_status": "Запрос состояния сети",
    "loading_status": "Загрузка состояния сети",
    "loading_keys": "Загрузка ключей",
    "requesting_descriptors": "Запрос описаний узлов",
    "loading_descriptors": "Загрузка описаний узлов",
    "enough_dirinfo": "Каталог узлов получен",
    "ap_conn_pt": "Строим цепочку через мост",
    "ap_conn_done_pt": "Цепочка через мост поднята",
    "ap_conn": "Строим цепочку",
    "ap_conn_done": "Цепочка построена",
    "ap_handshake": "Согласование в цепочке",
    "ap_handshake_done": "Цепочка согласована",
    "circuit_create": "Создание рабочей цепочки",
    "done": "Готово — Tor подключён",
}


def _exe(name: str) -> str:
    return name + ".exe" if IS_WINDOWS else name


# --------------------------------------------------------- поиск бинарников --

def _tor_search_paths() -> list:
    paths = []
    env = os.environ.get("TOR_BINARY")
    if env:
        paths.append(env)

    root = app_root()
    for sub in ("", "tor", "bin", os.path.join("tor", "tor")):
        paths.append(os.path.join(root, sub, _exe("tor")))

    which = shutil.which("tor")
    if which:
        paths.append(which)

    if IS_WINDOWS:
        bases = [os.environ.get("LOCALAPPDATA"), os.environ.get("APPDATA"),
                 os.environ.get("ProgramFiles"), os.environ.get("ProgramFiles(x86)"),
                 os.path.expanduser("~/Desktop"), os.path.expanduser("~/Downloads"),
                 "C:\\", "C:\\Tor"]
        for base in filter(None, bases):
            paths += [
                os.path.join(base, "Tor Browser", "Browser", "TorBrowser", "Tor", "tor.exe"),
                os.path.join(base, "Tor", "tor.exe"),
                os.path.join(base, "tor", "tor.exe"),
            ]
    elif IS_MACOS:
        paths += [
            "/Applications/Tor Browser.app/Contents/MacOS/Tor/tor",
            os.path.expanduser("~/Applications/Tor Browser.app/Contents/MacOS/Tor/tor"),
            "/opt/homebrew/bin/tor", "/usr/local/bin/tor", "/opt/local/bin/tor",
        ]
    else:
        paths += ["/usr/bin/tor", "/usr/local/bin/tor", "/usr/sbin/tor",
                  "/snap/bin/tor"]
        home = os.path.expanduser("~")
        for pattern in ("tor-browser", "tor-browser_en-US", ".local/share/torbrowser"):
            paths.append(os.path.join(home, pattern, "Browser", "TorBrowser",
                                      "Tor", "tor"))
    return paths


def find_tor(hint: str = "") -> str:
    """Возвращает путь к tor или "" если не найден."""
    candidates = ([hint] if hint else []) + _tor_search_paths()
    for path in candidates:
        if path and os.path.isfile(path) and os.access(path, os.X_OK):
            return os.path.abspath(path)
    return ""


def pt_search_dirs(tor_binary: str) -> list:
    dirs = []
    if tor_binary:
        d = os.path.dirname(tor_binary)
        dirs += [
            d,
            os.path.join(d, "PluggableTransports"),
            os.path.join(os.path.dirname(d), "PluggableTransports"),
            os.path.join(os.path.dirname(d), "Tor", "PluggableTransports"),
        ]
    root = app_root()
    dirs += [root, os.path.join(root, "tor"),
             os.path.join(root, "tor", "pluggable_transports"),
             os.path.join(root, "pluggable_transports")]
    if not IS_WINDOWS:
        dirs += ["/usr/bin", "/usr/local/bin", "/opt/homebrew/bin", "/usr/lib/tor/pluggable-transports"]
    seen, out = set(), []
    for d in dirs:
        ad = os.path.abspath(d)
        if ad not in seen and os.path.isdir(ad):
            seen.add(ad)
            out.append(ad)
    return out


def find_pt(transport: str, search_dirs: list) -> str:
    """Ищет исполняемый файл pluggable transport для данного транспорта."""
    for name in PT_BINARIES.get(transport, []):
        for d in search_dirs:
            path = os.path.join(d, _exe(name))
            if os.path.isfile(path) and os.access(path, os.X_OK):
                return os.path.abspath(path)
        which = shutil.which(name)
        if which:
            return which
    return ""


# ------------------------------------------------------------------ torrc --

def build_torrc(*, datadir: str, control_file: str, log_file: str,
                socks_port, bridges: list, pt_plugins: dict,
                socks_host: str = "127.0.0.1", extra: str = "") -> str:
    """Собирает содержимое torrc.

    pt_plugins: {путь_к_бинарнику: [транспорты, ...]}
    socks_port: число или "auto"
    """
    lines = [
        "# Сгенерировано TorLocalProxy — правки будут перезаписаны",
        "DataDirectory " + quote_torrc(datadir),
        "Log notice file " + quote_torrc(log_file),
        "ControlPort auto",
        "ControlPortWriteToFile " + quote_torrc(control_file),
        "CookieAuthentication 1",
        "ClientOnly 1",
        "AvoidDiskWrites 1",
        f"__OwningControllerProcess {os.getpid()}",
    ]
    if socks_port == "auto":
        lines.append("SocksPort auto")
    else:
        lines.append(f"SocksPort {socks_host}:{int(socks_port)}")

    if bridges:
        lines.append("")
        lines.append("UseBridges 1")
        for binary, transports in pt_plugins.items():
            lines.append("ClientTransportPlugin "
                         + ",".join(sorted(transports))
                         + " exec " + short_path(binary))
        for bridge in bridges:
            lines.append(bridge.torrc_line())
    if extra.strip():
        lines.append("")
        lines.append("# Дополнительные параметры пользователя")
        lines.append(extra.strip())
    return "\n".join(lines) + "\n"


# --------------------------------------------------------------- процесс --

class TorStartError(RuntimeError):
    pass


class TorManager:
    """Запускает собственный процесс tor и следит за его подключением."""

    def __init__(self):
        self.process: subprocess.Popen | None = None
        self.control: ControlClient | None = None
        self.socks_address = ""
        self.progress = 0
        self.phase = ""
        self.datadir = data_dir()
        self.torrc_path = os.path.join(self.datadir, "torrc")
        self.control_file = os.path.join(self.datadir, "control_port")
        self.log_file = os.path.join(self.datadir, "tor.log")
        self.cookie_path = os.path.join(self.datadir, "control_auth_cookie")
        self.on_progress = None       # callable(percent, text)
        self._stopping = threading.Event()

    # ------------------------------------------------------------ helpers --
    def _emit(self, percent: int, text: str) -> None:
        self.progress, self.phase = percent, text
        if self.on_progress:
            try:
                self.on_progress(percent, text)
            except Exception:
                pass

    def _plan_plugins(self, bridges: list, tor_binary: str):
        """Сопоставляет транспорты с бинарниками. Возвращает (plugins, missing)."""
        dirs = pt_search_dirs(tor_binary)
        plugins: dict = {}
        missing: list = []
        for transport in transports_used(bridges):
            binary = find_pt(transport, dirs)
            if not binary:
                missing.append(transport)
                continue
            plugins.setdefault(binary, []).append(transport)
        return plugins, missing

    # -------------------------------------------------------------- старт --
    def start(self, bridges: list, *, tor_binary: str = "",
              socks_port=9052, socks_host: str = "127.0.0.1",
              extra_torrc: str = "", timeout: float = 180.0) -> str:
        """Запускает tor и ждёт bootstrap 100%. Возвращает адрес SOCKS5."""
        if self.process and self.process.poll() is None:
            raise TorStartError("Tor уже запущен")
        self._stopping.clear()

        binary = find_tor(tor_binary)
        if not binary:
            raise TorStartError(
                "Не найден исполняемый файл tor. Скачайте Tor Expert Bundle "
                "и положите его в папку «tor» рядом с приложением "
                "(см. README) либо укажите путь вручную."
            )
        log(f"Tor: {binary}")

        plugins, missing = self._plan_plugins(bridges, binary)
        if missing:
            raise TorStartError(
                "Нет программ для транспортов: " + ", ".join(missing) + ". "
                "Нужны файлы " + ", ".join(
                    sorted({n for t in missing for n in PT_BINARIES.get(t, [])})
                ) + " из Tor Expert Bundle."
            )
        for binary_path, transports in plugins.items():
            log(f"Транспорт {', '.join(transports)}: {os.path.basename(binary_path)}")

        if socks_port not in ("auto", 0) and not port_free(socks_host, socks_port):
            log(f"Порт {socks_port} занят — Tor выберет свободный сам.")
            socks_port = "auto"

        for path in (self.control_file, self.log_file):
            try:
                os.remove(path)
            except OSError:
                pass

        torrc = build_torrc(datadir=self.datadir, control_file=self.control_file,
                            log_file=self.log_file, socks_port=socks_port,
                            socks_host=socks_host, bridges=bridges,
                            pt_plugins=plugins, extra=extra_torrc)
        with open(self.torrc_path, "w", encoding="utf-8") as f:
            f.write(torrc)

        creationflags = 0
        if IS_WINDOWS:
            creationflags = getattr(subprocess, "CREATE_NO_WINDOW", 0)
        self._emit(0, "Запуск процесса Tor")
        self.process = subprocess.Popen(
            [binary, "-f", self.torrc_path],
            cwd=os.path.dirname(binary) or None,
            stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
            creationflags=creationflags,
        )
        threading.Thread(target=self._drain_stderr, daemon=True).start()

        try:
            control_port = self._wait_control_port(30.0)
            self._connect_control(control_port)
            self._wait_bootstrap(timeout)
            self.socks_address = self.control.socks_listener()
            if not self.socks_address:
                raise TorStartError("Tor не сообщил адрес SOCKS-порта")
            log(f"Tor подключён. SOCKS5: {self.socks_address}")
            return self.socks_address
        except Exception:
            self.stop()
            raise

    def _drain_stderr(self) -> None:
        proc = self.process
        if not proc or not proc.stderr:
            return
        for raw in proc.stderr:
            if self._stopping.is_set():
                return
            line = raw.decode("utf-8", "replace").strip()
            if line:
                log("tor: " + line)

    def _wait_control_port(self, timeout: float) -> int:
        deadline = time.time() + timeout
        while time.time() < deadline:
            if self.process.poll() is not None:
                raise TorStartError(
                    f"Процесс tor завершился с кодом {self.process.returncode}. "
                    f"Подробности в {self.log_file}"
                )
            try:
                with open(self.control_file, "r", encoding="utf-8") as f:
                    content = f.read().strip()
                if content.startswith("PORT="):
                    return int(content.split(":")[-1])
            except (OSError, ValueError):
                pass
            time.sleep(0.2)
        raise TorStartError("Tor не открыл control-порт за отведённое время")

    def _connect_control(self, port: int) -> None:
        self.control = ControlClient("127.0.0.1", port,
                                     cookie_path=self.cookie_path)
        self.control.event_handler = self._on_event
        self.control.connect()
        self.control.authenticate()
        self.control.take_ownership()
        self.control.set_events("STATUS_CLIENT")

    # ---------------------------------------------------------- bootstrap --
    def _on_event(self, lines: list) -> None:
        for line in lines:
            if "BOOTSTRAP" not in line:
                continue
            percent = self.progress
            tag = ""
            summary = ""
            for token in line.split():
                if token.startswith("PROGRESS="):
                    try:
                        percent = int(token.split("=", 1)[1])
                    except ValueError:
                        pass
                elif token.startswith("TAG="):
                    tag = token.split("=", 1)[1]
            if 'SUMMARY="' in line:
                summary = line.split('SUMMARY="', 1)[1].split('"', 1)[0]
            if 'WARN' in line and 'REASON="' in line:
                reason = line.split('REASON="', 1)[1].split('"', 1)[0]
                log(f"Tor: проблема подключения — {reason}")
            self._emit(percent, BOOTSTRAP_TAGS.get(tag) or summary or tag)

    def _wait_bootstrap(self, timeout: float) -> None:
        deadline = time.time() + timeout
        try:
            phase = self.control.getinfo("status/bootstrap-phase")
            if phase:
                self._on_event([phase])
        except ControlError:
            pass
        last_seen = self.progress
        stall_since = time.time()
        while time.time() < deadline:
            if self._stopping.is_set():
                raise TorStartError("Подключение отменено")
            if self.process.poll() is not None:
                raise TorStartError("Процесс tor неожиданно завершился")
            if self.progress >= 100:
                return
            if self.progress != last_seen:
                last_seen = self.progress
                stall_since = time.time()
            elif time.time() - stall_since > 90:
                raise TorStartError(
                    f"Подключение зависло на {self.progress}% "
                    f"({self.phase or 'без описания'}). Обычно это значит, что "
                    f"мосты заблокированы или нерабочие — запросите новые."
                )
            time.sleep(0.3)
        raise TorStartError(
            f"Не удалось подключиться за {int(timeout)} с "
            f"(остановились на {self.progress}%). Попробуйте другие мосты."
        )

    # -------------------------------------------------------------- стоп --
    def stop(self) -> None:
        self._stopping.set()
        if self.control:
            try:
                self.control.signal("HALT")
            except Exception:
                pass
            self.control.close()
            self.control = None
        proc, self.process = self.process, None
        if proc:
            if proc.poll() is None:
                try:
                    proc.terminate()
                    proc.wait(timeout=10)
                except Exception:
                    try:
                        proc.kill()
                    except Exception:
                        pass
            for stream in (proc.stdout, proc.stderr):
                try:
                    if stream:
                        stream.close()
                except Exception:
                    pass
        self.socks_address = ""
        self._emit(0, "Остановлен")

    # ------------------------------------------------------------ прочее --
    @property
    def running(self) -> bool:
        return self.process is not None and self.process.poll() is None

    def new_identity(self) -> None:
        if not self.control:
            raise TorStartError("Tor не запущен")
        self.control.signal("NEWNYM")

    def tor_version(self) -> str:
        try:
            return self.control.getinfo("version") if self.control else ""
        except ControlError:
            return ""
