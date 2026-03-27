// ── 工具函数 ──────────────────────────────────────────────
function fmtTime(ts) {
  const d = new Date(ts);
  return d.toLocaleTimeString('zh-CN', { hour12: false });
}

function fmtDuration(ms) {
  return ms < 1000 ? `${ms}ms` : `${(ms / 1000).toFixed(1)}s`;
}

function fmtNum(n) {
  if (!n) return '0';
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
    ? c.map(x => x.text || JSON.stringify(x)).join('\n')
    : (typeof c === 'string' ? c : JSON.stringify(c));
  return `
    <div class="msg">
      <span class="role-tag role-result">result</span>
      <div class="msg-content" data-lines="${text.split('\n').length}">${escHtml(text)}</div>
    </div>`;
}

// ── 渲染单条记录 ──────────────────────────────────────────
function renderRecord(rec, isNew) {
  const el = document.createElement('div');
  el.className = 'record' + (isNew ? ' new' : '');
  el.dataset.id = rec.id;

  // 解析请求体
  const reqBody = rec.request?.body || {};
  const messages = reqBody.messages || [];
  const respBody = rec.response?.body || {};
  const usage = respBody.usage || {};
  const stopReason = respBody.stop_reason || '—';

  const timeStr = fmtTime(rec.timestamp);
  const durStr = fmtDuration(rec.duration_ms);
  const inTok = fmtNum(usage.input_tokens);
  const outTok = fmtNum(usage.output_tokens);
  const seqNum = rec.id; // e.g. "req_001"

  el.innerHTML = `
    <div class="record-header" onclick="toggleRecord(this)">
      <div class="record-header-left">
        <span class="req-id">${escHtml(seqNum)}</span>
        <span class="req-time">${timeStr}</span>
        <span class="req-duration">${durStr}</span>
        <span class="req-tokens">${inTok} in / ${outTok} out</span>
        <span class="req-stop">${escHtml(stopReason)}</span>
      </div>
      <span class="expand-icon">▼</span>
    </div>
    <div class="record-body"></div>`;

  // 懒渲染：展开时才填充 body
  el._rec = rec;
  el._rendered = false;

  return el;
}

function toggleRecord(headerEl) {
  const el = headerEl.closest('.record');
  el.classList.toggle('expanded');

  if (el.classList.contains('expanded') && !el._rendered) {
    el._rendered = true;
    const body = el.querySelector('.record-body');
    const rec = el._rec;
    const reqBody = rec.request?.body || {};
    const messages = reqBody.messages || [];
    const respBody = rec.response?.body || {};
    const usage = respBody.usage || {};

    let html = '';
    for (const msg of messages) {
      html += renderMessage(msg);
    }

    // 响应 footer
    const cacheCreate = fmtNum(usage.cache_creation_input_tokens);
    const cacheRead = fmtNum(usage.cache_read_input_tokens);
    html += `
      <div class="response-footer">
        <span><span class="label">stop:</span> <span class="val">${escHtml(respBody.stop_reason || '—')}</span></span>
        <span><span class="label">dur:</span> <span class="val">${fmtDuration(rec.duration_ms)}</span></span>
        <span><span class="label">in:</span> <span class="tok-in">${fmtNum(usage.input_tokens)}</span></span>
        <span><span class="label">out:</span> <span class="tok-out">${fmtNum(usage.output_tokens)}</span></span>
        <span><span class="label">cache_create:</span> <span class="tok-cache">${cacheCreate}</span></span>
        <span><span class="label">cache_read:</span> <span class="tok-cache">${cacheRead}</span></span>
      </div>`;

    body.innerHTML = html;

    // 对超长内容启用折叠
    body.querySelectorAll('.msg-content, .tool-param').forEach(el => {
      const lines = parseInt(el.dataset.lines || '0', 10);
      const threshold = el.classList.contains('tool-param') ? 5 : 10;
      if (lines > threshold) makeCollapsible(el, threshold);
    });
  }
}

// ── 统计更新 ──────────────────────────────────────────────
let totalReqs = 0, totalIn = 0, totalOut = 0;

function updateStats() {
  document.getElementById('stats').textContent =
    `${totalReqs} 请求 · ${fmtNum(totalIn)} in · ${fmtNum(totalOut)} out tokens`;
}

function accStats(rec) {
  totalReqs++;
  const u = rec.response?.body?.usage || {};
  totalIn  += u.input_tokens  || 0;
  totalOut += u.output_tokens || 0;
  updateStats();
}

// ── 主入口 ────────────────────────────────────────────────
async function main() {
  // 1. 获取模式信息
  const info = await fetch('/api/info').then(r => r.json());
  document.getElementById('filename').textContent = info.filename;

  const badge = document.getElementById('mode-badge');
  if (info.mode === 'live') {
    badge.textContent = 'live';
    badge.className = 'badge live';
  } else {
    badge.textContent = '回顾';
    badge.className = 'badge view';
  }

  // 2. 加载已有记录
  const list = document.getElementById('records-list');
  const records = await fetch('/api/records').then(r => r.json());
  for (const rec of (records || [])) {
    list.appendChild(renderRecord(rec, false));
    accStats(rec);
  }

  // 3. 实时模式：连接 SSE
  if (info.mode === 'live') {
    const es = new EventSource('/api/stream');
    es.addEventListener('record', e => {
      const rec = JSON.parse(e.data);
      const el = renderRecord(rec, true);
      list.appendChild(el);
      accStats(rec);
      el.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    });
  }
}

main().catch(console.error);
