# -*- coding: utf-8 -*-
"""Локальный HTTP-прокси поверх SOCKS5-порта Tor.

Нужен потому, что многие программы умеют только HTTP-прокси. Имя хоста
передаётся в SOCKS5 как есть — резолвит его сам Tor, утечки DNS нет.
"""

from __future__ import annotations

import select
import socket
import socketserver
import struct
import sys
import threading

from .util import log

SOCKS_ERRORS = {
    1: "общий сбой SOCKS-сервера",
    2: "соединение запрещено правилами",
    3: "сеть недоступна",
    4: "узел недоступен",
    5: "соединение отклонено",
    6: "истёк TTL",
    7: "команда не поддерживается",
    8: "тип адреса не поддерживается",
}

HOP_BY_HOP = {b"proxy-connection", b"proxy-authorization", b"proxy-authenticate",
              b"te", b"trailers", b"upgrade"}


class SocksError(OSError):
    pass


def _recv_exact(sock: socket.socket, n: int) -> bytes:
    buf = b""
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            raise SocksError("SOCKS: соединение закрыто досрочно")
        buf += chunk
    return buf


def socks5_connect(dest_host: str, dest_port: int, socks_host: str,
                   socks_port: int, timeout: float = 60.0,
                   username: str = "", password: str = "") -> socket.socket:
    sock = socket.create_connection((socks_host, int(socks_port)), timeout)
    try:
        sock.settimeout(timeout)
        if username:
            sock.sendall(b"\x05\x02\x00\x02")
        else:
            sock.sendall(b"\x05\x01\x00")
        ver, method = _recv_exact(sock, 2)
        if ver != 5:
            raise SocksError("ответ не похож на SOCKS5")
        if method == 0xFF:
            raise SocksError("SOCKS5: сервер не принял метод аутентификации")
        if method == 0x02:
            u = username.encode()[:255]
            p = password.encode()[:255]
            sock.sendall(b"\x01" + bytes([len(u)]) + u + bytes([len(p)]) + p)
            if _recv_exact(sock, 2)[1] != 0:
                raise SocksError("SOCKS5: неверный логин/пароль")

        try:
            host_bytes = dest_host.encode("idna")
        except UnicodeError:
            host_bytes = dest_host.encode("utf-8")
        if len(host_bytes) > 255:
            raise SocksError("SOCKS5: слишком длинное имя хоста")
        sock.sendall(b"\x05\x01\x00\x03" + bytes([len(host_bytes)]) + host_bytes
                     + struct.pack("!H", int(dest_port)))

        _, rep, _, atyp = _recv_exact(sock, 4)
        if rep != 0:
            raise SocksError(SOCKS_ERRORS.get(rep, f"код {rep}"))
        if atyp == 1:
            _recv_exact(sock, 4)
        elif atyp == 3:
            _recv_exact(sock, _recv_exact(sock, 1)[0])
        elif atyp == 4:
            _recv_exact(sock, 16)
        else:
            raise SocksError("SOCKS5: неизвестный тип адреса")
        _recv_exact(sock, 2)
        sock.settimeout(None)
        return sock
    except Exception:
        try:
            sock.close()
        except OSError:
            pass
        raise


def relay(a: socket.socket, b: socket.socket, idle_timeout: float = 300.0) -> None:
    socks = [a, b]
    try:
        while True:
            readable, _, errored = select.select(socks, [], socks, idle_timeout)
            if errored or not readable:
                break
            for s in readable:
                try:
                    data = s.recv(65536)
                except OSError:
                    return
                if not data:
                    return
                try:
                    (b if s is a else a).sendall(data)
                except OSError:
                    return
    finally:
        for s in socks:
            try:
                s.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            try:
                s.close()
            except OSError:
                pass


class ProxyHandler(socketserver.StreamRequestHandler):
    rbufsize = 0          # без буферизации: иначе «съедим» тело запроса
    wbufsize = 0
    timeout = 120

    def _error(self, code: int, text: str) -> None:
        body = text.encode("utf-8")
        try:
            self.wfile.write(
                (f"HTTP/1.1 {code} Proxy Error\r\n"
                 f"Content-Type: text/plain; charset=utf-8\r\n"
                 f"Content-Length: {len(body)}\r\n"
                 f"Connection: close\r\n\r\n").encode("latin-1") + body)
        except OSError:
            pass

    def handle(self) -> None:
        try:
            request_line = self.rfile.readline(65536)
        except OSError:
            return
        if not request_line:
            return
        try:
            parts = request_line.decode("latin-1").strip().split()
        except Exception:
            return
        if len(parts) != 3:
            self._error(400, "Некорректный запрос")
            return
        method, target, version = parts

        headers = []
        while True:
            line = self.rfile.readline(65536)
            if not line or line in (b"\r\n", b"\n"):
                break
            headers.append(line)
            if len(headers) > 200:
                break

        if method.upper() == "CONNECT":
            self._connect(target)
        else:
            self._plain(method, target, version, headers)

    def _open(self, host: str, port: int) -> socket.socket:
        return self.server.open_upstream(host, port)

    def _connect(self, target: str) -> None:
        host, _, port_s = target.rpartition(":")
        if not host:
            host, port_s = target, "443"
        host = host.strip("[]")
        try:
            port = int(port_s)
        except ValueError:
            self._error(400, "Некорректный порт в CONNECT")
            return
        try:
            remote = self._open(host, port)
        except Exception as e:
            log(f"CONNECT {host}:{port} — {e}")
            self._error(502, f"Через Tor подключиться не удалось: {e}")
            return
        if self.server.verbose:
            log(f"CONNECT {host}:{port}")
        try:
            self.wfile.write(b"HTTP/1.1 200 Connection established\r\n"
                             b"Proxy-Agent: TorLocalProxy\r\n\r\n")
        except OSError:
            remote.close()
            return
        self.connection.settimeout(None)
        relay(self.connection, remote)

    def _plain(self, method: str, target: str, version: str, headers) -> None:
        if not target.lower().startswith("http://"):
            self._error(400, "Ожидался абсолютный http:// URL или CONNECT. "
                             "Программу нужно настроить именно на HTTP-прокси.")
            return
        rest = target[7:]
        authority, slash, path = rest.partition("/")
        path = "/" + path if slash else "/"
        hostport = authority.rpartition("@")[2] or authority
        if hostport.startswith("["):
            host, _, port_s = hostport[1:].partition("]")
            port_s = port_s.lstrip(":")
        else:
            host, _, port_s = hostport.partition(":")
        if not host:
            self._error(400, "В URL не разобран хост")
            return
        try:
            port = int(port_s) if port_s else 80
        except ValueError:
            port = 80

        try:
            remote = self._open(host, port)
        except Exception as e:
            log(f"{method} {host}:{port} — {e}")
            self._error(502, f"Через Tor подключиться не удалось: {e}")
            return
        if self.server.verbose:
            log(f"{method} http://{host}:{port}{path}")

        out = [f"{method} {path} {version}\r\n".encode("latin-1")]
        content_length = 0
        for raw in headers:
            name = raw.split(b":", 1)[0].strip().lower()
            if name in HOP_BY_HOP:
                continue
            if name == b"content-length":
                try:
                    content_length = int(raw.split(b":", 1)[1].strip())
                except Exception:
                    content_length = 0
            out.append(raw)
        out.append(b"\r\n")
        try:
            remote.sendall(b"".join(out))
            remaining = content_length
            while remaining > 0:
                chunk = self.rfile.read(min(65536, remaining))
                if not chunk:
                    break
                remote.sendall(chunk)
                remaining -= len(chunk)
        except OSError as e:
            remote.close()
            log(f"Ошибка отправки запроса: {e}")
            return
        self.connection.settimeout(None)
        relay(self.connection, remote)


class HttpBridgeServer(socketserver.ThreadingTCPServer):
    daemon_threads = True
    allow_reuse_address = True
    request_queue_size = 128

    def __init__(self, listen_host: str, listen_port: int,
                 socks_host: str, socks_port: int, verbose: bool = False):
        self.socks_host = socks_host
        self.socks_port = int(socks_port)
        self.verbose = verbose
        super().__init__((listen_host, int(listen_port)), ProxyHandler)

    def open_upstream(self, host: str, port: int) -> socket.socket:
        return socks5_connect(host, port, self.socks_host, self.socks_port)

    def handle_error(self, request, client_address):
        exc = sys.exc_info()[1]
        if isinstance(exc, (ConnectionResetError, BrokenPipeError, TimeoutError)):
            return
        if self.verbose:
            log(f"Ошибка обработчика: {exc!r}")


class HttpBridge:
    """Обёртка: запуск/остановка HTTP-моста в фоновом потоке."""

    def __init__(self):
        self.server: HttpBridgeServer | None = None
        self._thread: threading.Thread | None = None

    @property
    def running(self) -> bool:
        return self.server is not None

    @property
    def address(self) -> str:
        if not self.server:
            return ""
        host, port = self.server.server_address[:2]
        return f"{host}:{port}"

    def start(self, listen_host: str, listen_port: int, socks_address: str,
              verbose: bool = False) -> str:
        if self.running:
            self.stop()
        socks_host, _, socks_port = socks_address.rpartition(":")
        self.server = HttpBridgeServer(listen_host, listen_port,
                                       socks_host.strip("[]") or "127.0.0.1",
                                       int(socks_port), verbose)
        self._thread = threading.Thread(target=self.server.serve_forever,
                                        kwargs={"poll_interval": 0.3},
                                        daemon=True)
        self._thread.start()
        return self.address

    def stop(self) -> None:
        if not self.server:
            return
        try:
            self.server.shutdown()
            self.server.server_close()
        except Exception as e:
            log(f"Ошибка остановки HTTP-моста: {e}")
        self.server = None
        self._thread = None
