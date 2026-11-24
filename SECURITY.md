# Безопасность и приватность

**Русский** · [English](./SECURITY.en.md)

## Хранение секретов

`GravProxy.exe` и файлы в этом репозитории не содержат зашитых VLESS-ключей, ссылок на подписки, API-ключей или телеметрии.

Все пользовательские параметры хранятся исключительно локально на устройстве:

- VLESS-ключ и URL подписки — в пользовательских переменных окружения `AG_PROXY_VLESS` и `AG_PROXY_SUB_URL`.
- Итоговая конфигурация `sing-box` — в `%LOCALAPPDATA%\ag-proxy\sing-box.json`.
- Профиль `3p` и API-ключи Claude Desktop — в `%LOCALAPPDATA%\Claude-3p\configLibrary\` (включая VFS-каталог MSIX `Packages\Claude_*\LocalCache\Local\Claude-3p`).

## Проверка целостности и аудит `GravProxy.exe`

Перед запуском вы можете проверить контрольную сумму и содержимое бинарника без его выполнения:

1. **Проверка SHA-256**:

```powershell
Get-FileHash .\GravProxy.exe -Algorithm SHA256
```

Сравните полученное значение со значением на странице [Releases](https://github.com/qlfu/grav-proxy/releases/latest) (для `v1.4.0`: `49CF00B60ACCC18E2DF41A5634BD2AE7715944DCC43A02DF52EA134D1BDE304B`).

2. **Прозрачный аудит встроенных скриптов**:

`GravProxy.exe` — это `.NET`-лаунчер со встроенным ZIP-архивом (`GravProxy.bundle.zip`). Внутри лежат исходные скрипты PowerShell (`setup.ps1`, `setup-ui.ps1`, `happ-config.ps1`), C#-код лаунчеров/трея (`payload\*.cs`), шаблон `payload\sing-box.json` без секретов и `payload\sing-box.exe`.

Вы можете открыть `GravProxy.exe` через 7-Zip или просмотреть список/содержимое архива встроенными средствами .NET / PowerShell:

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

## Сообщение об уязвимостях

Если вы обнаружили ошибку безопасности, сообщите о ней через **GitHub Security Advisories** или Issues репозитория `qlfu/grav-proxy`. Не прикладывайте к отчётам свои реальные VLESS-ссылки, подписки, API-ключи и фрагменты логов с токенами.
