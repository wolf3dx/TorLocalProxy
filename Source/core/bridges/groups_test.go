package bridges_test

import (
	"strings"
	"testing"

	"gitlab.com/vkandreevich/torlocalproxy/core/bridges"
)

// Разбор по типам и порядок перебора. Порядок — не косметика: от него
// зависит, сколько человек просидит перед мёртвым obfs4, прежде чем
// приложение доберётся до webtunnel, который на его сети работает.

const (
	отпечатокА = "0123456789ABCDEF0123456789ABCDEF01234567"
	отпечатокБ = "89ABCDEF0123456789ABCDEF0123456789ABCDEF"
)

func смесь() string {
	return strings.Join([]string{
		"obfs4 10.0.0.1:443 " + отпечатокА + " cert=AAAA iat-mode=0",
		"webtunnel [2001:db8::1]:443 " + отпечатокБ + " url=https://example.org/x",
		"obfs4 10.0.0.2:9001 " + отпечатокА + " cert=BBBB iat-mode=0",
		"meek_lite 192.0.2.20:80 url=https://example.net front=example.com",
		"10.0.0.9:443 " + отпечатокА,
	}, "\n")
}

func TestГруппыПоТипам(t *testing.T) {
	годные, беды := bridges.Parse(смесь())
	if len(беды) != 0 {
		t.Fatalf("смесь не должна давать бед: %v", беды)
	}

	группы := bridges.GroupByTransport(годные, "")
	собрано := map[string]int{}
	for _, г := range группы {
		собрано[г.Transport] = len(г.Bridges)
	}
	ожидается := map[string]int{"obfs4": 2, "webtunnel": 1, "meek_lite": 1, bridges.БезТранспорта: 1}
	for имя, сколько := range ожидается {
		if собрано[имя] != сколько {
			t.Errorf("в группе %s мостов %d, ожидалось %d", имя, собрано[имя], сколько)
		}
	}
	if len(группы) != len(ожидается) {
		t.Errorf("групп %d, ожидалось %d: %v", len(группы), len(ожидается), собрано)
	}
}

func TestПорядокПеребораПоУмолчанию(t *testing.T) {
	годные, _ := bridges.Parse(смесь())
	группы := bridges.GroupByTransport(годные, "")

	var порядок []string
	for _, г := range группы {
		порядок = append(порядок, г.Transport)
	}
	ожидаемый := []string{"webtunnel", "obfs4", "meek_lite", bridges.БезТранспорта}
	if strings.Join(порядок, ",") != strings.Join(ожидаемый, ",") {
		t.Errorf("порядок перебора %v, ожидался %v", порядок, ожидаемый)
	}
}

func TestПрошлыйУдачныйТипИдётПервым(t *testing.T) {
	годные, _ := bridges.Parse(смесь())

	// meek_lite самый медленный и в обычном порядке идёт третьим. Но
	// если в прошлый раз подключились именно им, перебирать заново
	// незачем — это минуты ожидания на пустом месте.
	группы := bridges.GroupByTransport(годные, "meek_lite")
	if группы[0].Transport != "meek_lite" {
		t.Errorf("первым идёт %q, а должен прошлый удачный meek_lite", группы[0].Transport)
	}
	// Остальные не должны перемешаться.
	if группы[1].Transport != "webtunnel" {
		t.Errorf("после прошлого удачного ожидался webtunnel, получено %q", группы[1].Transport)
	}
}

func TestНезнакомыйТипНеТеряется(t *testing.T) {
	годные, _ := bridges.Parse(
		"conjure 10.0.0.1:443 " + отпечатокА + " url=https://example.org/x")
	группы := bridges.GroupByTransport(годные, "")
	if len(группы) != 1 || группы[0].Transport != "conjure" {
		t.Errorf("незнакомый транспорт должен попасть в свою группу, получено %v", группы)
	}
}

// Чистка одного типа не должна задевать ни другие типы, ни посторонний
// текст: в поле у людей лежит письмо целиком.
func TestЧисткаТипаЩадитОстальное(t *testing.T) {
	текст := "Здравствуйте! Вот ваши мосты:\n" + смесь() + "\nС уважением, BridgeDB"

	без := bridges.RemoveTransport(текст, "obfs4")

	if strings.Contains(без, "obfs4") {
		t.Errorf("мосты obfs4 остались:\n%s", без)
	}
	for _, должно := range []string{
		"Здравствуйте", "С уважением",
		"webtunnel [2001:db8::1]:443", "meek_lite 192.0.2.20:80", "10.0.0.9:443",
	} {
		if !strings.Contains(без, должно) {
			t.Errorf("после чистки потерялось %q:\n%s", должно, без)
		}
	}

	годные, _ := bridges.Parse(без)
	if len(годные) != 3 {
		t.Errorf("после чистки obfs4 должно остаться три моста, осталось %d", len(годные))
	}
}

func TestЧисткаОбычныхМостов(t *testing.T) {
	// Мосты без транспорта тоже должны чиститься — по метке vanilla,
	// потому что пустое имя в интерфейсе не назовёшь.
	без := bridges.RemoveTransport(смесь(), bridges.БезТранспорта)
	годные, _ := bridges.Parse(без)
	for _, мост := range годные {
		if мост.Transport == "" {
			t.Errorf("обычный мост остался: %s", мост.Short())
		}
	}
	if len(годные) != 4 {
		t.Errorf("должно остаться четыре моста с транспортами, осталось %d", len(годные))
	}
}

func TestЧисткаНесуществующегоТипаНичегоНеМеняет(t *testing.T) {
	было := смесь()
	стало := bridges.RemoveTransport(было, "snowflake")
	if strings.TrimSpace(было) != strings.TrimSpace(стало) {
		t.Errorf("чистка типа, которого нет, изменила текст:\n%s", стало)
	}
}

func TestПодписиПоТипам(t *testing.T) {
	годные, _ := bridges.Parse(смесь())
	подписи := strings.Join(bridges.Counts(годные), ", ")
	for _, ожидается := range []string{"webtunnel: 1", "obfs4: 2", "meek_lite: 1"} {
		if !strings.Contains(подписи, ожидается) {
			t.Errorf("в подписи нет %q: %s", ожидается, подписи)
		}
	}
}
