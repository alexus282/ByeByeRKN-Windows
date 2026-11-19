# Development notes

Architecture, known pitfalls and the roadmap for the Windows port. The user-facing documentation is
in [README.md](README.md).

## Upstream

- Android app: <https://github.com/alexus282/ByeByeRKN> — the reference for the connection ladder,
  obfuscation presets and WARP registration.
- Tunnel core: `github.com/amnezia-vpn/amneziawg-go`. The current branch uses v1.0.4; the v3.1 tags
  live under the separate `/v3` module path and are the next upgrade.
- UI: `gioui.org`, no CGO and no WebView2.
- Go 1.26+ is required; `build.ps1` uses `go` from PATH, with the local dev path as a fallback.

Version lives in [VERSION](VERSION) and is injected into both the app and Setup.exe by `build.ps1`.

## Packages

| Package | Purpose |
|---|---|
| `awg` | obfuscation presets, QUIC/STUN/DNS/SIP I1, UAPI and wg-quick lines |
| `catalog` | WARP endpoints and ports from the signed catalog |
| `probe` | real handshake probes and the parallel scanner |
| `warp` | device registration, WARP+, split ClientHello |
| `wintunnel` | Wintun adapter: addresses, MTU, DNS, routes, rollback, diagnostics |
| `store` | settings, WARP device and last-good route: `%AppData%` portable, `%ProgramData%` service |
| `engine` | search ladder, UAPI/IpcGet, trace, stall detector |
| `ui` | Gio window: home, settings, log, dialogs, tray icon |
| `control` | JSON-over-named-pipe protocol, service-side server, window-side client |
| `winsvc` | SCM host: start/stop the server, wait for Stop/Shutdown |
| `install` | files, service, ACL, shortcuts, uninstall entry |
| `cmd/setup` | Gio installer wizard and the one-file Setup.exe |
| `tray` | Win32 notification icon, menu, tooltips, WM_ENDSESSION |
| `instance` | one window per session; show and quit events |
| `winres` | icon and its source artwork |

## Architecture

The installed app is split in two processes:

- `ByeByeRKN.exe --service` runs as LocalSystem and owns `engine.Controller`, the store and the
  WARP account. It listens on `\\.\pipe\ByeByeRKN`.
- The window starts as the ordinary user, connects to the pipe and sends commands. It never touches
  the adapter, routes or the settings file.

The pipe is restricted to the installing user (SID is recorded by the installer), SYSTEM and
Administrators. Settings in `%ProgramData%\ByeByeRKN` get an explicit ACL for the same reason: they
contain the WARP private key.

If the pipe breaks (service update or crash recovery), `ui/backend.go` retries in the background and
notifies the window. Settings and account state are delivered in the hello message; engine events
are broadcast.

Portable mode still exists: without a reachable service, an elevated window hosts the engine itself
and uses `%AppData%\ByeByeRKN`.

## Things that bit us

1. **DAD on Wintun.** `172.16.0.2/32` stayed `Tentative`, so Windows returned `unreachable host` even
   after a successful handshake. `wintunnel.Configure` sets `dadtransmits=0 routerdiscovery=disabled`.
2. **Teardown order.** `engine.establish` calls `net.Teardown()` before `closeSessionLocked()`.
   Closing Wintun first makes `netsh` fail and leaves stale routes on a reused interface index.
3. **Routes.** Use `0.0.0.0/1` + `128.0.0.0/1` and `::/1` + `8000::/1`, interface metric 0. IPv6 MTU
   must stay at least 1280.
4. **Telegram probe removed.** It added seconds to every connection and declared working routes
   broken based on an approximate colo. Traffic through WARP (`warp=on`) is the only proof now.
5. **Server vs exit.** The card shows the real WARP endpoint separately from the approximate
   Cloudflare colo, so a city label is not mistaken for the server.
6. **No `SendMessage` from the Gio event loop.** Calls into the native window are asynchronous
   (`PostMessage`, `ShowWindowAsync`) or run on another goroutine; Gio waits for its event handler.
7. **Glow.** Gio has no radial gradient and its stroker left streaks; the halo is a precomputed image
   and arcs are filled outlines. Screens are rendered headlessly in tests.
8. **Installer files in use.** Install closes the window and stops the service before copying.
   Uninstall renames the running exe into TEMP and lets `cmd` delete it after exit.
9. **Setup elevation.** Setup.exe has an icon-only resource and relaunches itself with `--elevated`
   instead of a `requireAdministrator` manifest, so its tests can run unelevated.
10. **Service restart.** The window's remote backend reconnects to the pipe and shows
    "Служба ByeByeRKN остановилась. Переподключаюсь…" while retrying.

## Testing

```powershell
go vet ./...
go test ./... -count=1
```

The race detector needs CGO and a C toolchain; the project deliberately builds without CGO, so
race runs are done separately on a machine that has one.

Network code is tested against a fake command runner, so no administrator rights are needed. The
real tunnel must be checked manually as administrator: connect, trace, MTU fallback, Telegram,
tray, close-to-tray, exit, Windows shutdown.

The control tests use a real named pipe with a unique name; UI and installer tests render through
Gio's headless backend and skip when no GPU is available.

## Roadmap

1. Verify Setup.exe, service registration and uninstall on a clean VM.
2. Upgrade to `github.com/amnezia-vpn/amneziawg-go/v3` (v3.1.20261116, or 20260814 to match the
   Android bundle) and test the real tunnel.
3. MASQUE as an alternative transport when AmneziaWG exits are restricted.
4. Code signing for Setup.exe and ByeByeRKN.exe.
5. Per-app split tunneling and richer exit monitoring, following the Android app.
