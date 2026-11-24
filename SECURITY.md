# Security & Privacy

**English** · [Русский](./SECURITY.ru.md)

## Secret Storage

`GravProxy.exe` and the files in this repository do not contain hardcoded VLESS keys, subscription URLs, API keys, or telemetry.

All user credentials and runtime configurations are stored locally on your machine only:

- VLESS key and subscription URL — in user environment variables `AG_PROXY_VLESS` and `AG_PROXY_SUB_URL`.
- Generated `sing-box` configuration — in `%LOCALAPPDATA%\ag-proxy\sing-box.json`.
- Claude Desktop `3p` profile and API keys — in `%LOCALAPPDATA%\Claude-3p\configLibrary\` (including MSIX VFS `Packages\Claude_*\LocalCache\Local\Claude-3p`).

## Integrity & Binary Audit (`GravProxy.exe`)

Before running `GravProxy.exe`, you can verify its checksum and inspect all bundled scripts without executing it:

1. **Verify SHA-256**:

```powershell
Get-FileHash .\GravProxy.exe -Algorithm SHA256
```

Compare the output with the hash published on the [Releases](https://github.com/qlfu/grav-proxy/releases/latest) page (`v1.4.0`: `49CF00B60ACCC18E2DF41A5634BD2AE7715944DCC43A02DF52EA134D1BDE304B`).

2. **Inspect the embedded ZIP bundle**:

`GravProxy.exe` is a `.NET` launcher with an embedded ZIP archive (`GravProxy.bundle.zip`) containing the PowerShell scripts (`setup.ps1`, `setup-ui.ps1`, `happ-config.ps1`), C# source files (`payload\*.cs`), secret-free `payload\sing-box.json` template, and `payload\sing-box.exe`.

You can open `GravProxy.exe` in 7-Zip or list/extract entries with built-in PowerShell/.NET APIs:

```powershell
Add-Type -AssemblyName System.IO.Compression.FileSystem
$bytes = [IO.File]::ReadAllBytes('.\GravProxy.exe')
for ($i = $bytes.Length - 22; $i -ge 0; $i--) {
    if ($bytes[$i] -eq 0x50 -and $bytes[$i+1] -eq 0x4B -and $bytes[$i+2] -eq 0x05 -and $bytes[$i+3] -eq 0x06) {
        $zipStart = $i - [BitConverter]::ToUInt32($bytes, $i + 16) - [BitConverter]::ToUInt32($bytes, $i + 12)
        $ms = New-Object IO.MemoryStream($bytes, $zipStart, ($bytes.Length - $zipStart), $false)
        $za = New-Object IO.Compression.ZipArchive($ms, 'Read')
        $za.Entries | Select-Object FullName, Length
        $za.Dispose(); $ms.Dispose()
        break
    }
}
```

## Reporting Vulnerabilities

Please report security issues via **GitHub Security Advisories** or Issues in `qlfu/grav-proxy`. Do not include real VLESS URIs, subscription URLs, API keys, or token-bearing logs in public reports.
