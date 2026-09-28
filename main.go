package main

import (
	"errors"
	"fmt"
	"log"
	"math"
	"runtime"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"fyne.io/systray"
	"github.com/mevdschee/fyne-updown/traffic"
)

const (
	// autoAdapter meters the adapter with the most traffic, summing all
	// adapters would count bridged and virtual traffic more than once
	autoAdapter = "Auto"
	// autoScale uses the link speed that the adapter reports
	autoScale = "Auto"
)

// scalePresets are offered in the scale dialog, other values can be typed.
var scalePresets = []string{autoScale, "10 Mbit/s", "100 Mbit/s", "1 Gbit/s", "10 Gbit/s"}

// rates turns cumulative counters into bytes per second.
type rates struct {
	prev map[string]traffic.Counter
	time time.Time
}

type rate struct {
	traffic.Counter
	down, up float64
}

func (r *rates) update(counters []traffic.Counter, now time.Time) []rate {
	seconds := now.Sub(r.time).Seconds()
	result := make([]rate, 0, len(counters))
	next := make(map[string]traffic.Counter, len(counters))
	for _, c := range counters {
		next[c.Key] = c
		rt := rate{Counter: c}
		if p, ok := r.prev[c.Key]; ok && seconds > 0 {
			// counters may be reset, for instance when an adapter reconnects
			if c.Recv >= p.Recv {
				rt.down = float64(c.Recv-p.Recv) / seconds
			}
			if c.Sent >= p.Sent {
				rt.up = float64(c.Sent-p.Sent) / seconds
			}
		}
		result = append(result, rt)
	}
	r.prev, r.time = next, now
	return result
}

type updown struct {
	app       fyne.App
	desk      desktop.App
	window    fyne.Window
	summary   *widget.Label
	adapter   *widget.Select
	status    *widget.Label
	graph     *graph
	histories map[string]*history
	adapters  *listView
	processes *listView
	monitor   traffic.ProcessMonitor

	adapterRates rates
	processRates rates
	trayIcon     fyne.Resource
	trayText     string
}

func main() {
	a := app.NewWithID("com.tqdev.fyne-updown")
	u := &updown{app: a, histories: map[string]*history{}}
	u.window = a.NewWindow("Fyne UpDown")
	u.buildUI()
	u.window.Resize(fyne.NewSize(720, 540))
	u.window.SetCloseIntercept(u.window.Hide)

	if desk, ok := a.(desktop.App); ok {
		u.desk = desk
		desk.SetSystemTrayMenu(fyne.NewMenu("Fyne UpDown",
			fyne.NewMenuItem("Show", u.show),
		))
		u.trayIcon = meterIcon(0, 0)
		desk.SetSystemTrayIcon(u.trayIcon)
	}

	monitor, err := traffic.StartProcessMonitor()
	if err != nil {
		log.Println(err)
		u.status.SetText(err.Error())
		u.status.Show()
	}
	u.monitor = monitor
	a.Lifecycle().SetOnStarted(func() {
		hideFromDock()
		activateApp()
	})
	a.Lifecycle().SetOnStopped(func() {
		if u.monitor != nil {
			u.monitor.Close()
		}
	})

	go u.run()
	u.window.ShowAndRun()
}

func (u *updown) buildUI() {
	u.summary = widget.NewLabel("")
	u.summary.TextStyle.Monospace = true
	u.adapter = widget.NewSelect([]string{autoAdapter}, func(name string) {
		u.app.Preferences().SetString("meterAdapter", name)
	})
	selected := u.app.Preferences().StringWithFallback("meterAdapter", autoAdapter)
	if selected != autoAdapter {
		u.adapter.Options = append(u.adapter.Options, selected)
	}
	u.adapter.SetSelected(selected)

	u.adapters = newListView([]column{
		{"Adapter", 220, false},
		{"Down", 100, true},
		{"Up", 100, true},
		{"Received", 110, true},
		{"Sent", 110, true},
	}, 0, false)
	u.adapters.onTapped = func(r row) { u.editScale(r.text[0]) }
	u.processes = newListView([]column{
		{"Process", 200, false},
		{"PID", 70, true},
		{"Down", 100, true},
		{"Up", 100, true},
		{"Received", 110, true},
		{"Sent", 110, true},
	}, 2, true)
	u.status = widget.NewLabel("")
	u.status.Wrapping = fyne.TextWrapWord
	u.status.Importance = widget.WarningImportance
	u.status.Hide()

	top := container.NewBorder(nil, nil, widget.NewLabel("Meter"), u.summary, u.adapter)
	tabs := container.NewAppTabs(
		container.NewTabItem("Adapters", u.adapters.table),
		container.NewTabItem("Processes", container.NewBorder(nil, u.status, nil, nil, u.processes.table)),
	)
	u.graph = newGraph()
	u.window.SetContent(container.NewBorder(container.NewVBox(top, u.graph), nil, nil, nil, tabs))
}

func (u *updown) show() {
	u.window.Show()
	activateApp()
	u.window.RequestFocus()
}

// scale returns the full scale of the meter in bits per second: the value
// configured for the adapter or else its link speed, zero when unknown.
func (u *updown) scale(r rate) (down, up float64) {
	down = u.app.Preferences().Float("scaleDown/" + r.Name)
	up = u.app.Preferences().Float("scaleUp/" + r.Name)
	if down == 0 {
		down = float64(r.LinkDown)
	}
	if up == 0 {
		up = float64(r.LinkUp)
	}
	return down, up
}

// editScale lets the user override the link speed of an adapter, which may
// be unknown or higher than the speed of the internet connection.
func (u *updown) editScale(name string) {
	downKey, upKey := "scaleDown/"+name, "scaleUp/"+name
	down := scaleEntry(u.app.Preferences().Float(downKey))
	up := scaleEntry(u.app.Preferences().Float(upKey))
	items := []*widget.FormItem{
		widget.NewFormItem("Download", down),
		widget.NewFormItem("Upload", up),
	}
	d := dialog.NewForm("Meter scale of "+name, "Save", "Cancel", items, func(ok bool) {
		if !ok {
			return
		}
		downBits, _ := parseBits(down.Text)
		upBits, _ := parseBits(up.Text)
		u.app.Preferences().SetFloat(downKey, downBits)
		u.app.Preferences().SetFloat(upKey, upBits)
	}, u.window)
	d.Resize(fyne.NewSize(360, d.MinSize().Height))
	d.Show()
}

func scaleEntry(bits float64) *widget.SelectEntry {
	e := widget.NewSelectEntry(scalePresets)
	e.SetText(autoScale)
	if bits > 0 {
		e.SetText(formatBits(bits))
	}
	e.Validator = func(s string) error {
		_, err := parseBits(s)
		return err
	}
	return e
}

// parseBits reads a speed in bits per second like "50M", "2.5 Gbit/s" or
// "512k", Auto (or nothing) gives zero.
func parseBits(s string) (float64, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" || s == strings.ToLower(autoScale) {
		return 0, nil
	}
	for _, unit := range []string{"bit/s", "bps", "bit", "b"} {
		if t, ok := strings.CutSuffix(s, unit); ok {
			s = strings.TrimSpace(t)
			break
		}
	}
	mult := 1.0
	if i := strings.IndexAny(s, "kmg"); i >= 0 && i == len(s)-1 {
		mult = map[byte]float64{'k': 1e3, 'm': 1e6, 'g': 1e9}[s[i]]
		s = strings.TrimSpace(s[:i])
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || v <= 0 || math.IsInf(v, 0) {
		return 0, errors.New("enter a speed like 50M, 2.5G or 512k")
	}
	return v * mult, nil
}

func formatBits(bits float64) string {
	if bits < 1000 {
		return fmt.Sprintf("%g bit/s", bits)
	}
	exp := min(int(math.Log10(bits)/3), 3)
	return fmt.Sprintf("%g %cbit/s", math.Round(bits/math.Pow(1000, float64(exp))*100)/100, "kMG"[exp-1])
}

// formatScale shows why the meter stays empty when the scale is unknown.
func formatScale(bits float64) string {
	if bits == 0 {
		return "unknown"
	}
	return formatBits(bits)
}

// run samples the counters every second, like UpDown Meter does.
func (u *updown) run() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for now := range ticker.C {
		adapters, err := traffic.Adapters()
		if err != nil {
			log.Println(err)
		}
		adapterRates := u.adapterRates.update(adapters, now)
		var processRates []rate
		if u.monitor != nil {
			processRates = u.processRates.update(u.monitor.Processes(), now)
		}
		fyne.Do(func() {
			u.showAdapters(adapterRates, now)
			u.showProcesses(processRates)
		})
	}
}

func (u *updown) showAdapters(adapterRates []rate, now time.Time) {
	options := []string{autoAdapter}
	var metered *rate
	rows := make([]row, 0, len(adapterRates))
	seen := map[string]bool{}
	for i, r := range adapterRates {
		if r.Loopback {
			continue
		}
		options = append(options, r.Name)
		seen[r.Name] = true
		if u.histories[r.Name] == nil {
			u.histories[r.Name] = &history{}
		}
		u.histories[r.Name].add(now, r.down, r.up)
		switch u.adapter.Selected {
		case r.Name:
			metered = &adapterRates[i]
		case autoAdapter:
			if metered == nil || r.Recv+r.Sent > metered.Recv+metered.Sent {
				metered = &adapterRates[i]
			}
		}
		if r.Recv+r.Sent == 0 {
			continue
		}
		rows = append(rows, row{
			text:   []string{r.Name, formatRate(r.down), formatRate(r.up), formatBytes(float64(r.Recv)), formatBytes(float64(r.Sent))},
			values: []float64{0, r.down, r.up, float64(r.Recv), float64(r.Sent)},
		})
	}
	// forget adapters that are gone, like the veth adapters of containers
	for name := range u.histories {
		if !seen[name] {
			delete(u.histories, name)
		}
	}
	u.adapters.setRows(rows)
	if fmt.Sprint(options) != fmt.Sprint(u.adapter.Options) {
		u.adapter.SetOptions(options)
	}
	var down, up, scaleDown, scaleUp float64
	if metered != nil {
		down, up = metered.down, metered.up
		scaleDown, scaleUp = u.scale(*metered)
		u.graph.set(u.histories[metered.Name], scaleDown, scaleUp)
	}
	u.summary.SetText(fmt.Sprintf("Down %10s  Up %10s", formatBytes(down)+"/s", formatBytes(up)+"/s"))
	u.updateMeter(fraction(down, scaleDown), fraction(up, scaleUp))
	u.updateTrayText(down, up)
}

// fraction converts a rate in bytes per second to a part of the scale in
// bits per second, an unknown scale leaves the meter empty.
func fraction(bytes, scale float64) float64 {
	if scale <= 0 {
		return 0
	}
	return bytes * 8 / scale
}

// updateMeter draws the tray meter for the given fractions of the scale.
func (u *updown) updateMeter(down, up float64) {
	icon := meterIcon(up, down)
	if u.desk != nil && icon != u.trayIcon {
		u.desk.SetSystemTrayIcon(icon)
		u.trayIcon = icon
	}
}

// updateTrayText shows the speeds when hovering the tray icon, below the
// name and version of the app. Linux tray hosts show the title in bold above
// the tooltip, macOS would show the title next to the icon and Windows has
// none, so there the name goes in the tooltip.
func (u *updown) updateTrayText(down, up float64) {
	if u.desk == nil {
		return
	}
	meta := u.app.Metadata()
	title := meta.Name + " v" + meta.Version
	speeds := fmt.Sprintf("Down: %s\nUp: %s", formatBytes(down)+"/s", formatBytes(up)+"/s")
	text := title + "\n" + speeds
	if runtime.GOOS == "linux" {
		text = speeds
	}
	if text == u.trayText {
		return
	}
	if u.trayText == "" && runtime.GOOS == "linux" {
		systray.SetTitle(title)
	}
	u.trayText = text
	systray.SetTooltip(text)
}

func (u *updown) showProcesses(processRates []rate) {
	rows := make([]row, 0, len(processRates))
	for _, r := range processRates {
		if r.Recv+r.Sent == 0 {
			continue
		}
		pid := strconv.Itoa(r.PID)
		if r.PID == 0 {
			pid = ""
		}
		rows = append(rows, row{
			text:   []string{r.Name, pid, formatRate(r.down), formatRate(r.up), formatBytes(float64(r.Recv)), formatBytes(float64(r.Sent))},
			values: []float64{0, float64(r.PID), r.down, r.up, float64(r.Recv), float64(r.Sent)},
		})
	}
	u.processes.setRows(rows)
}

// formatRate leaves idle rows empty so that active ones stand out.
func formatRate(bytes float64) string {
	if bytes < 1 {
		return ""
	}
	return formatBytes(bytes) + "/s"
}

func formatBytes(bytes float64) string {
	if bytes < 1000 {
		return fmt.Sprintf("%.0f B", bytes)
	}
	exp := min(int(math.Log(bytes)/math.Log(1000)), 4)
	return fmt.Sprintf("%.1f %cB", bytes/math.Pow(1000, float64(exp)), "kMGT"[exp-1])
}
