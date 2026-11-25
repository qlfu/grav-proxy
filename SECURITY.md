# Безопасность и приватность

**Русский** · [English](./SECURITY.en.md)

## Хранение секретов

`GravProxy.exe` и файлы в этом репозитории не содержат зашитых VLESS-ключей, ссылок на подписки, API-ключей или телеметрии.

Все пользовательские параметры хранятся исключительно локально на устройстве:

- VLESS-ключ и URL подписки — в пользовательских переменных окружения `AG_PROXY_VLESS` и `AG_PROXY_SUB_URL`.
- Итоговая конфигурация `sing-box` — в `%LOCALAPPDATA%\ag-proxy\sing-box.json`.
- Профиль `3p` и API-ключи Claude Desktop — в `%LOCALAPPDATA%\Claude-3p\configLibrary\` (включая VFS-каталог MSIX `Packages\Claude_*\LocalCache\Local\Claude-3p`).

## Проверка целостности и защита сборки (`GravProxy.exe`)

Перед запуском проверяйте контрольную сумму бинарника:

1. **Проверка SHA-256**:

```powershell
Get-FileHash .\GravProxy.exe -Algorithm SHA256
```

Сравните полученное значение со значением на странице [Releases](https://github.com/qlfu/grav-proxy/releases/latest) (для `v1.6.0`: `C4DE07CA099C81E10AACED65AE135C8E8361C042ABA9C7668C8971BD2D5A949C`). Встроенный автообновитель также сверяет `SHA-256` и заголовок `PE/EXE` перед запуском новой версии.

2. **Шифрованный контейнер и хранение файлов**:

- Начиная с `v1.6.0`, внутренние компоненты `GravProxy.exe` упакованы в собственный бинарный контейнер `GravProxy.pkg`, сжатый `Deflate` и зашифрованный `AES-256-CBC` с контролем целостности `HMAC-SHA256` на случайных ключах сборки. Бинарник не содержит заголовков ZIP/архива и не открывается в 7-Zip, WinRAR или `System.IO.Compression.ZipArchive`.
- При установке C#-исходники помощников (`sing-box-bg.cs`, `proxy-tray.cs`, `ag_launch.cs`, `oc_launch.cs`) больше не копируются в `%LOCALAPPDATA%\ag-proxy\`, а оставшиеся от старых версий `.cs`-файлы удаляются.

## Сообщение об уязвимостях

Если вы обнаружили ошибку безопасности, сообщите о ней через **GitHub Security Advisories** или Issues репозитория `qlfu/grav-proxy`. Не прикладывайте к отчётам свои реальные VLESS-ссылки, подписки, API-ключи и фрагменты логов с токенами.
