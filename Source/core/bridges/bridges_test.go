package bridges

import (
	"slices"
	"strings"
	"testing"
)

// Отпечаток из тестов прототипа — 40 hex, ровно как в настоящих мостах.
const отп = "0123456789ABCDEF0123456789ABCDEF01234567"

// ---------------------------------------------------------- одна строка --

func TestObfs4(t *testing.T) {
	b, err := ParseLine("obfs4 192.0.2.1:9443 " + отп + " cert=hK8sABC+/= iat-mode=0")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if b.Transport != "obfs4" {
		t.Errorf("транспорт = %q, ожидался obfs4", b.Transport)
	}
	if b.Address != "192.0.2.1:9443" {
		t.Errorf("адрес = %q", b.Address)
	}
	if b.Fingerprint != отп {
		t.Errorf("отпечаток = %q", b.Fingerprint)
	}
	if !slices.Equal(b.Args, []string{"cert=hK8sABC+/=", "iat-mode=0"}) {
		t.Errorf("аргументы = %q", b.Args)
	}
	if b.Host() != "192.0.2.1" || b.Port() != 9443 {
		t.Errorf("host:port = %q:%d", b.Host(), b.Port())
	}
}

func TestIPv6Webtunnel(t *testing.T) {
	b, err := ParseLine("webtunnel [2001:db8::1]:443 " + отп + " url=https://a.example/x ver=0.0.1")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if b.Transport != "webtunnel" {
		t.Errorf("транспорт = %q", b.Transport)
	}
	if b.Address != "[2001:db8::1]:443" {
		t.Errorf("адрес = %q", b.Address)
	}
	// Скобки — часть записи адреса, а не самого хоста.
	if b.Host() != "2001:db8::1" {
		t.Errorf("host = %q, ожидался адрес без скобок", b.Host())
	}
	if b.Port() != 443 {
		t.Errorf("port = %d", b.Port())
	}
}

func TestОбычныйМостБезТранспорта(t *testing.T) {
	b, err := ParseLine("192.0.2.4:9001 " + отп)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if b.Transport != "" {
		t.Errorf("транспорт = %q, у обычного моста его быть не должно", b.Transport)
	}
	if b.Fingerprint != отп {
		t.Errorf("отпечаток = %q", b.Fingerprint)
	}
}

func TestПрефиксBridgeИКомментарий(t *testing.T) {
	b, err := ParseLine("Bridge obfs4 192.0.2.1:1 " + отп + " cert=x  # мой мост")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if b.Transport != "obfs4" {
		t.Errorf("транспорт = %q", b.Transport)
	}
	if !slices.Equal(b.Args, []string{"cert=x"}) {
		t.Errorf("аргументы = %q, комментарий должен был отпасть", b.Args)
	}
}

func TestТранспортВНижнийОтпечатокВВерхний(t *testing.T) {
	b, err := ParseLine("OBFS4 192.0.2.1:1 " + strings.ToLower(отп) + " cert=x")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if b.Transport != "obfs4" {
		t.Errorf("транспорт = %q, ожидался в нижнем регистре", b.Transport)
	}
	if b.Fingerprint != отп {
		t.Errorf("отпечаток = %q, ожидался в верхнем регистре", b.Fingerprint)
	}
}

func TestSnowflakeМногоАргументов(t *testing.T) {
	b, err := ParseLine("snowflake 192.0.2.3:80 " + отп + " fingerprint=" + отп +
		" url=https://s.example/ front=foo.example utls-imitate=hellorandomizedalpn")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if b.Transport != "snowflake" {
		t.Errorf("транспорт = %q", b.Transport)
	}
	if len(b.Args) != 4 {
		t.Errorf("аргументов %d, ожидалось 4: %q", len(b.Args), b.Args)
	}
}

func TestБезОтпечаткаТожеМост(t *testing.T) {
	b, err := ParseLine("meek_lite 192.0.2.2:80 url=https://a.example/")
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if b.Fingerprint != "" {
		t.Errorf("отпечаток = %q, его в строке не было", b.Fingerprint)
	}
	if !slices.Equal(b.Args, []string{"url=https://a.example/"}) {
		t.Errorf("аргументы = %q", b.Args)
	}
}

func TestСтрокиКоторыеМостамиНеЯвляются(t *testing.T) {
	случаи := []struct {
		имя    string
		строка string
	}{
		{"нет порта", "obfs4 192.0.2.1 " + отп},
		{"порт вне диапазона", "obfs4 192.0.2.1:99999 " + отп},
		{"неизвестный транспорт", "quantumfoo 192.0.2.1:443 " + отп},
		{"проза письма", "Здравствуйте, вот ваши мосты:"},
		{"лишнее слово в конце", "obfs4 192.0.2.1:443 " + отп + " мусор"},
	}
	for _, с := range случаи {
		t.Run(с.имя, func(t *testing.T) {
			if _, err := ParseLine(с.строка); err == nil {
				t.Errorf("ParseLine(%q) прошёл, а должен был отказать", с.строка)
			}
		})
	}
}

// ------------------------------------------------------- текст целиком --

// Письмо от bridges@torproject.org как есть: приветствие, мосты с
// отступами, повтор, сломанная строка и подпись.
const письмо = `
Здравствуйте!

Вот ваши мосты. Просто вставьте их в Tor Browser.

  obfs4 192.0.2.1:9443 ` + отп + ` cert=AAA iat-mode=0
  obfs4 192.0.2.2:443 ` + отп + ` cert=BBB iat-mode=0
  obfs4 192.0.2.1:9443 ` + отп + ` cert=AAA iat-mode=0

А это сломанная строка:
  obfs4 192.0.2.9:НЕПОРТ ` + отп + ` cert=CCC

С уважением,
Tor Project
`

func TestИзвлекаетИОтбрасываетДубли(t *testing.T) {
	мосты, _ := Parse(письмо)
	var адреса []string
	for _, b := range мосты {
		адреса = append(адреса, b.Address)
	}
	ожидалось := []string{"192.0.2.1:9443", "192.0.2.2:443"}
	if !slices.Equal(адреса, ожидалось) {
		t.Errorf("адреса = %q, ожидалось %q (третья строка — дубль первой)", адреса, ожидалось)
	}
}

func TestПрозаНеСчитаетсяОшибкой(t *testing.T) {
	_, проблемы := Parse(письмо)
	for _, п := range проблемы {
		if strings.Contains(п, "Здравствуйте") || strings.Contains(п, "уважением") {
			t.Errorf("проза попала в проблемы: %q", п)
		}
	}
}

func TestСломаннаяСтрокаПопадаетВПроблемы(t *testing.T) {
	_, проблемы := Parse(письмо)
	if len(проблемы) != 1 {
		t.Fatalf("проблем %d, ожидалась одна: %q", len(проблемы), проблемы)
	}
	if !strings.Contains(проблемы[0], "192.0.2.9") {
		t.Errorf("в проблеме нет адреса сломанной строки: %q", проблемы[0])
	}
}

func TestПустойВвод(t *testing.T) {
	мосты, проблемы := Parse("")
	if len(мосты) != 0 || len(проблемы) != 0 {
		t.Errorf("Parse(\"\") = %v, %v, ожидалось пусто", мосты, проблемы)
	}
}

func TestДлиннаяСтрокаОбрезаетсяПоРунам(t *testing.T) {
	// Кириллица: обрезка по байтам разрубила бы символ пополам и дала
	// бы в сообщении «кракозябру».
	длинная := "obfs4 192.0.2.1:443 " + strings.Repeat("длинныйхвост", 10)
	_, проблемы := Parse(длинная)
	if len(проблемы) != 1 {
		t.Fatalf("проблем %d, ожидалась одна", len(проблемы))
	}
	if !strings.Contains(проблемы[0], "…") {
		t.Errorf("длинная строка не обрезана: %q", проблемы[0])
	}
	if !strings.ContainsRune(проблемы[0], 'д') || strings.Contains(проблемы[0], "�") {
		t.Errorf("обрезка испортила кириллицу: %q", проблемы[0])
	}
}

func TestПорядокТранспортов(t *testing.T) {
	текст := "snowflake 192.0.2.1:80 " + отп + "\n" +
		"obfs4 192.0.2.2:443 " + отп + " cert=x\n" +
		"obfs4 192.0.2.3:443 " + отп + " cert=y\n" +
		"192.0.2.4:9001 " + отп + "\n"
	мосты, _ := Parse(текст)
	got := Transports(мосты)
	ожидалось := []string{"snowflake", "obfs4"}
	if !slices.Equal(got, ожидалось) {
		t.Errorf("Transports = %q, ожидалось %q (порядок появления, без обычного моста)", got, ожидалось)
	}
}

// ------------------------------------------------------------ обратно --

func TestСтрокаПереживаетКруг(t *testing.T) {
	исходная := "obfs4 192.0.2.1:9443 " + отп + " cert=AAA+/= iat-mode=0"
	b, err := ParseLine(исходная)
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if got := b.TorrcLine(); got != "Bridge "+исходная {
		t.Errorf("TorrcLine() = %q, ожидалось %q", got, "Bridge "+исходная)
	}
}

func TestShort(t *testing.T) {
	b, _ := ParseLine("192.0.2.4:9001 " + отп)
	if got := b.Short(); got != "vanilla 192.0.2.4:9001" {
		t.Errorf("Short() = %q, у моста без транспорта ожидалось vanilla", got)
	}
}
