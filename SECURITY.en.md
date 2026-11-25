# Security & Privacy

[Русский](./SECURITY.md) · **English**

## Open Source & Zero Telemetry

Starting with `v2.0.0`, the full source code of `GravProxy` is open in `qlfu/grav-proxy` and compiles as pure Go (`CGO_ENABLED=0`) + Wails v2 with no custom packers, encrypted blobs, telemetry, hardcoded VLESS keys, subscription URLs, or API keys.

All user configuration and credentials stay strictly local on your machine:

- Main configuration (`mode`, `endpointUrl`, `subscriptionUrl`, `smartDnsUrl`, selected components) — stored in `%LOCALAPPDATA%\GravProxy\config.json` (legacy `AG_PROXY_VLESS` / `AG_PROXY_SUB_URL` user env vars from `v1.x` are read for migration and removed on uninstall).
- Loopback HTTP control token (`/_ctl/status`, `/_ctl/toggle`, `/_ctl/reload`, `/_ctl/recheck`, `/_ctl/stop` on `127.0.0.1:28081`, header `X-GravProxy-Token`) — stored in `%LOCALAPPDATA%\GravProxy\control.token` (128-bit locally generated CSPRNG token).
- Claude Desktop `3p` profile and API keys — stored in `%LOCALAPPDATA%\Claude-3p\configLibrary\` (and MSIX VFS `Packages\Claude_*\LocalCache\Local\Claude-3p`).

## Integrity Verification (`GravProxy.exe`)

Always verify the digest of downloaded binaries before running:

```powershell
Get-FileHash .\GravProxy.exe -Algorithm SHA256
```

Verified `v2.0.0` SHA-256: `8A0253712D29F45E1FB0374EB7E5D194ADA83BFF4D6F2434EA69F10130F522E7` (also posted on [Releases](https://github.com/qlfu/grav-proxy/releases/latest)). The built-in auto-updater verifies GitHub HTTPS origin, `PE/EXE` (`MZ`) header, and `SHA-256` before replacing the running binary.

You can also build an identical binary directly from this repository (see "Building from Source" in [README.en.md](./README.en.md)) or inspect GitHub Actions build artifacts.

## SmartScreen & Antivirus False Positives

- `GravProxy.exe` is not signed with a commercial EV/Authenticode certificate, so Windows Defender SmartScreen may display *"Windows protected your PC"* on first launch — click **More info → Run anyway** after verifying the `SHA-256` checksum.
- Starting with `v2.0.0`, `GravProxy.exe` **no longer** extracts SFX packages into `%TEMP%`, invokes PowerShell, or runs `csc.exe` at runtime, sharply reducing heuristic detections. If any AV flags `HKCU\...\Run` registration or in-place binary patching, verify `SHA-256` and whitelist `%LOCALAPPDATA%\GravProxy` if desired.

## Privacy in SmartDNS Mode (No-VPN)

In `SmartDNS` mode (`dns.dns-ai.ru`, `eu.geohide.ru`, `us.geohide.ru`, `dns.malw.link`, `dns.commonnet.work`, or a custom DoH/DoT server), **no root CA certificates** are installed and TLS traffic remains encrypted end-to-end (the SNI proxy cannot read request/response payloads). The chosen SmartDNS operator can technically observe queried AI hostnames, SNI names, and your IP address; non-AI traffic goes `DIRECT` over your default DNS.

## Reporting Vulnerabilities

Report security issues via **GitHub Security Advisories** or Issues in `qlfu/grav-proxy`. Never include real VLESS URIs, subscription URLs, API keys, or `control.token` values in public reports.
