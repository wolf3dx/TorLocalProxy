# -*- coding: utf-8 -*-
"""Клиент control-протокола Tor.

Одно соединение обслуживает и команды, и асинхронные события 650:
отдельный поток читает ответы и раскладывает их по очередям.
"""

from __future__ import annotations

import binascii
import hashlib
import hmac
import os
import queue
import socket
import threading

from .util import log

SAFECOOKIE_CLIENT_KEY = b"Tor safe cookie authentication controller-to-server hash"
SAFECOOKIE_SERVER_KEY = b"Tor safe cookie authentication server-to-controller hash"


class ControlError(OSError):
    pass


class ControlClient:
    def __init__(self, host: str, port: int, cookie_path: str = "",
                 password: str = ""):
        self.host = host
        self.port = int(port)
        self.cookie_path = cookie_path
        self.password = password or ""
        self._sock: socket.socket | None = None
        self._f = None
        self._replies: "queue.Queue" = queue.Queue()
        self._cmd_lock = threading.Lock()
        self._reader: threading.Thread | None = None
        self._closed = threading.Event()
        self.event_handler = None      # callable(list[str])

    # ------------------------------------------------------------- сеть --
    def connect(self, timeout: float = 15.0) -> "ControlClient":
        self._sock = socket.create_connection((self.host, self.port), timeout)
        self._sock.settimeout(None)
        self._f = self._sock.makefile("rwb", buffering=0)
        self._reader = threading.Thread(target=self._read_loop, daemon=True)
        self._reader.start()
        return self

    def close(self) -> None:
        self._closed.set()
        try:
            if self._f:
                self._f.close()
        except Exception:
            pass
        try:
            if self._sock:
                self._sock.close()
        except Exception:
            pass
        self._f = self._sock = None

    def __enter__(self):
        return self.connect()

    def __exit__(self, *exc):
        self.close()

    @property
    def alive(self) -> bool:
        return self._sock is not None and not self._closed.is_set()

    # --------------------------------------------------------- протокол --
    def _read_block(self) -> list:
        """Читает один законченный блок ответа."""
        lines = []
        while True:
            raw = self._f.readline()
            if not raw:
                raise ControlError("control-соединение закрыто")
            line = raw.decode("utf-8", "replace").rstrip("\r\n")
            lines.append(line)
            if len(line) < 4:
                continue
            sep = line[3]
            if sep == "+":                      # блок данных до строки "."
                while True:
                    more = self._f.readline()
                    if not more:
                        raise ControlError("control-соединение закрыто")
                    s = more.decode("utf-8", "replace").rstrip("\r\n")
                    if s == ".":
                        break
                    lines.append(s)
                continue
            if sep == " ":                      # последняя строка ответа
                return lines

    def _read_loop(self) -> None:
        while not self._closed.is_set():
            try:
                block = self._read_block()
            except Exception:
                if not self._closed.is_set():
                    self._replies.put(ControlError("связь с Tor потеряна"))
                return
            if block and block[0].startswith("650"):
                handler = self.event_handler
                if handler:
                    try:
                        handler(block)
                    except Exception as e:
                        log(f"Обработчик события упал: {e}")
            else:
                self._replies.put(block)

    def command(self, cmd: str, timeout: float = 30.0) -> list:
        if not self.alive:
            raise ControlError("нет соединения с control-портом")
        with self._cmd_lock:
            self._f.write(cmd.encode("utf-8") + b"\r\n")
            try:
                reply = self._replies.get(timeout=timeout)
            except queue.Empty:
                raise ControlError(f"Tor не ответил на «{cmd}»")
        if isinstance(reply, Exception):
            raise reply
        if not reply or not reply[-1].startswith("250"):
            raise ControlError(f"{cmd} → " + (reply[-1] if reply else "пусто"))
        return reply

    # ------------------------------------------------------ аутентификация --
    def authenticate(self) -> None:
        info = self.command("PROTOCOLINFO 1")
        methods, cookiefile = "", ""
        for line in info:
            if "METHODS=" in line:
                methods = line.split("METHODS=", 1)[1].split()[0]
                if 'COOKIEFILE="' in line:
                    raw = line.split('COOKIEFILE="', 1)[1]
                    # Значение — QuotedString: закрывающей считается кавычка,
                    # перед которой нет обратного слэша.
                    out, i = [], 0
                    while i < len(raw):
                        ch = raw[i]
                        if ch == "\\" and i + 1 < len(raw):
                            out.append(raw[i + 1])
                            i += 2
                            continue
                        if ch == '"':
                            break
                        out.append(ch)
                        i += 1
                    cookiefile = "".join(out)
        cookiefile = self.cookie_path or cookiefile

        if self.password:
            esc = self.password.replace("\\", "\\\\").replace('"', '\\"')
            self.command(f'AUTHENTICATE "{esc}"')
            return
        if "NULL" in methods:
            self.command("AUTHENTICATE")
            return
        if cookiefile and os.path.isfile(cookiefile):
            with open(cookiefile, "rb") as fh:
                cookie = fh.read()
            if "SAFECOOKIE" in methods:
                self._safecookie(cookie)
            else:
                self.command("AUTHENTICATE " + binascii.hexlify(cookie).decode())
            return
        raise ControlError(
            "Tor не принимает аутентификацию: нет ни cookie-файла, ни пароля."
        )

    def _safecookie(self, cookie: bytes) -> None:
        client_nonce = os.urandom(32)
        reply = self.command("AUTHCHALLENGE SAFECOOKIE "
                             + binascii.hexlify(client_nonce).decode())
        line = reply[-1]
        server_hash = binascii.unhexlify(line.split("SERVERHASH=", 1)[1].split()[0])
        server_nonce = binascii.unhexlify(line.split("SERVERNONCE=", 1)[1].split()[0])
        expect = hmac.new(SAFECOOKIE_SERVER_KEY,
                          cookie + client_nonce + server_nonce,
                          hashlib.sha256).digest()
        if not hmac.compare_digest(expect, server_hash):
            raise ControlError("SAFECOOKIE: сервер не прошёл проверку")
        client_hash = hmac.new(SAFECOOKIE_CLIENT_KEY,
                               cookie + client_nonce + server_nonce,
                               hashlib.sha256).digest()
        self.command("AUTHENTICATE " + binascii.hexlify(client_hash).decode())

    # -------------------------------------------------------- удобные вызовы --
    def getinfo(self, key: str) -> str:
        prefix = key + "="
        for line in self.command("GETINFO " + key):
            body = line[4:] if len(line) > 4 else ""
            if body.startswith(prefix):
                value = body[len(prefix):]
                return value.strip('"')
        return ""

    def take_ownership(self) -> None:
        """Tor завершится сам, когда мы отпустим соединение."""
        self.command("TAKEOWNERSHIP")
        try:
            self.command("RESETCONF __OwningControllerProcess")
        except ControlError:
            pass

    def set_events(self, *events: str) -> None:
        self.command("SETEVENTS " + " ".join(events))

    def signal(self, name: str) -> None:
        self.command("SIGNAL " + name)

    def socks_listener(self) -> str:
        raw = self.getinfo("net/listeners/socks")
        return raw.split()[0].strip('"') if raw else ""
