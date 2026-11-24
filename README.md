# GravProxy (`grav-proxy`)

**Русский** · [English](./README.en.md)

![Version](https://img.shields.io/badge/version-v1.4.0-blue) ![Platform](https://img.shields.io/badge/platform-Windows%2010%2F11%20x64-lightgrey) ![License](https://img.shields.io/badge/license-MIT-green)

Портативный однофайловый установщик локального прокси (`127.0.0.1:28080`) и локального PAC-маршрутизатора (`127.0.0.1:28081`) на базе `sing-box` (`1.14.4`, `windows/amd64`) для **Antigravity IDE**, **v0.app**, **Gemini Desktop**, **NotebookLM**, **OpenCode Desktop / CLI** и **Claude Desktop** на Windows 10/11 x64.

Готовый автономный файл [`GravProxy.exe`](./GravProxy.exe) доступен на странице [Releases](https://github.com/qlfu/grav-proxy/releases/latest) (`SHA-256` для `v1.4.0`: `49CF00B60ACCC18E2DF41A5634BD2AE7715944DCC43A02DF52EA134D1BDE304B`).

---

## Возможности

- **Единый `.exe` файл**: при запуске распаковывает компоненты во временный каталог (`%TEMP%\ag-proxy-sfx-*`), запускает WPF-интерфейс с нативной иконкой прямо внутри процесса без окна PowerShell и очищает временные файлы после выхода. Внутри уже встроен `sing-box.exe` (при отсутствии предусмотрено автоскачивание с GitHub `SagerNet/sing-box`).
- **Автоопределение VLESS / Happ и локальный прокси**:
  - Находит ключ или подписку из клиента **Happ** (`subscription_log.txt`, конфиги/реестр Happ) или переменных `AG_PROXY_VLESS` / `AG_PROXY_SUB_URL`.
  - Поддерживает прямой upstream-режим `socks5://...` / `http://...` (например, `socks5://127.0.0.1:10808` при включённом Happ) и автоматически переключается на локальный прокси Happ, если подписка блокируется провайдером.
  - Тестирует серверы через реальный `sing-box` на **Google GeoIP** (отсеивая FakeIP и неподдерживаемые регионы) и выбирает узел с минимальной задержкой.
- **Патч оригинальных программ на месте (без дубликатов)**:
  - Патчит `agy.exe` и `language_server*.exe` на месте, сохраняя оригиналы и SHA-256 манифест в `%LOCALAPPDATA%\ag-proxy\backups\`.
  - Настраивает оригинальные ярлыки Antigravity и OpenCode (Рабочий стол, «Пуск», панель задач) через лаунчеры `ag_launch.exe` / `oc_launch.exe` с сохранением иконок.
  - Автоматически проверяет и восстанавливает патчи при каждом запуске и по таймеру из трея.
- **v0.app, Gemini Desktop и NotebookLM через локальный PAC**:
  - Поднимает локальный PAC-сервер `http://127.0.0.1:28081/proxy.pac` для WinINet, Chrome/Edge и **Gemini Desktop** (без модификации `Gemini.exe`, с сохранением `Alt+Space` и автообновлений).
  - Проксирует только целевые AI-домены (`v0.app`, `vercel.*`, `gemini.google.com`, `notebooklm.google.com`, `notebook.google.com`, `cloudcode-pa.googleapis.com`, `opencode.ai`, `openrouter.ai`, `api.openai.com`, `api.anthropic.com`, `anthropic.com`, `claude.ai`, `claude.com`, `claude.app`, `claudeusercontent.com`, `claudemcpcontent.com` и др.); весь остальной трафик идёт `DIRECT`.
- **Claude Desktop (прокси + опциональный API-ключ без входа в Anthropic)**:
  - Останавливает фоновые процессы `Claude.exe`, очищает мешающий `hybridPointer` и записывает `egressProxyUrl = http://127.0.0.1:28080` в `%LOCALAPPDATA%\Claude-3p\configLibrary\` (а также в MSIX VFS-папку `Packages\Claude_*\LocalCache\Local\Claude-3p` и существующий `%APPDATA%\Claude-3p`), работая без прав администратора.
  - При вводе API-ключа (`sk-ant-...` либо `sk-or-...` / стороннего шлюза с `Base URL` и схемой `Bearer` / `x-api-key`) синхронизирует `deploymentMode: "3p"` для работы без обязательной авторизации в `claude.ai`.
  - Проверяет установленную версию Claude Desktop (`>= 1.44121.1` для прокси, `>= 4.16336.0` для полного `3p`) и наличие групповых политик `HKLM` / `HKCU\Software\Policies\Claude`.
- **Управление из системного трея (`proxy-tray.exe`)**:
  - Быстрое открытие установщика/настроек через **«Открыть GravProxy»** (`%LOCALAPPDATA%\ag-proxy\GravProxy.exe`).
  - Цветовая индикация статуса, включение/выключение прокси (при паузе PAC отдаёт `DIRECT`), автоматическое переключение на запасной чистый сервер (failover), ручной подбор сервера, проверка соединения, **«Проверить и перепатчить всё»** (включая Claude Desktop), запуск приложений (**Antigravity**, **v0.app**, **Gemini Desktop**, **NotebookLM**, **OpenCode**, **Claude**), просмотр `proxy.log` и папки установки.
- **Очистка конфликтующих обходов и старого автозапуска**:
  - Убирает мешающие DNS/NRPT-правила, `ag_dns.exe`, задачи `AG Unlocker`, профиль `v0-chrome` и AI-записи из `hosts` (не затрагивая сторонние записи; UAC запрашивается только при наличии таких конфликтов).
  - Убирает сломанные/устаревшие файлы автозагрузки (`*.disabled`, `Run-Proxy.vbs`, `Update-Egress.vbs` и старые записи `HKCU\...\Run` / задач).
- **Работа без прав администратора и чистое удаление**: установка в `%LOCALAPPDATA%\ag-proxy` и полный откат при удалении.

---

## Системные требования

- **ОС**: Windows 10 или Windows 11 (x64).
- **Встроенные компоненты Windows**: `.NET Framework 4.5+`, `PowerShell 5.1`, `curl.exe`.
- **Права администратора**: не требуются.
- **Прокси / VPN**: клиент **Happ** с активной подпиской/локальным прокси (`10808`/`10809`), либо ссылка `vless://...` / `https://...` / `socks5://...`.

---

## Быстрый старт (GUI)

1. Скачайте [`GravProxy.exe`](./GravProxy.exe) со страницы [Releases](https://github.com/qlfu/grav-proxy/releases/latest) и запустите двойным кликом.
2. **Прокси-сервер (VLESS / Happ)**:
   - Оставьте поле пустым для автоподхвата (`AG_PROXY_VLESS`, `AG_PROXY_SUB_URL` или Happ), либо нажмите **«Найти в Happ»**.
   - При шифровании `subs.db` в новых версиях Happ или блокировке подписки вставьте вручную ссылку подписки `https://...`, ключ `vless://...` или адрес локального прокси `socks5://127.0.0.1:10808` (при включённом Happ на Нидерландах/Польше).
3. **Компоненты**: отметьте нужные пункты (`Значок в трее и автозапуск`, `Antigravity IDE`, `v0.app / Gemini Desktop / NotebookLM`, `OpenCode Desktop и CLI`, `Claude Desktop`). Для `Claude Desktop` при желании введите API-ключ, `Base URL` и выберите схему `Bearer` / `x-api-key`.
4. Нажмите **«Установить»** (или **«Перепатчить»** для повторной синхронизации патчей и настроек Claude Desktop без пересборки ключа прокси).
5. Нажмите **«Проверить»**, чтобы протестировать внешний IP, Google GeoIP, Antigravity, v0.app, Gemini, NotebookLM, OpenCode, OpenRouter и Claude / Anthropic API.

> Доступны переключатель темы (`Авто` / `Светлая` / `Тёмная`) и закрытие окна по `Esc` (когда установка не запущена).

---

## Использование после установки

- **GravProxy**: открывайте прямо из значка в трее → **«Открыть GravProxy»**.
- **Antigravity IDE** и **OpenCode Desktop**: запускайте через обычные ярлыки.
- **OpenCode CLI**: используйте `opencode`, `oc` или `opencode-proxy` в новом окне терминала.
- **Claude Desktop**: запускайте из ярлыка или через **«Открыть Claude»** в трее.
- **Gemini Desktop**: используйте обычный ярлык или `Alt+Space` (при активном прокси трафик идёт через PAC `:28081`).
- **v0.app** и **NotebookLM**: открывайте в Chrome или Edge при активном прокси.
- **Свои домены в PAC**: создайте `%LOCALAPPDATA%\ag-proxy\pac-domains.txt` (по одному домену в строке, `#` — комментарий).

---

## Режим командной строки (CLI)

При передаче любых аргументов [`GravProxy.exe`](./GravProxy.exe) запускается в консольном режиме:

```powershell
# Полная автоматическая установка
.\GravProxy.exe -Cli

# Установка с явным VLESS, подпиской или локальным SOCKS5/HTTP прокси
.\GravProxy.exe -VlessUrl "vless://..."
.\GravProxy.exe -SubUrl "https://example.com/sub"
.\GravProxy.exe -VlessUrl "socks5://127.0.0.1:10808"

# Повторный патч Antigravity, OpenCode, Claude Desktop и ярлыков
.\GravProxy.exe -PatchOnly

# Настройка Claude Desktop по API-ключу
.\GravProxy.exe -Cli -ClaudeApiKey "sk-ant-..."
.\GravProxy.exe -Cli -ClaudeApiKey "sk-or-v1-..." -ClaudeBaseUrl "https://openrouter.ai/api" -ClaudeAuthScheme x-api-key

# Полное удаление и откат
.\GravProxy.exe -Uninstall
```

| Параметр | Описание |
| --- | --- |
| `-TargetRoot <путь>` | Каталог установки (по умолчанию `%LOCALAPPDATA%\ag-proxy`) |
| `-VlessUrl <url>` | Ссылка `vless://...`, `socks5://...`, `http://...` или URL подписки `https://...` |
| `-SubUrl <url>` | URL подписки `https://...` с автоподбором чистого сервера Google GeoIP |
| `-ClaudeApiKey <key>` | API-ключ Claude Desktop (`sk-ant-...`, `sk-or-...` или ключ шлюза) |
| `-ClaudeBaseUrl <url>` | Свой URL шлюза `inferenceGatewayBaseUrl` для Claude Desktop |
| `-ClaudeAuthScheme <scheme>` | Схема авторизации шлюза: `bearer` (по умолчанию) или `x-api-key` |
| `-PatchOnly` | Только перепатчить Antigravity / ярлыки / PAC / OpenCode / Claude Desktop и очистить старый автозапуск |
| `-Uninstall` | Закрыть мешающие процессы, вернуть оригиналы и удалить установку |
| `-SkipTray` | Не собирать и не запускать `proxy-tray.exe` (использовать скрытый `sing-box-bg.exe`) |
| `-SkipTask` | Не добавлять автозапуск в `HKCU\...\Run` |
| `-SkipAntigravity` | Пропустить патч Antigravity IDE |
| `-SkipV0` | Не включать локальный PAC для браузера |
| `-SkipOpenCode` | Не настраивать OpenCode Desktop / CLI и `PATH` |
| `-SkipClaudeDesktop` | Не настраивать прокси и конфиг Claude Desktop |
| `-SkipShortcuts` | Не изменять ярлыки приложений |
| `-SkipPath` | Не изменять пользовательский `PATH` |

---

## Расположение рабочих файлов

- `%LOCALAPPDATA%\ag-proxy\`:
  - `GravProxy.exe` — копия установщика для запуска из меню трея.
  - `sing-box.exe`, `sing-box.json`, `sing-box.template.json` — ядро прокси и конфигурация.
  - `proxy-tray.exe`, `sing-box-bg.exe` — значок в трее и встроенный PAC-сервер (`:28081`).
  - `ag_launch.exe`, `oc_launch.exe` — лаунчеры с автопроверкой патча и ярлыков.
  - `backups\` — резервные копии Antigravity (`manifest.json` + `*.bak`) и перенесённые старые элементы автозагрузки (`startup-disabled\`, `startup-legacy\`).
  - `shortcuts-backup.json`, `pac-backup.json`, `proxy.log`.
- `%LOCALAPPDATA%\Claude-3p\configLibrary\` — локальный профиль `3p` для Claude Desktop.

---

## Безопасность, приватность и SmartScreen

- **Нет встроенных ключей и телеметрии**: подробности о хранении данных и проверке хэша/ZIP-содержимого `GravProxy.exe` приведены в [SECURITY.ru.md](./SECURITY.md).
- **Windows Defender SmartScreen**: бинарник `GravProxy.exe` не подписан коммерческим EV-сертификатом, поэтому при первом запуске Windows может показать предупреждение *«Система Windows защитила ваш компьютер»* — нажмите **«Подробнее» → «Выполнить в любом случае»** (предварительно можно сверить `SHA-256` и открыть `.exe` через 7-Zip).

---

## Частые вопросы

**Ошибка региона Google API (`User location is not supported`)**
Откройте меню трея → **«Подобрать сервер (Google GeoIP)»**, либо нажмите **«Найти в Happ»** и переустановите конфиг.

**Happ не определяется автоматически (`subs.db` зашифрован или подписка заблокирована провайдером)**
Вставьте ссылку подписки `https://...` или `vless://...` вручную, либо включите Happ (например, Нидерланды/Польшу) и укажите `socks5://127.0.0.1:10808` (или `http://127.0.0.1:10809`).

**Не работает патч Claude Desktop**
Убедитесь, что установлен актуальный Claude Desktop с `claude.ai/download` (`>= 1.44121.1` для `egressProxyUrl`, `>= 4.16336.0` для полного `3p`), запустите `GravProxy.exe v1.4.0` (или **«Проверить и перепатчить всё»** в трее) и проверьте отсутствие политик в реестре `HKLM`/`HKCU\Software\Policies\Claude`:

```powershell
Get-AppxPackage *Claude* | Select Name,Version; foreach($k in 'HKLM:\SOFTWARE\Policies\Claude','HKCU:\Software\Policies\Claude'){if(Test-Path $k){$k; Get-ItemProperty $k}}; Get-Content "$env:LOCALAPPDATA\Claude-3p\configLibrary\_meta.json" -EA 0; Select-String -Path "$env:LOCALAPPDATA\Claude\logs\main.log","$env:LOCALAPPDATA\Claude-3p\logs\main.log" -Pattern 'managedConfig|egress-proxy|deploymentMode|Error|fail' -EA 0 | Select -Last 25
```

**После входа в Windows появляется окно выбора программы для `.disabled` или ошибка `Run-Proxy.vbs`**
Запустите `GravProxy.exe v1.4.0` («Установить» или «Перепатчить») — он автоматически уберёт устаревшие файлы и записи автозагрузки.

**Порт `127.0.0.1:28080` уже занят**
Закройте предыдущий экземпляр через значок в трее («Выход») или Диспетчер задач и повторите запуск.

---

## Документация и лицензия

- История релизов: [CHANGELOG.ru.md](./CHANGELOG.md)
- Безопасность и аудит сборки: [SECURITY.ru.md](./SECURITY.md)
- Лицензия проекта: **MIT** ([LICENSE](./LICENSE)). В релизный бинарник включён компонент [`sing-box`](https://github.com/SagerNet/sing-box) (`v1.14.4`), распространяемый по лицензии **GPL-3.0-or-later**.
