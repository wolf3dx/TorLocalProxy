package main

import (
	"strings"
	"testing"
)

// Проверяется то, что уходит в чужие руки: строка для реестра, агент
// входа macOS и запись автозапуска Linux. Эти тексты читают системы, а
// не мы, и ошибку в них видно только при следующем входе в систему — то
// есть позже всего.

func TestКоманднаяСтрокаБеретПутьВКавычки(t *testing.T) {
	// Пробел в пути — не редкость, а правило: Program Files,
	// Applications, имя пользователя из двух слов.
	получено := команднаяСтрока(`C:\Program Files\TorLocalProxy\TorLocalProxy.exe`)
	хотим := `"C:\Program Files\TorLocalProxy\TorLocalProxy.exe" --hidden`
	if получено != хотим {
		t.Errorf("команднаяСтрока = %q, хотим %q", получено, хотим)
	}
}

func TestФлагСкрытоРазбирается(t *testing.T) {
	случаи := []struct {
		имя       string
		аргументы []string
		хотим     bool
	}{
		{"обычный запуск", []string{"TorLocalProxy"}, false},
		{"из автозапуска", []string{"TorLocalProxy", "--hidden"}, true},
		{"среди прочих", []string{"TorLocalProxy", "-x", "--hidden", "-y"}, true},
		{"похожий, но чужой", []string{"TorLocalProxy", "--hidden-extra"}, false},
	}
	for _, с := range случаи {
		t.Run(с.имя, func(t *testing.T) {
			if получено := скрытыйЗапускЗапрошен(с.аргументы); получено != с.хотим {
				t.Errorf("скрытыйЗапускЗапрошен(%v) = %v, хотим %v",
					с.аргументы, получено, с.хотим)
			}
		})
	}
}

func TestPlistСодержитПутьИФлаг(t *testing.T) {
	текст := текстPlist("/Applications/TorLocalProxy.app/Contents/MacOS/TorLocalProxy")

	// launchd запустит нас при входе только с этим ключом.
	if !strings.Contains(текст, "<key>RunAtLoad</key>") {
		t.Error("в агенте нет RunAtLoad — при входе в систему он не сработает")
	}
	for _, кусок := range []string{
		"<string>/Applications/TorLocalProxy.app/Contents/MacOS/TorLocalProxy</string>",
		"<string>--hidden</string>",
		"<string>" + ИдПриложения + "</string>",
	} {
		if !strings.Contains(текст, кусок) {
			t.Errorf("в агенте нет %q:\n%s", кусок, текст)
		}
	}
	// Путь в plist — отдельным <string>, без кавычек: launchd передаёт
	// аргументы массивом, и кавычки попали бы внутрь самого пути.
	if strings.Contains(текст, `"/Applications`) {
		t.Error("путь в plist взят в кавычки — launchd не найдёт такой файл")
	}
}

func TestPlistЭкранируетПуть(t *testing.T) {
	текст := текстPlist("/Users/я & он/TorLocalProxy")
	if strings.Contains(текст, "я & он") {
		t.Error("амперсанд не экранирован — plist не разберётся")
	}
	if !strings.Contains(текст, "я &amp; он") {
		t.Errorf("ожидали &amp; в пути:\n%s", текст)
	}
}

func TestDesktopEntryЗапускаетСФлагом(t *testing.T) {
	текст := текстDesktopEntry("/home/dm/TorLocalProxy/TorLocalProxy")

	if !strings.HasPrefix(текст, "[Desktop Entry]\n") {
		t.Error("запись не начинается с [Desktop Entry] — оболочка её пропустит")
	}
	for _, кусок := range []string{
		`Exec="/home/dm/TorLocalProxy/TorLocalProxy" --hidden`,
		"Type=Application",
		"X-GNOME-Autostart-enabled=true",
	} {
		if !strings.Contains(текст, кусок) {
			t.Errorf("в записи нет %q:\n%s", кусок, текст)
		}
	}
}
