// Package bridges разбирает строки мостов Tor.
//
// На вход приходит то, что пользователь скопировал из письма
// bridges@torproject.org или со страницы bridges.torproject.org, —
// вместе с прозой письма. Проза молча отсеивается: жаловаться на каждое
// «С уважением, Tor Project» бессмысленно, а выковыривать строки руками
// пользователь не обязан (ЛОГИКА.md, раздел 4).
//
// Поддерживаемые формы:
//
//	obfs4 192.0.2.1:1234 <ОТПЕЧАТОК> cert=... iat-mode=0
//	webtunnel [2001:db8::1]:443 <ОТПЕЧАТОК> url=https://... ver=0.0.1
//	snowflake 192.0.2.3:80 <ОТПЕЧАТОК> fingerprint=... url=... front=...
//	meek_lite 192.0.2.2:80 <ОТПЕЧАТОК> url=... front=...
//	192.0.2.4:9001 <ОТПЕЧАТОК>                        (обычный мост)
//
// Пакет ничего не знает ни о сети, ни о файлах: только текст на входе и
// разобранные значения на выходе.
package bridges

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// KnownTransports — транспорты, которые приложение умеет поднимать.
// Строка с чем-то другим на месте транспорта считается ошибкой, а не
// прозой: пользователю важно узнать, что этот мост не заработает.
var KnownTransports = map[string]bool{
	"obfs4":        true,
	"obfs3":        true,
	"scramblesuit": true,
	"meek":         true,
	"meek_lite":    true,
	"webtunnel":    true,
	"snowflake":    true,
	"conjure":      true,
}

var (
	// Адрес: host:port либо [v6]:port. Порт ограничен пятью цифрами,
	// диапазон проверяется отдельно — так понятнее сообщение об ошибке.
	реАдрес = regexp.MustCompile(`^(?:\[[0-9A-Fa-f:.]+\]|[0-9A-Za-z.\-]+):(\d{1,5})$`)
	// Отпечаток ключа моста — ровно 40 шестнадцатеричных цифр.
	реОтпечаток = regexp.MustCompile(`^[0-9A-Fa-f]{40}$`)
	// Имя транспорта — то, что вообще может им быть по форме.
	реИмяТранспорта = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]{1,30}$`)
	// Признак «строку задумывали мостом»: где-то есть «что-то:цифры».
	// Класс символов юникодный, как в прототипе на Python, где \w
	// покрывает и кириллицу.
	реПохожеНаАдрес = regexp.MustCompile(`[\p{L}\p{N}_\]]:\d{1,5}\b`)
)

// Bridge — один разобранный мост.
type Bridge struct {
	Transport   string   // "" — обычный мост без транспорта
	Address     string   // host:port или [v6]:port
	Fingerprint string   // 40 hex в верхнем регистре; может отсутствовать
	Args        []string // оставшиеся key=value, в исходном порядке
}

// Host возвращает адрес без порта и без скобок вокруг IPv6.
func (b Bridge) Host() string {
	if strings.HasPrefix(b.Address, "[") {
		if конец := strings.Index(b.Address, "]"); конец > 0 {
			return b.Address[1:конец]
		}
	}
	if i := strings.LastIndex(b.Address, ":"); i >= 0 {
		return b.Address[:i]
	}
	return b.Address
}

// Port возвращает порт. Ноль означает, что адрес не разобран, — у моста
// из ParseLine такого не бывает.
func (b Bridge) Port() int {
	i := strings.LastIndex(b.Address, ":")
	if i < 0 {
		return 0
	}
	порт, err := strconv.Atoi(b.Address[i+1:])
	if err != nil {
		return 0
	}
	return порт
}

// TorrcLine собирает строку для torrc. Порядок полей тот же, в котором
// мост пришёл от пользователя: строка обязана пережить круг «разобрали и
// собрали обратно» без изменений.
func (b Bridge) TorrcLine() string {
	части := make([]string, 0, 3+len(b.Args))
	if b.Transport != "" {
		части = append(части, b.Transport)
	}
	части = append(части, b.Address)
	if b.Fingerprint != "" {
		части = append(части, b.Fingerprint)
	}
	части = append(части, b.Args...)
	return "Bridge " + strings.Join(части, " ")
}

// Short — короткое имя для журнала и интерфейса.
func (b Bridge) Short() string {
	метка := b.Transport
	if метка == "" {
		метка = "vanilla"
	}
	return метка + " " + b.Address
}

// ключ определяет, что считать одним и тем же мостом при дедупликации:
// транспорт и адрес. Отпечаток и аргументы в него не входят — письмо
// нередко повторяет один мост с разным cert=.
func (b Bridge) ключ() string {
	return b.Transport + "|" + strings.ToLower(b.Address)
}

// ParseLine разбирает одну строку. Возвращает ошибку, если строка мостом
// не является: вызывающий сам решает, ошибка это или проза письма.
func ParseLine(line string) (Bridge, error) {
	текст := strings.TrimSpace(line)
	if i := strings.Index(текст, "#"); i >= 0 {
		текст = strings.TrimSpace(текст[:i])
	}
	if текст == "" {
		return Bridge{}, fmt.Errorf("пустая строка")
	}

	слова := strings.Fields(текст)
	// Письмо и веб-страница иногда отдают строки с префиксом «Bridge».
	if len(слова) > 0 && strings.EqualFold(слова[0], "bridge") {
		слова = слова[1:]
	}
	if len(слова) == 0 {
		return Bridge{}, fmt.Errorf("пустая строка")
	}

	транспорт := ""
	if !реАдрес.MatchString(слова[0]) {
		кандидат := strings.ToLower(слова[0])
		if !реИмяТранспорта.MatchString(кандидат) {
			return Bridge{}, fmt.Errorf("не похоже на мост: «%s»", слова[0])
		}
		if !KnownTransports[кандидат] {
			return Bridge{}, fmt.Errorf("неизвестный транспорт «%s»", слова[0])
		}
		транспорт = кандидат
		слова = слова[1:]
	}

	if len(слова) == 0 {
		return Bridge{}, fmt.Errorf("после транспорта нет адреса")
	}

	адрес := слова[0]
	совпадение := реАдрес.FindStringSubmatch(адрес)
	if совпадение == nil {
		return Bridge{}, fmt.Errorf("некорректный адрес «%s» (нужно host:port)", адрес)
	}
	порт, err := strconv.Atoi(совпадение[1])
	if err != nil || порт < 1 || порт > 65535 {
		return Bridge{}, fmt.Errorf("порт вне диапазона в «%s»", адрес)
	}
	слова = слова[1:]

	отпечаток := ""
	if len(слова) > 0 && реОтпечаток.MatchString(слова[0]) {
		отпечаток = strings.ToUpper(слова[0])
		слова = слова[1:]
	}

	for _, слово := range слова {
		if !strings.Contains(слово, "=") {
			return Bridge{}, fmt.Errorf("лишний параметр «%s» (ожидалось key=value)", слово)
		}
	}

	мост := Bridge{Transport: транспорт, Address: адрес, Fingerprint: отпечаток}
	if len(слова) > 0 {
		мост.Args = append([]string(nil), слова...)
	}
	return мост, nil
}

// задумывалосьМостом отличает сломанный мост от прозы письма. Ошибку
// показываем только про первое: иначе на каждую строку приветствия
// сыпалась бы жалоба (ЛОГИКА.md, раздел 4).
func задумывалосьМостом(line string) bool {
	слова := strings.Fields(line)
	if len(слова) > 0 && strings.EqualFold(слова[0], "bridge") {
		слова = слова[1:]
	}
	if len(слова) == 0 {
		return false
	}
	if KnownTransports[strings.ToLower(слова[0])] {
		return true
	}
	if реАдрес.MatchString(слова[0]) {
		return true
	}
	for _, слово := range слова {
		if реОтпечаток.MatchString(слово) {
			return true
		}
	}
	return реПохожеНаАдрес.MatchString(line)
}

// Parse разбирает текст целиком.
//
// Возвращает разобранные мосты и список проблем. В проблемы попадают
// только строки, которые задумывались мостами; всё остальное молча
// пропускается. Дубли по паре «транспорт + адрес» отбрасываются, порядок
// первых вхождений сохраняется.
func Parse(text string) (bridges []Bridge, problems []string) {
	видели := make(map[string]bool)

	for _, сырая := range strings.Split(text, "\n") {
		строка := strings.TrimSpace(сырая)
		if строка == "" || strings.HasPrefix(строка, "#") {
			continue
		}
		мост, err := ParseLine(строка)
		if err != nil {
			if задумывалосьМостом(строка) {
				problems = append(problems, обрезать(строка, 70)+" — "+err.Error())
			}
			continue
		}
		if видели[мост.ключ()] {
			continue
		}
		видели[мост.ключ()] = true
		bridges = append(bridges, мост)
	}
	return bridges, problems
}

// обрезать укорачивает строку для сообщения о проблеме. Считает руны, а
// не байты: строки бывают с кириллицей, и обрезка по байтам разрубила бы
// символ пополам.
func обрезать(s string, предел int) string {
	руны := []rune(s)
	if len(руны) <= предел {
		return s
	}
	return string(руны[:предел-3]) + "…"
}

// Transports возвращает уникальные транспорты в порядке появления.
// Обычные мосты транспорта не имеют и в список не попадают.
func Transports(bridges []Bridge) []string {
	var список []string
	видели := make(map[string]bool)
	for _, мост := range bridges {
		if мост.Transport == "" || видели[мост.Transport] {
			continue
		}
		видели[мост.Transport] = true
		список = append(список, мост.Transport)
	}
	return список
}
