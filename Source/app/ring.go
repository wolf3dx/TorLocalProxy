package main

import (
	"fmt"
	"image/color"
	"math"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// колоКнопка — «Луковица»: кольца-слои и ядро. Кольцо и есть кнопка
// подключения: тап запускает или останавливает.
//
// Пока идёт подключение, по слоям бежит волна — свет разливается от
// ядра наружу, слои подхватывают его с задержкой. Когда подключено,
// волна остаётся, но тише: лёгкие переливы вместо бегущего света.
// Ядро собрано из нескольких кругов разного оттенка — так получается
// градиент без заливки прямоугольником, который Fyne рисует квадратом.
type колоКнопка struct {
	widget.BaseWidget
	состояние string // значения service.State* ("idle"/"connecting"/…)
	процент   int
	фаза      float32 // 0..1, двигает волну
	анимация  *fyne.Animation
	приТапе   func()
}

func новоеКоло(приТапе func()) *колоКнопка {
	к := &колоКнопка{состояние: "idle", приТапе: приТапе}
	к.ExtendBaseWidget(к)
	return к
}

// установить меняет состояние и процент, заводит или гасит волну.
func (к *колоКнопка) установить(состояние string, процент int) {
	сменилось := к.состояние != состояние
	к.состояние = состояние
	к.процент = процент
	if сменилось {
		к.перезавестиВолну()
	}
	к.Refresh()
}

// перезавестиВолну включает анимацию под текущее состояние. В покое
// волна не нужна — гасим, чтобы не жечь батарею на телефоне.
func (к *колоКнопка) перезавестиВолну() {
	if к.анимация != nil {
		к.анимация.Stop()
		к.анимация = nil
	}
	длительность := time.Duration(0)
	switch к.состояние {
	case "connecting":
		длительность = 1500 * time.Millisecond // бежит заметно
	case "connected":
		длительность = 3600 * time.Millisecond // тихие переливы
	default:
		к.фаза = 0
		return
	}
	к.анимация = fyne.NewAnimation(длительность, func(f float32) {
		к.фаза = f
		к.Refresh()
	})
	к.анимация.RepeatCount = fyne.AnimationRepeatForever
	к.анимация.Curve = fyne.AnimationLinear
	к.анимация.Start()
}

func (к *колоКнопка) Tapped(_ *fyne.PointEvent) {
	if к.приТапе != nil {
		к.приТапе()
	}
}

func (к *колоКнопка) CreateRenderer() fyne.WidgetRenderer {
	кольцо := func() *canvas.Circle { c := &canvas.Circle{}; c.StrokeWidth = 3; return c }
	r := &кольцоRenderer{
		к:        к,
		кольца:   []*canvas.Circle{кольцо(), кольцо(), кольцо()},
		свечение: &canvas.Circle{},
		слои:     []*canvas.Circle{{}, {}, {}},
		центр:    canvas.NewText("Tor", color.White),
	}
	r.центр.Alignment = fyne.TextAlignCenter
	r.центр.TextStyle = fyne.TextStyle{Bold: true}
	r.центр.TextSize = 18
	r.Refresh()
	return r
}

type кольцоRenderer struct {
	к        *колоКнопка
	кольца   []*canvas.Circle // внешние слои-обводки
	свечение *canvas.Circle   // мягкий ореол под ядром
	слои     []*canvas.Circle // ядро: от края к центру, светлеет
	центр    *canvas.Text
}

func (r *кольцоRenderer) MinSize() fyne.Size { return fyne.NewSize(156, 156) }

func (r *кольцоRenderer) Objects() []fyne.CanvasObject {
	об := make([]fyne.CanvasObject, 0, len(r.кольца)+len(r.слои)+2)
	for _, к := range r.кольца {
		об = append(об, к)
	}
	об = append(об, r.свечение)
	for _, с := range r.слои {
		об = append(об, с)
	}
	return append(об, r.центр)
}

func (r *кольцоRenderer) Destroy() {}

func (r *кольцоRenderer) Layout(размер fyne.Size) {
	d := размер.Width
	if размер.Height < d {
		d = размер.Height
	}
	ox := (размер.Width - d) / 2
	oy := (размер.Height - d) / 2
	круг := func(c *canvas.Circle, отступ float32) {
		c.Move(fyne.NewPos(ox+отступ, oy+отступ))
		c.Resize(fyne.NewSize(d-2*отступ, d-2*отступ))
	}
	for i, кольцо := range r.кольца {
		круг(кольцо, d*float32(i)*0.10)
	}
	// Ореол под ядром — мягкий свет, из-за которого ядро «горит».
	круг(r.свечение, d*0.19)
	// Ядро занимает 55% диаметра: его край почти касается внутреннего
	// кольца, и «Луковица» читается плотной, как в макете. Три вложенных
	// круга дают градиент от края к центру.
	for i, слой := range r.слои {
		круг(слой, d*(0.225+float32(i)*0.035))
	}

	h := r.центр.MinSize().Height
	r.центр.Resize(fyne.NewSize(размер.Width, h))
	r.центр.Move(fyne.NewPos(0, (размер.Height-h)/2))
}

// волна возвращает 0..1 — яркость слоя с номером i при текущей фазе.
// Сдвиг по номеру и создаёт бегущую волну.
func (r *кольцоRenderer) волна(i int) float64 {
	p := float64(r.к.фаза) - float64(i)*0.16
	return 0.5 + 0.5*math.Sin(2*math.Pi*p)
}

func (r *кольцоRenderer) Refresh() {
	тема := fyne.CurrentApp().Settings().Theme()
	вариант := fyne.CurrentApp().Settings().ThemeVariant()
	акцент := тема.Color(theme.ColorNamePrimary, вариант)
	наАкценте := тема.Color(theme.ColorNameForegroundOnPrimary, вариант)

	var основа, размах float64
	switch r.к.состояние {
	case "connecting":
		основа, размах = 0x28, 0xb0 // свет разливается заметно
	case "connected":
		основа, размах = 0x66, 0x44 // тихие переливы
	default:
		основа, размах = 0x24, 0 // покой: ровный приглушённый свет
	}
	активно := r.к.состояние == "connecting" || r.к.состояние == "connected"

	// Кольца: внешние ловят волну позже внутренних.
	for i, кольцо := range r.кольца {
		кольцо.FillColor = color.Transparent
		яркость := основа + размах*r.волна(len(r.кольца)-i)
		if !активно {
			яркость = основа + float64(i)*0x18
		}
		кольцо.StrokeColor = прозр(акцент, ограничить(яркость))
	}

	// Ореол: горит только когда активно, и дышит вместе с волной.
	r.свечение.StrokeWidth = 0
	r.свечение.StrokeColor = color.Transparent
	if активно {
		r.свечение.FillColor = прозр(акцент, ограничить(0x26+0x1a*r.волна(0)))
	} else {
		r.свечение.FillColor = color.Transparent
	}

	// Ядро: к центру светлее, и по нему тоже идёт перелив.
	for i, слой := range r.слои {
		слой.StrokeWidth = 0
		слой.StrokeColor = color.Transparent
		if активно {
			// Ближе к центру — плотнее; волна добавляет мерцание.
			альфа := 0x88 + float64(i)*0x30 + 0x20*r.волна(i)
			слой.FillColor = прозр(осветлить(акцент, float64(i)*0.16), ограничить(альфа))
		} else if i == len(r.слои)-1 {
			// В покое ядро — только контур, как в макете.
			слой.FillColor = color.Transparent
		} else {
			слой.FillColor = color.Transparent
		}
	}
	if !активно {
		// Контур ядра в покое.
		внешний := r.слои[0]
		внешний.StrokeWidth = 3
		внешний.StrokeColor = акцент
	}

	if активно {
		r.центр.Color = наАкценте
	} else {
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

// ограничить укладывает яркость в байт.
func ограничить(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// осветлить подмешивает белого — так центр ядра получается светлее края.
func осветлить(c color.Color, доля float64) color.Color {
	r, g, b, _ := c.RGBA()
	к := func(v uint32) uint8 {
		f := float64(v>>8)*(1-доля) + 255*доля
		return ограничить(f)
	}
	return color.NRGBA{R: к(r), G: к(g), B: к(b), A: 0xff}
}
