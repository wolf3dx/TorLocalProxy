package torconf

import (
	"strings"
	"testing"

	"gitlab.com/vkandreevich/torlocalproxy/core/bridges"
)

const отп = "0123456789ABCDEF0123456789ABCDEF01234567"

func разобрать(t *testing.T, текст string) []bridges.Bridge {
	t.Helper()
	мосты, проблемы := bridges.Parse(текст)
	if len(проблемы) != 0 {
		t.Fatalf("тестовые мосты не разобрались: %q", проблемы)
	}
	return мосты
}

func TestСМостами(t *testing.T) {
	мосты := разобрать(t, "obfs4 192.0.2.1:9443 "+отп+" cert=AAA iat-mode=0\n"+
		"snowflake 192.0.2.3:80 "+отп+" url=https://s.example/\n")

	torrc := Build(Config{
		DataDir:     "/tmp/d",
		ControlFile: "/tmp/d/cp",
		LogFile:     "/tmp/d/tor.log",
		SocksPort:   9052,
		Bridges:     мосты,
		Plugins: []Plugin{
			{Transports: []string{"obfs4"}, Address: "127.0.0.1:41000"},
			{Transports: []string{"snowflake"}, Address: "127.0.0.1:41001"},
		},
	})

	ожидаемые := []string{
		"UseBridges 1",
		"ClientTransportPlugin obfs4 socks5 127.0.0.1:41000",
		"ClientTransportPlugin snowflake socks5 127.0.0.1:41001",
		"SocksPort 127.0.0.1:9052",
		"ControlPort auto",
		"CookieAuthentication 1",
	}
	for _, строка := range ожидаемые {
		if !strings.Contains(torrc, строка) {
			t.Errorf("в torrc нет строки %q\n--- torrc ---\n%s", строка, torrc)
		}
	}
	if n := strings.Count(torrc, "Bridge "); n != 2 {
		t.Errorf("строк Bridge %d, ожидалось 2", n)
	}
}

// Главное отличие от Python-версии: транспорты уже запущены в нашем
// процессе, tor их не порождает. Ни одного exec в torrc быть не должно —
// на iOS дочерние процессы запрещены вовсе (ЛОГИКА.md, раздел 2.1).
func TestНиОдногоExec(t *testing.T) {
	torrc := Build(Config{
		DataDir:     "/d",
		ControlFile: "/c",
		LogFile:     "/l",
		SocksPort:   1,
		Bridges:     разобрать(t, "obfs4 192.0.2.1:443 "+отп+" cert=x"),
		Plugins:     []Plugin{{Transports: []string{"obfs4"}, Address: "127.0.0.1:41000"}},
	})
	if strings.Contains(torrc, "exec") {
		t.Errorf("в torrc встретился exec:\n%s", torrc)
	}
}

func TestБезМостов(t *testing.T) {
	torrc := Build(Config{DataDir: "/tmp/d", ControlFile: "/tmp/d/cp", LogFile: "/tmp/d/l"})

	if strings.Contains(torrc, "UseBridges") {
		t.Errorf("без мостов UseBridges не нужен:\n%s", torrc)
	}
	if strings.Contains(torrc, "ClientTransportPlugin") {
		t.Errorf("без мостов транспорты не нужны:\n%s", torrc)
	}
	if !strings.Contains(torrc, "SocksPort auto") {
		t.Errorf("нулевой порт означает auto:\n%s", torrc)
	}
}

func TestПутиСПробеламиВКавычках(t *testing.T) {
	torrc := Build(Config{
		DataDir:     "/home/a b/data",
		ControlFile: "/home/a b/cp",
		LogFile:     "/home/a b/l",
		SocksPort:   1234,
	})
	if !strings.Contains(torrc, `DataDirectory "/home/a b/data"`) {
		t.Errorf("путь с пробелом не в кавычках:\n%s", torrc)
	}
}

// Оба правила ниже выяснены на живом tor 0.4.9.11 под Windows, и каждое
// из них ломало запуск целиком, ещё до сети.
//
// Первое: экранированные обратные слэши тот не разворачивает, поэтому
// пути приводятся к прямым — их он понимает везде.
//
// Второе: параметр Log берёт остаток строки сырым, и кавычки попадают в
// имя файла — «Couldn't open file ...: Invalid argument». Поэтому путь
// журнала идёт без кавычек, в отличие от остальных.
func TestПутиВФормеКоторуюПонимаетTor(t *testing.T) {
	torrc := Build(Config{
		DataDir:     `C:\MY DOC\d`,
		ControlFile: `C:\MY DOC\d\control_port`,
		LogFile:     `C:\MY DOC\d\tor.log`,
	})
	if !strings.Contains(torrc, `DataDirectory "C:/MY DOC/d"`) {
		t.Errorf("путь не приведён к прямым слэшам:\n%s", torrc)
	}
	if !strings.Contains(torrc, "Log notice file C:/MY DOC/d/tor.log\n") {
		t.Errorf("путь журнала должен идти без кавычек:\n%s", torrc)
	}
	if strings.Contains(torrc, `\\`) {
		t.Errorf("в torrc остались экранированные слэши:\n%s", torrc)
	}
}

func TestТранспортыВСтрокеОтсортированы(t *testing.T) {
	torrc := Build(Config{
		DataDir:     "/d",
		ControlFile: "/c",
		LogFile:     "/l",
		SocksPort:   1,
		Bridges: разобрать(t, "meek_lite 192.0.2.2:80 "+отп+" url=https://a/\n"+
			"obfs4 192.0.2.1:443 "+отп+" cert=x\n"),
		Plugins: []Plugin{{
			Transports: []string{"obfs4", "meek_lite"},
			Address:    "127.0.0.1:41000",
		}},
	})
	if !strings.Contains(torrc, "ClientTransportPlugin meek_lite,obfs4 socks5 127.0.0.1:41000") {
		t.Errorf("транспорты в строке не отсортированы:\n%s", torrc)
	}
}

// У встроенной сборки своего процесса tor нет, и обещать ему владельца
// нельзя: tor завершил бы себя по исчезновению того, кого не было.
func TestВладелецПроцессаТолькоКогдаЗадан(t *testing.T) {
	без := Build(Config{DataDir: "/d", ControlFile: "/c", LogFile: "/l"})
	if strings.Contains(без, "__OwningControllerProcess") {
		t.Errorf("без OwningPID строки быть не должно:\n%s", без)
	}
	с := Build(Config{DataDir: "/d", ControlFile: "/c", LogFile: "/l", OwningPID: 4242})
	if !strings.Contains(с, "__OwningControllerProcess 4242") {
		t.Errorf("с OwningPID строка обязана быть:\n%s", с)
	}
}

func TestДополнительныеПараметры(t *testing.T) {
	torrc := Build(Config{
		DataDir: "/d", ControlFile: "/c", LogFile: "/l",
		Extra: "  ExitNodes {de}\n",
	})
	if !strings.Contains(torrc, "ExitNodes {de}") {
		t.Errorf("дополнительные параметры потерялись:\n%s", torrc)
	}
	if strings.Contains(torrc, "\n  ExitNodes") {
		t.Errorf("отступ пользователя не убран:\n%s", torrc)
	}
}

func TestЗавершаетсяПереводомСтроки(t *testing.T) {
	torrc := Build(Config{DataDir: "/d", ControlFile: "/c", LogFile: "/l"})
	if !strings.HasSuffix(torrc, "\n") {
		t.Errorf("torrc обязан заканчиваться переводом строки")
	}
}
