# Changelog

[Русский](./CHANGELOG.md) · **English**

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
