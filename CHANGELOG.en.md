# Changelog

[Русский](./CHANGELOG.md) · **English**

## `v1.7.1`

- **SmartDNS bootstrap & Defender startup fix**:
  - Switched the `sing-box` bootstrap resolver from `https://1.1.1.1/dns-query` to system `local` (`happ-config.ps1`, `proxy-tray.cs`, `sing-box-bg.cs`) so ISP throttling or blocking of Cloudflare `:443` no longer breaks every DoH preset at once.
  - Increased `sing-box.exe` probe startup wait from 6 s to 18 s (avoiding false failures during Defender's first scan) and HTTP probe timeouts from 5 s to 7 s with detailed failure diagnostics.
- **Patching `language_server.exe` while Antigravity is running**:
  - Added rename-away fallback (`.old.<pid>`) in `WinBytePatcher.WriteAtomic` (`setup.ps1`) and `ag_launch.cs` before replacing locked executables (fixes `MoveFileExW failed (5)`).
- **Reliable auto-updater with live progress, logs & slow-CDN fallback**:
  - Replaced single-shot `CopyTo` with a 64 KB chunked reader in `setup-ui.ps1` and `proxy-tray.cs`, showing progress percentage, MB downloaded, and KB/s in both the UI and Log tab.
  - Automatically switches from stalled/slow direct GitHub downloads (< 40 KB/s after 5 s) to the local proxy `127.0.0.1:28080` when available and cleans up stale `.part` files.

## `v1.7.0`

- **Built-in SmartDNS (no-VPN / no-VPS) mode inside `sing-box`**:
  - Added a **`VLESS / Happ` | `SmartDNS (без VPN)`** mode toggle in the installer GUI and CLI (`-SmartDns`, `-SmartDnsUrl`), featuring live-tested DoH presets (**`GeoHide EU`**, **`GeoHide US`**, **`MalwDNS`**, **`CommonNet`**), automatic selection, and custom DoH/DoT/IPv4 input.
  - Unlike system-wide DNS patchers, DoH resolution runs strictly inside `sing-box` (`domain_resolver`) for targeted AI traffic on `:28080` and PAC `:28081` — **no admin privileges** required and Windows system DNS, `hosts`, and NRPT stay untouched.
  - Added live DNS preset testing (**«Проверить DNS»**), SmartDNS status & connectivity checks in `proxy-tray.exe` / GUI, `sing-box-bg.exe` support (`-SkipTray`), and automatic tray failover across the SmartDNS pool.
- **Updated for `Antigravity 2.0`**: refreshed UI labels, probes, and documentation.

## `v1.6.0`

- **Cleaner install directory**: helper C# source files (`sing-box-bg.cs`, `proxy-tray.cs`, `ag_launch.cs`, `oc_launch.cs`) are no longer copied into `%LOCALAPPDATA%\ag-proxy\`, and leftover `.cs` files from earlier versions are removed.

## `v1.5.0`

- **GitHub Releases auto-updater (GUI and system tray)**:
  - Checks the latest `qlfu/grav-proxy` release on GitHub at GUI startup (`setup-ui.ps1`) and in the background from the tray app (`proxy-tray.exe`) via the Releases API with a `/releases/latest` HTML redirect fallback and optional fallback through `127.0.0.1:28080`.
  - Shows an update button and confirmation prompt in the installer window, plus a tray balloon notification and **«Проверить обновления»** (*Check for updates*) / **«Обновить до ...»** menu item.
  - Validates the GitHub HTTPS download URL, file size, `MZ` executable header, and `SHA-256` digest before launching the downloaded installer.

## `v1.4.0`

- **Windows startup popup fix**: installer, repatch, and uninstaller automatically clean up legacy `*.disabled`, `Run-Proxy.vbs`, `Update-Egress.vbs`, and old shortcuts in the Startup folder, as well as stale `HKCU\...\Run` / `RunOnce` entries and scheduled tasks (keeping only the active `proxy-tray.exe` entry).
- **Launch GravProxy from tray**: installer copies `GravProxy.exe` into `%LOCALAPPDATA%\ag-proxy\` and adds **«Открыть GravProxy»** (*Open GravProxy*) to the tray menu; uninstalling from a running tray-launched `GravProxy.exe` cleans the rest of the folder without failing.
- **Claude Desktop patch hardening**:
  - cleanly stops running `Claude.exe` processes before writing config,
  - strips conflicting `hybridPointer` keys in `_meta.json` and syncs `appliedId` + `"deploymentMode": "3p"` across `%LOCALAPPDATA%\Claude-3p`, MSIX VFS folders, and `%APPDATA%\Claude-3p`,
  - checks both `HKLM` and `HKCU\Software\Policies\Claude` registry policies and installed Claude Desktop version,
  - wires Claude Desktop sync/checks into the GUI **Repatch** button and tray **«Проверить и перепатчить всё»** / **«Открыть Claude»** actions.

## `v1.3.0`

- **Claude Desktop instead of Claude Code CLI**: removed the earlier CLI wrapper patch (`claude.cmd`, `claude-proxy.cmd`, `~/.claude/*`) and added non-admin Windows **Claude Desktop** (`Claude` / MSIX) support via `%LOCALAPPDATA%\Claude-3p\configLibrary\`.
- **Claude Desktop proxy & PAC**: sets `egressProxyUrl = http://127.0.0.1:28080` and routes `claude.ai`, `claude.com`, `claude.app`, `claudeusercontent.com`, and `claudemcpcontent.com` via PAC `:28081`.
- **3P API key mode**: optional configuration for Anthropic API (`sk-ant-...`) or custom gateway (`Base URL`, `Bearer` / `x-api-key`, `deploymentMode: "3p"`) without mandatory Anthropic account login; tray item **«Открыть Claude»**.

## `v1.2.0`

- **Native taskbar icon**: embedded STA host runs the WPF installer directly inside `GravProxy.exe` without a child PowerShell window.
- Initial Claude integration and expanded tray quick-launch menu.

## `v1.1.0`

- **Local & upstream proxy mode**: supports `socks5://` and `http://` URIs (including **Happ**'s local proxy `127.0.0.1:10808` / `10809`) with automatic fallback when a subscription URL is blocked by an ISP.
- FakeIP filtering and improved subscription node diagnostics.

## Initial Releases (`352039a` – `83e52a0`)

- Single-file GUI/CLI installer with automatic **Happ** VLESS discovery and **Google GeoIP** node selection.
- Local `sing-box` proxy (`127.0.0.1:28080`) and PAC router (`127.0.0.1:28081`) for **v0.app**, **Gemini Desktop**, **NotebookLM**, Chrome, and Edge.
- In-place patching for **Antigravity IDE** and **OpenCode Desktop / CLI** with rollback backups in `%LOCALAPPDATA%\ag-proxy\backups\`.
- System tray app (`proxy-tray.exe`) with automatic failover and cleanup of conflicting bypass tools (`AG Unlocker`, NRPT, `hosts`).
