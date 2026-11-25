# Changelog

[Русский](./CHANGELOG.md) · **English**

## `v2.0.0` — Open Source Rewrite · Go + Wails v2 + Embedded `sing-box`

Complete architectural rewrite in native **Go 1.27** (`CGO_ENABLED=0`) with a **Wails v2 (WebView2)** UI and **in-process embedded `sing-box v1.14.4`** — zero external `.exe` downloads, zero PowerShell/C# compilation, and full source code published in this repository.

- **Single self-contained executable (`GravProxy.exe`)**:
  - Combines all 4 modes in one file: GUI installer, Win32 system tray service (`--tray`), app launcher (`--launch antigravity|opencode|claude`), and CLI (`--cli status|smartdns-test|test|install|repatch|uninstall`).
  - Single-instance GUI replacement (launching a new window closes the prior GUI instance via `WM_CLOSE` and `%TEMP%\gravproxy-gui.pid` while keeping `--tray` untouched).
  - Reproducible source builds and GitHub Actions CI ([`.github/workflows/release.yml`](./.github/workflows/release.yml)).
- **`SmartDNSRelay` IP-Health Filtering + `DNS-AI` preset**:
  - Local UDP DNS relay (`miekg/dns`) races upstream resolver IPs and probes the returned SNI-proxy IP pool with real `TLS + SNI` handshakes, filtering out blocked/blackholed IPs on restrictive ISPs.
  - Added **`DNS-AI`** (`https://dns.dns-ai.ru/dns-query`) alongside **`GeoHide EU`**, **`GeoHide US`**, **`MalwDNS`**, and **`CommonNet`**.
- **VLESS speed, cookie redirects, and Gemini clean-node discovery**:
  - Attaches `local-dns` (`ipv4_only`) strictly to the VLESS/upstream outbound `domain_resolver` (eliminates 5.1s AAAA DNS stalls while keeping remote DNS resolution intact).
  - Adds HTTP `cookiejar` support for subscription 307/cookie challenge redirects.
  - Raises `probeURL` body read cap from `256 KB` to `2 MB` so the 600+ KB `policies.google.com/terms` region marker (`Country version:`) is always parsed, runs parallel `NotebookLM` + `Gemini /app` (`/sorry`) checks, and adds real VLESS failover/repick in the tray.
- **Graceful app closing, zero console flashes & clean uninstall**:
  - `StopTargetApps` cleanly closes running target apps (`Antigravity`, `OpenCode`, `Claude`) before patching/restoring while skipping ancestor IDE/terminal processes.
  - All hidden child invocations (`opencode-cli.exe`, `--tray`, `Claude:`, deferred self-delete) use `CREATE_NO_WINDOW`.
  - Fixes cross-thread tray shutdown (`PostMessageW(wmClose)` + idempotent `NIM_DELETE`), COM STA apartment handling for `.lnk` shortcuts (`withWScriptShell`), automatic migration from `%LOCALAPPDATA%\ag-proxy` to `%LOCALAPPDATA%\GravProxy`, and complete cleanup of `%LOCALAPPDATA%\GravProxy` + `AG_PROXY_*` env vars on uninstall.

---

## `v1.7.1`

- **SmartDNS bootstrap & Defender startup fix**: switched bootstrap from `https://1.1.1.1/dns-query` to system `local` and increased probe startup waits.
- **Patching `language_server.exe` while Antigravity is running**: added `.old.<pid>` rename-away fallback via `MoveFileExW`.
- **Reliable auto-updater**: 64 KB chunked progress with fallback to `127.0.0.1:28080` below 40 KB/s.

## `v1.7.0`

- **Built-in SmartDNS (no-VPN / no-VPS) mode inside `sing-box`**: `GeoHide EU`, `GeoHide US`, `MalwDNS`, `CommonNet`, live DNS probe, and tray auto-failover.
- **Updated for `Antigravity 2.0`**.

## `v1.6.0`

- **Cleaner install directory**: stopped copying helper `.cs` sources into `%LOCALAPPDATA%\ag-proxy\`.

## `v1.5.0`

- **GitHub Releases auto-updater (GUI & system tray)** with `MZ` and `SHA-256` verification.

## `v1.4.0`

- **Windows startup popup cleanup**, launching GravProxy from the tray, and Claude Desktop `3p` improvements.

## `v1.3.0`

- **Claude Desktop (`3p` + `egressProxyUrl`)** support without administrator privileges.

## `v1.2.0`

- **Native taskbar icon** and expanded tray quick-launch menu.

## `v1.1.0`

- **Local & upstream proxy mode (`socks5://` / `http://`)** and FakeIP filtering.

## Initial Releases (`352039a` – `83e52a0`)

- Single-file GUI/CLI installer with **Happ** VLESS discovery, **Google GeoIP** node selection, local `sing-box` (`:28080`), and PAC router (`:28081`).
