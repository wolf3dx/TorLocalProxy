# -*- coding: utf-8 -*-
"""Окно приложения (Tkinter — есть во всех обычных сборках Python)."""

from __future__ import annotations

import queue
import threading
import webbrowser

from .app import (TorProxyService, load_bridges_text, load_config,
                  save_bridges_text, save_config)
from .bridges import parse_bridges
from .tormgr import find_tor
from .util import add_log_sink, log

TITLE = "Tor Local Proxy"
BRIDGE_HINT = (
    "Вставьте сюда строки мостов из письма от bridges@torproject.org.\n"
    "Лишний текст письма можно не вычищать — он будет пропущен.\n\n"
    "Пример:\n"
    "obfs4 192.0.2.1:9443 8AF3...E21 cert=hK8s... iat-mode=0"
)


def run(initial_cfg: dict | None = None) -> int:
    import tkinter as tk
    from tkinter import filedialog, ttk, scrolledtext

    cfg = initial_cfg or load_config()
    service = TorProxyService(cfg)
    msgs: "queue.Queue[str]" = queue.Queue()
    add_log_sink(msgs.put)

    root = tk.Tk()
    root.title(TITLE)
    root.geometry("760x680")
    root.minsize(660, 560)

    state = {"busy": False}

    # ------------------------------------------------------------ мосты --
    top = ttk.LabelFrame(root, text="Мосты", padding=8)
    top.pack(fill="both", expand=False, padx=10, pady=(10, 6))

    bridges_box = scrolledtext.ScrolledText(top, height=8, wrap="none",
                                            font=("Consolas", 9))
    bridges_box.pack(fill="both", expand=True)
    saved = load_bridges_text()
    if saved.strip():
        bridges_box.insert("1.0", saved)
    else:
        bridges_box.insert("1.0", BRIDGE_HINT)
        bridges_box.configure(foreground="#888")

        def clear_hint(_event=None):
            if bridges_box.get("1.0", "end").strip() == BRIDGE_HINT.strip():
                bridges_box.delete("1.0", "end")
                bridges_box.configure(foreground="")
            bridges_box.unbind("<FocusIn>")
        bridges_box.bind("<FocusIn>", clear_hint)

    bridge_info = tk.StringVar(value="")
    bar = ttk.Frame(top)
    bar.pack(fill="x", pady=(6, 0))

    def bridges_text() -> str:
        text = bridges_box.get("1.0", "end")
        return "" if text.strip() == BRIDGE_HINT.strip() else text

    def refresh_bridge_info(*_):
        parsed, problems = parse_bridges(bridges_text())
        if not parsed and not problems:
            bridge_info.set("Мостов нет — будет прямое подключение к сети Tor.")
            return
        kinds = {}
        for b in parsed:
            key = b.transport or "обычный"
            kinds[key] = kinds.get(key, 0) + 1
        text = "Распознано: " + (", ".join(f"{k} × {v}" for k, v in kinds.items())
                                 or "0")
        if problems:
            text += f"; с ошибками: {len(problems)}"
        bridge_info.set(text)

    def paste_clipboard():
        try:
            data = root.clipboard_get()
        except tk.TclError:
            log("Буфер обмена пуст.")
            return
        if bridges_box.cget("foreground") == "#888":
            bridges_box.delete("1.0", "end")
            bridges_box.configure(foreground="")
        bridges_box.insert("end", "\n" + data)
        refresh_bridge_info()

    def check_bridges():
        parsed, problems = parse_bridges(bridges_text())
        log(f"Проверка: пригодных мостов {len(parsed)}.")
        for b in parsed:
            log("  ✓ " + b.short())
        for p in problems:
            log("  ✗ " + p)
        refresh_bridge_info()

    ttk.Button(bar, text="Вставить из буфера", command=paste_clipboard).pack(side="left")
    ttk.Button(bar, text="Проверить", command=check_bridges).pack(side="left", padx=4)
    ttk.Button(bar, text="Где взять мосты",
               command=lambda: webbrowser.open("https://bridges.torproject.org/")
               ).pack(side="left", padx=4)
    ttk.Label(bar, textvariable=bridge_info).pack(side="left", padx=10)
    bridges_box.bind("<KeyRelease>", refresh_bridge_info)

    # -------------------------------------------------------- настройки --
    opts = ttk.LabelFrame(root, text="Настройки", padding=8)
    opts.pack(fill="x", padx=10, pady=6)
    opts.columnconfigure(1, weight=1)
    opts.columnconfigure(3, weight=1)

    v_http_port = tk.StringVar(value=str(cfg.get("http_port", 8080)))
    v_socks_port = tk.StringVar(value=str(cfg.get("socks_port", 9052)))
    v_tor = tk.StringVar(value=cfg.get("tor_binary", "") or find_tor() or "")
    v_verbose = tk.BooleanVar(value=bool(cfg.get("verbose")))

    ttk.Label(opts, text="HTTP-прокси, порт:").grid(row=0, column=0, sticky="w", pady=3)
    ttk.Entry(opts, textvariable=v_http_port, width=10).grid(row=0, column=1, sticky="w")
    ttk.Label(opts, text="SOCKS5, порт:").grid(row=0, column=2, sticky="w", padx=(16, 4))
    ttk.Entry(opts, textvariable=v_socks_port, width=10).grid(row=0, column=3, sticky="w")

    ttk.Label(opts, text="Файл tor:").grid(row=1, column=0, sticky="w", pady=3)
    tor_entry = ttk.Entry(opts, textvariable=v_tor)
    tor_entry.grid(row=1, column=1, columnspan=2, sticky="ew", padx=(0, 6))

    def pick_tor():
        path = filedialog.askopenfilename(title="Выберите исполняемый файл tor")
        if path:
            v_tor.set(path)
    ttk.Button(opts, text="Обзор…", command=pick_tor).grid(row=1, column=3, sticky="w")
    ttk.Checkbutton(opts, text="Подробный журнал соединений",
                    variable=v_verbose).grid(row=2, column=0, columnspan=3,
                                             sticky="w", pady=(4, 0))

    # ------------------------------------------------------- подключение --
    conn = ttk.LabelFrame(root, text="Подключение", padding=8)
    conn.pack(fill="x", padx=10, pady=6)

    progress = ttk.Progressbar(conn, mode="determinate", maximum=100)
    progress.pack(fill="x")
    status_var = tk.StringVar(value="Не подключено")
    status_lbl = ttk.Label(conn, textvariable=status_var, font=("", 10, "bold"))
    status_lbl.pack(anchor="w", pady=(6, 0))
    addr_var = tk.StringVar(value="")
    ttk.Label(conn, textvariable=addr_var, foreground="#1a7f37").pack(anchor="w")

    buttons = ttk.Frame(conn)
    buttons.pack(fill="x", pady=(8, 0))

    def set_status(text: str, ok: bool | None = None):
        status_var.set(text)
        color = "" if ok is None else ("#1a7f37" if ok else "#a33")
        status_lbl.configure(foreground=color)

    def collect_cfg():
        try:
            cfg.update({
                "http_port": int(v_http_port.get()),
                "socks_port": int(v_socks_port.get()),
                "tor_binary": v_tor.get().strip(),
                "verbose": bool(v_verbose.get()),
            })
        except ValueError:
            log("Порты должны быть числами.")
            return False
        service.cfg = cfg
        save_config(cfg)
        save_bridges_text(bridges_text())
        return True

    def busy(flag: bool):
        state["busy"] = flag
        btn_connect.configure(state="disabled" if flag or service.running else "normal")
        btn_disconnect.configure(state="normal" if service.running else "disabled")
        for b in (btn_newnym, btn_check):
            b.configure(state="normal" if service.running and not flag else "disabled")

    def in_thread(fn):
        threading.Thread(target=fn, daemon=True).start()

    def on_progress(percent, text):
        def apply():
            progress["value"] = percent
            set_status(f"{percent}% — {text}" if text else f"{percent}%")
        root.after(0, apply)

    def on_connect():
        if not collect_cfg():
            return
        busy(True)
        progress["value"] = 0
        set_status("Запуск…")

        def work():
            try:
                result = service.connect(bridges_text(), on_progress=on_progress)
            except Exception as e:
                root.after(0, lambda: (set_status(f"Не подключилось: {e}", False),
                                       busy(False)))
                log(f"Ошибка: {e}")
                return

            def done():
                progress["value"] = 100
                set_status("Подключено к сети Tor", True)
                lines = []
                if result.get("http"):
                    lines.append("HTTP-прокси: " + result["http"])
                if result.get("socks"):
                    lines.append("SOCKS5: " + result["socks"])
                addr_var.set("     ".join(lines))
                busy(False)
            root.after(0, done)
        in_thread(work)

    def on_disconnect():
        busy(True)

        def work():
            service.disconnect()

            def done():
                progress["value"] = 0
                addr_var.set("")
                set_status("Не подключено", False)
                busy(False)
            root.after(0, done)
        in_thread(work)

    def on_newnym():
        def work():
            try:
                service.new_identity()
            except Exception as e:
                log(f"Смена цепочки не удалась: {e}")
        in_thread(work)

    def on_check():
        def work():
            log("Проверяю выходной IP через check.torproject.org…")
            try:
                log(service.check_ip())
            except Exception as e:
                log(f"Проверка не удалась: {e}")
        in_thread(work)

    def on_copy():
        text = addr_var.get()
        if not text:
            return
        root.clipboard_clear()
        root.clipboard_append(text.split("     ")[0].split(": ", 1)[-1])
        log("Адрес прокси скопирован в буфер обмена.")

    btn_connect = ttk.Button(buttons, text="Подключиться", command=on_connect)
    btn_disconnect = ttk.Button(buttons, text="Отключиться", command=on_disconnect,
                                state="disabled")
    btn_newnym = ttk.Button(buttons, text="Новая цепочка", command=on_newnym,
                            state="disabled")
    btn_check = ttk.Button(buttons, text="Проверить IP", command=on_check,
                           state="disabled")
    btn_copy = ttk.Button(buttons, text="Копировать адрес", command=on_copy)
    for b in (btn_connect, btn_disconnect, btn_newnym, btn_check, btn_copy):
        b.pack(side="left", padx=3)

    # ------------------------------------------------------------ журнал --
    log_frame = ttk.LabelFrame(root, text="Журнал", padding=6)
    log_frame.pack(fill="both", expand=True, padx=10, pady=(0, 10))
    log_text = scrolledtext.ScrolledText(log_frame, height=10, wrap="word",
                                         state="disabled", font=("Consolas", 9))
    log_text.pack(fill="both", expand=True)

    def drain():
        try:
            while True:
                line = msgs.get_nowait()
                log_text.configure(state="normal")
                log_text.insert("end", line + "\n")
                log_text.see("end")
                log_text.configure(state="disabled")
        except queue.Empty:
            pass
        root.after(150, drain)

    def on_close():
        try:
            collect_cfg()
        except Exception:
            pass
        if service.running:
            service.disconnect()
        root.destroy()

    root.protocol("WM_DELETE_WINDOW", on_close)
    root.after(150, drain)

    refresh_bridge_info()
    found = find_tor(cfg.get("tor_binary", ""))
    if found:
        log(f"Найден Tor: {found}")
    else:
        log("Исполняемый файл tor не найден. Положите Tor Expert Bundle в папку "
            "«tor» рядом с приложением или укажите путь в настройках (README).")
    log("Вставьте мосты и нажмите «Подключиться».")

    root.mainloop()
    return 0
