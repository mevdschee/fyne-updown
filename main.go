package main

import (
	"fmt"
	"log"
	"math"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
	"github.com/mevdschee/fyne-updown/traffic"
)

const (
	// autoAdapter meters the adapter with the most traffic, summing all
	// adapters would count bridged and virtual traffic more than once
	autoAdapter = "Auto"
	// lowest full scale of the meter, so that idle traffic stays small
	minScale = 64 * 1000
)

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
	adapters  *listView
	processes *listView
	monitor   traffic.ProcessMonitor

	adapterRates rates
	processRates rates
	peakDown     float64
	peakUp       float64
	trayIcon     fyne.Resource
}

func main() {
	a := app.NewWithID("com.tqdev.fyne-updown")
	u := &updown{app: a}
	u.peakDown = max(minScale, a.Preferences().Float("peakDown"))
	u.peakUp = max(minScale, a.Preferences().Float("peakUp"))

	u.window = a.NewWindow("UpDown Meter")
	u.buildUI()
	u.window.Resize(fyne.NewSize(720, 420))
	u.window.SetCloseIntercept(u.window.Hide)

	if desk, ok := a.(desktop.App); ok {
		u.desk = desk
		desk.SetSystemTrayMenu(fyne.NewMenu("UpDown Meter",
			fyne.NewMenuItem("Show", u.show),
			fyne.NewMenuItem("Reset meter scale", u.resetScale),
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
	u.window.SetContent(container.NewBorder(top, nil, nil, nil, tabs))
}

func (u *updown) show() {
	u.window.Show()
	u.window.RequestFocus()
}

func (u *updown) resetScale() {
	u.peakDown, u.peakUp = minScale, minScale
	u.app.Preferences().SetFloat("peakDown", u.peakDown)
	u.app.Preferences().SetFloat("peakUp", u.peakUp)
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
			u.showAdapters(adapterRates)
			u.showProcesses(processRates)
		})
	}
}

func (u *updown) showAdapters(adapterRates []rate) {
	options := []string{autoAdapter}
	var metered *rate
	rows := make([]row, 0, len(adapterRates))
	for i, r := range adapterRates {
		if r.Loopback {
			continue
		}
		options = append(options, r.Name)
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
	u.adapters.setRows(rows)
	if fmt.Sprint(options) != fmt.Sprint(u.adapter.Options) {
		u.adapter.SetOptions(options)
	}
	var down, up float64
	if metered != nil {
		down, up = metered.down, metered.up
	}
	u.summary.SetText(fmt.Sprintf("Down %10s  Up %10s", formatBytes(down)+"/s", formatBytes(up)+"/s"))
	u.updateMeter(down, up)
}

// updateMeter scales the tray meter to the highest speed seen so far, which
// takes the place of the manual calibration in UpDown Meter.
func (u *updown) updateMeter(down, up float64) {
	if down > u.peakDown {
		u.peakDown = down
		u.app.Preferences().SetFloat("peakDown", down)
	}
	if up > u.peakUp {
		u.peakUp = up
		u.app.Preferences().SetFloat("peakUp", up)
	}
	icon := meterIcon(up/u.peakUp, down/u.peakDown)
	if u.desk != nil && icon != u.trayIcon {
		u.desk.SetSystemTrayIcon(icon)
		u.trayIcon = icon
	}
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
