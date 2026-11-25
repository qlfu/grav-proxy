# GravProxy (`grav-proxy`)

[Русский](./README.md) · **English**

![Version](https://img.shields.io/badge/version-v2.0.0-blue) ![Stack](https://img.shields.io/badge/Go%201.27%20%2B%20Wails%20v2-sing--box%201.14.4-00ADD8) ![Platform](https://img.shields.io/badge/platform-Windows%2010%2F11%20x64-lightgrey) ![License](https://img.shields.io/badge/license-MIT%20%2F%20GPL--3.0-green)

Fully open-source (**Open Source**) native single-binary local proxy (`127.0.0.1:28080`) and PAC router (`127.0.0.1:28081`) built with **Go + Wails v2 (WebView2)** and **in-process embedded `sing-box` (`v1.14.4`, `CGO_ENABLED=0`, `windows/amd64`)** for **Antigravity 2.0**, **v0.app**, **Gemini Desktop**, **NotebookLM**, **OpenCode Desktop / CLI**, and **Claude Desktop** on Windows 10/11 x64 — supporting both **VLESS / Happ** and built-in **SmartDNS (no-VPN)** mode.

Prebuilt [`GravProxy.exe`](https://github.com/qlfu/grav-proxy/releases/latest) binaries are published on GitHub [Releases](https://github.com/qlfu/grav-proxy/releases/latest) (`v2.0.0` SHA-256: `8A0253712D29F45E1FB0374EB7E5D194ADA83BFF4D6F2434EA69F10130F522E7`, `41,047,552` bytes).

> Note: Installer and tray UI labels are currently in Russian; CLI flags and documentation are available in English below.

<p align="center">
  <img src="./docs/installer-ui.png" alt="GravProxy Installer UI" width="760">
</p>

---

## Problems Solved

- **Fixes `Antigravity 2.0` region and agent errors**:
  - `User location is not supported`
  - `Agent execution terminated due to error.`
  - `There was an unexpected issue setting up your account.` (`Your account is not eligible for Gemini Code Assist for individuals at this time`)
  Automatically reads your **Happ** client or subscription URL, tests **every** VLESS node through in-process `sing-box` against **Google GeoIP** (reading the full 600+ KB `policies.google.com/terms` page), **NotebookLM**, and **Gemini** (filtering `/sorry`), picks the best clean node, and patches `agy.exe` / `language_server*.exe` in-place with rollback backups in `%LOCALAPPDATA%\GravProxy\backups\`.
- **Claude Desktop proxy & API-key mode (`3p`) on Windows**: writes `egressProxyUrl = http://127.0.0.1:28080` across `%LOCALAPPDATA%\Claude-3p\configLibrary\` (including MSIX VFS and `%APPDATA%\Claude-3p`) and optionally configures `deploymentMode: "3p"` (`sk-ant-...`, `sk-or-...`, or custom gateway) without administrator rights.
- **Unblock Gemini Desktop, NotebookLM, v0.app, and OpenCode without slowing down your internet**: serves a local PAC router at `http://127.0.0.1:28081/proxy.pac` so **only targeted AI domains** go through `:28080` while regular traffic stays `DIRECT`.

---

## `v2.0.0` Architecture (Open Source Go)

Unlike the `v1.x` PowerShell + C# + external 80 MB `sing-box.exe` wrapper, everything runs inside **one native executable**:

| Mode | Invocation | Behavior |
| --- | --- | --- |
| **GUI Installer** | `GravProxy.exe` | Frameless Wails v2 (WebView2) window with automatic single-instance replacement |
| **System Tray Service** | `GravProxy.exe --tray` | Pure Win32 tray icon + in-process `sing-box v1.14.4` (`:28080`) + PAC/control server (`:28081`) + failover + update checks |
| **App Launcher** | `GravProxy.exe --launch antigravity\|opencode\|claude` | Ensures tray proxy is up, auto-rechecks binary patch, and launches the app with proxy flags/env |
| **CLI** | `GravProxy.exe --cli <command>` | Terminal diagnostics and unattended install/repatch/uninstall |

Key `v2.0.0` capabilities:
- **Zero temp extraction or runtime compilation**: no `%TEMP%\ag-proxy-sfx-*`, no `csc.exe`, no `PowerShell`, no external `sing-box.exe`.
- **SmartDNS IP-Health Filtering Local DNS Relay (`SmartDNSRelay`)**: races all upstream IP addresses of DoH/DoT/UDP resolvers and probes the returned SNI-proxy IP pool with real `TLS + SNI` handshakes to filter out blocked/blackholed IPs on restrictive ISPs. Built-in presets include **`DNS-AI`** (`https://dns.dns-ai.ru/dns-query`), **`GeoHide EU`**, **`GeoHide US`**, **`MalwDNS`**, **`CommonNet`**, plus custom DoH/DoT/IPv4.
- **Fast VLESS without 5s AAAA DNS stalls**: attaches `local-dns` (`ipv4_only`) strictly to the VLESS/upstream outbound `domain_resolver` while keeping remote DNS resolution for target AI domains intact.
- **Full 2 MB GeoIP/Gemini subscription probing & cookie redirects**: handles HTTP 307 + `Set-Cookie` subscription gates via `cookiejar` and ranks nodes across reachability, Google GeoIP country, `NotebookLM`, and `Gemini` (`/sorry` filtering).
- **Zero console window flashes**: background helpers and child commands run with `CREATE_NO_WINDOW`; target apps (`Antigravity`, `OpenCode`, `Claude`) are gracefully closed before patching/restoring (never killing ancestor IDE/terminal PIDs); uninstall cleanly removes `%LOCALAPPDATA%\GravProxy\` and `AG_PROXY_*` env vars.
- **Seamless `v1.x` migration**: detects and stops legacy `%LOCALAPPDATA%\ag-proxy` processes and removes stale Startup/registry entries automatically.

---

## System Requirements

- **OS**: Windows 10 or Windows 11 (x64).
- **WebView2**: Microsoft Edge WebView2 Runtime (preinstalled on Windows 11 and modern Windows 10).
- **Administrator rights**: not required.
- **Upstream / Mode**: a `vless://...` key, subscription URL, or **Happ** client — or built-in **SmartDNS (no-VPN)** mode.

---

## Quick Start (GUI)

1. Download `GravProxy.exe` from [Releases](https://github.com/qlfu/grav-proxy/releases/latest) and run it.
2. Choose a mode:
   - **VLESS / Happ**: leave the input blank for auto-detection (Happ / `AG_PROXY_VLESS` / `AG_PROXY_SUB_URL`), click **«Найти в Happ»** (*Detect in Happ*), or paste a `vless://...` key, `https://...` subscription URL, or `socks5://127.0.0.1:10808`.
   - **SmartDNS (без VPN)**: pick **Авто** (*Auto*), **`DNS-AI`**, **`GeoHide EU`**, or a custom DoH/DoT/IPv4 resolver and click **«Проверить DNS»** (*Test DNS*) for live diagnostics.
3. Select desired components (`Tray & Autostart`, `Antigravity 2.0`, `v0.app / Gemini Desktop / NotebookLM (PAC)`, `OpenCode Desktop & CLI`, `Claude Desktop`).
4. Click **«Установить»** (*Install*) — running target apps are cleanly closed, `GravProxy.exe --tray` starts on `:28080` / `:28081`, binaries and shortcuts are patched, and all 9 connectivity checks run immediately.

---

## Command-Line Interface (CLI)

```powershell
# Show proxy, PAC, and app detection status
.\GravProxy.exe --cli status

# Live-test all built-in SmartDNS presets
.\GravProxy.exe --cli smartdns-test

# Run connectivity checks through 127.0.0.1:28080
.\GravProxy.exe --cli test

# Unattended install (VLESS/Happ or SmartDNS)
.\GravProxy.exe --cli install
.\GravProxy.exe --cli install --smartdns
.\GravProxy.exe --cli install --smartdns-url=smartdns://https://dns.dns-ai.ru/dns-query

# Re-patch Antigravity, OpenCode, Claude Desktop, and shortcuts
.\GravProxy.exe --cli repatch

# Full uninstall and clean rollback
.\GravProxy.exe --cli uninstall
```

---

## File Locations

- `%LOCALAPPDATA%\GravProxy\`:
  - `GravProxy.exe` — single standalone binary (GUI + `--tray` + `--launch` + `--cli`).
  - `config.json` — mode, endpoint/subscription, theme, and selected components.
  - `control.token` — local random 128-bit token protecting `/_ctl/*` on `127.0.0.1:28081`.
  - `backups\` — original Antigravity binary backups and `manifest.json` (`SHA-256`).
  - `shortcuts-backup.json`, `pac-backup.json`, `app.ico`, `opencode.cmd`, `oc.cmd`.
- `%LOCALAPPDATA%\Claude-3p\configLibrary\` — local `3p` configuration for Claude Desktop.
- **Custom PAC domains**: create `%LOCALAPPDATA%\GravProxy\pac-domains.txt` (one domain per line; `#` lines are comments).

---

## Building from Source

Prerequisites: **Go 1.27+**, **Node.js 22+**, **Wails CLI v2.17.1**.

```powershell
# 1. Install Wails CLI (once)
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.1

# 2. Run unit tests (zero CGO)
$env:CGO_ENABLED = '0'
go test -short -count=1 -tags "with_utls,with_quic" ./...

# 3. Build single-file GravProxy.exe
wails build -clean -platform windows/amd64 -tags "with_utls,with_quic" -ldflags "-s -w" -skipbindings
```

Project layout:
- [`main.go`](./main.go) — entrypoint routing GUI / `--tray` / `--launch` / `--cli`.
- [`app.go`](./app.go) — Wails backend for install, repatch, diagnostics, and full uninstall.
- [`internal/engine/`](./internal/engine) — embedded `sing-box v1.14.4`, `SmartDNSRelay`, node probes, and connectivity checks.
- [`internal/happ/`](./internal/happ) — VLESS / Reality / gRPC / WS / SmartDNS / Upstream parsers, subscriptions, and Happ discovery.
- [`internal/pac/`](./internal/pac) — local HTTP PAC/control server (`:28081`) and WinINet PAC configuration.
- [`internal/patcher/`](./internal/patcher) — atomic Antigravity patcher, COM `.lnk` shortcuts, Claude Desktop `3p`, OpenCode, single-instance GUI, graceful process closing.
- [`internal/tray/`](./internal/tray) — pure-Go Win32 tray icon, quick-launch menu, and failover.
- [`internal/updater/`](./internal/updater) — GitHub Releases checker, chunked 64 KB downloader with proxy fallback, `MZ` + `SHA-256` verification.
- [`frontend/`](./frontend) — Vite + vanilla HTML/CSS/JS GUI.

---

## Security & License

- See [SECURITY.en.md](./SECURITY.en.md) for local storage, checksums, and SmartScreen/AV details.
- Release history: [CHANGELOG.en.md](./CHANGELOG.en.md).
- Project license: **MIT** ([LICENSE](./LICENSE)). Release binaries statically link [`sing-box`](https://github.com/SagerNet/sing-box) (`v1.14.4`) under **GPL-3.0-or-later**.
