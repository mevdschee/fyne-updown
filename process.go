package main

import (
	"context"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
	"github.com/shirou/gopsutil/v4/process"
)

// hosts resolves remote addresses to host names in the background. The names
// come from reverse DNS, so they may differ from the names that the process
// connected to.
type hosts struct {
	mu    sync.Mutex
	names map[netip.Addr]string
}

// name returns the host name of addr, empty while it is looked up or when
// the address has none.
func (h *hosts) name(addr netip.Addr) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	name, ok := h.names[addr]
	if !ok {
		h.names[addr] = ""
		go h.lookup(addr)
	}
	return name
}

func (h *hosts) lookup(addr netip.Addr) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	names, err := net.DefaultResolver.LookupAddr(ctx, addr.String())
	if err != nil || len(names) == 0 {
		return
	}
	h.mu.Lock()
	h.names[addr] = strings.TrimSuffix(names[0], ".")
	h.mu.Unlock()
}

// showProcess opens a window with the details of a process and the traffic
// per remote host, or brings it to the front when it is already open.
func (u *updown) showProcess(pid int, name string) {
	if w, ok := u.details[pid]; ok {
		w.Show()
		w.RequestFocus()
		return
	}
	title := name
	if pid != 0 {
		title += " (" + strconv.Itoa(pid) + ")"
	}
	w := u.app.NewWindow(title)
	remotes := newListView([]column{
		{"Host", 240, false},
		{"Address", 160, false},
		{"Port", 60, true},
		{"Protocol", 70, false},
		{"Down", 90, true},
		{"Up", 90, true},
		{"Received", 90, true},
		{"Sent", 90, true},
	}, 6, true)
	w.SetContent(container.NewBorder(processInfo(pid, name), nil, nil, nil, remotes.table))
	w.Resize(fyne.NewSize(920, 500))
	done := make(chan struct{})
	w.SetOnClosed(func() {
		close(done)
		delete(u.details, pid)
	})
	u.details[pid] = w
	go u.watchRemotes(pid, remotes, done)
	w.Show()
	activateApp()
}

// processInfo shows what is known about a process, a process that has
// exited only has its name and pid.
func processInfo(pid int, name string) fyne.CanvasObject {
	if pid == 0 {
		return widget.NewLabel("Traffic that could not be linked to a process.")
	}
	form := widget.NewForm()
	add := func(label, text string) {
		l := widget.NewLabel(text)
		l.Truncation = fyne.TextTruncateEllipsis
		l.Selectable = true
		form.Append(label, l)
	}
	add("Name", name)
	add("PID", strconv.Itoa(pid))
	p, err := process.NewProcess(int32(pid))
	if err != nil {
		add("Status", "exited")
		return form
	}
	if exe, err := p.Exe(); err == nil && exe != "" {
		add("Path", exe)
	}
	if cmdline, err := p.Cmdline(); err == nil && cmdline != "" {
		add("Command line", cmdline)
	}
	if user, err := p.Username(); err == nil && user != "" {
		add("User", user)
	}
	if ms, err := p.CreateTime(); err == nil && ms > 0 {
		add("Started", time.UnixMilli(ms).Format("2006-01-02 15:04:05"))
	}
	return form
}

// watchRemotes updates the remotes of a process every second until done is
// closed.
func (u *updown) watchRemotes(pid int, remotes *listView, done chan struct{}) {
	var r rates
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for now := time.Now(); ; now = <-ticker.C {
		select {
		case <-done:
			return
		default:
		}
		var remoteRates []rate
		if u.monitor != nil {
			remoteRates = r.update(u.monitor.Remotes(pid), now)
		}
		rows := make([]row, 0, len(remoteRates))
		for _, rt := range remoteRates {
			addr := rt.Remote.Addr()
			port := ""
			if rt.Remote.Port() != 0 {
				port = strconv.Itoa(int(rt.Remote.Port()))
			}
			rows = append(rows, row{
				text:   []string{u.hosts.name(addr), addr.String(), port, rt.Proto, formatRate(rt.down), formatRate(rt.up), formatBytes(float64(rt.Recv)), formatBytes(float64(rt.Sent))},
				values: []float64{0, 0, float64(rt.Remote.Port()), 0, rt.down, rt.up, float64(rt.Recv), float64(rt.Sent)},
			})
		}
		fyne.Do(func() { remotes.setRows(rows) })
	}
}
