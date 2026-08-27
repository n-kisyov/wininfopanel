# wininfopanel

A Go implementation of [InfoPanel](https://github.com/habibrehmansg/infopanel) — desktop
visualization software that renders hardware sensor data as customizable panels on desktop
overlays, external displays, and USB LCD panels.

**Windows 11 x64 only.** This project deliberately does not abstract over other platforms.

## Status

Under active development. See `PLAN.md` for the milestone breakdown.

## Building

Requires Go 1.24+. No C toolchain is needed — the project is built without cgo.

```powershell
./build.ps1              # builds bin/wininfopanel.exe and bin/panelctl.exe
go build ./...           # compile everything
go test ./...            # unit and golden-image render tests
```

## Data sources

| Source | Coverage |
|---|---|
| HWiNFO (shared memory) | Full. Requires HWiNFO running with Shared Memory Support enabled. |
| Native monitor | Built in, no external software. Coverage grows in phases (see `PLAN.md`). |
| Plugins | Out-of-process, language agnostic. A Go SDK lives in `pkg/plugin`. |

## Credits

InfoPanel is by [habibrehmansg](https://github.com/habibrehmansg) and contributors, licensed
GPL-3.0. This is an independent reimplementation in Go and is not affiliated with InfoPanel or
HWiNFO.

## License

GPL-3.0
