package main

// windowHTML is the updates window UI. Self-contained (no network), styled
// after the "Updater macOS App" Claude Design mockups.
const windowHTML = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<style>
  :root { color-scheme: light dark;
    --bg: #f5f5f7; --fg: #1d1d1f; --muted: rgba(60,60,67,.6);
    --card: #fff; --line: rgba(0,0,0,.08); --accent: #3478F6;
    --ok: #34c759; --warn: #ff9500; --err: #ff3b30; }
  @media (prefers-color-scheme: dark) { :root {
    --bg: #1e1e20; --fg: #f5f5f7; --muted: rgba(235,235,245,.55);
    --card: #2a2a2c; --line: rgba(255,255,255,.10); } }
  * { box-sizing: border-box; }
  body { margin: 0; font: 13px/1.45 -apple-system, "SF Pro Text", sans-serif;
         background: var(--bg); color: var(--fg); user-select: none; }

  header { position: sticky; top: 0; z-index: 2; background: var(--bg);
           padding: 14px 18px 10px; border-bottom: 1px solid var(--line); }
  .bar { display: flex; align-items: center; gap: 12px; }
  .status { font-size: 15px; font-weight: 600; }
  .sub { color: var(--muted); font-size: 12px; margin-top: 1px; }
  .spacer { flex: 1; }
  button { font: inherit; border: 1px solid var(--line); border-radius: 7px;
           background: var(--card); color: var(--fg); padding: 4px 12px; cursor: pointer; }
  button:hover { filter: brightness(1.05); }
  button.primary { background: var(--accent); border-color: var(--accent); color: #fff; font-weight: 600; }
  button:disabled { opacity: .45; cursor: default; }
  .progress { height: 3px; background: var(--line); border-radius: 2px; margin-top: 10px;
              overflow: hidden; visibility: hidden; }
  .progress.on { visibility: visible; }
  .progress > div { height: 100%; width: 0%; background: var(--accent); transition: width .3s; }

  main { padding: 10px 18px 20px; }
  h2 { font-size: 11px; text-transform: uppercase; letter-spacing: .05em;
       color: var(--muted); margin: 18px 4px 6px; }
  .card { background: var(--card); border: 1px solid var(--line); border-radius: 10px; overflow: hidden; }
  .row { display: flex; align-items: center; gap: 10px; padding: 8px 14px;
         border-bottom: 1px solid var(--line); }
  .row:last-child { border-bottom: none; }
  .name { font-weight: 500; min-width: 180px; }
  .ver { color: var(--muted); font-variant-numeric: tabular-nums; }
  .ver b { color: var(--fg); font-weight: 600; }
  .src { color: var(--muted); font-size: 11px; background: var(--line);
         border-radius: 5px; padding: 1px 7px; }
  .state { margin-left: auto; display: flex; align-items: center; gap: 8px; }
  .dot { font-size: 12px; }
  .dot.ok { color: var(--ok); } .dot.err { color: var(--err); } .dot.pin { color: var(--warn); }
  .mini { font-size: 11px; color: var(--muted); background: none; border: none;
          padding: 2px 4px; cursor: pointer; }
  .mini:hover { color: var(--accent); }
  .msg { font-size: 12px; color: var(--muted); }
  .msg.err { color: var(--err); }
  .empty { padding: 26px; text-align: center; color: var(--muted); }
</style>
</head>
<body>
<header>
  <div class="bar">
    <div>
      <div class="status" id="status">Loading…</div>
      <div class="sub" id="sub"></div>
    </div>
    <div class="spacer"></div>
    <button class="primary" id="updateAll" style="display:none">Update All</button>
    <button id="refresh">Refresh</button>
  </div>
  <div class="progress" id="progress"><div id="progressFill"></div></div>
</header>
<main>
  <div id="updates"></div>
  <div id="others"></div>
</main>
<script>
let entries = [];
let checking = false;
let busy = {};      // bundleId -> true while updating
let rowMsg = {};    // bundleId -> {ok, message}

function esc(s) { return String(s).replace(/[&<>"]/g, c => ({"&":"&amp;","<":"&lt;",">":"&gt;","\"":"&quot;"}[c])); }

function render() {
  const ups = entries.filter(e => e.status === "update_available");
  const others = entries.filter(e => e.status !== "update_available");

  const st = document.getElementById("status");
  if (checking) st.textContent = "Checking for updates…";
  else if (ups.length) st.textContent = ups.length + " update" + (ups.length > 1 ? "s" : "") + " available";
  else st.textContent = "✓ Everything up to date";

  const all = document.getElementById("updateAll");
  all.style.display = ups.length > 1 && !checking ? "" : "none";
  all.textContent = "Update All (" + ups.length + ")";
  document.getElementById("refresh").disabled = checking;

  const row = e => {
    const m = rowMsg[e.bundle_id];
    let stateHtml;
    if (busy[e.bundle_id]) stateHtml = '<span class="msg">Updating…</span>';
    else if (m) stateHtml = '<span class="msg ' + (m.ok ? "" : "err") + '">' + esc(m.message) + '</span>';
    else if (e.status === "update_available")
      stateHtml = '<button onclick="doUpdate(\'' + esc(e.bundle_id) + '\')" ' + (checking ? "disabled" : "") + '>Update</button>' +
        '<button class="mini" onclick="doPin(\'' + esc(e.bundle_id) + '\')" title="Skip this update">Pin</button>' +
        '<button class="mini" onclick="doIgnore(\'' + esc(e.bundle_id) + '\')" title="Stop checking this app">Ignore</button>';
    else if (e.status === "pinned")
      stateHtml = '<span class="dot pin">●</span><span class="msg">pinned</span>' +
        '<button class="mini" onclick="doUnpin(\'' + esc(e.bundle_id) + '\')">Unpin</button>';
    else if (e.status === "error")
      stateHtml = '<span class="dot err">●</span><span class="msg err" title="' + esc(e.error || "") + '">check failed</span>';
    else stateHtml = '<span class="dot ok">●</span>';

    const ver = e.status === "update_available" || e.status === "pinned"
      ? esc(e.current_version) + ' → <b>' + esc(e.latest_version) + '</b>'
      : esc(e.current_version);
    return '<div class="row"><span class="name">' + esc(e.name) + '</span>' +
      '<span class="ver">' + ver + '</span><span class="src">' + esc(e.source) + '</span>' +
      '<span class="state">' + stateHtml + '</span></div>';
  };

  const upsEl = document.getElementById("updates");
  upsEl.innerHTML = ups.length
    ? '<h2>Updates</h2><div class="card">' + ups.map(row).join("") + '</div>'
    : "";
  const othersEl = document.getElementById("others");
  othersEl.innerHTML = others.length
    ? '<h2>Up to date · ' + others.length + '</h2><div class="card">' + others.map(row).join("") + '</div>'
    : (ups.length ? "" : '<div class="empty">No apps discovered yet — refresh to scan.</div>');
}

function setSub(text) { document.getElementById("sub").textContent = text; }

function onProgress(p) {
  checking = true;
  const bar = document.getElementById("progress");
  bar.classList.add("on");
  document.getElementById("progressFill").style.width =
    p.total ? Math.round(100 * p.done / p.total) + "%" : "0%";
  setSub(p.done + " of " + p.total + " checked");
  render();
}

function onResults(r) {
  checking = false;
  rowMsg = {};
  entries = r.entries || [];
  document.getElementById("progress").classList.remove("on");
  setSub(entries.length + " apps · checked " + r.checkedAt);
  render();
}

function onError(msg) {
  checking = false;
  document.getElementById("progress").classList.remove("on");
  document.getElementById("status").textContent = "Check failed";
  setSub(msg);
}

function onUpdateDone(r) {
  delete busy[r.bundleId];
  rowMsg[r.bundleId] = r;
  if (r.ok) {
    const e = entries.find(x => x.bundle_id === r.bundleId);
    if (e) e.status = "ok";
  }
  render();
}

function doUpdate(id) { busy[id] = true; render(); goUpdate(id); }
function doIgnore(id) { goIgnore(id); entries = entries.filter(e => e.bundle_id !== id); render(); }
function doPin(id) {
  goPin(id);
  const e = entries.find(x => x.bundle_id === id);
  if (e) e.status = "pinned";
  render();
}
function doUnpin(id) {
  goUnpin(id);
  const e = entries.find(x => x.bundle_id === id);
  if (e) e.status = "update_available";
  render();
}

document.getElementById("refresh").onclick = () => { if (!checking) { checking = true; render(); goRefresh(); } };
document.getElementById("updateAll").onclick = () => {
  entries.filter(e => e.status === "update_available").forEach(e => { busy[e.bundle_id] = true; goUpdate(e.bundle_id); });
  render();
};

goInit().then(r => {
  entries = r.entries || [];
  if (r.checkedAt) setSub(entries.length + " apps · checked " + r.checkedAt);
  render();
  checking = true; render(); goRefresh(); // always refresh on open
});
</script>
</body>
</html>`
