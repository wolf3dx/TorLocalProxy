package main

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// колоКнопка — «Луковица»: три кольца-слоя и ядро. Само кольцо и есть
// кнопка подключения — тап запускает или останавливает. Внутри ядра
// «Tor» в покое, процент при подключении и «ВКЛ», когда готово. Цвета
// берутся из активной темы, поэтому кольцо всегда в тон палитре.
type колоКнопка struct {
	widget.BaseWidget
	состояние string // значения service.State* ("idle"/"connecting"/…)
	процент   int
	приТапе   func()
}

func новоеКоло(приТапе func()) *колоКнопка {
	к := &колоКнопка{состояние: "idle", приТапе: приТапе}
	к.ExtendBaseWidget(к)
	return к
}

// установить меняет состояние и процент и перерисовывает кольцо.
func (к *колоКнопка) установить(состояние string, процент int) {
	к.состояние = состояние
	к.процент = процент
	к.Refresh()
}

func (к *колоКнопка) Tapped(_ *fyne.PointEvent) {
	if к.приТапе != nil {
		к.приТапе()
	}
}

func (к *колоКнопка) CreateRenderer() fyne.WidgetRenderer {
	кольцо := func() *canvas.Circle { c := &canvas.Circle{}; c.StrokeWidth = 3; return c }
	r := &кольцоRenderer{
		к:     к,
		r1:    кольцо(),
		r2:    кольцо(),
		r3:    кольцо(),
		ядро:  &canvas.Circle{},
		центр: canvas.NewText("Tor", color.White),
	}
	r.центр.Alignment = fyne.TextAlignCenter
	r.центр.TextStyle = fyne.TextStyle{Bold: true}
	r.центр.TextSize = 18
	r.Refresh()
	return r
}

type кольцоRenderer struct {
	к          *колоКнопка
	r1, r2, r3 *canvas.Circle
	ядро       *canvas.Circle
	центр      *canvas.Text
}

func (r *кольцоRenderer) MinSize() fyne.Size { return fyne.NewSize(156, 156) }

func (r *кольцоRenderer) Objects() []fyne.CanvasObject {
	return []fyne.CanvasObject{r.r1, r.r2, r.r3, r.ядро, r.центр}
}

func (r *кольцоRenderer) Destroy() {}

func (r *кольцоRenderer) Layout(размер fyne.Size) {
	d := размер.Width
	if размер.Height < d {
		d = размер.Height
	}
	ox := (размер.Width - d) / 2
	oy := (размер.Height - d) / 2
	круг := func(c *canvas.Circle, inset float32) {
		c.Move(fyne.NewPos(ox+inset, oy+inset))
		c.Resize(fyne.NewSize(d-2*inset, d-2*inset))
	}
	круг(r.r1, 0)
	круг(r.r2, d*0.11)
	круг(r.r3, d*0.22)
	круг(r.ядро, d*0.32)

	h := r.центр.MinSize().Height
	r.центр.Resize(fyne.NewSize(размер.Width, h))
	r.центр.Move(fyne.NewPos(0, (размер.Height-h)/2))
}

func (r *кольцоRenderer) Refresh() {
	тема := fyne.CurrentApp().Settings().Theme()
	вариант := fyne.CurrentApp().Settings().ThemeVariant()
	акцент := тема.Color(theme.ColorNamePrimary, вариант)
	наАкценте := тема.Color(theme.ColorNameForegroundOnPrimary, вариант)
	активно := r.к.состояние == "connected" || r.к.состояние == "connecting"

	// Кольца: ярче по мере приближения к центру; в покое — приглушённые.
	for i, кольцо := range []*canvas.Circle{r.r1, r.r2, r.r3} {
		кольцо.FillColor = color.Transparent
		if активно {
			кольцо.StrokeColor = прозр(акцент, uint8(0x55+i*0x50))
		} else {
			кольцо.StrokeColor = прозр(акцент, uint8(0x22+i*0x18))
		}
	}

	if активно {
		r.ядро.FillColor = акцент
		r.ядро.StrokeWidth = 0
		r.ядро.StrokeColor = color.Transparent
		r.центр.Color = наАкценте
	} else {
		r.ядро.FillColor = color.Transparent
		r.ядро.StrokeWidth = 3
		r.ядро.StrokeColor = акцент
		r.центр.Color = акцент
	}

	switch r.к.состояние {
	case "connecting":
		r.центр.Text = fmt.Sprintf("%d%%", r.к.процент)
	case "connected":
		r.центр.Text = "ВКЛ"
	default:
		r.центр.Text = "Tor"
	}

	canvas.Refresh(r.к)
}
