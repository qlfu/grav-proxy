# Security & Privacy

[Русский](./SECURITY.md) · **English**

## Secret Storage

`GravProxy.exe` and the files in this repository do not contain hardcoded VLESS keys, subscription URLs, API keys, or telemetry.

All user credentials and runtime configurations are stored locally on your machine only:

- VLESS key and subscription URL — in user environment variables `AG_PROXY_VLESS` and `AG_PROXY_SUB_URL`.
- Generated `sing-box` configuration — in `%LOCALAPPDATA%\ag-proxy\sing-box.json`.
- Claude Desktop `3p` profile and API keys — in `%LOCALAPPDATA%\Claude-3p\configLibrary\` (including MSIX VFS `Packages\Claude_*\LocalCache\Local\Claude-3p`).

## Integrity & Package Protection (`GravProxy.exe`)

Before running `GravProxy.exe`, verify its checksum:

1. **Verify SHA-256**:

```powershell
Get-FileHash .\GravProxy.exe -Algorithm SHA256
```

Compare the output with the hash published on the [Releases](https://github.com/qlfu/grav-proxy/releases/latest) page (`v1.7.0`: `4993A8307245E526FA3A5921CCB0BF8492D26ABCA7F5BC2623B5BDCFFB321CD4`). The built-in auto-updater also verifies the downloaded `SHA-256` digest and `PE/EXE` header before launching updates.

2. **Encrypted runtime container & disk footprint**:

- Starting with `v1.6.0`, runtime components inside `GravProxy.exe` are stored in a custom binary container (`GravProxy.pkg`) compressed with raw `Deflate` and encrypted with per-build random `AES-256-CBC` + `HMAC-SHA256` keys. The executable contains no ZIP/archive headers and cannot be opened in 7-Zip, WinRAR, or `System.IO.Compression.ZipArchive`.
- During installation, helper C# source files (`sing-box-bg.cs`, `proxy-tray.cs`, `ag_launch.cs`, `oc_launch.cs`) are no longer copied into `%LOCALAPPDATA%\ag-proxy\`, and any leftover `.cs` files from earlier versions are removed.

## Antivirus False Positives (`Sabsik.FL.A!ml`, etc.)

Windows Defender and other antivirus engines may flag `GravProxy.exe` with machine-learning / heuristic detections such as `Trojan:Win32/Sabsik.FL.A!ml`, `Trojan:Win32/Wacatac.B!ml`, or similar `!ml` labels. This is a **false positive** caused by legitimate installer mechanics:

1. **No commercial code-signing (EV/Authenticode) certificate** — unsigned binaries automatically trigger SmartScreen and stricter ML scoring.
2. **High-entropy encrypted bundle (`GravProxy.pkg`)** and runtime extraction into `%TEMP%\ag-proxy-sfx-*`.
3. **Behavior heuristics**: compiling local helpers via built-in `csc.exe`, patching Antigravity binaries in-place (with rollback backups), registering autostart in `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`, and enabling a local PAC server (`127.0.0.1:28081`).

**Recommended steps:**
- Always download `GravProxy.exe` from [Releases](https://github.com/qlfu/grav-proxy/releases/latest) and verify its digest using `Get-FileHash .\GravProxy.exe -Algorithm SHA256`.
- Once the hash matches the published release (`v1.7.0`: `4993A8307245E526FA3A5921CCB0BF8492D26ABCA7F5BC2623B5BDCFFB321CD4`), add `GravProxy.exe` and `%LOCALAPPDATA%\ag-proxy` to Windows Security exclusions (`Virus & threat protection → Manage settings → Exclusions`) if Defender blocks execution.

## Privacy in SmartDNS Mode (No-VPN)

When using `SmartDNS` mode (`eu.geohide.ru`, `us.geohide.ru`, `dns.malw.link`, `dns.commonnet.work`, or a custom DoH/DoT server), **no root CA certificates** are installed and TLS traffic stays encrypted end-to-end (the SNI proxy cannot read request/response contents). However, the third-party SmartDNS operator can observe which targeted AI hostnames are queried/connected to and your external IP address. Non-AI traffic goes `DIRECT` and never touches the SmartDNS resolver.

## Reporting Vulnerabilities

Please report security issues via **GitHub Security Advisories** or Issues in `qlfu/grav-proxy`. Do not include real VLESS URIs, subscription URLs, API keys, or token-bearing logs in public reports.
