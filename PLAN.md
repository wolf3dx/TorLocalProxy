# План разработки: Tor Local Proxy на Go

Рабочий план для реализации. Один этап — одна сессия. Переход к следующему
этапу только когда тесты текущего зелёные.

**Что делаем:** кросс-платформенный клиент луковой сети с поддержкой мостов.
На десктопе отдаёт локальный прокси (SOCKS5 + HTTP), на мобильных работает
как VPN. Функционала браузера нет.

**Целевые платформы (правило 9):** Windows x64, Linux, macOS x86_64, macOS arm64,
Android, iOS.

**Разделение ролей (правило 12):** этот план составлен в Cowork. Сборка по нему —
работа Code. Cowork в реализацию не вмешивается.

**Язык (правила 10–11):** Go вместо приоритетного C#, по прямому согласованию.
Причина: весь экосистем pluggable transports написан на Go, и на iOS он
подключается только как Go-код в том же процессе.

---

## 1. Ограничения, из которых следует вся архитектура

Прочитать до написания первой строки кода. Каждое из них уже стоило бы
переписывания, если вспомнить о нём на середине.

### 1.1. iOS запрещает дочерние процессы

Ни `tor`, ни `lyrebird` не могут быть отдельными исполняемыми файлами.
Всё — библиотеки внутри процесса приложения.

**Следствие:** архитектура «запускаем tor и разговариваем с ним по
control-порту» допустима только как *одна из реализаций* для десктопа.
Основной путь — tor и транспорты как слинкованные библиотеки.

### 1.2. gomobile bind поддерживает узкий набор типов

Через границу Go↔Swift/Kotlin проходят только: знаковые целые, float,
`string`, `bool`, `[]byte`, `error`, интерфейсы и структуры, у которых все
экспортированные поля и методы тоже из этого набора. Функция возвращает
не больше двух значений, и второе обязано быть `error`.

**Срезов структур и map нет.** Совсем.

**Следствие:** пакет `mobileapi` проектируется под это с самого начала.
Сложные структуры уезжают наружу как JSON-строки, события — через
интерфейс-наблюдатель, реализованный на стороне Swift/Kotlin.

### 1.3. На мобильных локальный прокси не виден другим приложениям

Android и iOS не дают приложению обслуживать чужой трафик через
`127.0.0.1`. Единственный путь — VPN: `VpnService` на Android,
`NEPacketTunnelProvider` на iOS. Между TUN-интерфейсом и SOCKS-портом Tor
нужен слой tun2socks.

**Следствие:** десктоп и мобильные — два разных способа доставки одного
ядра. `core` об этом не знает вообще.

### 1.4. iOS Network Extension живёт в жёстком лимите памяти

Расширение пакетного туннеля получает около 50 МБ. Внутри должны
уместиться tor, транспорт и tun2socks. Это узкое место, и мерить его надо
на этапе 11, а не после.

---

## 2. Зависимости

| Что | Пакет | Роль | Риск |
|---|---|---|---|
| Control-протокол Tor | `github.com/cretz/bine` | клиент control-порта, запуск tor | низкий, MIT |
| Встроенный tor | `berty.tech/go-libtor` | tor статической библиотекой | **см. 2.1** |
| Транспорты | `IPtProxy` (`github.com/tladesignz/IPtProxy`) | lyrebird, snowflake, webtunnel в процессе | средний |
| Десктопный GUI | `fyne.io/fyne/v2` | одно окно на Windows и macOS | низкий |
| tun2socks | выбрать на этапе 10 | TUN → SOCKS5 на мобильных | средний |

### 2.1. go-libtor и Windows — открытый вопрос

Форк `berty` заявляет Linux, Android и Darwin (macOS + iOS, amd64 + arm64).
**Windows в нём нет.** Форк `gen2brain/go-libtor` заявляет Windows amd64 и
386, но он менее поддерживаемый.

Решение принимается на этапе 8, до тех пор Windows работает на внешнем
`tor.exe`. Именно поэтому `torrun` с самого начала делается интерфейсом с
двумя реализациями, а не одной функцией.

### 2.2. Лицензии — проверить до релиза

Отдельная задача этапа 12: свести лицензии tor, lyrebird, snowflake,
IPtProxy, Fyne и убедиться, что выбранная лицензия проекта с ними
совместима. Особенно внимательно к тому, что тянет за собой go-libtor
(OpenSSL, libevent, zlib).

---

## 3. Структура репозитория

Обязательные `Source` и `Release` — правило 4. Весь код Go живёт внутри `Source`.

```
TorLocalProxy/                     = репозиторий GitLab (правило 3)
├── README.md                      описание; clone-all берёт отсюда текст для GitLab
├── PLAN.md                        этот файл
├── .gitattributes                 * text=auto обязателен, см. 3.1
├── .gitignore
├── Source/                        ← правило 4
│   ├── ЛОГИКА.md                  карта логики, тоже требование правила 4
│   ├── Prototype-Python/          прототип: эталон логики и тестовый стенд
│   ├── go.work                    объединяет модули
│   ├── core/                      ядро, ничего не знает о UI и платформах
│   │   ├── go.mod
│   │   ├── bridges/               разбор строк мостов
│   │   ├── torconf/               генерация torrc
│   │   ├── control/               control-протокол, события bootstrap
│   │   ├── ptrun/                 запуск pluggable transports
│   │   ├── torrun/                интерфейс Runtime + реализации
│   │   │   ├── runtime.go
│   │   │   ├── external/          отдельный процесс tor (десктоп)
│   │   │   └── embedded/          go-libtor (мобильные, потом десктоп)
│   │   ├── socksproxy/            HTTP↔SOCKS5 мост
│   │   ├── service/               фасад: Connect / Disconnect / Status
│   │   └── mobileapi/             gomobile-совместимая обёртка
│   ├── cmd/torproxy/              CLI для десктопа
│   ├── desktop/                   Fyne GUI
│   ├── android/                   Kotlin, VpnService, потребляет .aar
│   ├── ios/                       SwiftUI, NEPacketTunnelProvider, .xcframework
│   └── build/                     скрипты сборки, CI, подпись
└── Release/                       ← правило 4: выпуск + PDF-мануал
```

Правило: `core` не импортирует ничего из `cmd`, `desktop`, `android`, `ios`.
Проверяется линтером на этапе 0.

### 3.1. Грабли репозитория, известные заранее

Взято из памяти проекта, повторять не надо.

- **`.gitattributes` уже написан** — без `* text=auto` файлы числятся изменёнными
  без единой правки. Собранные бинарники Go под Linux и macOS идут без расширения,
  поэтому помечены двоичными по маске имени: иначе git примет их за текст и испортит.
- **Коммиты только от `VKAndreevich <v.k.andreevich@gmail.com>`.** Не передавать
  `-c user.email` — глобальный конфиг машины уже правильный.
- **git-MCP подключён, значит работать через него** (правило 8). git из оболочки
  Claude оставляет неудаляемые `index.lock`.
- **Имя папки = путь проекта в GitLab.** Пробелов в имени быть не должно.

---

## 4. Что переносится с Python-версии

Логика уже написана и покрыта тестами — это перевод, а не проектирование.

| Python | Go | Комментарий |
|---|---|---|
| `Prototype-Python/tornode/bridges.py` | `core/bridges` | перенести вместе с тестами один в один |
| `Prototype-Python/tornode/tormgr.py` → `build_torrc` | `core/torconf` | чистая функция, переносится тривиально |
| `Prototype-Python/tornode/tormgr.py` → `TorManager` | `core/torrun/external` | часть работы возьмёт на себя `bine` |
| `Prototype-Python/tornode/control.py` | `core/control` | `bine/control` закрывает низкий уровень; свои — события bootstrap и перевод фаз |
| `Prototype-Python/tornode/httpbridge.py` | `core/socksproxy` | почти дословно, `net` вместо `socketserver` |
| `Prototype-Python/tornode/app.py` | `core/service` | фасад |
| `Prototype-Python/tests/fake_tor.py` | оставить как есть | **см. 5.2** |

---

## 5. Тестирование

### 5.1. Правило

Каждый этап заканчивается зелёными тестами. `go test ./...` без флагов не
должен требовать ни сети, ни установленного tor. Всё, что их требует, —
за тегом `//go:build integration`.

### 5.2. Заглушка tor уже есть

`Prototype-Python/tests/fake_tor.py` из прототипа изображает tor: control-порт с
cookie- и SAFECOOKIE-аутентификацией, события BOOTSTRAP от 0 до 100,
рабочий SOCKS5, имитация падения по `FAKE_TOR_FAIL`.

Go-тесты этапов 2 и 3 гоняются против неё же. Это экономит день работы и
заодно перекрёстно проверяет, что обе реализации понимают протокол
одинаково.

### 5.3. Что проверять обязательно

- Разбор письма от `bridges@torproject.org` целиком, вместе с прозой.
- DNS не резолвится локально — имя хоста уходит в SOCKS как есть.
- `Stop()` действительно убивает tor и освобождает порты.
- Отсутствие нужного транспорта диагностируется **до** запуска.
- Прогресс, застрявший на месте, отваливается по таймауту с внятным текстом.

---

## 6. Этапы

### Этап 0 — каркас

**Цель:** пустой, но собирающийся и проверяемый репозиторий.

- `go.work`, `core/go.mod` (Go 1.22+), `cmd/torproxy/go.mod`
- CI: `go vet`, `go test ./...`, `golangci-lint`, матрица windows / linux / macos
- Линтер запрещает импорт из `core` наружу
- `LICENSE`, `README.md`, `CONTRIBUTING.md`

**Готово когда:** CI зелёный на пустом проекте.

---

### Этап 1 — мосты и конфиг

**Цель:** `core/bridges` и `core/torconf`.

```go
package bridges

type Bridge struct {
    Transport   string   // "" — обычный мост
    Address     string   // host:port или [v6]:port
    Fingerprint string
    Args        []string
}

func ParseLine(line string) (Bridge, error)
func Parse(text string) (bridges []Bridge, problems []string)
func (b Bridge) TorrcLine() string
func Transports(bridges []Bridge) []string
```

Перенести таблицу транспортов и все 22 теста из
`Prototype-Python/tests/test_bridges.py`. Регулярки для адреса и отпечатка — оттуда же.

**Готово когда:** тесты этапа проходят, включая разбор письма целиком,
IPv6, дедупликацию и отсев прозы.

---

### Этап 2 — control-протокол

**Цель:** `core/control` поверх `bine/control`.

Своего здесь: подписка на `STATUS_CLIENT`, разбор `BOOTSTRAP PROGRESS=`,
таблица перевода тегов фаз на русский (взять из `tormgr.py`,
`BOOTSTRAP_TAGS`), детект застревания.

```go
type Bootstrap struct {
    Percent int
    Tag     string
    Phase   string     // человекочитаемо
}

type Client interface {
    Authenticate(ctx context.Context) error
    TakeOwnership(ctx context.Context) error
    WatchBootstrap(ctx context.Context) (<-chan Bootstrap, error)
    NewIdentity(ctx context.Context) error
    SocksAddress(ctx context.Context) (string, error)
    Close() error
}
```

**Готово когда:** тест против `fake_tor.py` проходит весь цикл —
SAFECOOKIE, события 0→100, `NEWNYM`, `GETINFO net/listeners/socks`.

---

### Этап 3 — запуск tor отдельным процессом

**Цель:** `core/torrun` (интерфейс) и `core/torrun/external`.

```go
package torrun

type Config struct {
    DataDir    string
    Bridges    []bridges.Bridge
    SocksPort  int          // 0 — выбрать свободный
    PTPlugins  []PTPlugin   // заполняется этапом 4
    ExtraTorrc string
}

type Endpoint struct {
    SocksAddress   string
    ControlAddress string
    CookiePath     string
}

type Runtime interface {
    Start(ctx context.Context, cfg Config) (Endpoint, error)
    Stop() error
}
```

Поиск `tor` по системе — перенести список путей из `tormgr.py`,
`_tor_search_paths()`. Он уже учитывает Tor Browser на трёх ОС.

**Готово когда:** интеграционный тест поднимает `fake_tor.py` через
`external.Runtime` и доходит до 100 %.

---

### Этап 4 — транспорты через IPtProxy

**Цель:** `core/ptrun` — lyrebird, snowflake и webtunnel **в процессе**,
без отдельных исполняемых файлов ни на одной платформе.

IPtProxy инстанцируется один раз через `Controller`, ссылку держать
живой. Свободные порты библиотека находит сама и возвращает — их и
подставлять в `ClientTransportPlugin ... socks5 127.0.0.1:<порт>`.

Сверить точные сигнатуры с README и CHANGELOG репозитория: API менялся
между 3.x и 4.x, наизусть не писать.

```go
type PTPlugin struct {
    Transports []string   // obfs4, meek_lite
    Address    string     // 127.0.0.1:порт, который вернул IPtProxy
}

type Runner interface {
    Start(transports []string, stateDir string) ([]PTPlugin, error)
    Stop() error
}
```

**Готово когда:** ручная проверка на реальных obfs4-мостах доходит до
100 %, и в `torrc` нет ни одного `exec`.

> После этого этапа исчезает необходимость в Tor Expert Bundle и в
> подкладывании бинарников рядом с приложением. Это главное отличие от
> Python-версии.

---

### Этап 5 — HTTP↔SOCKS мост

**Цель:** `core/socksproxy`. Прямой перенос `httpbridge.py`.

`CONNECT`, обычный HTTP с телом, чистка hop-by-hop заголовков, передача
имени хоста в SOCKS без локального резолвинга.

**Готово когда:** тесты `Prototype-Python/tests/test_integration.py` (`test_http_proxy_over_tor`,
`test_connect_tunnel_over_tor`, `test_dns_is_not_resolved_locally`)
переписаны на Go и проходят.

---

### Этап 6 — фасад и CLI ← первая поставка

**Цель:** `core/service` и `cmd/torproxy`. Работающий бинарник.

```go
type Observer interface {
    OnBootstrap(percent int, phase string)
    OnLog(line string)
    OnState(state string)   // idle | connecting | connected | failed
}

type Service struct{ /* ... */ }

func (s *Service) Connect(ctx context.Context, bridgesText string, obs Observer) error
func (s *Service) Disconnect() error
func (s *Service) NewIdentity(ctx context.Context) error
func (s *Service) Endpoints() (socks, http string)
func (s *Service) CheckExitIP(ctx context.Context) (ip string, isTor bool, err error)
```

`Observer` уже спроектирован под ограничение 1.2 — на этапе 9 он
переезжает через границу gomobile без изменений.

CLI повторяет ключи Python-версии (`--bridges-file`, `--no-bridges`,
`--http-port`, `--socks-port`, `--check`, `--print-torrc`).

**Готово когда:** `go build` даёт рабочие бинарники под Windows x64, Linux amd64,
macOS amd64 и macOS arm64, и они подключаются через реальные мосты.

---

### Этап 7 — десктопный GUI ← можно раздавать

**Цель:** `desktop/` на Fyne. Одно окно, повторяет `Prototype-Python/tornode/gui.py`:
поле для вставки мостов, счётчик распознанных, полоса прогресса, адреса
прокси, кнопки «Новая цепочка» и «Проверить IP», журнал.

Плюс к Python-версии: сворачивание в системный трей и автозапуск.

**Готово когда:** собраны `.app` для macOS (universal), `.exe` для Windows и
бинарник для Linux; все три запускаются на чистой машине без установленного Go.

Linux даётся почти даром: Fyne и go-libtor его поддерживают штатно, отдельной
работы сверх строки в матрице сборки не требуется.

---

### Этап 8 — встроенный tor

**Цель:** `core/torrun/embedded` на go-libtor. То же поведение, что у
`external`, но без отдельного процесса.

Первым делом — эксперимент на полдня: собирается ли go-libtor под
Windows x64. Если форк `gen2brain` не заводится, Windows остаётся на
`external`, и это нормально: интерфейс `Runtime` ровно для этого и нужен.

**Готово когда:** те же интеграционные тесты проходят на `embedded` под
macOS arm64, и принято решение по Windows.

---

### Этап 9 — мобильный API

**Цель:** `core/mobileapi` — тонкая обёртка под ограничения 1.2.

```go
package mobileapi

// Реализуется на стороне Swift и Kotlin.
type Observer interface {
    OnBootstrap(percent int, phase string)
    OnLog(line string)
    OnState(state string)
}

type App struct{ /* ... */ }

func New(stateDir string) *App
func (a *App) Connect(bridgesText string, obs Observer) error
func (a *App) Disconnect() error
func (a *App) NewIdentity() error
func (a *App) SocksPort() int
func (a *App) StatusJSON() string     // сложные структуры — только так
func (a *App) ValidateBridgesJSON(text string) string
```

Ни срезов структур, ни map, ни `context.Context` через границу. Отмена —
через `Disconnect()`.

**Готово когда:** `gomobile bind` собирает и `.aar`, и `.xcframework` без
ошибок, а тест на Go проверяет, что `StatusJSON` разбирается обратно.

---

### Этап 10 — Android

**Цель:** приложение на Kotlin, `VpnService` + tun2socks → SOCKS-порт ядра.

Выбрать tun2socks на этом этапе, посмотрев, чем пользуется Orbot.
Минимальный UI: поле мостов, кнопка, прогресс, статус.

**Готово когда:** APK ставится на устройство, трафик всей системы идёт
через Tor, `check.torproject.org` это подтверждает.

> Android раздаётся APK напрямую, без магазина и ревью. Это самая быстрая
> из мобильных платформ, поэтому она идёт раньше iOS.

---

### Этап 11 — iOS

**Цель:** SwiftUI-приложение и Network Extension.

Порядок работ:

1. Собрать `.xcframework`, подключить к пустому приложению, проверить,
   что ядро вообще запускается на устройстве.
2. **Сразу замерить память** в Network Extension под нагрузкой. Лимит
   около 50 МБ, внутри tor, транспорт и tun2socks. Если не помещается —
   решать здесь, а не после написания UI.
3. `NEPacketTunnelProvider`, entitlement `packet-tunnel-provider`
   (выдаётся автоматически, просить Apple не нужно).
4. UI, обмен настройками между приложением и расширением через App Group.

**Готово когда:** сборка проходит на устройстве через TestFlight и
трафик идёт через Tor.

---

### Этап 12 — выпуск

- Подпись и нотаризация macOS, иначе Gatekeeper не пустит.
- Подпись Windows — иначе SmartScreen.
- Воспроизводимые сборки: одинаковый вход даёт одинаковый бинарник.
  Для инструмента обхода блокировок это не роскошь, а способ доказать,
  что в сборке нет лишнего.
- CI-матрица: windows/amd64, linux/amd64, darwin/amd64, darwin/arm64, android, ios.
- Аудит лицензий (см. 2.2).
- Политика конфиденциальности — обязательна для App Store.

---

## 7. Риски

| Риск | Вероятность | Что делаем |
|---|---|---|
| go-libtor не собирается под Windows | высокая | Windows остаётся на внешнем `tor.exe`; интерфейс `Runtime` это предусматривает |
| Не влезаем в память iOS-расширения | средняя | мерить на этапе 11 шагом 2, до написания UI |
| API IPtProxy разошёлся с документацией | средняя | сверять с CHANGELOG репозитория, не писать по памяти |
| Ревью App Store для VPN-приложения | средняя | политика конфиденциальности и внятное описание назначения заранее |
| Лицензионная несовместимость | низкая | аудит на этапе 12, но проверить go-libtor раньше |
| Fyne плохо выглядит на Windows | низкая | запасной вариант — Wails, ядро от UI не зависит |

---

## 8. Порядок работы с этим планом

1. Один этап — одна сессия. Не начинать следующий, пока тесты текущего
   не зелёные.
2. Перед этапом перечитать раздел 1. Ограничения оттуда дороже всего
   стоят, если вспомнить о них поздно.
3. Сигнатуры внешних библиотек проверять в их репозиториях. В этом плане
   они — намерение, а не цитата.
4. Первая настоящая веха — этап 6: бинарник, который работает. Всё до
   неё промежуточное, всё после — доставка на новые платформы.
