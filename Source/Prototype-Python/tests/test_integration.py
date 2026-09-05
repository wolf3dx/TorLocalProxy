# -*- coding: utf-8 -*-
"""Сквозной тест: torrc → процесс tor → control-порт → bootstrap → прокси.

Настоящий tor не нужен: вместо него подставляется tests/fake_tor.py.
"""

import http.server
import json
import os
import socket
import socketserver
import struct
import sys
import threading
import unittest
import urllib.request

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, os.path.dirname(HERE))

from tornode.app import TorProxyService
from tornode.bridges import parse_bridges
from tornode.httpbridge import HttpBridge, socks5_connect
from tornode.tormgr import TorManager, TorStartError, find_pt
from tornode.util import free_port

FP = "0123456789ABCDEF0123456789ABCDEF01234567"
FAKE_TOR = os.path.join(HERE, "fake_tor.py")
BRIDGES = (f"obfs4 127.0.0.1:9443 {FP} cert=AAA iat-mode=0\n"
           f"obfs4 127.0.0.1:9444 {FP} cert=BBB iat-mode=0\n")


def make_launcher(tmpdir):
    """Обёртка, чтобы fake_tor.py можно было запустить как исполняемый файл."""
    path = os.path.join(tmpdir, "tor")
    with open(path, "w", encoding="utf-8") as f:
        f.write("#!/bin/sh\nexec %s %s \"$@\"\n" % (sys.executable, FAKE_TOR))
    os.chmod(path, 0o755)
    return path


def make_pt_stub(tmpdir, name="lyrebird"):
    path = os.path.join(tmpdir, name)
    with open(path, "w", encoding="utf-8") as f:
        f.write("#!/bin/sh\nsleep 3600\n")
    os.chmod(path, 0o755)
    return path


class TargetSite:
    """Локальный сайт, изображающий check.torproject.org."""

    def __init__(self):
        outer = self

        class H(http.server.BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, *a):
                pass

            def do_GET(self):
                body = json.dumps({"path": self.path,
                                   "host": self.headers.get("Host"),
                                   "IsTor": True}).encode()
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def do_POST(self):
                n = int(self.headers.get("Content-Length", 0))
                body = json.dumps({"got": self.rfile.read(n).decode()}).encode()
                self.send_response(200)
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

        self.server = socketserver.ThreadingTCPServer(("127.0.0.1", 0), H)
        self.server.daemon_threads = True
        self.port = self.server.server_address[1]
        threading.Thread(target=self.server.serve_forever, daemon=True).start()

    def stop(self):
        self.server.shutdown()
        self.server.server_close()


class IntegrationBase(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        import tempfile
        cls.tmp = tempfile.mkdtemp(prefix="torlocalproxy-test-")
        cls.tor = make_launcher(cls.tmp)
        cls.pt = make_pt_stub(cls.tmp)
        cls.site = TargetSite()

    @classmethod
    def tearDownClass(cls):
        cls.site.stop()
        import shutil
        shutil.rmtree(cls.tmp, ignore_errors=True)


@unittest.skipIf(os.name == "nt", "оболочка-обёртка только для UNIX")
class TestTorManager(IntegrationBase):

    def setUp(self):
        self.mgr = TorManager()
        self.mgr.datadir = self.tmp
        self.mgr.torrc_path = os.path.join(self.tmp, "torrc")
        self.mgr.control_file = os.path.join(self.tmp, "control_port")
        self.mgr.log_file = os.path.join(self.tmp, "tor.log")
        self.mgr.cookie_path = os.path.join(self.tmp, "control_auth_cookie")
        self.seen = []
        self.mgr.on_progress = lambda p, t: self.seen.append((p, t))

    def tearDown(self):
        self.mgr.stop()

    def test_bootstrap_to_100_and_socks_reported(self):
        bridges, _ = parse_bridges(BRIDGES)
        socks = self.mgr.start(bridges, tor_binary=self.tor,
                               socks_port="auto", timeout=60)
        self.assertRegex(socks, r"^127\.0\.0\.1:\d+$")
        self.assertEqual(self.mgr.progress, 100)
        self.assertTrue(self.mgr.running)
        # прогресс приходил постепенно, а не одним скачком
        percents = [p for p, _ in self.seen]
        self.assertIn(50, percents)
        self.assertEqual(max(percents), 100)
        # фазы переведены на русский
        self.assertIn("Готово — Tor подключён", [t for _, t in self.seen])

    def test_torrc_contains_bridges_and_plugin(self):
        bridges, _ = parse_bridges(BRIDGES)
        self.mgr.start(bridges, tor_binary=self.tor, socks_port="auto", timeout=60)
        with open(self.mgr.torrc_path, encoding="utf-8") as f:
            torrc = f.read()
        self.assertIn("UseBridges 1", torrc)
        self.assertEqual(torrc.count("Bridge obfs4 127.0.0.1:"), 2)
        self.assertIn("ClientTransportPlugin obfs4 exec " + self.pt, torrc)

    def test_socks_actually_works(self):
        bridges, _ = parse_bridges(BRIDGES)
        socks = self.mgr.start(bridges, tor_binary=self.tor,
                               socks_port="auto", timeout=60)
        host, _, port = socks.rpartition(":")
        s = socks5_connect("127.0.0.1", self.site.port, host, int(port))
        s.sendall(b"GET /via-socks HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
        data = b""
        while True:
            chunk = s.recv(4096)
            if not chunk:
                break
            data += chunk
        s.close()
        self.assertIn(b"/via-socks", data)

    def test_new_identity(self):
        bridges, _ = parse_bridges(BRIDGES)
        self.mgr.start(bridges, tor_binary=self.tor, socks_port="auto", timeout=60)
        self.mgr.new_identity()          # не должно бросить
        self.assertEqual(self.mgr.tor_version(), "0.4.8.12-fake")

    def test_missing_pt_binary_gives_clear_error(self):
        bridges, _ = parse_bridges(f"conjure 127.0.0.1:443 {FP} url=https://a/\n")
        with self.assertRaises(TorStartError) as ctx:
            self.mgr.start(bridges, tor_binary=self.tor, socks_port="auto",
                           timeout=20)
        self.assertIn("conjure", str(ctx.exception))
        self.assertIn("conjure-client", str(ctx.exception))
        self.assertFalse(self.mgr.running)

    def test_tor_not_found(self):
        with self.assertRaises(TorStartError) as ctx:
            self.mgr.start([], tor_binary="/nonexistent/tor", timeout=5)
        self.assertIn("Tor Expert Bundle", str(ctx.exception))

    def test_process_crash_reported(self):
        os.environ["FAKE_TOR_FAIL"] = "1"
        try:
            with self.assertRaises(TorStartError) as ctx:
                self.mgr.start([], tor_binary=self.tor, socks_port="auto",
                               timeout=20)
            self.assertIn("завершил", str(ctx.exception))
        finally:
            os.environ.pop("FAKE_TOR_FAIL", None)

    def test_stop_kills_process(self):
        bridges, _ = parse_bridges(BRIDGES)
        self.mgr.start(bridges, tor_binary=self.tor, socks_port="auto", timeout=60)
        proc = self.mgr.process
        self.mgr.stop()
        self.assertIsNotNone(proc.poll())
        self.assertFalse(self.mgr.running)


@unittest.skipIf(os.name == "nt", "оболочка-обёртка только для UNIX")
class TestFullService(IntegrationBase):

    def setUp(self):
        self.http_port = free_port()
        self.service = TorProxyService({
            "http_host": "127.0.0.1", "http_port": self.http_port,
            "socks_port": "auto", "tor_binary": self.tor,
            "extra_torrc": "", "verbose": False, "connect_timeout": 60,
        })
        self.service.tor.datadir = self.tmp
        self.service.tor.torrc_path = os.path.join(self.tmp, "torrc2")
        self.service.tor.control_file = os.path.join(self.tmp, "control_port2")
        self.service.tor.log_file = os.path.join(self.tmp, "tor2.log")
        self.service.tor.cookie_path = os.path.join(self.tmp, "control_auth_cookie")

    def tearDown(self):
        self.service.disconnect()

    def test_http_proxy_over_tor(self):
        result = self.service.connect(BRIDGES)
        self.assertEqual(result["bridges"], 2)
        self.assertEqual(result["http"], f"127.0.0.1:{self.http_port}")

        proxy = "http://" + result["http"]
        opener = urllib.request.build_opener(
            urllib.request.ProxyHandler({"http": proxy, "https": proxy}))

        body = opener.open(f"http://127.0.0.1:{self.site.port}/plain?a=1",
                           timeout=20).read()
        self.assertIn(b'"/plain?a=1"', body)

        req = urllib.request.Request(f"http://127.0.0.1:{self.site.port}/post",
                                     data=b"telo-zaprosa")
        self.assertIn(b"telo-zaprosa", opener.open(req, timeout=20).read())

    def test_connect_tunnel_over_tor(self):
        self.service.connect(BRIDGES)
        s = socket.create_connection(("127.0.0.1", self.http_port), 10)
        s.sendall(f"CONNECT 127.0.0.1:{self.site.port} HTTP/1.1\r\n"
                  f"Host: 127.0.0.1:{self.site.port}\r\n\r\n".encode())
        self.assertIn(b"200", s.recv(200))
        s.sendall(b"GET /tunnel HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")
        data = b""
        while True:
            chunk = s.recv(4096)
            if not chunk:
                break
            data += chunk
        s.close()
        self.assertIn(b"/tunnel", data)

    def test_dns_is_not_resolved_locally(self):
        """Имя хоста должно уходить в SOCKS как есть — иначе утечка DNS."""
        self.service.connect(BRIDGES)
        captured = {}
        real = socket.getaddrinfo

        def spy(host, *a, **kw):
            captured.setdefault("hosts", []).append(host)
            return real(host, *a, **kw)

        socket.getaddrinfo = spy
        try:
            s = socket.create_connection(("127.0.0.1", self.http_port), 10)
            s.sendall(b"CONNECT secret.example.onion:443 HTTP/1.1\r\n\r\n")
            s.recv(200)
            s.close()
        finally:
            socket.getaddrinfo = real
        self.assertNotIn("secret.example.onion", captured.get("hosts", []))

    def test_disconnect_releases_ports(self):
        self.service.connect(BRIDGES)
        self.service.disconnect()
        self.assertFalse(self.service.running)
        self.assertFalse(self.service.http.running)
        # порт снова свободен — значит, можно переподключиться
        s = socket.socket()
        s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
        s.bind(("127.0.0.1", self.http_port))
        s.close()


if __name__ == "__main__":
    unittest.main(verbosity=2)
