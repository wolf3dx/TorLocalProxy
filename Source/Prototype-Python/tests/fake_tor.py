#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Заглушка вместо настоящего tor — только для тестов.

Ведёт себя как tor настолько, насколько это нужно приложению:
читает torrc, поднимает control-порт с cookie-аутентификацией, пишет
файл с номером порта, шлёт события BOOTSTRAP от 0 до 100 и обслуживает
настоящий SOCKS5 (соединения идут напрямую, без всякой анонимности).
"""

import hashlib
import hmac
import os
import socket
import socketserver
import struct
import sys
import threading
import time

VERSION = "0.4.8.12-fake"

SAFECOOKIE_CLIENT_KEY = b"Tor safe cookie authentication controller-to-server hash"
SAFECOOKIE_SERVER_KEY = b"Tor safe cookie authentication server-to-controller hash"


def parse_torrc(path):
    cfg = {}
    with open(path, "r", encoding="utf-8") as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith("#"):
                continue
            key, _, value = line.partition(" ")
            value = value.strip()
            if value.startswith('"') and value.endswith('"'):
                value = value[1:-1].replace('\\"', '"').replace("\\\\", "\\")
            cfg.setdefault(key, []).append(value)
    return cfg


# ------------------------------------------------------------------ SOCKS5 --

class SocksServer(socketserver.ThreadingTCPServer):
    daemon_threads = True
    allow_reuse_address = True


class SocksHandler(socketserver.BaseRequestHandler):
    def handle(self):
        c = self.request
        head = c.recv(2)
        if len(head) < 2 or head[0] != 5:
            return
        c.recv(head[1])
        c.sendall(b"\x05\x00")
        req = c.recv(4)
        if len(req) < 4:
            return
        atyp = req[3]
        if atyp == 1:
            host = socket.inet_ntoa(c.recv(4))
        elif atyp == 3:
            host = c.recv(c.recv(1)[0]).decode()
        else:
            c.sendall(b"\x05\x08\x00\x01" + b"\x00" * 6)
            return
        port = struct.unpack("!H", c.recv(2))[0]
        try:
            up = socket.create_connection((host, port), 10)
        except OSError:
            c.sendall(b"\x05\x05\x00\x01" + b"\x00" * 6)
            return
        c.sendall(b"\x05\x00\x00\x01" + b"\x00" * 6)
        _relay(c, up)


def _relay(a, b):
    import select
    try:
        while True:
            r, _, x = select.select([a, b], [], [a, b], 120)
            if x or not r:
                return
            for s in r:
                data = s.recv(65536)
                if not data:
                    return
                (b if s is a else a).sendall(data)
    except OSError:
        pass
    finally:
        for s in (a, b):
            try:
                s.close()
            except OSError:
                pass


# ----------------------------------------------------------------- control --

class State:
    def __init__(self):
        self.progress = 0
        self.tag = "starting"
        self.summary = "Starting"
        self.event_clients = []
        self.lock = threading.Lock()
        self.socks_address = ""
        self.halt = threading.Event()
        self.newnym_count = 0


STATE = State()
COOKIE = b""

PHASES = [
    (0, "starting", "Starting"),
    (5, "conn_pt", "Connecting to pluggable transport"),
    (10, "conn_done_pt", "Connected to pluggable transport"),
    (14, "handshake", "Handshaking with a relay"),
    (25, "requesting_status", "Asking for networkstatus consensus"),
    (50, "loading_descriptors", "Loading relay descriptors"),
    (75, "enough_dirinfo", "Loaded enough directory info"),
    (90, "ap_handshake_done", "Handshake finished with a relay"),
    (100, "done", "Done"),
]


def bootstrap_loop(delay):
    for percent, tag, summary in PHASES:
        if STATE.halt.is_set():
            return
        time.sleep(delay)
        with STATE.lock:
            STATE.progress, STATE.tag, STATE.summary = percent, tag, summary
            clients = list(STATE.event_clients)
        line = (f'650 STATUS_CLIENT NOTICE BOOTSTRAP PROGRESS={percent} '
                f'TAG={tag} SUMMARY="{summary}"\r\n').encode()
        for f in clients:
            try:
                f.write(line)
            except OSError:
                pass


class ControlServer(socketserver.ThreadingTCPServer):
    daemon_threads = True
    allow_reuse_address = True


class ControlHandler(socketserver.StreamRequestHandler):
    rbufsize = 0
    wbufsize = 0

    def handle(self):
        authed = False
        while True:
            raw = self.rfile.readline(8192)
            if not raw:
                return
            line = raw.decode("utf-8", "replace").strip()
            cmd = line.split()[0].upper() if line else ""

            if cmd == "PROTOCOLINFO":
                cookie_path = os.path.join(STATE.datadir, "control_auth_cookie")
                # Настоящий tor отдаёт путь как QuotedString: обратные слэши
                # и кавычки внутри экранированы. На UNIX это ничего не меняет,
                # а на Windows без экранирования строгий клиент путь отвергает.
                quoted = cookie_path.replace("\\", "\\\\").replace('"', '\\"')
                self.send('250-PROTOCOLINFO 1',
                          f'250-AUTH METHODS=COOKIE,SAFECOOKIE COOKIEFILE="{quoted}"',
                          f'250-VERSION Tor="{VERSION}"',
                          '250 OK')
            elif cmd == "AUTHCHALLENGE":
                parts = line.split()
                if len(parts) < 3 or parts[1].upper() != "SAFECOOKIE":
                    self.send("513 AUTHCHALLENGE requires SAFECOOKIE")
                    continue
                client_nonce = bytes.fromhex(parts[2])
                server_nonce = os.urandom(32)
                blob = COOKIE + client_nonce + server_nonce
                server_hash = hmac.new(SAFECOOKIE_SERVER_KEY, blob,
                                       hashlib.sha256).hexdigest()
                self.expected = hmac.new(SAFECOOKIE_CLIENT_KEY, blob,
                                         hashlib.sha256).hexdigest()
                self.send(f"250 AUTHCHALLENGE SERVERHASH={server_hash} "
                          f"SERVERNONCE={server_nonce.hex()}")
            elif cmd == "AUTHENTICATE":
                arg = (line.split(None, 1)[1] if " " in line else "").lower()
                allowed = {COOKIE.hex()}
                if getattr(self, "expected", None):
                    allowed.add(self.expected)
                if arg and arg not in allowed:
                    self.send("515 Authentication failed")
                    continue
                authed = True
                self.send("250 OK")
            elif not authed:
                self.send("514 Authentication required")
            elif cmd in ("TAKEOWNERSHIP", "RESETCONF", "SETCONF"):
                self.send("250 OK")
            elif cmd == "SETEVENTS":
                with STATE.lock:
                    STATE.event_clients.append(self.wfile)
                self.send("250 OK")
            elif cmd == "GETINFO":
                self.getinfo(line.split()[1:])
            elif cmd == "SIGNAL":
                name = line.split()[1].upper() if len(line.split()) > 1 else ""
                self.send("250 OK")
                if name == "NEWNYM":
                    with STATE.lock:
                        STATE.newnym_count += 1
                elif name in ("HALT", "SHUTDOWN"):
                    STATE.halt.set()
                    threading.Thread(target=lambda: (time.sleep(0.2),
                                                     os._exit(0)),
                                     daemon=True).start()
            elif cmd == "QUIT":
                self.send("250 closing connection")
                return
            else:
                self.send("510 Unrecognized command")

    def getinfo(self, keys):
        out = []
        for key in keys:
            if key == "status/bootstrap-phase":
                with STATE.lock:
                    out.append(f'250-status/bootstrap-phase=NOTICE BOOTSTRAP '
                               f'PROGRESS={STATE.progress} TAG={STATE.tag} '
                               f'SUMMARY="{STATE.summary}"')
            elif key == "net/listeners/socks":
                out.append(f'250-net/listeners/socks="{STATE.socks_address}"')
            elif key == "version":
                out.append(f"250-version={VERSION}")
            else:
                self.send(f"552 Unrecognized key {key}")
                return
        out.append("250 OK")
        self.send(*out)

    def send(self, *lines):
        try:
            self.wfile.write(("\r\n".join(lines) + "\r\n").encode())
        except OSError:
            pass


# -------------------------------------------------------------------- main --

def main(argv):
    global COOKIE
    if "-f" not in argv:
        sys.stderr.write("fake_tor: ожидался -f torrc\n")
        return 1
    torrc_path = argv[argv.index("-f") + 1]
    cfg = parse_torrc(torrc_path)

    if os.environ.get("FAKE_TOR_FAIL"):
        sys.stderr.write("fake_tor: имитирую сбой запуска\n")
        return 3

    STATE.datadir = cfg["DataDirectory"][0]
    os.makedirs(STATE.datadir, exist_ok=True)
    COOKIE = os.urandom(32)
    with open(os.path.join(STATE.datadir, "control_auth_cookie"), "wb") as f:
        f.write(COOKIE)

    socks_cfg = cfg.get("SocksPort", ["auto"])[0]
    if socks_cfg == "auto":
        socks_host, socks_port = "127.0.0.1", 0
    else:
        socks_host, _, port_s = socks_cfg.rpartition(":")
        socks_host = socks_host or "127.0.0.1"
        socks_port = int(port_s)
    socks = SocksServer((socks_host, socks_port), SocksHandler)
    STATE.socks_address = "%s:%d" % socks.server_address[:2]
    threading.Thread(target=socks.serve_forever, daemon=True).start()

    control = ControlServer(("127.0.0.1", 0), ControlHandler)
    threading.Thread(target=control.serve_forever, daemon=True).start()

    # tor записывает файл с портом в самом конце инициализации
    with open(cfg["ControlPortWriteToFile"][0], "w", encoding="utf-8") as f:
        f.write("PORT=127.0.0.1:%d\n" % control.server_address[1])

    log_path = cfg["Log"][0].split(None, 2)[-1].strip('"')
    with open(log_path, "a", encoding="utf-8") as f:
        f.write("fake tor started, bridges=%d\n" % len(cfg.get("Bridge", [])))

    delay = float(os.environ.get("FAKE_TOR_DELAY", "0.05"))
    threading.Thread(target=bootstrap_loop, args=(delay,), daemon=True).start()

    while not STATE.halt.is_set():
        time.sleep(0.2)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
