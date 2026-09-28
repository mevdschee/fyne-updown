# Fyne UpDown

![screenshot1](screenshot1.png) ![screenshot2](screenshot2.png)

Network traffic monitor that lives in the system tray, written in Go using the
[Fyne](https://fyne.io/) GUI library. It runs on Windows, Linux and macOS and
is inspired by [UpDown Meter](https://github.com/ScriptFUSION/UpDown-Meter) by
ScriptFUSION, a Windows only .NET application.

### Features

The tray icon shows two animated meters, like UpDown Meter does. Unlike UpDown
Meter the icon is flat and the meters stand upright: download on the left and
upload on the right. Upload is red and download green, as uploads are
the ones to watch. They are sampled once per
second, hovering the icon shows the current speeds. The full scale of the meters is the link speed that the
adapter reports. Like in UpDown Meter it can be set per adapter, separately
for download and upload, by clicking the adapter in the Adapters list. Pick a
preset or type a speed in bits per second like 50M, 2.5G or 512k, Auto goes
back to the link speed. Wireless adapters on Linux and the adapters of a macOS
virtual machine do not report a link speed, their meters stay empty until a
scale is set.

The window (click Show in the tray menu) shows a graph of the recent traffic
of the metered adapter, like btop does: download above the axis and upload
below it. Both halves have the same height and use the download and upload
scale of the adapter. Light gray lines mark 25, 50, 75 and 100% of the scale
and every 10 seconds on the clock, the start of each minute is marked brighter
and labeled with the time. Each second takes two pixels, a wider window shows
more (up to an hour). Below the graph are two list views:

- Adapters: the current download and upload speed and the totals of each
  network adapter. The meter shows the adapter selected at the top, Auto picks
  the adapter with the most traffic.
- Processes: the current download and upload speed and the totals of each
  process since the app was started. Processes that have been idle for an
  hour are removed from the list. Traffic that can not be linked to a
  process is listed as "(unknown)".

Closing the window hides it, use Quit in the tray menu to exit. On macOS the
app is not shown in the Dock, as it lives in the menu bar.

Blocking and shaping traffic per process is planned for a next version.

### Per-process traffic

The operating systems do not offer per-process traffic counters to normal
users, so each platform has its own implementation:

- Linux: packets are captured on a raw socket and linked to processes via the
  socket tables in /proc, like nethogs does. This needs the capabilities to
  capture packets and to read the file descriptors of other processes:

      sudo setcap cap_net_raw,cap_sys_ptrace,cap_dac_read_search+ep ./fyne-updown

  Without these the Processes tab explains what to run. Note that setcap needs
  to be repeated after every build.
- Windows: the Microsoft-Windows-Kernel-Network ETW provider reports the
  process id and size of every TCP and UDP send and receive. Starting a trace
  session needs administrator rights, so run the app as administrator.
- macOS: the output of the built in `nettop` command is read, this does not
  need extra rights.

On Linux traffic of containers passes the host adapters, but it belongs to
processes in other network namespaces, so it shows up as "(unknown)".

### Building

Install Fyne dependencies:

    sudo apt install golang gcc libgl1-mesa-dev xorg-dev

Install go packages:

    go mod download

Run the application:

    go run .

Note that the first build may take several minutes (!).

### Package using fyne-cross

Install fyne-cross using:

    go install github.com/fyne-io/fyne-cross@latest

Now run the package.sh script to build all binaries. Cross compiling to macOS
needs a copy of the macOS SDK, see the "OSX build" section of the
[fyne-mines](https://github.com/mevdschee/fyne-mines) README.

### Releasing

Bump `Version` in FyneApp.toml, commit and push, then run the release.sh script.
It reads the version, tags the current commit and uploads the six binaries from
fyne-cross/dist as a GitHub release, so run package.sh first.

### Known issues

On GNOME the tray icon is only shown with the AppIndicator extension installed.

macOS has no accelerated OpenGL renderer in a virtual machine, and glfw always
asks for one, so the tray and the window would fail to come up with "NSGL:
Failed to find a suitable pixel format". `third_party/glfw` carries a patched
copy of glfw that retries with Apple's CPU renderer when that happens, wired up
through a `replace` in go.mod. See third_party/README.md for the details.
