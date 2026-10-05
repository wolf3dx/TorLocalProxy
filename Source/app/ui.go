package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// Мелкие части облика «Луковицы», которых нет в наборе Fyne: компактная
// карточка со скруглением, чип-«пилюля» и цветная точка состояния.
// Все три — виджеты, а не голые canvas-объекты: так Fyne сам
// перекрашивает их при смене темы.

// ---------------------------------------------------------- карточка --

// карточка — скруглённая подложка под произвольное содержимое. Заменяет
// widget.Card там, где его крупный заголовок не нужен: в макете у
// адресов мелкая подпись, а не заголовок во всю ширину.
type карточка struct {
	widget.BaseWidget
	содержимое fyne.CanvasObject
}

func новаяКарточка(содержимое fyne.CanvasObject) *карточка {
	к := &карточка{содержимое: содержимое}
	к.ExtendBaseWidget(к)
	return к
}

func (к *карточка) CreateRenderer() fyne.WidgetRenderer {
	фон := canvas.NewRectangle(color.Transparent)
	фон.CornerRadius = 14
	фон.StrokeWidth = 1
	r := &карточкаRenderer{к: к, фон: фон}
	r.Refresh()
	return r
}

type карточкаRenderer struct {
	к   *карточка
	фон *canvas.Rectangle
}

func (r *карточкаRenderer) отступ() float32 { return theme.Padding() * 2 }

func (r *карточкаRenderer) Layout(размер fyne.Size) {
	r.фон.Resize(размер)
	о := r.отступ()
	r.к.содержимое.Move(fyne.NewPos(о, о))
	r.к.содержимое.Resize(fyne.NewSize(размер.Width-2*о, размер.Height-2*о))
}

func (r *карточкаRenderer) MinSize() fyne.Size {
	м := r.к.содержимое.MinSize()
	о := r.отступ()
	return fyne.NewSize(м.Width+2*о, м.Height+2*о)
}

func (r *карточкаRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.фон, r.к.содержимое}
}

func (r *карточкаRenderer) Destroy() {}

func (r *карточкаRenderer) Refresh() {
	тема := fyne.CurrentApp().Settings().Theme()
	в := fyne.CurrentApp().Settings().ThemeVariant()
	r.фон.FillColor = тема.Color(theme.ColorNameInputBackground, в)
	r.фон.StrokeColor = тема.Color(theme.ColorNameSeparator, в)
	canvas.Refresh(r.фон)
}

// -------------------------------------------------------------- чип --

// чип — «пилюля» с подписью, по которой можно нажать. На главном экране
// это сводка по мостам, открывающая их отдельный экран.
type чип struct {
	widget.BaseWidget
	текст   string
	приТапе func()
}

func новыйЧип(текст string, приТапе func()) *чип {
	ч := &чип{текст: текст, приТапе: приТапе}
	ч.ExtendBaseWidget(ч)
	return ч
}

func (ч *чип) SetText(текст string) {
	ч.текст = текст
	ч.Refresh()
}

func (ч *чип) Tapped(_ *fyne.PointEvent) {
	if ч.приТапе != nil {
		ч.приТапе()
	}
}

func (ч *чип) CreateRenderer() fyne.WidgetRenderer {
	фон := canvas.NewRectangle(color.Transparent)
	фон.StrokeWidth = 1
	подпись := canvas.NewText(ч.текст, color.White)
	подпись.TextSize = 12
	r := &чипRenderer{ч: ч, фон: фон, подпись: подпись}
	r.Refresh()
	return r
}

type чипRenderer struct {
	ч       *чип
	фон     *canvas.Rectangle
	подпись *canvas.Text
}

func (r *чипRenderer) поля() (гор, верт float32) {
	return theme.Padding() * 3, theme.Padding() * 1.5
}

func (r *чипRenderer) Layout(размер fyne.Size) {
	r.фон.Resize(размер)
	r.фон.CornerRadius = размер.Height / 2
	гор, верт := r.поля()
	r.подпись.Move(fyne.NewPos(гор, верт))
	r.подпись.Resize(fyne.NewSize(размер.Width-2*гор, размер.Height-2*верт))
}

func (r *чипRenderer) MinSize() fyne.Size {
	м := r.подпись.MinSize()
	гор, верт := r.поля()
	return fyne.NewSize(м.Width+2*гор, м.Height+2*верт)
}

func (r *чипRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.фон, r.подпись}
}

func (r *чипRenderer) Destroy() {}

func (r *чипRenderer) Refresh() {
	тема := fyne.CurrentApp().Settings().Theme()
	в := fyne.CurrentApp().Settings().ThemeVariant()
	акцент := тема.Color(theme.ColorNamePrimary, в)
	r.фон.FillColor = прозр(акцент, 0x1f)
	r.фон.StrokeColor = прозр(акцент, 0x55)
	r.подпись.Text = r.ч.текст
	r.подпись.Color = акцент
	canvas.Refresh(r.фон)
	canvas.Refresh(r.подпись)
}

// ------------------------------------------------------------ точка --

// точка — кружок состояния рядом с подписью. Зелёный «включено» рядом с
// тёплым акцентом и делает облик спокойным: тепло плюс знакомый сигнал.
type точка struct {
	widget.BaseWidget
}

func новаяТочка() *точка {
	т := &точка{}
	т.ExtendBaseWidget(т)
	return т
}

func (т *точка) CreateRenderer() fyne.WidgetRenderer {
	кружок := &canvas.Circle{}
	r := &точкаRenderer{кружок: кружок}
	r.Refresh()
	return r
}

type точкаRenderer struct {
	кружок *canvas.Circle
}

func (r *точкаRenderer) Layout(размер fyne.Size) {
	d := float32(8)
	r.кружок.Resize(fyne.NewSize(d, d))
	r.кружок.Move(fyne.NewPos((размер.Width-d)/2, (размер.Height-d)/2))
}

func (r *точкаRenderer) MinSize() fyne.Size { return fyne.NewSize(12, 12) }

func (r *точкаRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.кружок}
}

func (r *точкаRenderer) Destroy() {}

func (r *точкаRenderer) Refresh() {
	тема := fyne.CurrentApp().Settings().Theme()
	в := fyne.CurrentApp().Settings().ThemeVariant()
	r.кружок.FillColor = тема.Color(theme.ColorNameSuccess, в)
	canvas.Refresh(r.кружок)
}

// ----------------------------------------------------- адресная строка --

// адреснаяСтрока — карточка адреса из макета: мелкая приглушённая
// подпись, под ней крупный моноширинный адрес и иконка копирования
// справа. Стандартный Label так не выглядит: у него свой размер шрифта
// и набивка, поэтому текст рисуем сами.
type адреснаяСтрока struct {
	widget.BaseWidget
	подпись        string
	значение       string
	приКопировании func()
}

func новаяАдреснаяСтрока(подпись string, приКопировании func()) *адреснаяСтрока {
	а := &адреснаяСтрока{подпись: подпись, значение: "—", приКопировании: приКопировании}
	а.ExtendBaseWidget(а)
	return а
}

// SetText меняет адрес. Имя как у Label — чтобы вызывающий код не менялся.
func (а *адреснаяСтрока) SetText(значение string) {
	а.значение = значение
	а.Refresh()
}

func (а *адреснаяСтрока) CreateRenderer() fyne.WidgetRenderer {
	фон := canvas.NewRectangle(color.Transparent)
	фон.CornerRadius = 14
	фон.StrokeWidth = 1
	метка := canvas.NewText(а.подпись, color.White)
	метка.TextSize = 11
	значение := canvas.NewText(а.значение, color.White)
	значение.TextSize = 17
	значение.TextStyle = fyne.TextStyle{Monospace: true, Bold: true}
	кнопка := widget.NewButtonWithIcon("", theme.ContentCopyIcon(), а.приКопировании)
	кнопка.Importance = widget.LowImportance
	r := &адреснаяRenderer{а: а, фон: фон, метка: метка, значение: значение, кнопка: кнопка}
	r.Refresh()
	return r
}

type адреснаяRenderer struct {
	а        *адреснаяСтрока
	фон      *canvas.Rectangle
	метка    *canvas.Text
	значение *canvas.Text
	кнопка   *widget.Button
}

func (r *адреснаяRenderer) отступ() float32 { return theme.Padding() * 2.5 }

func (r *адреснаяRenderer) Layout(размер fyne.Size) {
	о := r.отступ()
	r.фон.Resize(размер)

	кн := r.кнопка.MinSize()
	r.кнопка.Resize(кн)
	r.кнопка.Move(fyne.NewPos(размер.Width-о-кн.Width, (размер.Height-кн.Height)/2))

	r.метка.Move(fyne.NewPos(о, о))
	r.метка.Resize(fyne.NewSize(размер.Width-2*о-кн.Width, r.метка.MinSize().Height))
	r.значение.Move(fyne.NewPos(о, о+r.метка.MinSize().Height+2))
	r.значение.Resize(fyne.NewSize(размер.Width-2*о-кн.Width, r.значение.MinSize().Height))
}

func (r *адреснаяRenderer) MinSize() fyne.Size {
	о := r.отступ()
	в := r.метка.MinSize().Height + 2 + r.значение.MinSize().Height
	ш := r.значение.MinSize().Width + r.кнопка.MinSize().Width
	return fyne.NewSize(ш+2*о, в+2*о)
}

func (r *адреснаяRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.фон, r.метка, r.значение, r.кнопка}
}

func (r *адреснаяRenderer) Destroy() {}

func (r *адреснаяRenderer) Refresh() {
	тема := fyne.CurrentApp().Settings().Theme()
	в := fyne.CurrentApp().Settings().ThemeVariant()
	r.фон.FillColor = тема.Color(theme.ColorNameInputBackground, в)
	r.фон.StrokeColor = тема.Color(theme.ColorNameSeparator, в)
	r.метка.Text = r.а.подпись
	r.метка.Color = тема.Color(theme.ColorNamePlaceHolder, в)
	r.значение.Text = r.а.значение
	r.значение.Color = тема.Color(theme.ColorNameForeground, в)
	canvas.Refresh(r.фон)
	canvas.Refresh(r.метка)
	canvas.Refresh(r.значение)
}

// --------------------------------------------------- контурная кнопка --

// контурнаяКнопка — кнопка «обводкой», как «Новая цепочка» в макете:
// прозрачная внутри, тонкая рамка акцентом. У Fyne такой нет: его
// кнопки либо залиты, либо совсем без рамки.
type контурнаяКнопка struct {
	widget.BaseWidget
	текст     string
	выключена bool
	приТапе   func()
}

func новаяКонтурнаяКнопка(текст string, приТапе func()) *контурнаяКнопка {
	к := &контурнаяКнопка{текст: текст, приТапе: приТапе}
	к.ExtendBaseWidget(к)
	return к
}

func (к *контурнаяКнопка) Enable()  { к.выключена = false; к.Refresh() }
func (к *контурнаяКнопка) Disable() { к.выключена = true; к.Refresh() }

func (к *контурнаяКнопка) Tapped(_ *fyne.PointEvent) {
	if к.выключена || к.приТапе == nil {
		return
	}
	к.приТапе()
}

func (к *контурнаяКнопка) CreateRenderer() fyne.WidgetRenderer {
	фон := canvas.NewRectangle(color.Transparent)
	фон.CornerRadius = 14
	фон.StrokeWidth = 1
	подпись := canvas.NewText(к.текст, color.White)
	подпись.TextSize = 14
	подпись.TextStyle = fyne.TextStyle{Bold: true}
	подпись.Alignment = fyne.TextAlignCenter
	r := &контурнаяRenderer{к: к, фон: фон, подпись: подпись}
	r.Refresh()
	return r
}

type контурнаяRenderer struct {
	к       *контурнаяКнопка
	фон     *canvas.Rectangle
	подпись *canvas.Text
}

func (r *контурнаяRenderer) Layout(размер fyne.Size) {
	r.фон.Resize(размер)
	h := r.подпись.MinSize().Height
	r.подпись.Resize(fyne.NewSize(размер.Width, h))
	r.подпись.Move(fyne.NewPos(0, (размер.Height-h)/2))
}

func (r *контурнаяRenderer) MinSize() fyne.Size {
	м := r.подпись.MinSize()
	return fyne.NewSize(м.Width+theme.Padding()*8, м.Height+theme.Padding()*5)
}

func (r *контурнаяRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.фон, r.подпись}
}

func (r *контурнаяRenderer) Destroy() {}

func (r *контурнаяRenderer) Refresh() {
	тема := fyne.CurrentApp().Settings().Theme()
	в := fyne.CurrentApp().Settings().ThemeVariant()
	цвет := тема.Color(theme.ColorNamePrimary, в)
	if r.к.выключена {
		цвет = тема.Color(theme.ColorNameDisabled, в)
	}
	r.фон.FillColor = color.Transparent
	r.фон.StrokeColor = прозр(цвет, 0x66)
	r.подпись.Text = r.к.текст
	r.подпись.Color = цвет
	canvas.Refresh(r.фон)
	canvas.Refresh(r.подпись)
}
