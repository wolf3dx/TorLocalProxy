// Package torconf собирает содержимое torrc.
//
// Чистая функция: ничего не запускает, никуда не пишет, в файловую
// систему не заглядывает. Всё, что ей нужно, приходит в Config.
//
// Главное отличие от прототипа на Python — форма строки транспорта.
// Там было «ClientTransportPlugin obfs4 exec C:\...\lyrebird.exe»:
// tor сам запускал отдельный процесс. Здесь транспорты живут в нашем же
// процессе (IPtProxy), уже слушают свой порт, и tor только подключается
// к ним по socks5. Отсюда два следствия, ради которых всё и затевалось:
// на iOS, где дочерние процессы запрещены, это единственная работающая
// форма (ЛОГИКА.md, раздел 2.1), и рядом с приложением больше не нужно
// класть посторонние исполняемые файлы. Заодно отпала возня с короткими
// путями 8.3 на Windows: tor резал строку exec по пробелам, и кавычки в
// ней не работали.
package torconf

import (
	"fmt"
	"slices"
	"strings"

	"gitlab.com/vkandreevich/torlocalproxy/core/bridges"
)

// Plugin — один запущенный pluggable transport: какие транспорты он
// обслуживает и на каком адресе слушает. Адрес выдаёт IPtProxy, порты
// он выбирает сам, поэтому здесь они не фиксированы.
type Plugin struct {
	Transports []string // obfs4, meek_lite
	Address    string   // 127.0.0.1:порт
}

// Config — всё, из чего собирается torrc.
type Config struct {
	DataDir     string           // каталог состояния tor
	LogFile     string           // файл журнала tor
	ControlFile string           // куда tor запишет адрес control-порта
	SocksHost   string           // пусто — 127.0.0.1
	SocksPort   int              // 0 — «auto», порт выберет сам tor
	Bridges     []bridges.Bridge // пусто — подключение без мостов
	Plugins     []Plugin         // транспорты, уже поднятые ptrun
	OwningPID   int              // 0 — строка не пишется, см. ниже
	Extra       string           // дополнительные строки от пользователя
}

// Build возвращает содержимое torrc целиком, с завершающим переводом
// строки.
func Build(cfg Config) string {
	хост := cfg.SocksHost
	if хост == "" {
		хост = "127.0.0.1"
	}

	строки := []string{
		"# Сгенерировано TorLocalProxy — правки будут перезаписаны",
		"DataDirectory " + вКавычках(cfg.DataDir),
		// Путь журнала — без кавычек, в отличие от остальных. Параметр Log
		// разбирается как «уровень способ остаток-строки», и остаток
		// берётся сырым: кавычки попадают в имя файла, tor пытается
		// открыть «"C:/...»" и падает с «Invalid argument» — причём
		// целиком, ещё до сети. Проверено на tor 0.4.9.11: без кавычек
		// тот же путь открывается, с кавычками — нет.
		//
		// Отсюда требование к пути журнала: без пробелов. На Windows его
		// обеспечивает короткое имя 8.3, которое подставляет torrun.
		"Log notice file " + безКавычек(cfg.LogFile),
		"ControlPort auto",
		"ControlPortWriteToFile " + вКавычках(cfg.ControlFile),
		"CookieAuthentication 1",
		"ClientOnly 1",
		"AvoidDiskWrites 1",
	}

	// Своим процессом tor владеет только тогда, когда он и правда
	// отдельный процесс. У встроенной сборки (go-libtor) его нет, и
	// строка была бы ложью: tor завершил бы себя по исчезновению
	// «владельца», которого никогда не существовало.
	if cfg.OwningPID != 0 {
		строки = append(строки, fmt.Sprintf("__OwningControllerProcess %d", cfg.OwningPID))
	}

	if cfg.SocksPort == 0 {
		строки = append(строки, "SocksPort auto")
	} else {
		строки = append(строки, fmt.Sprintf("SocksPort %s:%d", хост, cfg.SocksPort))
	}

	if len(cfg.Bridges) > 0 {
		строки = append(строки, "", "UseBridges 1")
		for _, плагин := range cfg.Plugins {
			транспорты := append([]string(nil), плагин.Transports...)
			slices.Sort(транспорты)
			строки = append(строки, "ClientTransportPlugin "+
				strings.Join(транспорты, ",")+" socks5 "+плагин.Address)
		}
		for _, мост := range cfg.Bridges {
			строки = append(строки, мост.TorrcLine())
		}
	}

	if strings.TrimSpace(cfg.Extra) != "" {
		строки = append(строки, "", "# Дополнительные параметры пользователя",
			strings.TrimSpace(cfg.Extra))
	}

	return strings.Join(строки, "\n") + "\n"
}

// вКавычках оформляет путь для torrc.
//
// Обратные слэши заменяются на прямые, а не экранируются. Причина
// выяснена на живом tor 0.4.9.11 под Windows: значение параметра Log
// разбирается вложенно — «notice file "<путь>"», — и экранированные
// слэши до внутреннего разбора доходят как есть. Путь становится
// негодным, tor отказывается стартовать с «Invalid argument», и
// подключение обрывается ещё до сети. Прямые слэши Windows понимает
// во всех параметрах, и вложенный разбор их переживает.
//
// Кавычки внутри значения всё же экранируются: без этого путь с
// кавычкой разорвал бы строку.
func вКавычках(значение string) string {
	return `"` + strings.ReplaceAll(безКавычек(значение), `"`, `\"`) + `"`
}

// безКавычек приводит путь к прямым слэшам, но кавычками не оборачивает.
// Нужно для параметра Log, который кавычки не понимает (см. Build).
func безКавычек(значение string) string {
	return strings.ReplaceAll(значение, `\`, "/")
}
