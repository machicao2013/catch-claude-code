// ── 工具函数 ──────────────────────────────────────────────
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

// ── 折叠/展开长内容 ─────────────────────────────────────────
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
  btn.textContent = '▼ 点击展开全文';
  btn.onclick = () => {
    const collapsed = wrapper.classList.toggle('collapsed');
    btn.textContent = collapsed ? '▼ 点击展开全文' : '▲ 收起';
  };
  wrapper.appendChild(inner);
  wrapper.appendChild(btn);
  contentEl.replaceWith(wrapper);
}

// ── 渲染单条 message ──────────────────────────────────────
function renderMessage(msg) {
  const role = msg.role || 'unknown';
  const content = msg.content;

  // content 可能是字符串或数组
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
    } else {
      frags.push(renderTextMsg(role, JSON.stringify(block)));
    }
  }
  return frags.join('');
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

// ── 从摘要数据渲染折叠行 ──────────────────────────────────
// summary 格式: { id, timestamp, duration_ms, model, msg_count, stop_reason, in_tokens, out_tokens, cache_read, cache_create }
function renderSummaryRecord(summary, isNew) {
  const el = document.createElement('div');
  el.className = 'record' + (isNew ? ' new' : '');
  el.dataset.id = summary.id;

  const timeStr = fmtTime(summary.timestamp);
  const durStr = fmtDuration(summary.duration_ms);
  const inTok = fmtNum(summary.in_tokens);
  const outTok = fmtNum(summary.out_tokens);
  const model = summary.model || '—';
  const stop = summary.stop_reason || '—';

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
    <div class="record-body"></div>`;

  el._summary = summary;
  el._rendered = false;

  return el;
}

// ── 展开时异步加载完整记录 ────────────────────────────────
async function toggleRecord(headerEl) {
  const el = headerEl.closest('.record');
  el.classList.toggle('expanded');

  if (el.classList.contains('expanded') && !el._rendered) {
    el._rendered = true;
    const body = el.querySelector('.record-body');
    const summary = el._summary;

    // 如果已有完整数据（SSE 实时推送的），直接用
    if (el._fullRec) {
      renderRecordBody(body, el._fullRec, summary);
      return;
    }

    // 否则异步加载
    body.innerHTML = '<div style="color:var(--muted);padding:8px;">加载中...</div>';

    try {
      const resp = await fetch(`api/records/${encodeURIComponent(summary.id)}`);
      if (!resp.ok) throw new Error(`${resp.status}`);
      const rec = await resp.json();
      renderRecordBody(body, rec, summary);
    } catch (e) {
      body.innerHTML = `<div style="color:var(--user);padding:8px;">加载失败: ${escHtml(e.message)}</div>`;
      el._rendered = false; // 允许重试
    }
  }
}

function renderRecordBody(body, rec, summary) {
  const reqBody = rec.request?.body || {};
  const messages = reqBody.messages || [];
  const respBody = rec.response?.body || {};
  const usage = respBody.usage || {};

  let html = '';
  for (const msg of messages) {
    html += renderMessage(msg);
  }

  // 响应 footer
  const cacheCreate = fmtNum(usage.cache_creation_input_tokens || summary.cache_create);
  const cacheRead = fmtNum(usage.cache_read_input_tokens || summary.cache_read);
  html += `
    <div class="response-footer">
      <span><span class="label">stop:</span> <span class="val">${escHtml(respBody.stop_reason || summary.stop_reason || '—')}</span></span>
      <span><span class="label">dur:</span> <span class="val">${fmtDuration(rec.duration_ms)}</span></span>
      <span><span class="label">in:</span> <span class="tok-in">${fmtNum(usage.input_tokens || summary.in_tokens)}</span></span>
      <span><span class="label">out:</span> <span class="tok-out">${fmtNum(usage.output_tokens || summary.out_tokens)}</span></span>
      <span><span class="label">cache_create:</span> <span class="tok-cache">${cacheCreate}</span></span>
      <span><span class="label">cache_read:</span> <span class="tok-cache">${cacheRead}</span></span>
    </div>`;

  body.innerHTML = html;

  // 对超长内容启用折叠
  body.querySelectorAll('.msg-content, .tool-param').forEach(contentEl => {
    const lines = parseInt(contentEl.dataset.lines || '0', 10);
    const threshold = contentEl.classList.contains('tool-param') ? 5 : 10;
    if (lines > threshold) makeCollapsible(contentEl, threshold);
  });
}

// ── 统计更新 ──────────────────────────────────────────────
let totalReqs = 0, totalIn = 0, totalOut = 0;

function updateStats() {
  document.getElementById('stats').textContent =
    `${totalReqs} 请求 · ${fmtNum(totalIn)} in · ${fmtNum(totalOut)} out tokens`;
}

function accStatsSummary(s) {
  totalReqs++;
  totalIn  += s.in_tokens  || 0;
  totalOut += s.out_tokens || 0;
  updateStats();
}

// ── 主入口 ────────────────────────────────────────────────
async function main() {
  // 1. 获取模式信息
  const infoResp = await fetch('api/info');
  if (!infoResp.ok) throw new Error(`api/info failed: ${infoResp.status}`);
  const info = await infoResp.json();
  document.getElementById('filename').textContent = info.filename;

  const badge = document.getElementById('mode-badge');
  if (info.mode === 'live') {
    badge.textContent = 'live';
    badge.className = 'badge live';
  } else {
    badge.textContent = '回顾';
    badge.className = 'badge view';
  }

  // 2. 加载摘要列表（轻量，几十 KB 而非几十 MB）
  const list = document.getElementById('records-list');
  const recsResp = await fetch('api/records');
  if (!recsResp.ok) throw new Error(`api/records failed: ${recsResp.status}`);
  const summaries = await recsResp.json();
  for (const s of (summaries || [])) {
    list.appendChild(renderSummaryRecord(s, false));
    accStatsSummary(s);
  }

  // 3. 实时模式：连接 SSE
  if (info.mode === 'live') {
    const es = new EventSource('api/stream');
    es.addEventListener('record', e => {
      const rec = JSON.parse(e.data);
      // SSE 推送的是完整记录，提取摘要用于渲染折叠行
      const summary = {
        id: rec.id,
        timestamp: rec.timestamp,
        duration_ms: rec.duration_ms,
        stop_reason: rec.response?.body?.stop_reason || '—',
        in_tokens: rec.response?.body?.usage?.input_tokens || 0,
        out_tokens: rec.response?.body?.usage?.output_tokens || 0,
        cache_read: rec.response?.body?.usage?.cache_read_input_tokens || 0,
        cache_create: rec.response?.body?.usage?.cache_creation_input_tokens || 0,
      };
      const el = renderSummaryRecord(summary, true);
      el._fullRec = rec; // 缓存完整数据，展开时不需要再请求
      list.appendChild(el);
      accStatsSummary(summary);
      el.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    });
    es.onerror = () => {
      const badge = document.getElementById('mode-badge');
      badge.textContent = '连接中断';
      badge.className = 'badge view';
    };
    es.onopen = () => {
      const badge = document.getElementById('mode-badge');
      badge.textContent = 'live';
      badge.className = 'badge live';
    };
  }
}

main().catch(e => {
  console.error(e);
  document.getElementById('records-list').innerHTML =
    `<div style="color:var(--user);padding:20px;">加载失败: ${e.message}</div>`;
});
