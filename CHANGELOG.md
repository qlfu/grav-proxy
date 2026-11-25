# История изменений (`CHANGELOG`)

**Русский** · [English](./CHANGELOG.en.md)

## `v1.5.0`

- **Автообновление через GitHub Releases (GUI и трей)**:
  - При запуске установщика (`setup-ui.ps1`) и периодически в фоне из трея (`proxy-tray.exe`) проверяется последний релиз `qlfu/grav-proxy` на GitHub (через GitHub Releases API с fallback на `/releases/latest` и локальный прокси `127.0.0.1:28080`).
  - В окне установщика появляется кнопка обновления и диалог подтверждения; в трее — всплывающее уведомление и пункт меню **«Проверить обновления»** / **«Обновить до ...»**.
  - Перед запуском скачанного обновления проверяются HTTPS-источник GitHub, размер файла, заголовок `MZ` (`PE/EXE`) и контрольная сумма `SHA-256`.

## `v1.4.0`

- **Очистка сломанного автозапуска Windows**: при установке, перепатче и удалении автоматически убираются устаревшие файлы `*.disabled`, `Run-Proxy.vbs`, `Update-Egress.vbs` и старые ярлыки из `Автозагрузки`, а также устаревшие записи `HKCU\...\Run` / `RunOnce` и задач планировщика (оставляя только актуальный `proxy-tray.exe`).
- **Открытие GravProxy из трея**: установщик копирует `GravProxy.exe` в `%LOCALAPPDATA%\ag-proxy\`, а в меню трея добавлен пункт **«Открыть GravProxy»** (безопасное удаление из запущенного GUI больше не падает, если `GravProxy.exe` ещё открыт).
- **Улучшение патча Claude Desktop**:
  - перед обновлением конфигурации корректно завершаются фоновые процессы `Claude.exe`,
  - удаляется конфликтующий `hybridPointer` в `_meta.json` и синхронизируются `appliedId` и `"deploymentMode": "3p"` в `%LOCALAPPDATA%\Claude-3p`, MSIX VFS-папке и `%APPDATA%\Claude-3p`,
  - проверяются политики `HKLM` / `HKCU\Software\Policies\Claude` и установленная версия Claude Desktop,
  - кнопка **«Перепатчить»** в GUI и пункт трея **«Проверить и перепатчить всё»** (а также **«Открыть Claude»**) проверяют и пробрасывают настройки Claude Desktop.

## `v1.3.0`

- **Claude Desktop вместо Claude Code CLI**: убран ошибочный CLI-патч (`claude.cmd`, `claude-proxy.cmd`, `~/.claude/*`), добавлена настройка Windows-приложения **Claude Desktop** (`Claude` / MSIX) без прав администратора через `%LOCALAPPDATA%\Claude-3p\configLibrary\`.
- **Прокси и PAC для Claude Desktop**: запись `egressProxyUrl = http://127.0.0.1:28080`, маршрутизация `claude.ai`, `claude.com`, `claude.app`, `claudeusercontent.com`, `claudemcpcontent.com` через PAC `:28081`.
- **Режим `3p` с API-ключом**: опциональная настройка Anthropic API (`sk-ant-...`) или стороннего шлюза (`Base URL`, `Bearer` / `x-api-key`, `deploymentMode: "3p"`) без обязательного входа в аккаунт Anthropic; пункт трея **«Открыть Claude»**.

## `v1.2.0`

- **Нативная иконка в панели задач**: встроенный STA-хост запускает WPF-интерфейс внутри процесса `GravProxy.exe` без отдельного дочернего окна PowerShell.
- Первоначальная интеграция Claude и расширение меню быстрого запуска в трее.

## `v1.1.0`

- **Локальный и upstream-режим прокси**: поддержка `socks5://` и `http://` (в том числе локального прокси **Happ** `127.0.0.1:10808` / `10809`) с автоматическим fallback, если подписка заблокирована провайдером.
- Фильтрация FakeIP и расширенная диагностика доступности серверов из подписки.

## Начальные релизы (`352039a` – `83e52a0`)

- Однофайловый GUI/CLI-установщик с автоподбором VLESS-узла через **Happ** по **Google GeoIP**.
- Локальный прокси `sing-box` (`127.0.0.1:28080`) и PAC-сервер (`127.0.0.1:28081`) для **v0.app**, **Gemini Desktop**, **NotebookLM**, Chrome и Edge.
- Патчинг **Antigravity IDE** и **OpenCode Desktop / CLI** на месте с резервными копиями в `%LOCALAPPDATA%\ag-proxy\backups\`.
- Значок в трее (`proxy-tray.exe`) с автопереключением узла (failover), очистка конфликтующих обходов (`AG Unlocker`, NRPT, `hosts`).
