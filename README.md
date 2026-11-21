# SilverSpoon (Go edition)

A Go rewrite of [SilverSpoon](https://github.com/billysams21/SilverSpoon), the bulk downloader for FitGirl/SteamRIP-style file hosts, with a redesigned interface. It is one native executable with no Python runtime.

It reads and writes the same files as the Python app, so an existing queue, its progress and its settings carry over:

- `~/.silverspoon_settings.json` and `~/.silverspoon_history.json`
- `<file>.segments.json` next to each partial download
- `~/.silverspoon.log` and `~/.silverspoon_offpeak_report.jsonl`

Only one copy of either build can run at a time, because both use `~/.silverspoon.lock`.

## Supported hosts

| Host | How links are resolved |
|---|---|
| `fuckingfast.co`, `datanodes.to`, `bzzhr.to` | A real Chromium driven over the DevTools protocol passes the Cloudflare check. It runs off-screen and comes to the front only when a check needs a click. |
| `filekeeper.net` | Plain HTTP: waits out the countdown and posts the form |
| Any other `http(s)` link | Downloaded directly |

Downloads use several connections when the server allows it (Settings → Connections per download). They can be spread over more than one network adapter (Settings → Download via IPs), and they pause and resume where every part stopped. Solver-host downloads use a Chrome TLS/HTTP2 fingerprint.

## Using it

- **Add downloads** (or Ctrl+N): paste links, or press Ctrl+V anywhere. Links inside copied web pages are picked out too. Then choose the folder and the batch name.
- The **sidebar** filters by view (Downloading, Queued & paused, Completed, Errors) or by batch. Ctrl+F searches.
- **Select** with click, Ctrl/Shift-click, the check boxes or the arrow keys. A bar appears with Start, Pause, Cancel, Retry, Redownload, Extract, Copy error and Delete. Right-click opens the full menu.
- **Shortcuts in the list:**
  - S: Start
  - P: Pause
  - Space: Start or Pause
  - C: Cancel
  - R: Retry
  - F: Force redownload
  - Del: Delete
  - Ctrl+A: Select all
  - ←/→: Fold or unfold a batch
  - Esc: Clear the selection
- **Scheduler** (sidebar): an off-peak download window, weekly or one-off, with an optional Windows wake timer.
- **Auto-extract** (status bar) extracts finished batches with 7-Zip.

## Building

Requires Go 1.27+. On Windows no C compiler is needed.

```powershell
go test ./...
.\build.ps1                       # dist\SilverSpoon\ + a release zip
.\build.ps1 -Repo owner/name      # also set the GitHub repo updates come from
```

`build.ps1` bundles `7z.exe`. It also bundles Playwright's Chromium when installed (`python -m playwright install chromium`); otherwise the solver uses an installed Chrome, Edge or Brave. Run `go install github.com/tc-hib/go-winres@latest` first to embed the app icon.

The auto-updater stays off until a release repository is set with `-Repo`.

## Layout

```
cmd/silverspoon      entry point: lock, logging, wiring
internal/engine      queue, download manager and workers, extraction, off-peak window
internal/segmented   multi-connection ranged downloads with resume
internal/solver      Cloudflare/Turnstile solver over raw CDP
internal/transport   Chrome-fingerprinted HTTP client, bind addresses
internal/providers   host routing, FileKeeper form flow, file names
internal/extraction  7-Zip listing and progress
internal/scheduler   off-peak window logic, wake task
internal/store       settings and history files
internal/update      release check and self-update
internal/platform    clipboard, pickers, keep-awake, single-instance lock
internal/ui          Gio interface
```

Licensed under the GNU GPLv3, like the original.
