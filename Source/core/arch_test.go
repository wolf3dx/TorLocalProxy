package core

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// Слои, которых в ядре быть не должно: ядро ничего не знает ни о UI,
// ни о способах доставки (ЛОГИКА.md, раздел 3).
var запрещённыеПрефиксы = []string{
	"gitlab.com/vkandreevich/torlocalproxy/cmd",
	"gitlab.com/vkandreevich/torlocalproxy/desktop",
	"gitlab.com/vkandreevich/torlocalproxy/android",
	"gitlab.com/vkandreevich/torlocalproxy/ios",
}

type нарушение struct {
	Файл   string
	Импорт string
}

// собратьНарушения обходит дерево .go-файлов от корня и возвращает все
// импорты запрещённых слоёв. Каталоги, начинающиеся с точки, и вложенные
// testdata не просматриваются; сам корень просматривается всегда.
func собратьНарушения(корень string) ([]нарушение, error) {
	fset := token.NewFileSet()
	var найдено []нарушение

	err := filepath.WalkDir(корень, func(путь string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if путь == корень {
				return nil
			}
			if имя := d.Name(); strings.HasPrefix(имя, ".") || имя == "testdata" {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(путь, ".go") {
			return nil
		}

		файл, err := parser.ParseFile(fset, путь, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range файл.Imports {
			путьИмпорта, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			for _, префикс := range запрещённыеПрефиксы {
				if путьИмпорта == префикс || strings.HasPrefix(путьИмпорта, префикс+"/") {
					найдено = append(найдено, нарушение{Файл: filepath.ToSlash(путь), Импорт: путьИмпорта})
				}
			}
		}
		return nil
	})
	return найдено, err
}

// TestЯдроНеИмпортируетВерхниеСлои — линтер границы модуля из этапа 0.
// Держится тестом, а не только правилом golangci-lint, чтобы нарушение
// ловилось обычным `go test ./...` на любой машине.
func TestЯдроНеИмпортируетВерхниеСлои(t *testing.T) {
	найдено, err := собратьНарушения(".")
	if err != nil {
		t.Fatalf("обход дерева ядра: %v", err)
	}
	for _, н := range найдено {
		t.Errorf("%s импортирует %s — граница из ЛОГИКА.md, раздел 3, нарушена", н.Файл, н.Импорт)
	}
}

// TestСторожЛовитНарушение проверяет сам сторож на заведомо плохом файле:
// молчащая проверка границы бесполезна, а замечают это обычно поздно.
func TestСторожЛовитНарушение(t *testing.T) {
	найдено, err := собратьНарушения(filepath.Join("testdata", "badimport"))
	if err != nil {
		t.Fatalf("обход фикстуры: %v", err)
	}
	ожидалось := "gitlab.com/vkandreevich/torlocalproxy/desktop/ui"
	if !slices.ContainsFunc(найдено, func(н нарушение) bool { return н.Импорт == ожидалось }) {
		t.Errorf("сторож не заметил импорт %s, найдено: %v", ожидалось, найдено)
	}
}
