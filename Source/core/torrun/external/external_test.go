package external

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
)

// ------------------------------------------------------------- поиск tor --

func TestСписокПутейНеПуст(t *testing.T) {
	пути := SearchPaths()
	if len(пути) < 4 {
		t.Fatalf("путей всего %d — список поиска подозрительно короткий", len(пути))
	}
	for _, путь := range пути {
		if путь == "" {
			t.Error("в списке поиска пустой путь")
		}
	}
}

// Tor Browser — самый вероятный источник tor у обычного пользователя,
// поэтому его каталог обязан быть в списке на каждой из трёх систем.
func TestВСпискеЕстьTorBrowser(t *testing.T) {
	все := strings.Join(SearchPaths(), "\n")
	if !strings.Contains(все, "Tor Browser") && !strings.Contains(все, "tor-browser") {
		t.Errorf("Tor Browser не ищется на %s:\n%s", runtime.GOOS, все)
	}
}

func TestTOR_BINARYИдётПервым(t *testing.T) {
	t.Setenv("TOR_BINARY", филеПоддельныйTor(t))
	пути := SearchPaths()
	if len(пути) == 0 || пути[0] != os.Getenv("TOR_BINARY") {
		t.Errorf("первым путём должен быть TOR_BINARY, а не %q", пути[0])
	}
}

func TestFindTorБерётПодсказку(t *testing.T) {
	поддельный := филеПоддельныйTor(t)

	найденный, err := FindTor(поддельный)
	if err != nil {
		t.Fatalf("FindTor с подсказкой: %v", err)
	}
	if !strings.EqualFold(найденный, поддельный) {
		абсолютный, _ := filepath.Abs(поддельный)
		if !strings.EqualFold(найденный, абсолютный) {
			t.Errorf("найден %q, ожидался %q", найденный, поддельный)
		}
	}
}

// Отказ проверяется на пустом списке путей: на машине разработчика tor
// обычно установлен, и настоящий список нашёл бы его.
func TestОтказГоворитЧтоДелать(t *testing.T) {
	несуществующий := filepath.Join(t.TempDir(), "тоже-нет")

	_, err := найтиСреди(несуществующий, nil)
	if !errors.Is(err, ErrTorNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrTorNotFound", err)
	}
	if !strings.Contains(err.Error(), "тоже-нет") {
		t.Errorf("в ошибке нет указанного пути: %v", err)
	}

	_, err = найтиСреди("", nil)
	if !errors.Is(err, ErrTorNotFound) {
		t.Fatalf("ошибка = %v, ожидалась ErrTorNotFound", err)
	}
	if !strings.Contains(err.Error(), "TOR_BINARY") {
		t.Errorf("ошибка не подсказывает выход: %v", err)
	}
}

func TestКаталогНеСчитаетсяИсполняемым(t *testing.T) {
	if исполняемый(t.TempDir()) {
		t.Error("каталог принят за исполняемый файл")
	}
}

// филеПоддельныйTor кладёт файл, который на этой системе сойдёт за
// исполняемый: на Windows решает расширение, на остальных — права.
func филеПоддельныйTor(t *testing.T) string {
	t.Helper()
	имя := "tor"
	if runtime.GOOS == "windows" {
		имя = "tor.exe"
	}
	путь := filepath.Join(t.TempDir(), имя)
	if err := os.WriteFile(путь, []byte("не настоящий tor"), 0o755); err != nil {
		t.Fatalf("не создался поддельный tor: %v", err)
	}
	return путь
}

// ------------------------------------------------------------- запуск --

func TestБезКаталогаСостоянияНеЗапускается(t *testing.T) {
	среда := New(Options{Binary: филеПоддельныйTor(t)})
	_, err := среда.Start(context.Background(), torrun.Config{})
	if err == nil {
		t.Fatal("ожидалась ошибка про каталог состояния")
	}
	if !strings.Contains(err.Error(), "каталог") {
		t.Errorf("ошибка = %v, ожидалось упоминание каталога", err)
	}
}

func TestЗанятыйПортОтклоняетсяДоЗапуска(t *testing.T) {
	слушатель, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("не занялся порт: %v", err)
	}
	defer func() { _ = слушатель.Close() }()
	порт := слушатель.Addr().(*net.TCPAddr).Port

	среда := New(Options{Binary: филеПоддельныйTor(t)})
	_, err = среда.Start(context.Background(), torrun.Config{
		DataDir:   t.TempDir(),
		SocksPort: порт,
	})
	if err == nil {
		t.Fatal("занятый порт должен был остановить запуск")
	}
	if !strings.Contains(err.Error(), "занят") {
		t.Errorf("ошибка = %v, ожидалось «порт занят»", err)
	}
}

// Stop зовут в том числе из обработчиков ошибок, где неизвестно, успел
// ли Start что-нибудь сделать.
func TestStopБезопасенБезЗапуска(t *testing.T) {
	среда := New(Options{})
	if err := среда.Stop(); err != nil {
		t.Errorf("Stop до Start вернул ошибку: %v", err)
	}
	if err := среда.Stop(); err != nil {
		t.Errorf("повторный Stop вернул ошибку: %v", err)
	}
}

func TestКаталогСостоянияСоздаётсяСПравами(t *testing.T) {
	каталог := filepath.Join(t.TempDir(), "вложенный", "состояние")
	if err := подготовитьКаталог(каталог); err != nil {
		t.Fatalf("подготовитьКаталог: %v", err)
	}

	сведения, err := os.Stat(каталог)
	if err != nil {
		t.Fatalf("каталог не создан: %v", err)
	}
	if !сведения.IsDir() {
		t.Fatal("создан файл вместо каталога")
	}
	// tor отказывается работать, если права на DataDirectory шире 0700.
	if runtime.GOOS != "windows" && сведения.Mode().Perm() != 0o700 {
		t.Errorf("права %o, ожидались 0700", сведения.Mode().Perm())
	}
}

func TestРазборАдресаControlПорта(t *testing.T) {
	каталог := t.TempDir()
	файл := filepath.Join(каталог, "control_port")

	if _, err := прочитатьАдрес(файл); err == nil {
		t.Error("несуществующий файл не должен разбираться")
	}

	if err := os.WriteFile(файл, []byte("PORT=127.0.0.1:9051\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	адрес, err := прочитатьАдрес(файл)
	if err != nil {
		t.Fatalf("прочитатьАдрес: %v", err)
	}
	if адрес != "127.0.0.1:9051" {
		t.Errorf("адрес = %q", адрес)
	}

	if err := os.WriteFile(файл, []byte("что-то не то\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := прочитатьАдрес(файл); err == nil {
		t.Error("строка без PORT= не должна разбираться")
	}
}

func TestХвостЖурналаПопадаетВОшибку(t *testing.T) {
	if хвостЖурнала(nil) != "" {
		t.Error("пустой журнал не должен ничего добавлять к ошибке")
	}
	текст := хвостЖурнала([]string{"первая", "вторая"})
	if !strings.Contains(текст, "первая") || !strings.Contains(текст, "вторая") {
		t.Errorf("строки журнала потерялись: %q", текст)
	}
}

func TestКольцоХранитПоследние(t *testing.T) {
	var к кольцо
	for i := range строкЖурнала + 5 {
		к.добавить(strings.Repeat("x", i%3+1))
	}
	if len(к.строки()) != строкЖурнала {
		t.Errorf("в кольце %d строк, ожидалось %d", len(к.строки()), строкЖурнала)
	}
	к.очистить()
	if len(к.строки()) != 0 {
		t.Error("очистить не очистило")
	}
}
