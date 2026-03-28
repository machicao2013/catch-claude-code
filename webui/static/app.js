// ═══════════════════════════════════════════════════════════════
// claude-spy viewer — Frontend Logic
// Features: theme switching, search/filter, keyboard nav,
//           lazy loading, SSE live updates, collapsible content
// ═══════════════════════════════════════════════════════════════

// ── Utilities ─────────────────────────────────────────────────

function fmtTime(ts) {
  const d = new Date(ts);
  return d.toLocaleTimeString('zh-CN', { hour12: false });
}

function fmtDuration(ms) {
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
}

function fmtNum(n) {
  if (n == null || isNaN(n)) return '0';
  if (n === 0) return '0';
  return n.toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',');
}

function escHtml(s) {
  return String(s)
    .replace(/&/g, '&amp;').replace(/</g, '&lt;')
    .replace(/>/g, '&gt;').replace(/"/g, '&quot;');
}

// ── Theme System ──────────────────────────────────────────────

const THEMES = ['obsidian', 'daylight', 'phosphor'];
const THEME_ICONS = { obsidian: '🌙', daylight: '☀️', phosphor: '⚡' };

function getTheme() {
  return document.documentElement.getAttribute('data-theme') || 'obsidian';
}

function setTheme(theme) {
  document.documentElement.setAttribute('data-theme', theme);
  localStorage.setItem('claude-spy-theme', theme);
  const btn = document.getElementById('theme-toggle');
  if (btn) btn.textContent = THEME_ICONS[theme] || '◐';
}

function cycleTheme() {
  const cur = getTheme();
  const idx = THEMES.indexOf(cur);
  setTheme(THEMES[(idx + 1) % THEMES.length]);
}

// ── Search / Filter ───────────────────────────────────────────

function setupSearch() {
  const input = document.getElementById('search-input');
  const countEl = document.getElementById('search-count');
  let timer;
  input.addEventListener('input', () => {
    clearTimeout(timer);
    timer = setTimeout(() => filterRecords(input.value, countEl), 300);
  });
  input.addEventListener('keydown', e => {
    if (e.key === 'Escape') {
      input.value = '';
      filterRecords('', countEl);
      input.blur();
    }
  });
}

function filterRecords(query, countEl) {
  const q = query.toLowerCase().trim();
  const records = document.querySelectorAll('.record');
  const groupHeaders = document.querySelectorAll('.group-header');
  let visible = 0, total = records.length;
  records.forEach(el => {
    if (!q) {
      el.classList.remove('hidden');
      visible++;
      return;
    }
    const s = el._summary;
    const haystack = [s.id, s.model || '', s.stop_reason || ''].join(' ').toLowerCase();
    const isError = !s.stop_reason && s.out_tokens === 0;
    const type = classifyRecord(s);
    const match = haystack.includes(q)
      || (q === 'error' && isError)
      || (q === 'err' && isError)
      || (q === 'sub' && type === 'sub')
      || (q === 'main' && type === 'main')
      || (q === '主对话' && type === 'main')
      || (q === '子请求' && type === 'sub');
    el.classList.toggle('hidden', !match);
    if (match) visible++;
  });
  // Hide group headers when filtering
  groupHeaders.forEach(h => h.classList.toggle('hidden', !!q));
  countEl.textContent = q ? `${visible}/${total}` : '';
  currentFocusIndex = -1;
}

// ── Keyboard Navigation ──────────────────────────────────────

let currentFocusIndex = -1;

function getVisibleRecords() {
  return Array.from(document.querySelectorAll('.record:not(.hidden)'));
}

function focusRecord(idx) {
  const records = getVisibleRecords();
  // Remove old focus
  document.querySelectorAll('.record.focused').forEach(el => el.classList.remove('focused'));
  if (idx < 0 || idx >= records.length) return;
  currentFocusIndex = idx;
  const el = records[idx];
  el.classList.add('focused');
  el.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
}

function setupKeyboard() {
  document.addEventListener('keydown', e => {
    const searchInput = document.getElementById('search-input');
    const inSearch = document.activeElement === searchInput;

    // Always available
    if (e.key === '/' && !inSearch) {
      e.preventDefault();
      searchInput.focus();
      return;
    }

    // Don't intercept when typing in search
    if (inSearch) return;

    const records = getVisibleRecords();

    switch (e.key) {
      case 'j':
      case 'ArrowDown':
        e.preventDefault();
        focusRecord(Math.min(currentFocusIndex + 1, records.length - 1));
        break;
      case 'k':
      case 'ArrowUp':
        e.preventDefault();
        focusRecord(Math.max(currentFocusIndex - 1, 0));
        break;
      case 'Enter':
      case ' ':
        e.preventDefault();
        if (currentFocusIndex >= 0 && currentFocusIndex < records.length) {
          const header = records[currentFocusIndex].querySelector('.record-header');
          if (header) toggleRecord(header);
        }
        break;
      case 'Escape':
        // Collapse focused record
        if (currentFocusIndex >= 0 && currentFocusIndex < records.length) {
          const el = records[currentFocusIndex];
          if (el.classList.contains('expanded')) {
            const header = el.querySelector('.record-header');
            if (header) toggleRecord(header);
          }
        }
        break;
      case 't':
      case 'T':
        cycleTheme();
        break;
    }
  });
}

// ── Collapsible Content ──────────────────────────────────────

function makeCollapsible(contentEl, threshold) {
  const lines = contentEl.innerText.split('\n');
  if (lines.length <= threshold) return;

  const wrapper = document.createElement('div');
  wrapper.className = 'collapsible collapsed';
  const inner = document.createElement('div');
  inner.className = 'collapsible-inner';
  inner.innerHTML = contentEl.innerHTML;
  const btn = document.createElement('span');
  btn.className = 'toggle-btn';
  btn.textContent = '▼ 展开全文';
  btn.onclick = (e) => {
    e.stopPropagation();
    const collapsed = wrapper.classList.toggle('collapsed');
    btn.textContent = collapsed ? '▼ 展开全文' : '▲ 收起';
  };
  wrapper.appendChild(inner);
  wrapper.appendChild(btn);
  contentEl.replaceWith(wrapper);
}

// ── Message Rendering ────────────────────────────────────────

function renderMessage(msg) {
  const role = msg.role || 'unknown';
  const content = msg.content;
  const blocks = Array.isArray(content)
    ? content
    : [{ type: 'text', text: typeof content === 'string' ? content : JSON.stringify(content) }];

  const frags = [];
  for (const block of blocks) {
    if (block.type === 'text') {
      frags.push(renderTextMsg(role, block.text));
    } else if (block.type === 'tool_use') {
      frags.push(renderToolUse(block));
    } else if (block.type === 'tool_result') {
      frags.push(renderToolResult(block));
    } else if (block.type === 'thinking') {
      frags.push(renderThinking(block.thinking || ''));
    } else if (block.type === 'redacted_thinking') {
      frags.push(renderRedacted());
    } else {
      frags.push(renderTextMsg(role, JSON.stringify(block)));
    }
  }
  // 包裹在 msg-group 容器中，确保多 block 消息布局正确
  return `<div class="msg-group">${frags.join('')}</div>`;
}

function renderTextMsg(role, text) {
  const tagClass = role === 'user' ? 'role-user' : 'role-assistant';
  const label = role === 'user' ? 'user' : 'asst';
  return `
    <div class="msg">
      <span class="role-tag ${tagClass}">${label}</span>
      <div class="msg-content" data-lines="${(text || '').split('\n').length}">${escHtml(text || '')}</div>
    </div>`;
}

function renderToolUse(block) {
  const inputStr = block.input ? JSON.stringify(block.input, null, 2) : '{}';
  return `
    <div class="msg">
      <span class="role-tag role-tool-use">tool</span>
      <div class="msg-content tool-use-content">
        <div class="tool-name">⚙ ${escHtml(block.name || '')}</div>
        <div class="tool-param" data-lines="${inputStr.split('\n').length}"><span>${escHtml(inputStr)}</span></div>
      </div>
    </div>`;
}

function renderToolResult(block) {
  const c = block.content;
  const text = Array.isArray(c)
    ? c.map(x => x.text != null ? x.text : JSON.stringify(x)).join('\n')
    : (typeof c === 'string' ? c : JSON.stringify(c));
  return `
    <div class="msg">
      <span class="role-tag role-result">result</span>
      <div class="msg-content" data-lines="${text.split('\n').length}">${escHtml(text)}</div>
    </div>`;
}

function renderThinking(text) {
  if (!text) {
    return `
    <div class="msg">
      <span class="role-tag role-thinking">think</span>
      <div class="msg-content thinking-content" style="opacity:0.5;font-style:italic;">（空 thinking block）</div>
    </div>`;
  }
  return `
    <div class="msg">
      <span class="role-tag role-thinking">think</span>
      <div class="msg-content thinking-content" data-lines="${text.split('\n').length}">${escHtml(text)}</div>
    </div>`;
}

function renderRedacted() {
  return `
    <div class="msg">
      <span class="role-tag role-redacted">redacted</span>
      <div class="msg-content redacted-content">思考内容已被 Anthropic API 审查删除，原始响应中无此部分数据</div>
    </div>`;
}

// ── Record Summary Rendering ─────────────────────────────────

function renderSummaryRecord(summary, isNew) {
  const el = document.createElement('div');
  const isError = !summary.stop_reason && summary.out_tokens === 0;
  const isSub = classifyRecord(summary) === 'sub';
  el.className = 'record' + (isNew ? ' new' : '') + (isError ? ' has-error' : '') + (isSub ? ' is-sub' : '');
  el.dataset.id = summary.id;

  const timeStr = fmtTime(summary.timestamp);
  const durStr = fmtDuration(summary.duration_ms);
  const inTok = fmtNum(summary.in_tokens);
  const outTok = fmtNum(summary.out_tokens);
  const stop = summary.stop_reason || (isError ? 'error' : '—');

  el.innerHTML = `
    <div class="record-header" onclick="toggleRecord(this)">
      <div class="record-header-left">
        <span class="req-id">${escHtml(summary.id)}</span>
        <span class="req-time">${timeStr}</span>
        <span class="req-duration">${durStr}</span>
        <span class="req-tokens">${inTok} in / ${outTok} out</span>
        <span class="req-stop">${escHtml(stop)}</span>
      </div>
      <span class="expand-icon">▼</span>
    </div>
    <div class="record-body"><div class="record-body-inner"></div></div>`;

  el._summary = summary;
  el._rendered = false;

  return el;
}

// ── Record Detail (Expand) ───────────────────────────────────

async function toggleRecord(headerEl) {
  const el = headerEl.closest('.record');
  el.classList.toggle('expanded');

  if (el.classList.contains('expanded') && !el._rendered) {
    el._rendered = true;
    const body = el.querySelector('.record-body-inner');
    const summary = el._summary;

    if (el._fullRec) {
      renderRecordBody(body, el._fullRec, summary);
      return;
    }

    body.innerHTML = '<div style="color:var(--text-muted);padding:8px;">加载中...</div>';

    try {
      const resp = await fetch(`api/records/${encodeURIComponent(summary.id)}`);
      if (!resp.ok) throw new Error(`${resp.status}`);
      const rec = await resp.json();
      renderRecordBody(body, rec, summary);
    } catch (e) {
      body.innerHTML = `<div style="color:var(--accent-red);padding:8px;">加载失败: ${escHtml(e.message)}</div>`;
      el._rendered = false;
    }
  }
}

function renderRecordBody(body, rec, summary) {
  const reqBody = rec.request?.body || {};
  const messages = reqBody.messages || [];
  const respBody = rec.response?.body || {};
  const usage = respBody.usage || {};
  const status = rec.response?.status || 0;

  let reqHtml = '';
  let respHtml = '';

  // ── LEFT: Request ──
  reqHtml += `<div class="section-header section-request">▶ REQUEST</div>`;
  reqHtml += `<div class="section-meta">`;
  reqHtml += `<span><span class="label">model:</span> <span class="val">${escHtml(reqBody.model || '—')}</span></span>`;
  if (reqBody.system) {
    const sysLen = JSON.stringify(reqBody.system).length;
    reqHtml += `<span><span class="label">system:</span> <span class="val">${fmtNum(sysLen)} chars</span></span>`;
  }
  reqHtml += `<span><span class="label">messages:</span> <span class="val">${messages.length}</span></span>`;
  if (reqBody.tools) {
    reqHtml += `<span><span class="label">tools:</span> <span class="val">${reqBody.tools.length}</span></span>`;
  }
  reqHtml += `</div>`;

  // 渲染 system prompt
  if (reqBody.system) {
    reqHtml += `<div class="system-block">`;
    reqHtml += `<div class="system-label">SYSTEM</div>`;
    const sys = reqBody.system;
    if (Array.isArray(sys)) {
      for (const block of sys) {
        const text = block.text || JSON.stringify(block);
        reqHtml += `<div class="system-item" data-lines="${text.split('\n').length}">${escHtml(text)}</div>`;
      }
    } else if (typeof sys === 'string') {
      reqHtml += `<div class="system-item" data-lines="${sys.split('\n').length}">${escHtml(sys)}</div>`;
    } else {
      reqHtml += `<div class="system-item">${escHtml(JSON.stringify(sys))}</div>`;
    }
    reqHtml += `</div>`;
  }

  if (messages.length > 0) {
    reqHtml += `<div class="messages-list">`;
    for (let i = 0; i < messages.length; i++) {
      reqHtml += `<div class="msg-numbered"><span class="msg-index">${i}</span>${renderMessage(messages[i])}</div>`;
    }
    reqHtml += `</div>`;
  }

  // ── RIGHT: Response ──
  const statusClass = status >= 200 && status < 300 ? 'status-ok' : 'status-err';
  respHtml += `<div class="section-header section-response">◀ RESPONSE</div>`;
  respHtml += `<div class="section-meta">`;
  respHtml += `<span><span class="label">status:</span> <span class="${statusClass}">${status}</span></span>`;
  respHtml += `<span><span class="label">duration:</span> <span class="val">${fmtDuration(rec.duration_ms)}</span></span>`;
  if (respBody.stop_reason) {
    respHtml += `<span><span class="label">stop:</span> <span class="val">${escHtml(respBody.stop_reason)}</span></span>`;
  }
  respHtml += `</div>`;

  if (respBody.error) {
    const errMsg = respBody.error.message || JSON.stringify(respBody.error);
    const errType = respBody.error.type || '';
    respHtml += `<div class="error-block">`;
    if (errType) respHtml += `<div class="error-type">${escHtml(errType)}</div>`;
    respHtml += `<div class="error-message">${escHtml(errMsg)}</div>`;
    respHtml += `</div>`;
  }

  if (respBody.content && Array.isArray(respBody.content)) {
    respHtml += `<div class="messages-list">`;
    for (const block of respBody.content) {
      if (block.type === 'text') {
        respHtml += renderTextMsg('assistant', block.text);
      } else if (block.type === 'tool_use') {
        respHtml += renderToolUse(block);
      } else if (block.type === 'thinking') {
        respHtml += renderThinking(block.thinking || '');
      } else if (block.type === 'redacted_thinking') {
        respHtml += renderThinking('[redacted thinking]');
      } else {
        respHtml += renderTextMsg('assistant', JSON.stringify(block));
      }
    }
    respHtml += `</div>`;
  }

  const inTok = usage.input_tokens || summary.in_tokens || 0;
  const outTok = usage.output_tokens || summary.out_tokens || 0;
  const cacheCreate = usage.cache_creation_input_tokens || summary.cache_create || 0;
  const cacheRead = usage.cache_read_input_tokens || summary.cache_read || 0;

  if (inTok || outTok || cacheCreate || cacheRead) {
    respHtml += `
      <div class="response-footer">
        <span><span class="label">in:</span> <span class="tok-in">${fmtNum(inTok)}</span></span>
        <span><span class="label">out:</span> <span class="tok-out">${fmtNum(outTok)}</span></span>
        <span><span class="label">cache_create:</span> <span class="tok-cache">${fmtNum(cacheCreate)}</span></span>
        <span><span class="label">cache_read:</span> <span class="tok-cache">${fmtNum(cacheRead)}</span></span>
      </div>`;
  }

  body.innerHTML = `
    <div class="split-view">
      <div class="split-pane split-request">${reqHtml}</div>
      <div class="split-pane split-response">${respHtml}</div>
    </div>`;

  body.querySelectorAll('.msg-content, .tool-param, .system-item').forEach(contentEl => {
    const lines = parseInt(contentEl.dataset.lines || '0', 10);
    const threshold = contentEl.classList.contains('tool-param') ? 5 : 10;
    if (lines > threshold) makeCollapsible(contentEl, threshold);
  });
}

// ── Record Grouping (对话 vs 子请求) ─────────────────────────

const SYS_LEN_THRESHOLD = 5000; // system prompt > 5000 chars = 主对话

function classifyRecord(s) {
  // 主对话：有长 system prompt（完整的 Claude Code 指令）
  // 子请求：短 system prompt（工具内部调用、摘要等）
  return s.sys_len > SYS_LEN_THRESHOLD ? 'main' : 'sub';
}

function groupRecords(summaries) {
  const groups = [];
  let currentGroup = null;

  for (const s of summaries) {
    const type = classifyRecord(s);

    if (!currentGroup || currentGroup.type !== type) {
      // 开始新组
      currentGroup = {
        type,
        records: [s],
        startTime: s.timestamp,
        startId: s.id,
      };
      groups.push(currentGroup);
    } else {
      currentGroup.records.push(s);
    }
  }

  return groups;
}

function renderGroupHeader(group) {
  const el = document.createElement('div');
  el.className = `group-header group-${group.type}`;

  const count = group.records.length;
  const timeStr = fmtTime(group.startTime);

  if (group.type === 'main') {
    el.innerHTML = `<span class="group-icon">💬</span> <span class="group-title">主对话</span> <span class="group-meta">${count} 次请求 · 起始 ${timeStr}</span>`;
  } else {
    el.innerHTML = `<span class="group-icon">⚡</span> <span class="group-title">子请求</span> <span class="group-meta">${count} 次 · 起始 ${timeStr} · 工具调用 / 摘要等内部请求</span>`;
  }

  return el;
}

// ── Stats ─────────────────────────────────────────────────────

let totalReqs = 0, totalIn = 0, totalOut = 0;

function updateStats() {
  document.getElementById('stats').textContent =
    `${totalReqs} 请求 · ${fmtNum(totalIn)} in · ${fmtNum(totalOut)} out`;
}

function accStatsSummary(s) {
  totalReqs++;
  totalIn  += s.in_tokens  || 0;
  totalOut += s.out_tokens || 0;
  updateStats();
}

// ── Main Entry ────────────────────────────────────────────────

async function main() {
  // Init theme button icon
  setTheme(getTheme());

  // Theme toggle click
  document.getElementById('theme-toggle').addEventListener('click', cycleTheme);

  // Setup search & keyboard
  setupSearch();
  setupKeyboard();

  // 1. Fetch info
  const infoResp = await fetch('api/info');
  if (!infoResp.ok) throw new Error(`api/info failed: ${infoResp.status}`);
  const info = await infoResp.json();
  document.getElementById('filename').textContent = info.filename;

  const badge = document.getElementById('mode-badge');
  if (info.mode === 'live') {
    badge.textContent = 'LIVE';
    badge.className = 'badge live';
  } else {
    badge.textContent = '回顾';
    badge.className = 'badge view';
  }

  // 2. Load summaries
  const list = document.getElementById('records-list');
  const recsResp = await fetch('api/records');
  if (!recsResp.ok) throw new Error(`api/records failed: ${recsResp.status}`);
  const summaries = await recsResp.json();

  if (!summaries || summaries.length === 0) {
    list.innerHTML = '<div class="empty-state"><div class="emoji">📋</div>暂无记录</div>';
  } else {
    const groups = groupRecords(summaries);
    for (const group of groups) {
      list.appendChild(renderGroupHeader(group));
      for (const s of group.records) {
        list.appendChild(renderSummaryRecord(s, false));
        accStatsSummary(s);
      }
    }
  }

  // 3. SSE for live mode
  if (info.mode === 'live') {
    const es = new EventSource('api/stream');
    es.addEventListener('record', e => {
      const rec = JSON.parse(e.data);
      const summary = {
        id: rec.id,
        timestamp: rec.timestamp,
        duration_ms: rec.duration_ms,
        model: rec.request?.body?.model || '',
        msg_count: rec.request?.body?.messages?.length || 0,
        sys_len: JSON.stringify(rec.request?.body?.system || '').length,
        stop_reason: rec.response?.body?.stop_reason || '',
        in_tokens: rec.response?.body?.usage?.input_tokens || 0,
        out_tokens: rec.response?.body?.usage?.output_tokens || 0,
        cache_read: rec.response?.body?.usage?.cache_read_input_tokens || 0,
        cache_create: rec.response?.body?.usage?.cache_creation_input_tokens || 0,
      };
      // Remove empty state if present
      const empty = list.querySelector('.empty-state');
      if (empty) empty.remove();

      const el = renderSummaryRecord(summary, true);
      el._fullRec = rec;
      list.appendChild(el);
      accStatsSummary(summary);
      el.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    });
    es.onerror = () => {
      badge.textContent = '连接中断';
      badge.className = 'badge view';
    };
    es.onopen = () => {
      badge.textContent = 'LIVE';
      badge.className = 'badge live';
    };
  }
}

main().catch(e => {
  console.error(e);
  document.getElementById('records-list').innerHTML =
    `<div class="empty-state"><div class="emoji">⚠️</div>加载失败: ${escHtml(e.message)}</div>`;
});
