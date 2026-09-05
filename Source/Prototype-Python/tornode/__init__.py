# -*- coding: utf-8 -*-
"""Tor Local Proxy — локальный прокси через луковую сеть, без браузера.

Приложение поднимает собственный процесс tor (с мостами и pluggable
transports, как это делает Tor Browser) и отдаёт наружу два адреса:
SOCKS5-порт самого Tor и HTTP-прокси поверх него.
"""

__version__ = "2.0.0"
__all__ = ["bridges", "control", "tormgr", "httpbridge", "app", "cli"]
