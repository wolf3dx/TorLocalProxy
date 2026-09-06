package control

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestРазборСобытияBootstrap(t *testing.T) {
	случаи := []struct {
		имя     string
		строка  string
		годится bool
		процент int
		тег     string
		фаза    string
	}{
		{
			имя:     "событие STATUS_CLIENT",
			строка:  `NOTICE BOOTSTRAP PROGRESS=25 TAG=requesting_status SUMMARY="Asking for networkstatus consensus"`,
			годится: true, процент: 25, тег: "requesting_status", фаза: "Запрос состояния сети",
		},
		{
			имя:     "ответ GETINFO выглядит так же",
			строка:  `NOTICE BOOTSTRAP PROGRESS=0 TAG=starting SUMMARY="Starting"`,
			годится: true, процент: 0, тег: "starting", фаза: "Запуск",
		},
		{
			имя:     "сто процентов",
			строка:  `NOTICE BOOTSTRAP PROGRESS=100 TAG=done SUMMARY="Done"`,
			годится: true, процент: 100, тег: "done", фаза: "Готово — Tor подключён",
		},
		{
			имя: "незнакомый тег — берём английский SUMMARY",
			строка: `NOTICE BOOTSTRAP PROGRESS=42 TAG=quantum_foo ` +
				`SUMMARY="Something new and unheard of"`,
			годится: true, процент: 42, тег: "quantum_foo",
			фаза: "Something new and unheard of",
		},
		{
			имя:     "нет ни тега, ни SUMMARY — показываем что есть",
			строка:  `NOTICE BOOTSTRAP PROGRESS=7`,
			годится: true, процент: 7, тег: "", фаза: "",
		},
		{
			имя: "предупреждение о недоступном мосте",
			строка: `WARN BOOTSTRAP PROGRESS=5 TAG=conn_pt SUMMARY="Connecting to bridge" ` +
				`WARNING="Connection refused" REASON=CONNECTREFUSED COUNT=3`,
			годится: true, процент: 5, тег: "conn_pt", фаза: "Подключение к мосту",
		},
		{имя: "не про bootstrap", строка: `NOTICE CIRC_BW ID=1 READ=100`, годится: false},
		{имя: "без процента", строка: `NOTICE BOOTSTRAP TAG=done`, годится: false},
		{имя: "процент не число", строка: `NOTICE BOOTSTRAP PROGRESS=много TAG=done`, годится: false},
	}

	for _, с := range случаи {
		t.Run(с.имя, func(t *testing.T) {
			got, ок := ParseBootstrap(с.строка)
			if ок != с.годится {
				t.Fatalf("признак = %v, ожидался %v", ок, с.годится)
			}
			if !с.годится {
				return
			}
			if got.Percent != с.процент {
				t.Errorf("процент = %d, ожидался %d", got.Percent, с.процент)
			}
			if got.Tag != с.тег {
				t.Errorf("тег = %q, ожидался %q", got.Tag, с.тег)
			}
			if got.Phase != с.фаза {
				t.Errorf("фаза = %q, ожидалась %q", got.Phase, с.фаза)
			}
		})
	}
}

// SUMMARY приходит в кавычках и с пробелами внутри. Разбор по пробелам,
// как в bine, обрезал бы его на первом же слове — отсюда свой парсер.
func TestSUMMARYСПробеламиНеРежется(t *testing.T) {
	got, ок := ParseBootstrap(
		`NOTICE BOOTSTRAP PROGRESS=50 TAG=неизвестный SUMMARY="Loading relay descriptors" COUNT=1`)
	if !ок {
		t.Fatal("строка не разобралась")
	}
	if got.Phase != "Loading relay descriptors" {
		t.Errorf("фаза = %q, SUMMARY обрезан", got.Phase)
	}
}

func TestПереводФаз(t *testing.T) {
	if got := PhaseName("conn_done_pt", "Connected to pluggable transport"); got != "Мост ответил" {
		t.Errorf("PhaseName = %q, свой перевод должен побеждать английский SUMMARY", got)
	}
	if got := PhaseName("", ""); got != "" {
		t.Errorf("PhaseName без данных = %q, ожидалась пустая строка", got)
	}
	if got := PhaseName("новый_тег", ""); got != "новый_тег" {
		t.Errorf("PhaseName = %q, без перевода и SUMMARY остаётся тег", got)
	}
}

// ---------------------------------------------------------------- Wait --

func TestWaitДоходитДоСта(t *testing.T) {
	события := make(chan Bootstrap, 4)
	события <- Bootstrap{Percent: 10, Phase: "Мост ответил"}
	события <- Bootstrap{Percent: 100, Phase: "Готово — Tor подключён"}

	итог, err := Wait(context.Background(), события, WaitOptions{StallTimeout: time.Second})
	if err != nil {
		t.Fatalf("неожиданная ошибка: %v", err)
	}
	if итог.Percent != 100 {
		t.Errorf("итог = %d%%, ожидалось 100", итог.Percent)
	}
}

func TestWaitЗамечаетЗастревание(t *testing.T) {
	события := make(chan Bootstrap, 1)
	события <- Bootstrap{Percent: 10, Phase: "Мост ответил"}

	_, err := Wait(context.Background(), события, WaitOptions{StallTimeout: 40 * time.Millisecond})

	var застряло *StallError
	if !errors.As(err, &застряло) {
		t.Fatalf("ошибка = %v, ожидалась *StallError", err)
	}
	if застряло.Percent != 10 {
		t.Errorf("застряло на %d%%, ожидалось 10", застряло.Percent)
	}
	if застряло.Phase != "Мост ответил" {
		t.Errorf("фаза в ошибке = %q", застряло.Phase)
	}
}

// Повторные события с тем же процентом не считаются движением: иначе tor,
// бодро повторяющий «всё ещё 25 %», выглядел бы живым сколь угодно долго.
func TestПовторыНеСбрасываютТаймерЗастревания(t *testing.T) {
	события := make(chan Bootstrap)
	go func() {
		for range 20 {
			события <- Bootstrap{Percent: 25, Phase: "Запрос состояния сети"}
			time.Sleep(5 * time.Millisecond)
		}
	}()

	начало := time.Now()
	_, err := Wait(context.Background(), события, WaitOptions{StallTimeout: 40 * time.Millisecond})
	прошло := time.Since(начало)

	var застряло *StallError
	if !errors.As(err, &застряло) {
		t.Fatalf("ошибка = %v, ожидалась *StallError", err)
	}
	if прошло > 90*time.Millisecond {
		t.Errorf("ждали %s — таймер сбрасывался на повторах", прошло)
	}
}

func TestДвижениеПродлеваетОжидание(t *testing.T) {
	события := make(chan Bootstrap)
	go func() {
		for _, процент := range []int{10, 25, 50, 75, 100} {
			время := 25 * time.Millisecond
			time.Sleep(время)
			события <- Bootstrap{Percent: процент}
		}
	}()

	итог, err := Wait(context.Background(), события, WaitOptions{StallTimeout: 60 * time.Millisecond})
	if err != nil {
		t.Fatalf("ошибка при живом прогрессе: %v", err)
	}
	if итог.Percent != 100 {
		t.Errorf("итог = %d%%", итог.Percent)
	}
}

func TestWaitОбщийСрок(t *testing.T) {
	события := make(chan Bootstrap)
	go func() {
		for {
			select {
			case события <- Bootstrap{Percent: 50}:
				time.Sleep(5 * time.Millisecond)
			case <-time.After(time.Second):
				return
			}
		}
	}()

	_, err := Wait(context.Background(), события, WaitOptions{
		Timeout:      60 * time.Millisecond,
		StallTimeout: time.Hour,
	})
	if err == nil {
		t.Fatal("ожидалась ошибка по общему сроку")
	}
	var застряло *StallError
	if errors.As(err, &застряло) {
		t.Errorf("ошибка = %v, ожидалась не про застревание", err)
	}
}

func TestWaitОбрывПотока(t *testing.T) {
	события := make(chan Bootstrap, 1)
	события <- Bootstrap{Percent: 40}
	close(события)

	_, err := Wait(context.Background(), события, WaitOptions{StallTimeout: time.Second})
	if !errors.Is(err, ErrBootstrapChannelClosed) {
		t.Errorf("ошибка = %v, ожидалась ErrBootstrapChannelClosed", err)
	}
}

func TestWaitОтмена(t *testing.T) {
	ctx, отменить := context.WithCancel(context.Background())
	события := make(chan Bootstrap)
	go func() {
		time.Sleep(20 * time.Millisecond)
		отменить()
	}()

	_, err := Wait(ctx, события, WaitOptions{StallTimeout: time.Hour})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("ошибка = %v, ожидалась context.Canceled", err)
	}
}

// Место остановки довольно точно указывает на причину, и подсказка —
// единственное, что отличает внятный отказ от неподвижной полосы.
func TestПодсказкиПоПроценту(t *testing.T) {
	случаи := []struct{ процент int }{{0}, {5}, {14}, {25}, {75}, {90}, {100}}
	видели := make(map[string]bool)
	for _, с := range случаи {
		подсказка := StallHint(с.процент)
		if подсказка == "" {
			t.Errorf("на %d%% подсказки нет", с.процент)
		}
		видели[подсказка] = true
	}
	if len(видели) < 3 {
		t.Errorf("подсказок всего %d — они не различают причины", len(видели))
	}
	if StallHint(10) == StallHint(50) {
		t.Error("застревание на мосте и на сети должны объясняться по-разному")
	}
}

func TestТекстОшибкиЗастревания(t *testing.T) {
	err := &StallError{Percent: 10, Phase: "Мост ответил", After: 90 * time.Second}
	текст := err.Error()
	for _, часть := range []string{"10%", "Мост ответил", "мост"} {
		if !strings.Contains(текст, часть) {
			t.Errorf("в тексте ошибки нет %q: %s", часть, текст)
		}
	}

	безФазы := &StallError{Percent: 5}
	if !strings.Contains(безФазы.Error(), "без описания") {
		t.Errorf("без фазы текст должен это признавать: %s", безФазы.Error())
	}
}
