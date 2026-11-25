const app = () => window.go && window.go.main && window.go.main.App;
const rt = () => window.runtime;

const state = {
  mode: 'vless',
  sdUrl: 'smartdns://auto',
  claudeScheme: 'bearer',
  busy: false,
  availUpdate: null,
  installed: false
};

const $ = (id) => document.getElementById(id);

function appendLog(line) {
  const pre = $('txt-log');
  pre.textContent += (pre.textContent ? '\n' : '') + line;
  pre.scrollTop = pre.scrollHeight;
  const clean = String(line || '').trim();
  if (clean) $('txt-last-log').textContent = clean;
}

function setBusy(v) {
  state.busy = v;
  ['btn-install', 'btn-test', 'btn-repatch', 'btn-uninstall', 'btn-detect-happ', 'btn-test-sd'].forEach((id) => {
    const el = $(id);
    if (el) el.disabled = v;
  });
}

function applyTheme(theme) {
  let effective = theme || 'auto';
  if (effective === 'auto') {
    effective = window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
  }
  document.body.setAttribute('data-theme', effective);
  document.querySelectorAll('[data-theme-btn]').forEach((b) => {
    b.classList.toggle('active', b.dataset.themeBtn === (theme || 'auto'));
  });
}

function setEndpointDisplay(kind, sourceTag, text) {
  const dot = $('dot-endpoint');
  dot.className = 'dot' + (kind ? ' ' + kind : '');
  $('txt-source-tag').textContent = sourceTag || '';
  $('txt-endpoint-status').textContent = text || '';
}

function setMode(mode) {
  state.mode = mode === 'smartdns' ? 'smartdns' : 'vless';
  document.querySelectorAll('[data-mode]').forEach((b) => b.classList.toggle('active', b.dataset.mode === state.mode));
  $('panel-vless').classList.toggle('hidden', state.mode !== 'vless');
  $('panel-smartdns').classList.toggle('hidden', state.mode !== 'smartdns');
  updateEndpointSummary();
}

function setSmartDnsPreset(urlVal) {
  document.querySelectorAll('[data-sd-url]').forEach((b) => b.classList.toggle('active', b.dataset.sdUrl === urlVal));
  const inp = $('txt-smartdns');
  if (urlVal === 'custom') {
    inp.classList.remove('hidden');
    inp.focus();
  } else {
    state.sdUrl = urlVal;
    inp.classList.add('hidden');
  }
  updateEndpointSummary();
}

function updateEndpointSummary() {
  if (state.mode === 'smartdns') {
    const isCustom = !$('txt-smartdns').classList.contains('hidden');
    const val = isCustom ? $('txt-smartdns').value.trim() : state.sdUrl;
    if (!val || val === 'smartdns://auto') {
      setEndpointDisplay('ok', 'SmartDNS', 'Автоподбор (DNS-AI, GeoHide EU/US, MalwDNS, CommonNet)');
    } else {
      const host = val.replace(/^smartdns:\/\//i, '').replace(/^https?:\/\//i, '').split('/')[0];
      setEndpointDisplay('ok', 'SmartDNS', `${host} · DoH / SmartDNS`);
    }
    return;
  }

  const typed = $('txt-vless').value.trim();
  if (!typed) {
    setEndpointDisplay('', 'Happ', 'Оставьте пустым для автопоиска или вставьте vless:// / подписку');
  } else if (/^vless:\/\//i.test(typed)) {
    const host = (typed.split('@')[1] || '').split(/[/?#]/)[0];
    setEndpointDisplay('ok', 'VLESS', host ? `Ключ: ${host}` : 'VLESS-ключ указан');
  } else if (/^(socks5?|http):\/\//i.test(typed)) {
    setEndpointDisplay('ok', 'Прокси', typed);
  } else if (/^https?:\/\//i.test(typed)) {
    setEndpointDisplay('ok', 'Подписка', typed);
  } else {
    setEndpointDisplay('warn', 'Проверка', 'Ожидается vless://, подписка https:// или socks5://');
  }
}

function selectExecTab(name) {
  const isSteps = name === 'steps';
  $('btn-tab-steps').classList.toggle('active', isSteps);
  $('btn-tab-log').classList.toggle('active', !isSteps);
  $('panel-steps').classList.toggle('hidden', !isSteps);
  $('panel-log').classList.toggle('hidden', isSteps);
}

function updateComponentCounter() {
  const ids = ['chk-tray', 'chk-ag', 'chk-pac', 'chk-oc', 'chk-claude'];
  const cnt = ids.filter((id) => $(id).checked).length;
  $('txt-comp-count').textContent = `${cnt} / 5`;
  $('panel-claude-inputs').classList.toggle('hidden', !$('chk-claude').checked);
}

function collectRequest() {
  const isCustom = !$('txt-smartdns').classList.contains('hidden');
  const sdUrl = isCustom ? $('txt-smartdns').value.trim() : state.sdUrl;
  return {
    mode: state.mode,
    endpointInput: $('txt-vless').value.trim(),
    subscriptionUrl: '',
    smartDnsUrl: sdUrl,
    tray: $('chk-tray').checked,
    antigravity: $('chk-ag').checked,
    browserPac: $('chk-pac').checked,
    openCode: $('chk-oc').checked,
    claudeDesktop: $('chk-claude').checked,
    claudeApiKey: $('txt-claude-key').value.trim(),
    claudeBaseUrl: $('txt-claude-base').value.trim(),
    claudeScheme: state.claudeScheme
  };
}

function updateStepRow(key, stepState, text) {
  const row = document.querySelector(`.step-row[data-step="${key}"]`);
  if (!row) return;
  row.setAttribute('data-state', stepState || 'idle');
  const stEl = row.querySelector('.step-status');
  if (stEl) stEl.textContent = text || '';
}

function refreshSidebarState(st) {
  state.installed = !!st.installed;
  const dot = $('dot-install-state');
  const txt = $('txt-install-state');
  if (st.proxyRunning) {
    dot.className = 'sq-dot ok';
    txt.textContent = 'Прокси активен';
  } else if (st.installed) {
    dot.className = 'sq-dot warn';
    txt.textContent = 'Установлен (остановлен)';
  } else {
    dot.className = 'sq-dot';
    txt.textContent = 'Не установлен';
  }
  $('txt-target-path').textContent = st.shortDataDir || 'AppData\\Local\\GravProxy';
  $('btn-install').textContent = st.installed ? 'Обновить' : 'Установить';
}

async function init() {
  if (rt()) {
    rt().EventsOn('log:line', (line) => appendLog(line));
    rt().EventsOn('progress:update', (p) => {
      if (p) {
        $('prog-bar').style.width = `${p.percent || 0}%`;
        $('txt-progress-header').textContent = p.header || '';
      }
    });
    rt().EventsOn('step:update', (s) => {
      if (s && s.key) updateStepRow(s.key, s.state, s.text);
    });
    rt().EventsOn('update:progress', (p) => {
      if (p) {
        const mb = (p.downloadedBytes / 1048576).toFixed(1);
        const tot = p.totalBytes > 0 ? `/ ${(p.totalBytes / 1048576).toFixed(1)} МБ` : 'МБ';
        $('prog-bar').style.width = `${p.percent || 0}%`;
        $('txt-progress-header').textContent = `${p.percent}% · ${mb} ${tot}`;
        $('txt-last-log').textContent = `Загрузка: ${mb} ${tot} (${p.speedKbps} КБ/с)`;
      }
    });
  }

  document.querySelectorAll('[data-theme-btn]').forEach((b) => b.addEventListener('click', () => {
    applyTheme(b.dataset.themeBtn);
    app()?.SaveTheme(b.dataset.themeBtn);
  }));
  $('btn-minimize').addEventListener('click', () => rt()?.WindowMinimise());
  $('btn-close').addEventListener('click', () => rt()?.Quit());

  document.querySelectorAll('[data-mode]').forEach((b) => b.addEventListener('click', () => setMode(b.dataset.mode)));
  document.querySelectorAll('[data-sd-url]').forEach((b) => b.addEventListener('click', () => setSmartDnsPreset(b.dataset.sdUrl)));
  $('txt-vless').addEventListener('input', updateEndpointSummary);
  $('txt-smartdns').addEventListener('input', updateEndpointSummary);

  ['chk-tray', 'chk-ag', 'chk-pac', 'chk-oc', 'chk-claude'].forEach((id) => {
    $(id).addEventListener('change', updateComponentCounter);
  });

  $('btn-claude-scheme').addEventListener('click', () => {
    state.claudeScheme = state.claudeScheme === 'bearer' ? 'x-api-key' : 'bearer';
    $('btn-claude-scheme').textContent = state.claudeScheme === 'bearer' ? 'Схема: Bearer' : 'Схема: x-api-key';
  });

  $('btn-tab-steps').addEventListener('click', () => selectExecTab('steps'));
  $('btn-tab-log').addEventListener('click', () => selectExecTab('log'));
  $('btn-log-preview').addEventListener('click', () => selectExecTab('log'));
  $('btn-copy-log').addEventListener('click', async () => {
    try {
      await navigator.clipboard.writeText($('txt-log').textContent);
      $('btn-copy-log').textContent = 'Скопировано';
      setTimeout(() => { $('btn-copy-log').textContent = 'Копировать'; }, 1400);
    } catch {}
  });
  $('btn-open-folder').addEventListener('click', () => app()?.OpenDataFolder());

  $('btn-detect-happ').addEventListener('click', async () => {
    const res = await app().DetectHapp();
    if (res.endpointUrl) {
      $('txt-vless').value = res.endpointUrl;
      updateEndpointSummary();
      setEndpointDisplay('ok', 'Happ', `Найден узел (${res.source})`);
    } else if (res.subUrl) {
      $('txt-vless').value = res.subUrl;
      updateEndpointSummary();
      setEndpointDisplay('ok', 'Happ', `Найдена подписка (${res.source})`);
    } else if (res.encryptedOnly) {
      setEndpointDisplay('warn', 'Happ', 'subs.db зашифрован — вставьте ключ или подписку вручную');
    } else {
      setEndpointDisplay('warn', 'Happ', 'Конфигурация Happ не найдена');
    }
  });

  $('btn-test-sd').addEventListener('click', async () => {
    setBusy(true);
    selectExecTab('log');
    $('txt-progress-header').textContent = 'Проверка DNS...';
    try {
      const isCustom = !$('txt-smartdns').classList.contains('hidden');
      const target = isCustom ? $('txt-smartdns').value.trim() : state.sdUrl;
      const res = await app().TestSmartDNS(target);
      appendLog('== Проверка SmartDNS');
      (res.results || []).forEach((r) => {
        appendLog(`  ${r.name}: score=${r.score} AG=${r.agOk} NB=${r.notebookOk} Gem=${r.geminiOk} Claude=${r.anthOk} (${r.latencyMs} ms)`);
      });
      if (res.ok) {
        setEndpointDisplay('ok', 'SmartDNS OK', res.summary);
        $('txt-progress-header').textContent = 'SmartDNS OK';
      } else {
        setEndpointDisplay('err', 'Ошибка DNS', res.error || 'Не отвечает');
        $('txt-progress-header').textContent = 'Ошибка DNS';
      }
    } finally {
      setBusy(false);
    }
  });

  $('btn-install').addEventListener('click', async () => {
    setBusy(true);
    $('txt-log').textContent = '';
    $('prog-bar').style.width = '0%';
    selectExecTab('steps');
    try {
      const res = await app().Install(collectRequest());
      $('txt-progress-header').textContent = res.ok ? 'Готово' : 'С замечаниями';
      if (!res.ok) selectExecTab('log');
      const st = await app().GetInitialState();
      refreshSidebarState(st);
    } finally {
      setBusy(false);
    }
  });

  $('btn-repatch').addEventListener('click', async () => {
    setBusy(true);
    selectExecTab('log');
    try {
      const res = await app().Repatch(collectRequest());
      $('txt-progress-header').textContent = res.ok ? 'Готово' : 'С замечаниями';
    } finally {
      setBusy(false);
    }
  });

  $('btn-test').addEventListener('click', async () => {
    setBusy(true);
    selectExecTab('log');
    $('txt-progress-header').textContent = 'Проверка...';
    try {
      await app().RunConnectivityTest();
      $('txt-progress-header').textContent = 'Готово';
    } finally {
      setBusy(false);
    }
  });

  $('btn-uninstall').addEventListener('click', async () => {
    setBusy(true);
    selectExecTab('log');
    try {
      const res = await app().Uninstall();
      $('txt-progress-header').textContent = res.ok ? 'Удалено' : 'С замечаниями';
      const st = await app().GetInitialState();
      refreshSidebarState(st);
    } finally {
      setBusy(false);
    }
  });

  $('btn-update').addEventListener('click', async () => {
    if (!state.availUpdate || state.busy) return;
    setBusy(true);
    selectExecTab('log');
    try {
      await app().DownloadAndApplyUpdate(state.availUpdate);
    } catch (e) {
      appendLog('  [FAIL] Ошибка обновления: ' + e);
    } finally {
      setBusy(false);
    }
  });

  if (app()) {
    const st = await app().GetInitialState();
    applyTheme(st.config.theme);
    $('version-label').textContent = `Установщик · v${st.version}`;
    if (st.config.mode === 'smartdns') setMode('smartdns');
    else setMode('vless');

    if (st.config.endpointUrl) $('txt-vless').value = st.config.endpointUrl;
    else if (st.config.subscriptionUrl) $('txt-vless').value = st.config.subscriptionUrl;
    else if (st.happDiscovery?.endpointUrl) $('txt-vless').value = st.happDiscovery.endpointUrl;
    else if (st.happDiscovery?.subUrl) $('txt-vless').value = st.happDiscovery.subUrl;

    const savedSd = st.config.smartDnsUrl || 'smartdns://auto';
    const matching = Array.from(document.querySelectorAll('[data-sd-url]')).find((b) => b.dataset.sdUrl.toLowerCase() === savedSd.toLowerCase());
    if (matching) {
      setSmartDnsPreset(matching.dataset.sdUrl);
    } else {
      setSmartDnsPreset('custom');
      $('txt-smartdns').value = savedSd.replace(/^smartdns:\/\//i, '');
    }

    $('chk-tray').checked = st.config.components.tray;
    $('chk-ag').checked = st.config.components.antigravity;
    $('chk-pac').checked = st.config.components.browserPac;
    $('chk-oc').checked = st.config.components.openCode;
    $('chk-claude').checked = st.config.components.claudeDesktop;
    $('txt-claude-base').value = st.config.claudeBaseUrl || '';
    state.claudeScheme = st.config.claudeAuthScheme || 'bearer';
    $('btn-claude-scheme').textContent = state.claudeScheme === 'bearer' ? 'Схема: Bearer' : 'Схема: x-api-key';

    updateComponentCounter();
    updateEndpointSummary();
    refreshSidebarState(st);

    if (st.bypassWarnings && st.bypassWarnings.length) {
      st.bypassWarnings.forEach((w) => appendLog('  [WARN] ' + w));
    }

    try {
      const upd = await app().CheckUpdates();
      if (upd && upd.isNewer) {
        state.availUpdate = upd;
        $('btn-update').textContent = `Обновление ${upd.tag}`;
        $('btn-update').classList.remove('hidden');
      }
    } catch {}
  }
}

window.addEventListener('DOMContentLoaded', init);
