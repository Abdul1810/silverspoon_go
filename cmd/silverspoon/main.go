// Command silverspoon is the SilverSpoon bulk downloader.
package main

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"runtime/debug"

	"gioui.org/app"

	"silverspoon/internal/applog"
	"silverspoon/internal/engine"
	"silverspoon/internal/platform"
	"silverspoon/internal/solver"
	"silverspoon/internal/store"
	"silverspoon/internal/ui"
)

func main() {
	background := flag.Bool("background", false, "started by the wake timer: don't nag when already running")
	flag.Parse()

	// Fatal Go errors (panics, races on maps) go to a crash log, since a
	// GUI build has no console.
	if f, err := os.OpenFile(filepath.Join(store.Dir, ".silverspoon_crash.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		debug.SetCrashOutput(f, debug.CrashOptions{})
	}
	// A soft memory limit makes the GC hand memory back to the OS sooner;
	// downloads stream through small buffers, so the heap stays small.
	debug.SetMemoryLimit(48 << 20)
	appLog, solverLog := applog.New(store.LogPath())
	engine.Log, solver.Log = appLog, solverLog

	// Single instance: two copies would corrupt the shared queue.
	lock, err := platform.AcquireLock(store.LockPath())
	if errors.Is(err, platform.ErrLocked) {
		if !*background {
			platform.MessageBox("SilverSpoon", "SilverSpoon is already running.\nPlease use the existing instance.")
		}
		os.Exit(1)
	}
	// Other lock errors (permissions): run unprotected, as the Python app does.
	defer lock.Release()

	settings := store.LoadSettings()
	sv := solver.New("", settings.CaptchaTimeout, settings.ShowSolverBrowser)
	var gui *ui.App
	eng := engine.New(store.LoadHistory(), func() engine.Config {
		s := gui.Settings()
		return engine.Config{MaxWorkers: s.MaxWorkers, MaxConnections: s.MaxConnections,
			BindAddresses: s.BindAddresses, BandwidthLimit: s.BandwidthLimit, AutoRetry: s.AutoRetryErrors}
	}, sv)
	gui = ui.New(eng, sv, settings)
	eng.Start()

	// An update's swap script waits for this marker to confirm the new build
	// started.
	if marker := os.Getenv("SILVERSPOON_UPDATE_SUCCESS_MARKER"); marker != "" {
		os.WriteFile(marker, []byte("SilverSpoon started successfully.\n"), 0o644)
	}

	gui.ShowWelcomeIfEnabled()
	gui.StartupUpdateCheck()

	go func() {
		err := gui.Run(engine.Version)
		lock.Release()
		if err != nil {
			appLog.Error(err.Error())
			os.Exit(1)
		}
		os.Exit(0)
	}()
	app.Main()
}
