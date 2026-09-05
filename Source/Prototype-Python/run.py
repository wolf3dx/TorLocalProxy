#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Запуск Tor Local Proxy.

    python run.py                       # окно приложения
    python run.py --no-gui --check      # без окна, с проверкой выходного IP
    python run.py --help                # все параметры
"""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from tornode.cli import main

if __name__ == "__main__":
    sys.exit(main())
