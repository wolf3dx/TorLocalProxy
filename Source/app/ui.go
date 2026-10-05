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
