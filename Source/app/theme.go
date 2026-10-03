package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// Тема приложения — тёплая «Луковица» в трёх оттянках янтаря. Это не три
// отдельные темы Fyne, а одна тема с подставляемой палитрой: выбранный
// оттенок хранится в настройках и применяется при запуске, а кнопка в
// окне переключает его на лету.

// палитра — набор цветов одного оттенка.
type палитра struct {
	ключ, имя                 string
	фон, поверхность, граница color.Color
	текст, тусклый            color.Color
	акцент, наАкценте         color.Color
	успех, ошибка             color.Color
}

func ц(hex uint32) color.Color {
	return color.NRGBA{R: uint8(hex >> 16), G: uint8(hex >> 8), B: uint8(hex), A: 0xff}
}

// прозр возвращает цвет с заданной прозрачностью (0..255).
func прозр(c color.Color, a uint8) color.Color {
	r, g, b, _ := c.RGBA()
	return color.NRGBA{R: uint8(r >> 8), G: uint8(g >> 8), B: uint8(b >> 8), A: a}
}

// темы — три оттенка в порядке переключения.
var темы = []палитра{
	{
		ключ: "night", имя: "Тёплая ночь",
		фон: ц(0x171007), поверхность: ц(0x241a10), граница: ц(0x312517),
		текст: ц(0xf3e9da), тусклый: ц(0xb7a48c),
		акцент: ц(0xedab5f), наАкценте: ц(0x2a1c08),
		успех: ц(0x86c9a0), ошибка: ц(0xe07a5f),
	},
	{
		ключ: "day", имя: "Янтарный день",
		фон: ц(0xf8efe1), поверхность: ц(0xfffdf9), граница: ц(0xeadcc8),
		текст: ц(0x2d241b), тусклый: ц(0x8a745a),
		акцент: ц(0xe3922f), наАкценте: ц(0x2a1c08),
		успех: ц(0x3f9d6d), ошибка: ц(0xc0492f),
	},
	{
		ключ: "clay", имя: "Терракота",
		фон: ц(0x17110e), поверхность: ц(0x241c18), граница: ц(0x322620),
		текст: ц(0xefe2d7), тусклый: ц(0xb09a8c),
		акцент: ц(0xcd7b54), наАкценте: ц(0x2a160c),
		успех: ц(0x8fbf9f), ошибка: ц(0xd9775a),
	},
}

const ключТемы = "тема"

func темаПоКлючу(ключ string) палитра {
	for _, п := range темы {
		if п.ключ == ключ {
			return п
		}
	}
	return темы[0]
}

// следующаяПалитра — та, что идёт за текущей по кругу.
func следующаяПалитра(ключ string) палитра {
	for i, п := range темы {
		if п.ключ == ключ {
			return темы[(i+1)%len(темы)]
		}
	}
	return темы[0]
}

// тёплаяТема реализует fyne.Theme поверх одной палитры. Шрифты и иконки
// берём из стандартной темы — меняем только цвета и чуть скругления.
type тёплаяТема struct{ п палитра }

var _ fyne.Theme = тёплаяТема{}

func (т тёплаяТема) Color(имя fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	switch имя {
	case theme.ColorNameBackground, theme.ColorNameMenuBackground, theme.ColorNameOverlayBackground:
		return т.п.фон
	case theme.ColorNameForeground:
		return т.п.текст
	case theme.ColorNamePrimary:
		return т.п.акцент
	case theme.ColorNameForegroundOnPrimary:
		return т.п.наАкценте
	case theme.ColorNameButton, theme.ColorNameInputBackground,
		theme.ColorNameHeaderBackground, theme.ColorNameDisabledButton:
		return т.п.поверхность
	case theme.ColorNameInputBorder, theme.ColorNameSeparator:
		return т.п.граница
	case theme.ColorNamePlaceHolder, theme.ColorNameDisabled:
		return т.п.тусклый
	case theme.ColorNameHover:
		return прозр(т.п.акцент, 0x33)
	case theme.ColorNamePressed, theme.ColorNameSelection, theme.ColorNameFocus:
		return прозр(т.п.акцент, 0x55)
	case theme.ColorNameScrollBar:
		return прозр(т.п.тусклый, 0x66)
	case theme.ColorNameShadow:
		return color.NRGBA{A: 0x55}
	case theme.ColorNameSuccess:
		return т.п.успех
	case theme.ColorNameError:
		return т.п.ошибка
	default:
		return theme.DefaultTheme().Color(имя, fyne.ThemeVariant(1))
	}
}

func (т тёплаяТема) Font(с fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(с)
}
func (т тёплаяТема) Icon(и fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(и)
}

func (т тёплаяТема) Size(имя fyne.ThemeSizeName) float32 {
	// Мягче скругления — приятнее для «успокаивающего» облика.
	switch имя {
	case theme.SizeNameInputRadius, theme.SizeNameSelectionRadius:
		return 9
	default:
		return theme.DefaultTheme().Size(имя)
	}
}

// применитьТему ставит палитру по ключу и запоминает выбор.
func применитьТему(приложение fyne.App, ключ string) палитра {
	п := темаПоКлючу(ключ)
	приложение.Settings().SetTheme(тёплаяТема{п})
	приложение.Preferences().SetString(ключТемы, п.ключ)
	return п
}

// текущаяПалитра — сохранённый выбор, по умолчанию «Тёплая ночь».
func текущаяПалитра(приложение fyne.App) палитра {
	return темаПоКлючу(приложение.Preferences().StringWithFallback(ключТемы, "night"))
}
