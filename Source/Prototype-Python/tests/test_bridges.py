# -*- coding: utf-8 -*-
"""Тесты парсера мостов и генератора torrc."""

import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from tornode.bridges import (Bridge, BridgeError, parse_bridge_line,
                             parse_bridges, transports_used)
from tornode.tormgr import build_torrc

FP = "0123456789ABCDEF0123456789ABCDEF01234567"


class TestParseLine(unittest.TestCase):

    def test_obfs4(self):
        b = parse_bridge_line(
            f"obfs4 192.0.2.1:9443 {FP} cert=hK8sABC+/= iat-mode=0")
        self.assertEqual(b.transport, "obfs4")
        self.assertEqual(b.address, "192.0.2.1:9443")
        self.assertEqual(b.fingerprint, FP)
        self.assertEqual(b.args, ("cert=hK8sABC+/=", "iat-mode=0"))
        self.assertEqual(b.host, "192.0.2.1")
        self.assertEqual(b.port, 9443)

    def test_ipv6_webtunnel(self):
        b = parse_bridge_line(
            f"webtunnel [2001:db8::1]:443 {FP} url=https://a.example/x ver=0.0.1")
        self.assertEqual(b.transport, "webtunnel")
        self.assertEqual(b.address, "[2001:db8::1]:443")
        self.assertEqual(b.host, "2001:db8::1")
        self.assertEqual(b.port, 443)

    def test_vanilla_without_transport(self):
        b = parse_bridge_line(f"192.0.2.4:9001 {FP}")
        self.assertEqual(b.transport, "")
        self.assertEqual(b.fingerprint, FP)

    def test_bridge_prefix_and_comment(self):
        b = parse_bridge_line(f"Bridge obfs4 192.0.2.1:1 {FP} cert=x  # мой мост")
        self.assertEqual(b.transport, "obfs4")
        self.assertEqual(b.args, ("cert=x",))

    def test_lowercases_transport_uppercases_fingerprint(self):
        b = parse_bridge_line(f"OBFS4 192.0.2.1:1 {FP.lower()} cert=x")
        self.assertEqual(b.transport, "obfs4")
        self.assertEqual(b.fingerprint, FP)

    def test_snowflake_many_args(self):
        b = parse_bridge_line(
            f"snowflake 192.0.2.3:80 {FP} fingerprint={FP} "
            f"url=https://s.example/ front=foo.example utls-imitate=hellorandomizedalpn")
        self.assertEqual(b.transport, "snowflake")
        self.assertEqual(len(b.args), 4)

    def test_missing_port(self):
        with self.assertRaises(BridgeError):
            parse_bridge_line(f"obfs4 192.0.2.1 {FP}")

    def test_port_out_of_range(self):
        with self.assertRaises(BridgeError):
            parse_bridge_line(f"obfs4 192.0.2.1:99999 {FP}")

    def test_unknown_transport(self):
        with self.assertRaises(BridgeError):
            parse_bridge_line(f"quantumfoo 192.0.2.1:443 {FP}")

    def test_prose_rejected(self):
        with self.assertRaises(BridgeError):
            parse_bridge_line("Здравствуйте, вот ваши мосты:")

    def test_stray_token(self):
        with self.assertRaises(BridgeError):
            parse_bridge_line(f"obfs4 192.0.2.1:443 {FP} мусор")

    def test_no_fingerprint_ok(self):
        b = parse_bridge_line("meek_lite 192.0.2.2:80 url=https://a.example/")
        self.assertEqual(b.fingerprint, "")
        self.assertEqual(b.args, ("url=https://a.example/",))


class TestParseEmail(unittest.TestCase):

    EMAIL = f"""
Здравствуйте!

Вот ваши мосты. Просто вставьте их в Tor Browser.

  obfs4 192.0.2.1:9443 {FP} cert=AAA iat-mode=0
  obfs4 192.0.2.2:443 {FP} cert=BBB iat-mode=0
  obfs4 192.0.2.1:9443 {FP} cert=AAA iat-mode=0

А это сломанная строка:
  obfs4 192.0.2.9:НЕПОРТ {FP} cert=CCC

С уважением,
Tor Project
"""

    def test_extracts_and_dedupes(self):
        bridges, problems = parse_bridges(self.EMAIL)
        self.assertEqual(len(bridges), 2)             # дубль отброшен
        self.assertEqual([b.address for b in bridges],
                         ["192.0.2.1:9443", "192.0.2.2:443"])

    def test_prose_not_reported_as_error(self):
        _, problems = parse_bridges(self.EMAIL)
        self.assertTrue(all("Здравствуйте" not in p for p in problems))
        self.assertTrue(all("уважением" not in p for p in problems))

    def test_broken_line_reported(self):
        _, problems = parse_bridges(self.EMAIL)
        self.assertEqual(len(problems), 1)
        self.assertIn("192.0.2.9", problems[0])

    def test_empty_input(self):
        bridges, problems = parse_bridges("")
        self.assertEqual((bridges, problems), ([], []))

    def test_transports_used_order(self):
        text = (f"snowflake 192.0.2.1:80 {FP}\n"
                f"obfs4 192.0.2.2:443 {FP} cert=x\n"
                f"obfs4 192.0.2.3:443 {FP} cert=y\n"
                f"192.0.2.4:9001 {FP}\n")
        bridges, _ = parse_bridges(text)
        self.assertEqual(transports_used(bridges), ["snowflake", "obfs4"])


class TestTorrcRoundTrip(unittest.TestCase):

    def test_line_survives_round_trip(self):
        original = f"obfs4 192.0.2.1:9443 {FP} cert=AAA+/= iat-mode=0"
        b = parse_bridge_line(original)
        self.assertEqual(b.torrc_line(), "Bridge " + original)

    def test_build_torrc_with_bridges(self):
        bridges, _ = parse_bridges(
            f"obfs4 192.0.2.1:9443 {FP} cert=AAA iat-mode=0\n"
            f"snowflake 192.0.2.3:80 {FP} url=https://s.example/\n")
        torrc = build_torrc(
            datadir="/tmp/d", control_file="/tmp/d/cp", log_file="/tmp/d/tor.log",
            socks_port=9052, bridges=bridges,
            pt_plugins={"/opt/tor/lyrebird": ["obfs4"],
                        "/opt/tor/snowflake-client": ["snowflake"]})
        self.assertIn("UseBridges 1", torrc)
        self.assertIn("ClientTransportPlugin obfs4 exec /opt/tor/lyrebird", torrc)
        self.assertIn("ClientTransportPlugin snowflake exec /opt/tor/snowflake-client",
                      torrc)
        self.assertIn("SocksPort 127.0.0.1:9052", torrc)
        self.assertIn("ControlPort auto", torrc)
        self.assertIn("CookieAuthentication 1", torrc)
        self.assertEqual(torrc.count("Bridge "), 2)

    def test_build_torrc_without_bridges(self):
        torrc = build_torrc(datadir="/tmp/d", control_file="/tmp/d/cp",
                            log_file="/tmp/d/l", socks_port="auto",
                            bridges=[], pt_plugins={})
        self.assertNotIn("UseBridges", torrc)
        self.assertNotIn("ClientTransportPlugin", torrc)
        self.assertIn("SocksPort auto", torrc)

    def test_paths_with_spaces_are_quoted(self):
        torrc = build_torrc(datadir="/home/a b/data", control_file="/home/a b/cp",
                            log_file="/home/a b/l", socks_port=1234,
                            bridges=[], pt_plugins={})
        self.assertIn('DataDirectory "/home/a b/data"', torrc)

    def test_grouped_transports_are_sorted(self):
        bridges, _ = parse_bridges(
            f"meek_lite 192.0.2.2:80 {FP} url=https://a/\n"
            f"obfs4 192.0.2.1:443 {FP} cert=x\n")
        torrc = build_torrc(datadir="/d", control_file="/c", log_file="/l",
                            socks_port=1, bridges=bridges,
                            pt_plugins={"/opt/lyrebird": ["obfs4", "meek_lite"]})
        self.assertIn("ClientTransportPlugin meek_lite,obfs4 exec /opt/lyrebird",
                      torrc)


if __name__ == "__main__":
    unittest.main(verbosity=2)
