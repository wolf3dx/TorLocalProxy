package service

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gitlab.com/vkandreevich/torlocalproxy/core/control"
	"gitlab.com/vkandreevich/torlocalproxy/core/torrun"
)

// службаСПроверкой — та же заготовка, что и в service_test.go, но с
// подменённой проверкой трафика. Нужна ради тестов самой проверки: в
// общей заготовке проверка всегда успешна, иначе каждый тест
// подключения спотыкался бы о неё.
func службаСПроверкой(t *testing.T, запуск *поддельныйЗапуск, клиент *поддельныйКлиент,
	проверка func(context.Context, string) error) *Service {
	t.Helper()
	s, err := New(Options{
		StateDir:   t.TempDir(),
		Runtime:    запуск,
		Transports: &поддельныеТранспорты{},
		Dial: func(context.Context, string, control.Options) (control.Client, error) {
			return клиент, nil
		},
		Verify: проверка,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// Сто процентов без трафика — это не успех.
//
// Главный смысл всей затеи: tor рапортует «Bootstrapped 100%» и тогда,
// когда наружу не идёт ничего (справочник сети взят из кэша, а мосты
// давно отвалились). Раньше служба такому рапорту верила и объявляла
// подключение состоявшимся.
func TestСтоПроцентовБезТрафикаНеСчитаетсяПодключением(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{
		ControlAddress: "127.0.0.1:9051",
		SocksAddress:   "127.0.0.1:9050",
	}}
	клиент := &поддельныйКлиент{шаги: полныйХод()}
	s := службаСПроверкой(t, запуск, клиент, func(context.Context, string) error {
		return ErrNoTraffic
	})

	err := s.Connect(context.Background(), "", &записнойНаблюдатель{})
	if err == nil {
		t.Fatal("Connect вернул успех, хотя трафик не идёт")
	}
	if !errors.Is(err, ErrNoTraffic) {
		t.Errorf("ошибка = %v, ожидалась ErrNoTraffic", err)
	}
	if s.State() != StateFailed {
		t.Errorf("состояние = %q, ожидалось %q", s.State(), StateFailed)
	}
}

// Неудачная проверка не должна запоминаться как рабочий тип мостов:
// иначе следующее подключение снова начнёт с того, что не работает.
func TestТипМостаНеЗапоминаетсяБезТрафика(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{
		ControlAddress: "127.0.0.1:9051",
		SocksAddress:   "127.0.0.1:9050",
	}}
	клиент := &поддельныйКлиент{шаги: полныйХод()}
	s := службаСПроверкой(t, запуск, клиент, func(context.Context, string) error {
		return ErrNoTraffic
	})

	_ = s.Connect(context.Background(), "", nil)

	if тип := s.LastTransport(); тип != "" {
		t.Errorf("запомнился тип %q, хотя подключение не состоялось", тип)
	}
}

// Сторож гасит надпись, когда связь пропала, и зажигает обратно, когда
// вернулась. Чинить руками то, что чинится само, человека заставлять
// нельзя.
func TestСторожГаситИЗажигаетОбратно(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{
		ControlAddress: "127.0.0.1:9051",
		SocksAddress:   "127.0.0.1:9050",
	}}
	клиент := &поддельныйКлиент{шаги: полныйХод()}

	// Связь переключается флагом, а не подменой самой проверки: сторож
	// берёт её один раз при запуске, да и менять настройки работающей
	// службы из чужой горутины — гонка.
	var оборвана atomic.Bool
	s := службаСПроверкой(t, запуск, клиент, func(context.Context, string) error {
		if оборвана.Load() {
			return ErrNoTraffic
		}
		return nil
	})

	if err := s.Connect(context.Background(), "", nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// Штатный сторож ходит раз в полминуты — тесту столько не выждать.
	s.остановитьСторожа()

	ctx, отмена := context.WithCancel(context.Background())
	defer отмена()
	go s.сторожить(ctx, "127.0.0.1:9050", 5*time.Millisecond)

	оборвана.Store(true)
	if !дождаться(func() bool { return s.State() == StateFailed }) {
		t.Fatalf("сторож не погасил состояние, оно = %q", s.State())
	}

	оборвана.Store(false)
	if !дождаться(func() bool { return s.State() == StateConnected }) {
		t.Fatalf("сторож не вернул состояние, оно = %q", s.State())
	}
}

// Журнал должен оставаться на диске: вопрос «почему показывало, что
// работает» задают задним числом, когда журнал в памяти уже пропал
// вместе с приложением.
func TestЖурналОстаётсяНаДиске(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{
		ControlAddress: "127.0.0.1:9051",
		SocksAddress:   "127.0.0.1:9050",
	}}
	клиент := &поддельныйКлиент{шаги: полныйХод()}
	s := службаСПроверкой(t, запуск, клиент, func(context.Context, string) error { return nil })

	if err := s.Connect(context.Background(), "", nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	данные, err := os.ReadFile(filepath.Join(s.настройки.StateDir, ФайлЖурнала))
	if err != nil {
		t.Fatalf("журнал не записался: %v", err)
	}
	текст := string(данные)
	if !strings.Contains(текст, "Проверяю, идёт ли трафик") {
		t.Errorf("в журнале нет записи о проверке:\n%s", текст)
	}
	// Дата нужна целиком: журнал переживает перезапуски, и без неё
	// строки за разные дни не различить.
	if !strings.Contains(текст, time.Now().Format("2006-01-02")) {
		t.Errorf("в журнале нет даты:\n%s", текст)
	}
}

// дождаться опрашивает условие, пока оно не выполнится или не кончится
// терпение. Сторож работает в своей горутине, и ждать его обычным сном
// значило бы либо тормозить тесты, либо ловить мигания.
func дождаться(условие func() bool) bool {
	срок := time.Now().Add(2 * time.Second)
	for time.Now().Before(срок) {
		if условие() {
			return true
		}
		time.Sleep(2 * time.Millisecond)
	}
	return false
}

// Молчащий сторож неотличим от умершего, поэтому раз в несколько
// удачных проверок он всё равно оставляет отметку в журнале.
func TestСторожОставляетОтметкуЖивости(t *testing.T) {
	запуск := &поддельныйЗапуск{конец: torrun.Endpoint{
		ControlAddress: "127.0.0.1:9051",
		SocksAddress:   "127.0.0.1:9050",
	}}
	клиент := &поддельныйКлиент{шаги: полныйХод()}
	s := службаСПроверкой(t, запуск, клиент, func(context.Context, string) error { return nil })

	if err := s.Connect(context.Background(), "", nil); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	s.остановитьСторожа()

	ctx, отмена := context.WithCancel(context.Background())
	defer отмена()
	go s.сторожить(ctx, "127.0.0.1:9050", time.Millisecond)

	есть := дождаться(func() bool {
		for _, строка := range s.Log() {
			if strings.Contains(строка, "Связь в порядке") {
				return true
			}
		}
		return false
	})
	if !есть {
		t.Error("сторож не оставил ни одной отметки живости")
	}
}
