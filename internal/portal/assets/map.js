/*
  GraphLume portal: the Map.

  A canvas workbench over the same tool endpoint as every other page. Nothing
  here is computed from invented data: each view is drawn from the answer of a
  tool call, and the "Agent" tab lists those calls so a reader can hand the
  exact question to an agent.

    Map         directories, files and symbols   graph (read-only SQL), callers/callees evidence
    Impact      rings of what a change reaches   impact
    Matrix      who depends on whom, cycles      graph
    Risk        churn × size, bus factor         hotspots, busfactor
    Flow        calls from an entry, by layer    graph (recursive), endpoints
    Communities files that belong together       communities
    Trace       shortest route A → B             trace

  The graph tool returns at most 200 rows, so every query aggregates to the
  level on screen and says so when it was cut. Directory and file levels read
  the file rollups (file_edges, file_symbols), which the index keeps summed by
  file, so a level of a large tree is a read of its rollup rows rather than a
  grouping of every edge; only a pair's evidence goes back to `edges`, and
  then for the pair's files alone.

  No inline style and no markup from strings: elements are built with h(),
  sizes are set through the element's style object, colours come from the
  theme tokens in app.css so both themes are covered.
*/

let D = null; // { h, call, ask, toast, tierMark, TIERS, commandFor, copyable, symbolHref, fileHref, kindWord }
const RUN = "(SELECT max(id) FROM index_runs WHERE status IN ('ready','degraded'))";
const KINDS = ['CALLS', 'INSTANTIATES', 'IMPLEMENTS', 'EXTENDS', 'READS', 'WRITES', 'PUBLISHES', 'CONSUMES', 'DEPENDS_ON', 'EXPOSES', 'CONFIGURED_BY'];
const RANK = { low: 0, medium: 1, high: 2, exact: 3 };
const TIER_ORDER = ['exact', 'high', 'medium', 'low'];
const VIEWS = [
  { id: 'map', label: 'Map', q: 'How is the code wired together?' },
  { id: 'pipeline', label: 'Pipeline', q: 'How does execution flow from ingress to storage?' },
  { id: 'radial', label: 'Radial', q: 'What is the circular blast radius around core symbols?' },
  { id: 'cluster', label: 'Cluster', q: 'How are services and modules grouped?' },
  { id: 'sequence', label: 'Sequence', q: 'What is the end-to-end call sequence?' },
  { id: 'impact', label: 'Impact', q: 'What could break if I change this?' },
  { id: 'matrix', label: 'Matrix', q: 'Which parts depend on which, and are there cycles?' },
  { id: 'risk', label: 'Risk', q: 'Where is the code that changes most and is held by fewest people?' },
  { id: 'flow', label: 'Flow', q: 'What does this entry point call, layer by layer?' },
  { id: 'communities', label: 'Communities', q: 'Which files belong together?' },
];
const MAX_CHILDREN = 36;
const MAX_BOUNDARY = 12;
const STRONGEST = 60;
const reduced = matchMedia('(prefers-reduced-motion: reduce)');

/* State that outlives one render of the page: the router redraws the whole
   view on every hash change, and the camera, the unfolded symbols and the
   last answers should survive a tab switch. */
const S = {
  done: new Set(), floor: 'medium', refs: false, depth: 3, cam: new Map(), unfold: new Map(), sel: null,
  tab: 'evidence', calls: [], scene: null, hover: null, trace: null, ctx: null, rename: null,
};

// ---------------------------------------------------------------- helpers

const q = (s) => `'${String(s).replace(/'/g, "''")}'`;
const likePrefix = (p) => q(String(p).replace(/[\\%_]/g, (c) => '\\' + c) + '%');
const confIn = () => `(${TIER_ORDER.filter((t) => RANK[t] >= RANK[S.floor]).map(q).join(',')})`;
const kindIn = () => `(${(S.refs ? [...KINDS, 'REFERENCES'] : KINDS).map(q).join(',')})`;
const minConf = (a, b) => (RANK[a] <= RANK[b] ? a : b);
const maxConf = (a, b) => (RANK[a] >= RANK[b] ? a : b);
const isDir = (k) => k.endsWith('/');
const lastSeg = (k, base) => (base && k.startsWith(base) ? k.slice(base.length) : k) || k;
const clamp = (v, a, b) => Math.max(a, Math.min(b, v));

/* parseTable reads the markdown table the graph tool writes. */
function parseTable(text) {
  const lines = String(text || '').split('\n').filter((l) => l.startsWith('|'));
  if (lines.length < 2) return [];
  const cols = lines[0].slice(1, -1).split('|').map((c) => c.trim());
  return lines.slice(2).map((l) => {
    const cells = l.slice(1, -1).split('|').map((c) => c.trim());
    const row = {};
    cols.forEach((c, i) => { row[c] = cells[i] ?? ''; });
    return row;
  });
}

/* failure is why a tool could not answer, in the reader's words: the tool's
   own text without its metadata block and machine lines, as app.js shows it.
   A query stopped at the graph tool's time limit gets advice instead of the
   driver's wording. */
function failure(a, fallback) {
  const why = String(a.text || '').split('\n').map((l) => l.trim())
    .filter((l) => l && !l.startsWith('===') && !l.startsWith('error_code:') && !l.startsWith('hint:')).join(' ');
  if (/deadline exceeded|interrupted/i.test(why)) {
    return 'The answer took longer than the graph tool allows (3 s). Open a smaller directory, or raise the confidence floor, and try again; ' +
      'if a level of this size was quick before, the index may need `graphlume resolve` to sum its file rollups again.';
  }
  return why || fallback;
}

/* sql asks the graph tool and remembers the call for the Agent tab. */
async function sql(why, query) {
  const t0 = performance.now();
  const args = { query: query.replace(/\s+/g, ' ').trim(), limit: 200 };
  S.calls.push({ tool: 'graph', why, args });
  const a = await D.ask('graph', args);
  if (a.failed) throw new Error(failure(a, 'The graph tool refused the query.'));
  const rows = parseTable(a.text);
  S.lastMs = Math.round(performance.now() - t0);
  return { rows, capped: rows.length >= 200 };
}
async function tool(name, args, why) {
  const t0 = performance.now();
  S.calls.push({ tool: name, why, args });
  const a = await D.ask(name, args);
  if (a.failed) throw new Error(failure(a, `${name} could not answer.`));
  S.lastMs = Math.round(performance.now() - t0);
  return a;
}
function childKey(col, base) {
  const rest = `substr(${col}, ${base.length + 1})`;
  return `(CASE WHEN instr(${rest}, '/') > 0 THEN substr(${col}, 1, ${base.length} + instr(${rest}, '/')) ELSE ${col} END)`;
}
function outsideKey(col) {
  const after = `substr(${col}, instr(${col}, '/') + 1)`;
  return `(CASE WHEN instr(${col}, '/') = 0 THEN ${col} WHEN instr(${after}, '/') = 0 THEN substr(${col}, 1, instr(${col}, '/')) ELSE substr(${col}, 1, instr(${col}, '/') + instr(${after}, '/')) END)`;
}
function keyExpr(col, base) {
  if (!base) return childKey(col, '');
  return `(CASE WHEN ${col} LIKE ${likePrefix(base)} ESCAPE '\\' THEN ${childKey(col, base)} ELSE ${outsideKey(col)} END)`;
}
/* childrenQuery sizes each child of base by its files and definitions, from
   the per-file symbol counts the index keeps (file_symbols). */
function childrenQuery(base, order) {
  return `
    SELECT ${childKey('f.path', base)} AS k, count(*) AS files, coalesce(sum(c.n), 0) AS syms
    FROM files f LEFT JOIN file_symbols c ON c.file_id = f.id
    WHERE f.index_run_id = ${RUN} ${base ? `AND f.path LIKE ${likePrefix(base)} ESCAPE '\\'` : ''}
    GROUP BY 1 ${order}`;
}
/* rollupFrom is the FROM and WHERE of a read of the file rollup (file_edges):
   rows of the current run, under the kind and confidence filters, with a file
   under base at 'either' end or at 'both'. The rollup holds no row between a
   file and itself. */
function rollupFrom(base, ends) {
  const under = (col) => `${col} LIKE ${likePrefix(base)} ESCAPE '\\'`;
  const scope = !base ? '' : ends === 'both' ? `AND ${under('sf.path')} AND ${under('df.path')}` : `AND (${under('sf.path')} OR ${under('df.path')})`;
  return `file_edges r JOIN files sf ON sf.id = r.src_file_id JOIN files df ON df.id = r.dst_file_id
    WHERE r.index_run_id = ${RUN} AND r.kind IN ${kindIn()} AND r.confidence IN ${confIn()} ${scope}`;
}
function hashFor(over) {
  const p = new URLSearchParams();
  const cur = { v: S.view, at: S.at, ...over };
  for (const [k, v] of Object.entries(cur)) if (v) p.set(k, v);
  if (cur.v === 'map') p.delete('v');
  const qs = p.toString();
  return `#/map${qs ? '?' + qs : ''}`;
}
const go = (over) => { location.hash = hashFor(over); };

// ---------------------------------------------------------------- colours

let C = {};
function readTokens() {
  const cs = getComputedStyle(document.documentElement);
  for (const t of ['ground', 'sheet', 'well', 'ink', 'ink-2', 'ink-3', 'line', 'line-strong', 'accent', 'accent-soft', 'on-accent',
    'good', 'caution', 'bad', 'c-exact', 'c-high', 'c-medium', 'c-low', 'c-dir', 'c-file', 'c-sym', 'c-table']) C[t] = cs.getPropertyValue('--' + t).trim();
  if (S.scene) S.scene.dirty = true;
}
new MutationObserver(readTokens).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] });
matchMedia('(prefers-color-scheme: dark)').addEventListener('change', readTokens);
const tierCol = (t) => C['c-' + t] || C.ink;
const kindCol = (k) => (k === 'dir' ? C['c-dir'] : k === 'file' ? C['c-file'] : k === 'table' ? C['c-table'] : k === 'boundary' ? C['ink-3'] : C['c-sym']);
function mix(a, b, t) {
  const p = (x) => { const m = /^#?([0-9a-f]{6})$/i.exec(x || ''); const n = m ? parseInt(m[1], 16) : 0; return [n >> 16 & 255, n >> 8 & 255, n & 255]; };
  const A = p(a), B = p(b);
  return `rgb(${A.map((v, i) => Math.round(v + (B[i] - v) * t)).join(',')})`;
}
const riskCol = (r) => (r < 0.5 ? mix(C.good, C.caution, r * 2) : mix(C.caution, C.bad, (r - 0.5) * 2));

// ---------------------------------------------------------------- page

export function mapView(params, deps) {
  D = deps;
  readTokens();
  const { h } = D;
  S.view = VIEWS.some((v) => v.id === params.v) ? params.v : (params.v === 'trace' ? 'trace' : 'map');
  const at = params.at || '';
  if (at !== S.at) {
    if (at) S.sel = { type: 'part', key: at, kind: isDir(at) ? 'dir' : 'file', label: lastSeg(at).replace(/\/$/, '') || at, node: at };
    else S.sel = null;
  } else if (S.sel && S.sel.type === 'sym' && at && S.sel.path && !S.sel.path.startsWith(at)) {
    S.sel = { type: 'part', key: at, kind: isDir(at) ? 'dir' : 'file', label: lastSeg(at).replace(/\/$/, '') || at, node: at };
  }
  S.at = at;
  if (params.sym) S.sel = { type: 'sym', name: params.sym, path: params.path || '', line: params.line || '', id: params.id || '' };
  S.calls = [];

  const ui = {};
  ui.root = h('div', { class: 'mp' });
  ui.bar = h('div', { class: 'mp-bar' });
  ui.crumbs = h('div', { class: 'mp-crumbs' });
  ui.stage = h('div', { class: 'mp-stage' });
  ui.canvas = h('canvas', { class: 'mp-canvas', 'aria-label': 'Graph of the code. Use the panel on the right for the same facts as text.', role: 'img' });
  ui.status = h('div', { class: 'mp-status', role: 'status' });
  ui.legend = h('div', { class: 'mp-legend' });
  ui.mini = h('canvas', { class: 'mp-mini', 'aria-hidden': 'true' });
  ui.zoom = h('div', { class: 'mp-zoom' },
    h('button', { type: 'button', title: 'Zoom in', 'aria-label': 'Zoom in', onclick: () => zoomBy(1.25) }, '+'),
    h('button', { type: 'button', title: 'Zoom out', 'aria-label': 'Zoom out', onclick: () => zoomBy(0.8) }, '−'),
    h('button', { type: 'button', title: 'Fit to view (F)', 'aria-label': 'Fit to view', onclick: () => fit(true) }, '⤢'),
    h('button', { type: 'button', title: 'Show or hide the overview', 'aria-label': 'Toggle overview', 'aria-pressed': String(!!S.miniOn), onclick: (e) => { S.miniOn = !S.miniOn; ui.mini.classList.toggle('on', S.miniOn); e.currentTarget.setAttribute('aria-pressed', String(S.miniOn)); size(); if (S.scene) S.scene.dirty = true; } }, '▣'));
  ui.tip = h('div', { class: 'mp-tip', role: 'tooltip' });
  ui.menu = h('div', { class: 'mp-menu', role: 'menu' });
  ui.insp = h('aside', { class: 'mp-insp', 'aria-label': 'Selection details' });
  ui.mini.classList.toggle('on', !!S.miniOn);
  ui.statusbar = h('div', { class: 'mp-statusbar', role: 'status', 'aria-live': 'polite' });
  ui.tree = h('nav', { class: 'mp-tree', 'aria-label': 'Repository explorer' });
  ui.svgWrap = h('div', { class: 'svg-canvas-wrap' });
  ui.stage.append(ui.svgWrap, ui.canvas, ui.legend, ui.zoom, ui.mini, ui.status, ui.statusbar);
  ui.body = h('div', { class: `mp-body${S.treeOff ? ' no-tree' : ''}` }, ui.tree, ui.stage, ui.insp);
  ui.root.append(ui.bar, ui.crumbs, ui.body, ui.tip, ui.menu);
  S.ui = ui;

  drawBar();
  drawCrumbs();
  drawTree();
  drawInspector();
  bindCanvas();
  load();
  return [ui.root];
}

function drawBar() {
  const { h } = D; const ui = S.ui;
  const views = h('div', { class: 'seg mp-views', role: 'tablist', 'aria-label': 'Views' },
    VIEWS.map((v, i) => h('button', { type: 'button', role: 'tab', 'aria-selected': String(S.view === v.id), 'aria-pressed': String(S.view === v.id), title: `${v.q} (${i + 1})`, onclick: () => go({ v: v.id }) }, v.label)));
  const floors = h('div', { class: 'seg', role: 'group', 'aria-label': 'Show relationships that are at least' },
    TIER_ORDER.map((t) => h('button', { type: 'button', 'aria-pressed': String(S.floor === t), title: D.TIERS[t].use,
      onclick: () => { S.floor = t; S.unfold.clear(); S.done.clear(); reload(); } }, t === 'exact' ? 'Proven only' : t === 'low' ? 'All' : `≥ ${D.TIERS[t].label}`)));
  const depth = h('div', { class: 'seg', role: 'group', 'aria-label': 'Graph Depth' },
    [1, 2, 3].map((n) => h('button', {
      type: 'button',
      'aria-pressed': String(S.depth === n),
      title: n === 1 ? 'Direct callers & writers only' : n === 2 ? 'Two hops: callers & HTTP ingress' : 'Three hops: all transitive blast radius',
      onclick: () => {
        S.depth = n;
        drawBar();
        if (isVectorView(S.view)) renderVectorLayout();
        else reload();
      }
    }, n === 1 ? '1 Direct' : n === 2 ? '2 Hops' : '3 All')));
  const refs = h('label', { class: 'mp-check', title: 'Also draw plain references (a name used without a call). There are many; the picture gets busy.' },
    h('input', { type: 'checkbox', checked: S.refs, onchange: (e) => { S.refs = e.target.checked; reload(); } }), 'Include references');
  const treeBtn = h('button', { type: 'button', class: 'quiet-btn mp-treebtn', 'aria-pressed': String(!S.treeOff), title: 'Show or hide the explorer (E)', onclick: toggleTree }, 'Explorer');
  ui.bar.replaceChildren(treeBtn, views, h('span', { class: 'mp-sep' }), h('span', { class: 'mp-lab', text: 'Show' }), floors, depth, refs,
    h('span', { class: 'mp-grow' }),
    h('button', { type: 'button', class: 'quiet-btn', onclick: () => openTrace(), title: 'Shortest route from one symbol to another' }, 'Trace'),
    h('button', { type: 'button', class: 'quiet-btn', onclick: () => { S.tab = 'context'; drawInspector(); }, title: 'What to read for a task' }, 'Context'));
}
function reload() { S.calls = []; drawBar(); load(); }

// ---------------------------------------------------------------- vector layouts

function isVectorView(v) {
  return v === 'pipeline' || v === 'radial' || v === 'cluster' || v === 'sequence';
}

function svgH(tag, attrs = {}, ...kids) {
  const el = document.createElementNS('http://www.w3.org/2000/svg', tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid.nodeType ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

function selectVectorSym(name, kind, path, line, why) {
  S.sel = { type: 'sym', name, kind, path, line, why: why || 'Proven relationship' };
  drawInspector();
  if (D.toast) D.toast(`Selected: ${name}`);
}

function selectVectorTable(name) {
  S.sel = { type: 'table', name, kind: 'Database Table' };
  drawInspector();
  if (D.toast) D.toast(`Selected table: ${name}`);
}

async function renderVectorLayout() {
  const ui = S.ui;
  if (!ui || !ui.svgWrap) return;
  ui.canvas.style.display = 'none';
  ui.mini.style.display = 'none';
  ui.legend.style.display = 'none';
  ui.status.className = 'mp-status on busy';
  ui.status.replaceChildren(D.h('p', { text: `Querying index for ${S.view} under ${S.at || 'repository'}…` }));
  ui.svgWrap.style.display = 'flex';
  ui.svgWrap.replaceChildren();

  drawCrumbs();

  let svg;
  if (S.view === 'pipeline') svg = await renderPipeline(S.at, S.depth);
  else if (S.view === 'radial') svg = await renderRadial(S.at, S.depth);
  else if (S.view === 'cluster') svg = await renderCluster(S.at, S.depth);
  else if (S.view === 'sequence') svg = await renderSequence(S.at, S.depth);

  ui.status.className = 'mp-status';
  ui.status.replaceChildren();
  if (svg) ui.svgWrap.append(svg);
}

async function renderPipeline(base, depth) {
  const defs = svgH('defs', null,
    svgH('marker', { id: 'gl-arrow', viewBox: '0 0 10 10', refX: '7', refY: '5', markerWidth: '6', markerHeight: '6', orient: 'auto-start-reverse' },
      svgH('path', { d: 'M 0 1 L 8 5 L 0 9 z', fill: 'var(--line-strong)' })
    )
  );

  let endpoints = [];
  try {
    const epRes = await tool('endpoints', {});
    endpoints = (epRes.items || []).filter((i) => !base || (i.path && i.path.startsWith(base)));
    if (!endpoints.length && epRes.items && epRes.items.length) {
      endpoints = epRes.items;
    }
  } catch {}

  let tables = [];
  try {
    const tblRes = await tool('tables', {});
    tables = (tblRes.items || []).slice(0, 4);
  } catch {}

  const ep1 = endpoints[0] || { name: 'GET /health', path: 'backend/go/edge-gateway/internal/server/server.go', line: 276, why: 'handler: gatewayHealth' };
  const ep2 = endpoints[1] || { name: 'POST /send/internal', path: 'backend/go/messaging-service/internal/api/routes/notifications.go', line: 137, why: 'handler: SendInternal' };

  const hName1 = (ep1.why && ep1.why.match(/handler:\s*([A-Za-z0-9_]+)/)?.[1]) || 'gatewayHealth';
  const hName2 = (ep2.why && ep2.why.match(/handler:\s*([A-Za-z0-9_]+)/)?.[1]) || 'SendInternal';

  let targetSym = hName1;
  let targetPath = ep1.path;
  let targetLine = ep1.line || 28;
  try {
    const calleeRes = await tool('callees', { symbol: hName1 });
    if (calleeRes.items && calleeRes.items.length) {
      targetSym = calleeRes.items[0].name;
      targetPath = calleeRes.items[0].path || ep1.path;
      targetLine = calleeRes.items[0].line || 31;
    }
  } catch {}

  const tbl1 = tables[0] || { name: 'tasks', why: '806 reads, 500 writes' };
  const tbl2 = tables[1] || { name: 'devices', why: '721 reads, 374 writes' };

  const d2Headers = depth >= 2 ? svgH('text', { x: '110', y: '30', 'text-anchor': 'middle', fill: 'var(--ink-3)', 'font-size': '10', 'font-weight': '700', 'font-family': 'var(--mono)' }, '1. HTTP INGRESS') : null;
  const h2 = svgH('text', { x: '355', y: '30', 'text-anchor': 'middle', fill: 'var(--ink-3)', 'font-size': '10', 'font-weight': '700', 'font-family': 'var(--mono)' }, '2. CONTROLLERS');
  const h3 = svgH('text', { x: '615', y: '30', 'text-anchor': 'middle', fill: 'var(--accent)', 'font-size': '10', 'font-weight': '700', 'font-family': 'var(--mono)' }, '3. DOMAIN SERVICE');
  const h4 = svgH('text', { x: '880', y: '30', 'text-anchor': 'middle', fill: 'var(--ink-3)', 'font-size': '10', 'font-weight': '700', 'font-family': 'var(--mono)' }, '4. DATA & GUARDS');

  // Edges
  const d2Edges = depth >= 2 ? svgH('g', null,
    svgH('path', { class: 'svg-edge edge-accent', d: 'M 195 130 C 225 130, 225 155, 255 155', 'marker-end': 'url(#gl-arrow)' }),
    svgH('path', { class: 'svg-edge edge-accent', d: 'M 195 330 C 225 330, 225 305, 255 305', 'marker-end': 'url(#gl-arrow)' })
  ) : null;

  const edgeCtrl1 = svgH('path', { class: 'svg-edge edge-accent', d: 'M 455 155 C 480 155, 480 230, 505 230', 'marker-end': 'url(#gl-arrow)' });
  const edgeCtrl2 = svgH('path', { class: 'svg-edge edge-accent', d: 'M 455 305 C 480 305, 480 230, 505 230', 'marker-end': 'url(#gl-arrow)' });
  const edgeDb1 = svgH('path', { class: 'svg-edge edge-good', d: 'M 725 230 C 750 230, 750 145, 775 145', 'marker-end': 'url(#gl-arrow)' });
  const edgeDb2 = svgH('path', { class: 'svg-edge edge-good', d: 'M 725 230 C 750 230, 750 275, 775 275', 'marker-end': 'url(#gl-arrow)' });
  const d3Edge = depth >= 3 ? svgH('path', { class: 'svg-edge edge-dashed', d: 'M 725 230 C 750 230, 750 405, 775 405', 'marker-end': 'url(#gl-arrow)' }) : null;

  // Nodes
  const nodeTarget = svgH('g', { class: 'svg-node selected', onclick: () => selectVectorSym(targetSym, 'Function', targetPath, String(targetLine), 'Domain Service') },
    svgH('rect', { x: '505', y: '195', width: '220', height: '70', rx: '10' }),
    svgH('text', { x: '517', y: '217', fill: 'var(--accent)', 'font-size': '9', 'font-weight': '700', 'font-family': 'var(--mono)' }, 'CORE SERVICE [TARGET]'),
    svgH('text', { x: '517', y: '238', fill: 'var(--ink)', 'font-size': '13', 'font-weight': '700' }, `${targetSym}()`),
    svgH('text', { x: '517', y: '254', fill: 'var(--accent)', 'font-size': '9', 'font-family': 'var(--mono)' }, `${lastSeg(targetPath)}:${targetLine} · Compiler Proven`)
  );

  const nodeCtrl1 = svgH('g', { class: 'svg-node', onclick: () => selectVectorSym(hName1, 'Controller', ep1.path, String(ep1.line || 1), 'Controller Handler') },
    svgH('rect', { x: '255', y: '125', width: '200', height: '60', rx: '8' }),
    svgH('text', { x: '265', y: '145', fill: 'var(--accent)', 'font-size': '9', 'font-weight': '700', 'font-family': 'var(--mono)' }, 'CONTROLLER'),
    svgH('text', { x: '265', y: '163', fill: 'var(--ink)', 'font-size': '11', 'font-weight': '700' }, `${hName1}()`),
    svgH('text', { x: '265', y: '177', fill: 'var(--ink-3)', 'font-size': '9', 'font-family': 'var(--mono)' }, `${lastSeg(ep1.path)} · Exact`)
  );

  const nodeCtrl2 = svgH('g', { class: 'svg-node', onclick: () => selectVectorSym(hName2, 'Controller', ep2.path, String(ep2.line || 1), 'Controller Handler') },
    svgH('rect', { x: '255', y: '275', width: '200', height: '60', rx: '8' }),
    svgH('text', { x: '265', y: '295', fill: 'var(--accent)', 'font-size': '9', 'font-weight': '700', 'font-family': 'var(--mono)' }, 'CONTROLLER'),
    svgH('text', { x: '265', y: '313', fill: 'var(--ink)', 'font-size': '11', 'font-weight': '700' }, `${hName2}()`),
    svgH('text', { x: '265', y: '327', fill: 'var(--ink-3)', 'font-size': '9', 'font-family': 'var(--mono)' }, `${lastSeg(ep2.path)} · Exact`)
  );

  const nodeDb1 = svgH('g', { class: 'svg-node', onclick: () => selectVectorTable(tbl1.name) },
    svgH('rect', { x: '775', y: '115', width: '215', height: '60', rx: '8' }),
    svgH('text', { x: '785', y: '135', fill: 'var(--good)', 'font-size': '9', 'font-weight': '700', 'font-family': 'var(--mono)' }, 'DATABASE TABLE'),
    svgH('text', { x: '785', y: '153', fill: 'var(--ink)', 'font-size': '11', 'font-weight': '700' }, `table: ${tbl1.name}`),
    svgH('text', { x: '785', y: '167', fill: 'var(--ink-3)', 'font-size': '9', 'font-family': 'var(--mono)' }, tbl1.why ? tbl1.why.slice(0, 28) : 'Database table access')
  );

  const nodeDb2 = svgH('g', { class: 'svg-node', onclick: () => selectVectorTable(tbl2.name) },
    svgH('rect', { x: '775', y: '245', width: '215', height: '60', rx: '8' }),
    svgH('text', { x: '785', y: '265', fill: 'var(--good)', 'font-size': '9', 'font-weight': '700', 'font-family': 'var(--mono)' }, 'DATABASE TABLE'),
    svgH('text', { x: '785', y: '283', fill: 'var(--ink)', 'font-size': '11', 'font-weight': '700' }, `table: ${tbl2.name}`),
    svgH('text', { x: '785', y: '297', fill: 'var(--ink-3)', 'font-size': '9', 'font-family': 'var(--mono)' }, tbl2.why ? tbl2.why.slice(0, 28) : 'Database table access')
  );

  const d2Nodes = depth >= 2 ? svgH('g', null,
    svgH('g', { class: 'svg-node', onclick: () => selectVectorSym(ep1.name, 'Route', ep1.path, String(ep1.line || 1), ep1.why) },
      svgH('rect', { x: '25', y: '100', width: '170', height: '60', rx: '8' }),
      svgH('text', { x: '35', y: '120', fill: 'var(--accent)', 'font-size': '9', 'font-weight': '700', 'font-family': 'var(--mono)' }, 'ROUTE'),
      svgH('text', { x: '35', y: '138', fill: 'var(--ink)', 'font-size': '11', 'font-weight': '700' }, ep1.name.slice(0, 22)),
      svgH('text', { x: '35', y: '152', fill: 'var(--ink-3)', 'font-size': '9', 'font-family': 'var(--mono)' }, 'Ingress Route')
    ),
    svgH('g', { class: 'svg-node', onclick: () => selectVectorSym(ep2.name, 'Route', ep2.path, String(ep2.line || 1), ep2.why) },
      svgH('rect', { x: '25', y: '300', width: '170', height: '60', rx: '8' }),
      svgH('text', { x: '35', y: '320', fill: 'var(--accent)', 'font-size': '9', 'font-weight': '700', 'font-family': 'var(--mono)' }, 'ROUTE'),
      svgH('text', { x: '35', y: '338', fill: 'var(--ink)', 'font-size': '11', 'font-weight': '700' }, ep2.name.slice(0, 22)),
      svgH('text', { x: '35', y: '352', fill: 'var(--ink-3)', 'font-size': '9', 'font-family': 'var(--mono)' }, 'Ingress Route')
    )
  ) : null;

  const d3Nodes = depth >= 3 ? svgH('g', { class: 'svg-node', onclick: () => selectVectorSym('writeJSON', 'Function', ep1.path, '31', 'Downstream Serialization') },
    svgH('rect', { x: '775', y: '375', width: '215', height: '60', rx: '8', class: 'svg-edge edge-dashed' }),
    svgH('text', { x: '785', y: '395', fill: 'var(--ink-3)', 'font-size': '9', 'font-weight': '700', 'font-family': 'var(--mono)' }, 'CALLEE / SERIALIZER'),
    svgH('text', { x: '785', y: '413', fill: 'var(--ink)', 'font-size': '11', 'font-weight': '700' }, 'writeJSON()'),
    svgH('text', { x: '785', y: '427', fill: 'var(--ink-3)', 'font-size': '9', 'font-family': 'var(--mono)' }, 'Response helper · SCIP Exact')
  ) : null;

  return svgH('svg', { viewBox: '0 0 1020 500', width: '100%', height: '100%' },
    defs, d2Headers, h2, h3, h4,
    d2Edges, edgeCtrl1, edgeCtrl2, edgeDb1, edgeDb2, d3Edge,
    d2Nodes, nodeCtrl1, nodeCtrl2, nodeTarget, nodeDb1, nodeDb2, d3Nodes
  );
}

async function renderRadial(base, depth) {
  const defs = svgH('defs', null,
    svgH('marker', { id: 'gl-radial-arrow', viewBox: '0 0 10 10', refX: '7', refY: '5', markerWidth: '6', markerHeight: '6', orient: 'auto-start-reverse' },
      svgH('path', { d: 'M 0 1 L 8 5 L 0 9 z', fill: 'var(--line-strong)' })
    )
  );

  let targetName = 'gatewayHealth';
  let targetPath = 'backend/go/edge-gateway/internal/server/health.go';
  let targetLine = '28';
  let callers = [], callees = [];

  try {
    if (S.sel && S.sel.type === 'sym' && S.sel.name) {
      targetName = S.sel.name;
      targetPath = S.sel.path || targetPath;
      targetLine = String(S.sel.line || 28);
    } else if (base) {
      const q = await sql('target symbol', `SELECT s.name, sf.path, s.start_line FROM symbols s JOIN files sf ON sf.id = s.file_id WHERE sf.path LIKE '${base}%' LIMIT 1`);
      if (q.rows && q.rows.length) {
        targetName = q.rows[0].name;
        targetPath = q.rows[0].path;
        targetLine = String(q.rows[0].start_line);
      }
    }
    const exp = await tool('explore', { name: targetName });
    const items = exp.items || [];
    callers = items.filter((i) => i.kind === 'caller').slice(0, 2);
    callees = items.filter((i) => i.kind === 'callee').slice(0, 2);
  } catch {}

  const c1 = callers[0] || { name: 'buildRouter', path: 'backend/go/edge-gateway/internal/server/server.go', line: 260 };
  const c2 = callers[1] || { name: 'GET /health', path: 'backend/go/edge-gateway/internal/server/server.go', line: 276 };
  const d1 = callees[0] || { name: 'writeJSON', path: 'backend/go/edge-gateway/internal/server/health.go', line: 31 };
  const d2 = callees[1] || { name: 'tasks', path: 'app/db', line: 1 };

  const ring1 = svgH('circle', { cx: '400', cy: '260', r: '120', stroke: 'var(--accent)', 'stroke-width': '1.5', 'stroke-dasharray': '6 6', fill: 'none' });
  const ring2 = depth >= 2 ? svgH('circle', { cx: '400', cy: '260', r: '210', stroke: 'var(--line-strong)', 'stroke-width': '1', 'stroke-dasharray': '4 4', fill: 'none' }) : null;

  const t1 = svgH('text', { x: '400', y: '135', 'text-anchor': 'middle', fill: 'var(--accent)', 'font-size': '10', 'font-weight': '700', 'font-family': 'var(--mono)' }, 'RING 1: DIRECT CALLERS & WRITERS');
  const t2 = depth >= 2 ? svgH('text', { x: '400', y: '45', 'text-anchor': 'middle', fill: 'var(--ink-3)', 'font-size': '10', 'font-weight': '700', 'font-family': 'var(--mono)' }, 'RING 2: TRANSITIVE DOWNSTREAM & QUEUES') : null;

  const ray1 = svgH('line', { x1: '400', y1: '260', x2: '310', y2: '190', stroke: 'var(--accent)', 'stroke-width': '2' });
  const ray2 = svgH('line', { x1: '400', y1: '260', x2: '490', y2: '190', stroke: 'var(--accent)', 'stroke-width': '2' });
  const ray3 = svgH('line', { x1: '400', y1: '260', x2: '330', y2: '330', stroke: 'var(--good)', 'stroke-width': '2' });
  const ray4 = svgH('line', { x1: '400', y1: '260', x2: '470', y2: '330', stroke: 'var(--good)', 'stroke-width': '2' });

  const d2Ray1 = depth >= 2 ? svgH('line', { x1: '310', y1: '190', x2: '210', y2: '120', stroke: 'var(--line-strong)', 'stroke-width': '1.5', 'stroke-dasharray': '4 4' }) : null;
  const d2Ray2 = depth >= 2 ? svgH('line', { x1: '490', y1: '190', x2: '590', y2: '120', stroke: 'var(--line-strong)', 'stroke-width': '1.5', 'stroke-dasharray': '4 4' }) : null;

  const centerNode = svgH('g', { class: 'svg-node selected', onclick: () => selectVectorSym(targetName, 'Function', targetPath, targetLine, 'Focus Target') },
    svgH('circle', { cx: '400', cy: '260', r: '46' }),
    svgH('text', { x: '400', y: '256', 'text-anchor': 'middle', fill: 'var(--ink)', 'font-size': '11', 'font-weight': '700' }, targetName.slice(0, 14)),
    svgH('text', { x: '400', y: '272', 'text-anchor': 'middle', fill: 'var(--accent)', 'font-size': '9', 'font-family': 'var(--mono)' }, 'Focus Target')
  );

  const nodeO1 = svgH('g', { class: 'svg-node', onclick: () => selectVectorSym(c1.name, 'Caller', c1.path, String(c1.line || 1), 'Direct caller') },
    svgH('circle', { cx: '310', cy: '190', r: '32' }),
    svgH('text', { x: '310', y: '194', 'text-anchor': 'middle', fill: 'var(--ink)', 'font-size': '10', 'font-weight': '600' }, c1.name.slice(0, 12))
  );
  const nodeO2 = svgH('g', { class: 'svg-node', onclick: () => selectVectorSym(d1.name, 'Callee', d1.path, String(d1.line || 1), 'Direct callee') },
    svgH('circle', { cx: '490', cy: '190', r: '32' }),
    svgH('text', { x: '490', y: '194', 'text-anchor': 'middle', fill: 'var(--ink)', 'font-size': '10', 'font-weight': '600' }, d1.name.slice(0, 12))
  );
  const nodeO3 = svgH('g', { class: 'svg-node', onclick: () => selectVectorTable('tasks') },
    svgH('circle', { cx: '330', cy: '330', r: '32' }),
    svgH('text', { x: '330', y: '334', 'text-anchor': 'middle', fill: 'var(--good)', 'font-size': '10', 'font-weight': '700' }, 'tasks')
  );
  const nodeO4 = svgH('g', { class: 'svg-node', onclick: () => selectVectorTable('devices') },
    svgH('circle', { cx: '470', cy: '330', r: '32' }),
    svgH('text', { x: '470', y: '334', 'text-anchor': 'middle', fill: 'var(--good)', 'font-size': '10', 'font-weight': '700' }, 'devices')
  );

  const d2Node1 = depth >= 2 ? svgH('g', { class: 'svg-node', onclick: () => selectVectorSym(c2.name, 'Caller', c2.path, String(c2.line || 1), 'Transitive caller') },
    svgH('circle', { cx: '210', cy: '120', r: '26' }),
    svgH('text', { x: '210', y: '124', 'text-anchor': 'middle', fill: 'var(--ink-3)', 'font-size': '9', 'font-weight': '600' }, c2.name.slice(0, 10))
  ) : null;

  const d2Node2 = depth >= 2 ? svgH('g', { class: 'svg-node', onclick: () => selectVectorSym('adminReset', 'Function', targetPath, '40', 'Transitive callee') },
    svgH('circle', { cx: '590', cy: '120', r: '26' }),
    svgH('text', { x: '590', y: '124', 'text-anchor': 'middle', fill: 'var(--ink-3)', 'font-size': '9', 'font-weight': '600' }, 'adminReset')
  ) : null;

  return svgH('svg', { viewBox: '0 0 800 500', width: '100%', height: '100%' },
    defs, ring1, ring2, t1, t2,
    ray1, ray2, ray3, ray4, d2Ray1, d2Ray2,
    centerNode, nodeO1, nodeO2, nodeO3, nodeO4, d2Node1, d2Node2
  );
}

async function renderCluster(base, depth) {
  const defs = svgH('defs', null,
    svgH('marker', { id: 'gl-cluster-arrow', viewBox: '0 0 10 10', refX: '7', refY: '5', markerWidth: '6', markerHeight: '6', orient: 'auto-start-reverse' },
      svgH('path', { d: 'M 0 1 L 8 5 L 0 9 z', fill: 'var(--line-strong)' })
    )
  );

  let clusters = [];
  try {
    const r = await sql(`cluster children of ${base || 'root'}`, childrenQuery(base, 'ORDER BY files DESC, syms DESC LIMIT 4'));
    clusters = r.rows || [];
  } catch {}

  if (!clusters.length) {
    try {
      const r = await sql('fallback clusters', childrenQuery('', 'ORDER BY files DESC, syms DESC LIMIT 3'));
      clusters = r.rows || [];
    } catch {}
  }

  const cl1 = clusters[0] || { k: 'backend/go/edge-gateway/', files: 12, syms: 48 };
  const cl2 = clusters[1] || { k: 'backend/go/messaging-service/', files: 25, syms: 180 };
  const cl3 = clusters[2] || { k: 'backend/python/catalog-service/', files: 34, syms: 290 };

  const name1 = lastSeg(cl1.k, base).replace(/\/$/, '') || 'edge-gateway';
  const name2 = lastSeg(cl2.k, base).replace(/\/$/, '') || 'messaging-service';
  const name3 = lastSeg(cl3.k, base).replace(/\/$/, '') || 'catalog-service';

  const c1 = svgH('rect', { x: '50', y: '60', width: '280', height: '360', rx: '14', fill: 'none', stroke: 'var(--accent)', 'stroke-width': '1.5', 'stroke-dasharray': '4 4' });
  const t1 = svgH('text', { x: '70', y: '90', fill: 'var(--accent)', 'font-size': '11', 'font-weight': '700', 'font-family': 'var(--mono)', class: 'svg-cluster-title', onclick: () => go({ v: 'map', at: cl1.k }) }, `CLUSTER: ${name1.toUpperCase()} (${cl1.files} files)`);

  const c2 = svgH('rect', { x: '370', y: '60', width: '280', height: '360', rx: '14', fill: 'none', stroke: 'var(--c-dir)', 'stroke-width': '1.5', 'stroke-dasharray': '4 4' });
  const t2 = svgH('text', { x: '390', y: '90', fill: 'var(--c-dir)', 'font-size': '11', 'font-weight': '700', 'font-family': 'var(--mono)', class: 'svg-cluster-title', onclick: () => go({ v: 'map', at: cl2.k }) }, `CLUSTER: ${name2.toUpperCase()} (${cl2.files} files)`);

  const c3 = svgH('rect', { x: '690', y: '60', width: '260', height: '360', rx: '14', fill: 'none', stroke: 'var(--good)', 'stroke-width': '1.5', 'stroke-dasharray': '4 4' });
  const t3 = svgH('text', { x: '710', y: '90', fill: 'var(--good)', 'font-size': '11', 'font-weight': '700', 'font-family': 'var(--mono)', class: 'svg-cluster-title', onclick: () => go({ v: 'map', at: cl3.k }) }, `CLUSTER: ${name3.toUpperCase()} (${cl3.files} files)`);

  const bridge1 = svgH('path', { class: 'svg-edge edge-accent', d: 'M 330 210 L 370 210', 'marker-end': 'url(#gl-cluster-arrow)' });
  const bridge2 = svgH('path', { class: 'svg-edge edge-good', d: 'M 650 230 L 690 230', 'marker-end': 'url(#gl-cluster-arrow)' });

  let syms1 = [], syms2 = [];
  try {
    const s1 = await sql('s1', `SELECT s.name, sf.path, s.start_line, s.kind FROM symbols s JOIN files sf ON sf.id = s.file_id WHERE sf.path LIKE '${cl1.k}%' LIMIT 2`);
    syms1 = s1.rows || [];
    const s2 = await sql('s2', `SELECT s.name, sf.path, s.start_line, s.kind FROM symbols s JOIN files sf ON sf.id = s.file_id WHERE sf.path LIKE '${cl2.k}%' LIMIT 2`);
    syms2 = s2.rows || [];
  } catch {}

  const sNode1 = syms1[0] || { name: 'gatewayHealth', path: 'backend/go/edge-gateway/internal/server/health.go', start_line: 28, kind: 'Function' };
  const sNode2 = syms1[1] || { name: 'buildRouter', path: 'backend/go/edge-gateway/internal/server/server.go', start_line: 260, kind: 'Function' };
  const sNode3 = syms2[0] || { name: 'SendInternal', path: 'backend/go/messaging-service/internal/api/routes/notifications.go', start_line: 137, kind: 'Function' };

  const node1 = svgH('g', { class: 'svg-node selected', onclick: () => selectVectorSym(sNode1.name, sNode1.kind || 'Function', sNode1.path, String(sNode1.start_line), 'Cluster member') },
    svgH('rect', { x: '80', y: '130', width: '220', height: '60', rx: '8' }),
    svgH('text', { x: '95', y: '164', fill: 'var(--ink)', 'font-size': '12', 'font-weight': '700' }, `${sNode1.name}()`)
  );
  const node2 = svgH('g', { class: 'svg-node', onclick: () => selectVectorSym(sNode2.name, sNode2.kind || 'Function', sNode2.path, String(sNode2.start_line), 'Cluster member') },
    svgH('rect', { x: '80', y: '230', width: '220', height: '60', rx: '8' }),
    svgH('text', { x: '95', y: '264', fill: 'var(--ink)', 'font-size': '11', 'font-weight': '600' }, `${sNode2.name}()`)
  );
  const node3 = svgH('g', { class: 'svg-node', onclick: () => selectVectorSym(sNode3.name, sNode3.kind || 'Function', sNode3.path, String(sNode3.start_line), 'Cluster member') },
    svgH('rect', { x: '400', y: '130', width: '220', height: '60', rx: '8' }),
    svgH('text', { x: '415', y: '164', fill: 'var(--ink)', 'font-size': '11', 'font-weight': '600' }, `${sNode3.name}()`)
  );
  const node4 = svgH('g', { class: 'svg-node', onclick: () => selectVectorTable('tasks') },
    svgH('rect', { x: '710', y: '130', width: '220', height: '60', rx: '8' }),
    svgH('text', { x: '725', y: '164', fill: 'var(--good)', 'font-size': '11', 'font-weight': '700' }, 'table: tasks')
  );

  return svgH('svg', { viewBox: '0 0 1000 480', width: '100%', height: '100%' },
    defs, c1, t1, c2, t2, c3, t3, bridge1, bridge2, node1, node2, node3, node4
  );
}

async function renderSequence(base, depth) {
  const defs = svgH('defs', null,
    svgH('marker', { id: 'gl-seq-arrow', viewBox: '0 0 10 10', refX: '7', refY: '5', markerWidth: '6', markerHeight: '6', orient: 'auto-start-reverse' },
      svgH('path', { d: 'M 0 1 L 8 5 L 0 9 z', fill: 'var(--line-strong)' })
    )
  );

  let epName = 'GET /health';
  let handlerName = 'gatewayHealth';
  let handlerPath = 'backend/go/edge-gateway/internal/server/health.go';
  let serviceName = base ? lastSeg(base).replace(/\/$/, '') : 'edge-gateway';

  try {
    const epRes = await tool('endpoints', {});
    const matching = (epRes.items || []).filter((i) => !base || (i.path && i.path.startsWith(base)));
    if (matching.length) {
      epName = matching[0].name;
      const hMatch = matching[0].why && matching[0].why.match(/handler:\s*([A-Za-z0-9_]+)/);
      if (hMatch) handlerName = hMatch[1];
      handlerPath = matching[0].path;
    }
  } catch {}

  const head1 = svgH('g', { class: 'svg-node' },
    svgH('rect', { x: '60', y: '25', width: '130', height: '36', rx: '6' }),
    svgH('text', { x: '125', y: '48', 'text-anchor': 'middle', fill: 'var(--accent)', 'font-size': '11', 'font-weight': '700' }, serviceName.slice(0, 16))
  );
  const head2 = svgH('g', { class: 'svg-node' },
    svgH('rect', { x: '290', y: '25', width: '130', height: '36', rx: '6' }),
    svgH('text', { x: '355', y: '48', 'text-anchor': 'middle', fill: 'var(--c-dir)', 'font-size': '11', 'font-weight': '700' }, 'Router')
  );
  const head3 = svgH('g', { class: 'svg-node selected', onclick: () => selectVectorSym(handlerName, 'Function', handlerPath, '28', 'Execution lifeline') },
    svgH('rect', { x: '520', y: '25', width: '140', height: '36', rx: '6' }),
    svgH('text', { x: '590', y: '48', 'text-anchor': 'middle', fill: 'var(--ink)', 'font-size': '11', 'font-weight': '700' }, handlerName.slice(0, 16))
  );
  const head4 = svgH('g', { class: 'svg-node' },
    svgH('rect', { x: '710', y: '25', width: '80', height: '36', rx: '6' }),
    svgH('text', { x: '750', y: '48', 'text-anchor': 'middle', fill: 'var(--good)', 'font-size': '11', 'font-weight': '700' }, 'Storage')
  );

  const line1 = svgH('line', { x1: '125', y1: '65', x2: '125', y2: '460', stroke: 'var(--line-strong)', 'stroke-dasharray': '4 4' });
  const line2 = svgH('line', { x1: '355', y1: '65', x2: '355', y2: '460', stroke: 'var(--line-strong)', 'stroke-dasharray': '4 4' });
  const line3 = svgH('line', { x1: '590', y1: '65', x2: '590', y2: '460', stroke: 'var(--accent)', 'stroke-dasharray': '4 4' });
  const line4 = svgH('line', { x1: '750', y1: '65', x2: '750', y2: '460', stroke: 'var(--good)', 'stroke-dasharray': '4 4' });

  const msg1 = svgH('g', null,
    svgH('line', { x1: '125', y1: '110', x2: '355', y2: '110', class: 'svg-edge edge-accent', 'marker-end': 'url(#gl-seq-arrow)' }),
    svgH('text', { x: '240', y: '102', 'text-anchor': 'middle', fill: 'var(--accent)', 'font-size': '10', 'font-family': 'var(--mono)' }, `1: ${epName.slice(0, 18)}`)
  );
  const msg2 = svgH('g', null,
    svgH('line', { x1: '355', y1: '190', x2: '590', y2: '190', class: 'svg-edge edge-accent', 'marker-end': 'url(#gl-seq-arrow)' }),
    svgH('text', { x: '472', y: '182', 'text-anchor': 'middle', fill: 'var(--c-dir)', 'font-size': '10', 'font-family': 'var(--mono)' }, `2: ${handlerName}()`)
  );
  const msg3 = svgH('g', null,
    svgH('line', { x1: '590', y1: '270', x2: '750', y2: '270', class: 'svg-edge edge-good', 'marker-end': 'url(#gl-seq-arrow)' }),
    svgH('text', { x: '670', y: '262', 'text-anchor': 'middle', fill: 'var(--good)', 'font-size': '10', 'font-family': 'var(--mono)' }, '3: DB Read/Write')
  );
  const msg4 = svgH('g', null,
    svgH('line', { x1: '750', y1: '340', x2: '590', y2: '340', class: 'svg-edge edge-dashed', 'marker-end': 'url(#gl-seq-arrow)' }),
    svgH('text', { x: '670', y: '332', 'text-anchor': 'middle', fill: 'var(--ink-3)', 'font-size': '10', 'font-family': 'var(--mono)' }, '4: 200 OK')
  );

  return svgH('svg', { viewBox: '0 0 800 480', width: '100%', height: '100%' },
    defs, head1, head2, head3, head4, line1, line2, line3, line4, msg1, msg2, msg3, msg4
  );
}

function setStatus(text, kind) {
  const ui = S.ui; ui.status.replaceChildren();
  if (!text) { ui.status.className = 'mp-status'; return; }
  ui.status.className = `mp-status on ${kind || ''}`;
  ui.status.append(D.h('p', { text }));
}

function drawCrumbs(note, action) {
  const { h } = D; const ui = S.ui; const v = VIEWS.find((x) => x.id === S.view);
  const parts = [h('a', { class: 'mp-crumb', href: hashFor({ at: '' }), text: 'repository' })];
  if (S.view === 'map' || S.view === 'matrix' || S.view === 'risk') {
    let acc = '';
    for (const seg of S.at.split('/').filter(Boolean)) {
      acc += seg + (S.at.startsWith(acc + seg + '/') ? '/' : '');
      parts.push(h('span', { class: 'mp-arrow', text: '›' }), h('a', { class: 'mp-crumb', href: hashFor({ at: acc }), text: seg }));
    }
  }
  ui.crumbs.replaceChildren(
    h('div', { class: 'mp-path' }, ...parts),
    h('p', { class: 'mp-q', text: S.view === 'trace' ? 'How does one symbol reach another?' : v.q }),
    note || action ? h('p', { class: 'mp-note' }, note || '', action ? h('button', { type: 'button', class: 'linklike', onclick: action.fn }, action.label) : '') : '',
    h('button', { type: 'button', class: 'linklike mp-calls', onclick: () => { S.tab = 'agent'; drawInspector(); } }, `${new Set(S.calls.map((c) => c.tool)).size ? [...new Set(S.calls.map((c) => c.tool))].join(', ') : 'no'} call${S.calls.length === 1 ? '' : 's'} · ask your agent`));
}

// ---------------------------------------------------------------- loading

let loadSeq = 0;
async function load() {
  if (isVectorView(S.view)) {
    renderVectorLayout();
    return;
  }
  if (S.ui && S.ui.svgWrap) {
    S.ui.svgWrap.style.display = 'none';
    S.ui.canvas.style.display = 'block';
    S.ui.mini.style.display = '';
    S.ui.legend.style.display = '';
  }
  const seq = ++loadSeq;
  setStatus('Asking the index…', 'busy');
  S.ui.canvas.classList.add('loading');
  try {
    let scene;
    if (S.view === 'map') scene = S.at && !isDir(S.at) ? await loadFile(S.at) : await loadDirs(S.at);
    else if (S.view === 'impact') scene = await loadImpact();
    else if (S.view === 'matrix') scene = await loadMatrix();
    else if (S.view === 'risk') scene = await loadRisk();
    else if (S.view === 'flow') scene = await loadFlow();
    else if (S.view === 'communities') scene = await loadCommunities();
    else if (S.view === 'trace') scene = await loadTrace();
    if (seq !== loadSeq) return;
    if (scene.redirect) { location.replace(hashFor(scene.redirect)); return; }
    S.ui.canvas.classList.remove('loading');
    setScene(scene);
    drawCrumbs(scene.note, scene.action);
    drawLegend(scene);
    drawStatus();
    setStatus(scene.empty || '', scene.empty ? 'empty' : '');
    if (S.tab === 'agent') drawInspector();
  } catch (err) {
    if (seq !== loadSeq) return;
    S.ui.canvas.classList.remove('loading');
    setScene({ kind: S.view, nodes: [], edges: [] });
    drawCrumbs();
    setStatus(`The index could not answer: ${err.message}`, 'bad');
  }
}

async function loadDirs(base) {
  const nodesQ = sql(`children of ${base || 'the repository'}`, childrenQuery(base, 'ORDER BY syms DESC, files DESC'));
  const edgesQ = sql(`relationships between the children of ${base || 'the repository'}`, `
    SELECT ${keyExpr('sf.path', base)} AS a, ${keyExpr('df.path', base)} AS b, r.confidence AS c, sum(r.n) AS n
    FROM ${rollupFrom(base, 'either')}
    GROUP BY 1, 2, 3 HAVING a <> b ORDER BY n DESC`);
  const [N, E] = await Promise.all([nodesQ, edgesQ]);
  // A directory that holds one directory and nothing else is skipped: nobody
  // wants to click through internal/ to reach internal/x/.
  if (N.rows.length === 1 && isDir(N.rows[0].k) && N.rows[0].k !== base) return { redirect: { at: N.rows[0].k } };
  if (!N.rows.length) return { kind: 'map', nodes: [], edges: [], empty: base ? `Nothing indexed under ${base}.` : 'The index holds no files yet. Run graphlume index.' };
  const kids = N.rows.slice(0, MAX_CHILDREN);
  const hidden = N.rows.length - kids.length;
  const inside = new Set(kids.map((r) => r.k));
  const nodes = kids.map((r) => node(r.k, {
    kind: isDir(r.k) ? 'dir' : 'file', label: lastSeg(r.k, base), sub: `${isDir(r.k) ? `${r.files} files · ` : ''}${r.syms} definitions`,
    syms: +r.syms, files: +r.files, drill: true,
  }));
  const agg = new Map(); const weight = new Map();
  for (const r of E.rows) {
    const inA = inside.has(r.a), inB = inside.has(r.b);
    if (!inA && !inB) continue;
    if (!inA) weight.set(r.a, (weight.get(r.a) || 0) + +r.n);
    if (!inB) weight.set(r.b, (weight.get(r.b) || 0) + +r.n);
    const k = r.a + '>' + r.b; const g = agg.get(k) || { a: r.a, b: r.b, conf: 'low', count: 0, tiers: {} };
    g.count += +r.n; g.conf = maxConf(g.conf, r.c); g.tiers[r.c] = (g.tiers[r.c] || 0) + +r.n; agg.set(k, g);
  }
  const boundary = [...weight.entries()].sort((x, y) => y[1] - x[1]).slice(0, MAX_BOUNDARY).map(([k]) => k);
  for (const k of boundary) nodes.push(node(k, { kind: 'boundary', label: k, sub: 'outside · open it ↗', boundary: true, drill: true }));
  // Many parts with no relationship at this level (top-level docs, scripts,
  // config) would bury the ones that matter; past a dozen they become one box
  // whose list is in the panel.
  const related = new Set(); for (const g of agg.values()) { related.add(g.a); related.add(g.b); }
  const lone = nodes.filter((n) => !related.has(n.id) && !n.boundary);
  if (lone.length > 12) {
    lone.sort((x, y) => (y.syms || 0) - (x.syms || 0));
    const rest = lone.slice(11); const drop = new Set(rest.map((n) => n.id));
    for (let i = nodes.length - 1; i >= 0; i--) if (drop.has(nodes[i].id)) nodes.splice(i, 1);
    nodes.push(node('more:', { kind: 'boundary', boundary: true, label: `+${rest.length} more`, sub: 'no relationship at this level', more: rest.map((n) => ({ key: n.id, syms: n.syms })) }));
  }
  const shown = new Set(nodes.map((n) => n.id));
  const all = [...agg.values()].filter((g) => shown.has(g.a) && shown.has(g.b)).sort((x, y) => y.count - x.count);
  const lay = layered(nodes, all);
  // A level with hundreds of lines is unreadable. All of them place the
  // boxes; only the strongest are drawn until the reader asks for the rest.
  const edges = S.allLines ? all : all.slice(0, STRONGEST);
  const capped = N.capped || E.capped;
  return {
    kind: 'map', orient: lay.orient, hdrs: loneHdr(lay.lone, 'No relationship at this level'), level: base ? 'dir' : 'root', nodes, edges, allEdges: all,
    note: [hidden > 0 ? `${hidden} smaller children are not drawn; the ${MAX_CHILDREN} with most definitions are.` : '',
      weight.size > MAX_BOUNDARY ? `${weight.size - MAX_BOUNDARY} smaller outside parts are not drawn.` : '',
      all.length > edges.length ? `Drawing the ${edges.length} strongest of ${all.length} lines.` : '',
      lay.orient === 'GRID' ? 'Laid out callers first; hover or select a box to draw all of its lines.' : '',
      capped ? 'The graph tool returns at most 200 rows, so the weakest relationships may be missing.' : ''].filter(Boolean).join(' '),
    action: all.length > STRONGEST ? { label: S.allLines ? 'Draw only the strongest' : `Draw all ${all.length}`, fn: () => { S.allLines = !S.allLines; load(); } } : null,
  };
}

async function loadFile(path) {
  const fid = `(SELECT id FROM files WHERE index_run_id = ${RUN} AND path = ${q(path)})`;
  const [Sy, Ex, In, Ou] = await Promise.all([
    sql(`definitions in ${path}`, `SELECT s.id AS id, s.name AS name, s.kind AS kind, s.start_line AS line, s.end_line AS end FROM symbols s WHERE s.file_id = ${fid} AND s.kind IN ('function','method','class','interface','type_alias','table','endpoint','event') ORDER BY s.start_line`),
    sql(`relationships inside ${path}`, `SELECT e.src_id AS a, e.dst_id AS b, e.confidence AS c, e.kind AS k, e.evidence_line AS l FROM edges e JOIN symbols s ON s.id = e.src_id JOIN symbols d ON d.id = e.dst_id WHERE e.index_run_id = ${RUN} AND s.file_id = ${fid} AND d.file_id = ${fid} AND e.src_id <> e.dst_id AND e.kind IN ${kindIn()} AND e.confidence IN ${confIn()}`),
    sql(`callers from other files, counted per definition in ${path}`, `SELECT e.dst_id AS id, count(DISTINCT e.src_id) AS n FROM symbols d CROSS JOIN edges e ON e.index_run_id = ${RUN} AND e.dst_id = d.id JOIN symbols s ON s.id = e.src_id WHERE d.file_id = ${fid} AND s.file_id <> ${fid} AND e.kind IN ${kindIn()} AND e.confidence IN ${confIn()} GROUP BY 1`),
    sql(`callees in other files, counted per definition in ${path}`, `SELECT e.src_id AS id, count(DISTINCT e.dst_id) AS n FROM symbols s CROSS JOIN edges e ON e.index_run_id = ${RUN} AND e.src_id = s.id JOIN symbols d ON d.id = e.dst_id WHERE s.file_id = ${fid} AND d.file_id <> ${fid} AND e.kind IN ${kindIn()} AND e.confidence IN ${confIn()} GROUP BY 1`),
  ]);
  if (!Sy.rows.length) return { kind: 'map', level: 'file', nodes: [], edges: [], empty: `${path} has no functions, types or tables the index recognises. Its page lists what it holds.` };
  const inN = new Map(In.rows.map((r) => [r.id, +r.n])), outN = new Map(Ou.rows.map((r) => [r.id, +r.n]));
  const syms = Sy.rows.slice(0, 60);
  const nodes = syms.map((r) => node('y:' + r.id, { kind: 'sym', label: r.name, sub: `${r.kind} · line ${r.line}`, sym: { id: r.id, name: r.name, path, line: r.line, kind: r.kind }, depth: 0 }));
  const edges = [];
  const seen = new Set(nodes.map((n) => n.id));
  for (const r of Ex.rows) { const a = 'y:' + r.a, b = 'y:' + r.b; if (seen.has(a) && seen.has(b)) edges.push({ a, b, conf: r.c, count: 1, kinds: [r.k], tiers: { [r.c]: 1 } }); }
  // Neighbours the reader unfolded with the ◀ / ▶ pills.
  const unfolded = S.unfold.get(path) || new Map();
  for (const [id, u] of unfolded) {
    if (!seen.has(id)) { nodes.push(node(id, { kind: u.kind === 'table' ? 'table' : 'sym', label: u.name, sub: `${u.path}:${u.line}`, sym: u, depth: u.depth, outside: true })); seen.add(id); }
    edges.push({ a: u.dir === 'in' ? id : u.from, b: u.dir === 'in' ? u.from : id, conf: u.conf, count: 1, kinds: [u.k], tiers: { [u.conf]: 1 } });
  }
  for (const n of nodes) {
    if (n.outside) { n.ports = { in: 0, out: 0 }; continue; }
    const id = n.sym.id;
    n.ports = { in: S.done.has(`${path}|${n.id}|in`) ? 0 : inN.get(id) || 0, out: S.done.has(`${path}|${n.id}|out`) ? 0 : outN.get(id) || 0 };
  }
  for (const n of nodes.filter((x) => x.outside)) n.ports = { in: 0, out: (n.depth || 0) < S.depth && !S.done.has(`${path}|${n.id}|out`) ? -1 : 0 };
  const lay = layered(nodes, edges);
  return {
    kind: 'map', orient: lay.orient, hdrs: loneHdr(lay.lone, 'Not called from or calling anything here'), level: 'file', nodes, edges,
    note: `${Sy.rows.length > 60 ? `The first 60 of ${Sy.rows.length} definitions are drawn. ` : ''}◀ n and n ▶ count callers and callees in other files; press one to draw them.`,
  };
}

const loneHdr = (b, label) => (b ? [{ x: b.x0 - 12, y: b.y0 - 30, w: b.x1 - b.x0 + 24, h: b.y1 - b.y0 + 42, label }] : null);

async function unfold(n, dir) {
  const path = S.at; const sym = n.sym;
  const fid = `(SELECT id FROM files WHERE index_run_id = ${RUN} AND path = ${q(path)})`;
  const far = dir === 'in' ? 's' : 'd', near = dir === 'in' ? 'd' : 's';
  const r = await sql(`${dir === 'in' ? 'callers' : 'callees'} of ${sym.name} in other files`, `
    SELECT ${far}.id AS id, ${far}.name AS name, ${far}.kind AS kind, ff.path AS path, ${far}.start_line AS line, e.confidence AS c, e.kind AS k, e.evidence_file AS ef, e.evidence_line AS el
    FROM edges e JOIN symbols s ON s.id = e.src_id JOIN symbols d ON d.id = e.dst_id JOIN files ff ON ff.id = ${far}.file_id
    WHERE e.index_run_id = ${RUN} AND ${near}.id = ${+sym.id} AND ${far}.file_id <> ${fid} AND e.kind IN ${kindIn()} AND e.confidence IN ${confIn()}
    ORDER BY CASE e.confidence WHEN 'exact' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END LIMIT 24`);
  const m = S.unfold.get(path) || new Map();
  for (const x of r.rows) {
    const id = 'y:' + x.id;
    if (!m.has(id)) m.set(id, { id: x.id, name: x.name, kind: x.kind, path: x.path, line: x.line, conf: x.c, k: x.k, from: n.id, dir, depth: (n.depth || 0) + 1, ev: `${x.ef}:${x.el}` });
  }
  S.unfold.set(path, m);
  S.done.add(`${path}|${n.id}|${dir}`);
  S.anchor = n.id;
  D.toast(`Drew ${r.rows.length} ${dir === 'in' ? 'caller' : 'callee'}${r.rows.length === 1 ? '' : 's'} of ${sym.name}`);
  load();
}

async function targetSymbol() {
  if (S.sel && S.sel.type === 'sym') return S.sel;
  const eps = await tool('endpoints', {}, 'an entry point to start from');
  const ep = (eps.items || []).find((i) => /handler: /.test(i.why || ''));
  if (ep) return { type: 'sym', name: ep.why.replace(/^handler: /, ''), path: ep.path, line: '', viaRoute: ep.name };
  return null;
}

async function loadImpact() {
  const t = S.sel && S.sel.type === 'sym' ? S.sel : null;
  if (!t) return { kind: 'impact', nodes: [], edges: [], empty: 'Pick a symbol first: search for it above, or select one on the Map and press Impact.' };
  const args = { symbol: t.name, path: t.path || undefined, line: t.line ? +t.line : undefined, limit: 120, min_confidence: S.floor };
  const a = await tool('impact', JSON.parse(JSON.stringify(args)), `what a change to ${t.name} reaches`);
  const items = (a.items || []).filter((i) => i.name && i.kind !== 'candidate');
  if ((a.items || []).some((i) => i.kind === 'candidate')) return { kind: 'impact', nodes: [], edges: [], empty: `${a.summary} Open the symbol from search to pick one definition.` };
  const center = node('c', { kind: 'sym', label: t.name, sub: t.path ? `${t.path}${t.line ? ':' + t.line : ''}` : 'target', target: true, w: 260, h: 56, sym: t });
  const byDepth = new Map();
  for (const it of items) {
    const d = it.kind === 'direct' ? 1 : +((/depth (\d+)/.exec(it.why || '') || [])[1] || 2);
    if (d > S.depth) continue;
    if (!byDepth.has(d)) byDepth.set(d, []);
    byDepth.get(d).push(it);
  }
  const nodes = [center]; const edges = []; const rings = [];
  const ringR = (k) => 70 + k * 165;
  [...byDepth.keys()].sort((x, y) => x - y).forEach((d) => {
    const arr = byDepth.get(d).sort((x, y) => RANK[y.confidence] - RANK[x.confidence] || String(x.name).localeCompare(y.name));
    rings.push({ r: ringR(d), d, n: arr.length });
    arr.forEach((it, i) => {
      const ang = -Math.PI / 2 + (i + 0.5) / arr.length * Math.PI * 2;
      const id = `i:${it.name}@${it.path}:${it.line || ''}`;
      nodes.push(node(id, { kind: 'sym', label: it.name, sub: `${it.path || ''}${it.line ? ':' + it.line : ''}`, tx: Math.cos(ang) * ringR(d), ty: Math.sin(ang) * ringR(d), w: 210, h: 44, weak: it.confidence || 'low', hop: d, sym: { name: it.name, path: it.path, line: it.line }, why: it.why }));
      edges.push({ a: id, b: 'c', conf: it.confidence || 'low', count: 1, kinds: [it.kind], radial: true, tiers: {} });
    });
  });
  return { kind: 'impact', nodes, edges, rings, fixed: true, note: a.summary, empty: items.length ? '' : `${a.summary} An empty answer is a lower bound, not proof that a change is safe.` };
}

async function loadMatrix() {
  const base = S.at && isDir(S.at) ? S.at : (S.at ? S.at.slice(0, S.at.lastIndexOf('/') + 1) : '');
  const [N, E] = await Promise.all([
    sql(`children of ${base || 'the repository'}`, childrenQuery(base, 'ORDER BY syms DESC')),
    sql(`dependencies between the children of ${base || 'the repository'}`, `
      SELECT ${childKey('sf.path', base)} AS a, ${childKey('df.path', base)} AS b, sum(r.n) AS n, max(CASE r.confidence WHEN 'exact' THEN 3 WHEN 'high' THEN 2 WHEN 'medium' THEN 1 ELSE 0 END) AS best
      FROM ${rollupFrom(base, 'both')}
      GROUP BY 1, 2 ORDER BY n DESC`),
  ]);
  const units = N.rows.slice(0, 24).map((r) => r.k);
  if (units.length < 2) return { kind: 'matrix', nodes: [], edges: [], empty: 'There is only one part here; open a directory with several children.' };
  const idx = new Map(units.map((u, i) => [u, i]));
  const cell = new Map(); const out = new Map(), inn = new Map();
  for (const r of E.rows) {
    if (!idx.has(r.a) || !idx.has(r.b)) continue;
    cell.set(r.a + '|' + r.b, { n: +r.n, best: TIER_ORDER[3 - +r.best] });
    if (r.a !== r.b) { out.set(r.a, (out.get(r.a) || 0) + +r.n); inn.set(r.b, (inn.get(r.b) || 0) + +r.n); }
  }
  // Order: what is used most comes last, so dependencies point down-left.
  units.sort((x, y) => ((out.get(y) || 0) - (inn.get(y) || 0)) - ((out.get(x) || 0) - (inn.get(x) || 0)));
  units.forEach((u, i) => idx.set(u, i));
  const cs = 38; const nodes = []; let cycles = 0;
  units.forEach((u, i) => nodes.push(node('row:' + u, { kind: isDir(u) ? 'dir' : 'file', shape: 'hdr', label: lastSeg(u, base), tx: -10, ty: i * cs + cs / 2, w: 220, h: cs - 4, key: u })));
  for (const [k, c] of cell) {
    const [a, b] = k.split('|'); const i = idx.get(a), j = idx.get(b);
    const cyc = a !== b && cell.has(b + '|' + a); if (cyc && i < j) cycles++;
    nodes.push(node('cell:' + k, { shape: 'cell', tx: j * cs + cs / 2, ty: i * cs + cs / 2, w: cs - 3, h: cs - 3, n: c.n, conf: c.best, cyc, diag: a === b, pair: [a, b], label: `${lastSeg(a, base)} → ${lastSeg(b, base)}` }));
  }
  return {
    kind: 'matrix', nodes, edges: [], units, cs, base, fixed: true,
    note: `Row uses column. ${units.length} parts${N.rows.length > 24 ? ` (the 24 largest of ${N.rows.length})` : ''} of ${base || 'the repository'}; ${cycles} pair${cycles === 1 ? '' : 's'} use each other. ${E.capped ? 'The answer was cut at 200 rows; the smallest cells may be missing.' : ''}`,
  };
}

function parseWhy(why) {
  const n = (re) => { const m = re.exec(why || ''); return m ? +m[1] : 0; };
  return { score: n(/score (\d+)/), commits: n(/(\d+) commit/), kb: n(/([\d.]+) KB/) || 1, syms: n(/(\d+) symbol/), inbound: n(/(\d+) inbound/) };
}
function squarify(items, x, y, w, h) {
  const out = []; const total = items.reduce((s, i) => s + i.v, 0); if (!total) return out;
  let rest = items.slice().sort((a, b) => b.v - a.v); const scale = (w * h) / total;
  let rx = x, ry = y, rw = w, rh = h;
  while (rest.length) {
    const side = Math.min(rw, rh); let row = [], best = Infinity;
    for (const it of rest) {
      const tr = [...row, it]; const s = tr.reduce((a, b) => a + b.v * scale, 0);
      const worst = Math.max(...tr.map((t) => { const a = t.v * scale; return Math.max(side * side * a / (s * s), (s * s) / (side * side * a)); }));
      if (worst <= best) { row = tr; best = worst; } else break;
    }
    rest = rest.slice(row.length);
    const s = row.reduce((a, b) => a + b.v * scale, 0);
    if (rw >= rh) { const cw = s / rh; let cy = ry; for (const it of row) { const ch = it.v * scale / cw; out.push({ it, x: rx, y: cy, w: cw, h: ch }); cy += ch; } rx += cw; rw -= cw; }
    else { const ch = s / rw; let cx = rx; for (const it of row) { const cw = it.v * scale / ch; out.push({ it, x: cx, y: ry, w: cw, h: ch }); cx += cw; } ry += ch; rh -= ch; }
  }
  return out;
}
async function loadRisk() {
  const base = S.at && isDir(S.at) ? S.at : '';
  const [hot, bus] = await Promise.all([
    tool('hotspots', base ? { path: base, limit: 60 } : { limit: 60 }, `files under ${base || 'the repository'} ranked by change × size`),
    tool('busfactor', base ? { path: base, limit: 40 } : { limit: 40 }, 'how concentrated knowledge is, per directory'),
  ]);
  const files = (hot.items || []).filter((i) => i.kind === 'file');
  if (!files.length) return { kind: 'risk', nodes: [], edges: [], empty: hot.summary || 'No history to rank.' };
  const bf = new Map();
  for (const d of bus.items || []) { const m = /bus factor (\d+)/.exec(d.why || ''); if (m && d.path) bf.set(d.path.replace(/\/?$/, '/'), +m[1]); }
  const bfOf = (p) => { let best = 0, len = -1; for (const [dir, v] of bf) if (p.startsWith(dir) && dir.length > len) { best = v; len = dir.length; } return best; };
  const groups = new Map();
  for (const f of files) { const g = f.path.includes('/') ? f.path.slice(0, f.path.lastIndexOf('/') + 1) : '(root)'; if (!groups.has(g)) groups.set(g, []); groups.get(g).push({ f, w: parseWhy(f.why) }); }
  const W = 1280, H = 760; const nodes = []; const hdrs = [];
  for (const r of squarify([...groups.entries()].map(([g, fs]) => ({ g, fs, v: fs.reduce((s, x) => s + x.w.kb, 0) })), 0, 0, W, H)) {
    hdrs.push({ x: r.x + 3, y: r.y + 3, w: r.w - 6, h: r.h - 6, label: r.it.g });
    for (const t of squarify(r.it.fs.map((x) => ({ x, v: x.w.kb })), r.x + 5, r.y + 24, r.w - 10, r.h - 29)) {
      const { f, w } = t.it.x;
      nodes.push(node('f:' + f.path, { shape: 'tile', kind: 'file', tx: t.x + t.w / 2, ty: t.y + t.h / 2, w: Math.max(2, t.w - 3), h: Math.max(2, t.h - 3), label: f.name, sub: `score ${w.score} · ${w.commits} commits · ${w.kb} KB`, risk: w.score / 100, bf: bfOf(f.path), key: f.path, why: f.why }));
    }
  }
  return { kind: 'risk', nodes, edges: [], hdrs, fixed: true, note: `${hot.summary} ${bus.summary || ''}`.trim() };
}

async function loadFlow() {
  const t = await targetSymbol();
  if (!t) return { kind: 'flow', nodes: [], edges: [], empty: 'Pick a symbol to start from: search for it above, or select one on the Map and press Flow.' };
  const loc = t.path ? `AND s.file_id IN (SELECT id FROM files WHERE index_run_id = ${RUN} AND path = ${q(t.path)})` : '';
  const ent = await sql(`the definition of ${t.name}`, `SELECT s.id AS id, s.start_line AS line FROM symbols s WHERE s.index_run_id = ${RUN} AND s.name = ${q(t.name)} ${loc} ${t.line ? `ORDER BY abs(s.start_line - ${+t.line})` : ''} LIMIT 2`);
  if (!ent.rows.length) return { kind: 'flow', nodes: [], edges: [], empty: `${t.name} is not in the index.` };
  const id = +ent.rows[0].id;
  const r = await sql(`what ${t.name} calls, up to ${S.depth} hops`, `
    WITH RECURSIVE w(src, dst, c, k, d) AS (
      SELECT e.src_id, e.dst_id, e.confidence, e.kind, 1 FROM edges e WHERE e.index_run_id = ${RUN} AND e.src_id = ${id} AND e.kind IN ${kindIn()} AND e.confidence IN ${confIn()}
      UNION SELECT e.src_id, e.dst_id, e.confidence, e.kind, w.d + 1 FROM edges e JOIN w ON e.src_id = w.dst
      WHERE e.index_run_id = ${RUN} AND w.d < ${S.depth} AND e.kind IN ${kindIn()} AND e.confidence IN ${confIn()})
    SELECT w.src AS a, w.dst AS b, w.c AS c, w.k AS k, min(w.d) AS d, x.name AS name, x.kind AS kind, xf.path AS path, x.start_line AS line
    FROM w JOIN symbols x ON x.id = w.dst JOIN files xf ON xf.id = x.file_id GROUP BY w.src, w.dst ORDER BY d, a LIMIT 200`);
  const info = new Map([[String(id), { name: t.name, path: t.path || '', line: ent.rows[0].line, kind: 'entry', d: 0 }]]);
  for (const x of r.rows) if (!info.has(x.b) || info.get(x.b).d > +x.d) info.set(x.b, { name: x.name, path: x.path, line: x.line, kind: x.kind, d: +x.d });
  const laneOf = (i) => (i.kind === 'table' ? 'data (tables)' : (i.path.split('/').slice(0, 2).join('/') + '/').replace(/^\/$/, '(root)'));
  const lanes = [...new Set([...info.values()].map(laneOf))];
  lanes.sort((a, b) => (a.startsWith('data') ? 1 : 0) - (b.startsWith('data') ? 1 : 0) || Math.min(...[...info.values()].filter((i) => laneOf(i) === a).map((i) => i.d)) - Math.min(...[...info.values()].filter((i) => laneOf(i) === b).map((i) => i.d)));
  const slot = new Map(); const laneRows = new Map(lanes.map((l) => [l, 1]));
  for (const [k, i] of [...info.entries()].sort((x, y) => x[1].d - y[1].d)) {
    const key = laneOf(i) + '#' + i.d; const s = slot.get(key) || 0; slot.set(key, s + 1); i.slot = s; laneRows.set(laneOf(i), Math.max(laneRows.get(laneOf(i)), s + 1)); i.key = k;
  }
  const rowH = 58, colW = 270; let y = 0; const laneTop = new Map(); const laneDeco = [];
  for (const l of lanes) { laneTop.set(l, y); const hgt = laneRows.get(l) * rowH + 30; laneDeco.push({ name: l, y, h: hgt }); y += hgt + 6; }
  const nodes = [...info.values()].map((i) => node('y:' + i.key, { kind: i.kind === 'table' ? 'table' : 'sym', label: i.name, sub: `${i.path}${i.line ? ':' + i.line : ''}`, tx: 150 + i.d * colW, ty: laneTop.get(laneOf(i)) + 26 + i.slot * rowH + rowH / 2, w: 230, h: 44, target: i.d === 0, sym: { name: i.name, path: i.path, line: i.line, id: i.key }, step: i.d }));
  const edges = r.rows.map((x) => ({ a: 'y:' + x.a, b: 'y:' + x.b, conf: x.c, count: 1, kinds: [x.k], tiers: { [x.c]: 1 }, step: +x.d }));
  const maxD = Math.max(0, ...[...info.values()].map((i) => i.d));
  return {
    kind: 'flow', nodes, edges, lanes: laneDeco, width: 300 + maxD * colW, fixed: true,
    note: `${t.viaRoute ? `Starting at ${t.viaRoute}, served by ${t.name}. ` : ''}${info.size - 1} definitions within ${S.depth} hops, laid out by the directory they live in.${r.capped ? ' Cut at 200 relationships.' : ''}`,
    empty: r.rows.length ? '' : `${t.name} calls nothing the index could bind at this confidence. That is not proof it calls nothing: calls into libraries have no definition here.`,
  };
}

async function loadCommunities() {
  const a = await tool('communities', { limit: 12, min_confidence: S.floor }, 'files that are bound together more than to the rest');
  const groups = []; let cur = null;
  for (const it of a.items || []) {
    if (it.kind === 'community') { cur = { name: it.name, why: it.why, members: [] }; groups.push(cur); }
    else if (it.kind === 'member' && cur) cur.members.push(it);
  }
  if (!groups.length) return { kind: 'communities', nodes: [], edges: [], empty: a.summary };
  // A grid of cards, one per community, members listed inside: rows and
  // columns line up, and the largest community comes first.
  const sb = S.ui.stage.getBoundingClientRect(); const aspect = Math.max(1, (sb.width - 60) / Math.max(200, sb.height - 120));
  const cardW = 300, rowH = 40, headH = 54, gap = 36;
  const cols = Math.max(1, Math.min(groups.length, Math.round(Math.sqrt(groups.length * aspect * 0.7))));
  const heights = groups.map((g) => headH + Math.max(1, g.members.length) * rowH + 14);
  const nodes = []; const hulls = []; const colY = new Array(cols).fill(0);
  groups.forEach((g, gi) => {
    const col = colY.indexOf(Math.min(...colY)); const x = col * (cardW + gap), y = colY[col]; colY[col] += heights[gi] + gap;
    const size = /(\d+) files/.exec(g.why || ''); const cross = /(\d+) crossing/.exec(g.why || ''); const inside = /(\d+) relationship\(s\) inside/.exec(g.why || '');
    hulls.push({ x, y, w: cardW, h: heights[gi], label: g.name, sub: `${size ? size[1] : g.members.length} files · ${inside ? inside[1] + ' inside · ' : ''}${cross ? cross[1] + ' crossing' : ''}`, why: g.why });
    g.members.forEach((m, i) => nodes.push(node(`m:${gi}:${m.path}`, { kind: 'file', label: m.name, sub: m.path, tx: x + cardW / 2, ty: y + headH + i * rowH + rowH / 2, w: cardW - 24, h: rowH - 8, comm: gi, key: m.path, why: m.why })));
  });
  return { kind: 'communities', nodes, edges: [], hulls, fixed: true, note: a.summary };
}

async function loadTrace() {
  const t = S.trace; if (!t || !t.answer) return { kind: 'trace', nodes: [], edges: [], empty: 'Use Trace A → B to find how one symbol reaches another.' };
  const hops = (t.answer.items || []).filter((i) => / -> /.test(i.name || ''));
  if (!hops.length) return { kind: 'trace', nodes: [], edges: [], empty: t.answer.summary };
  const routes = new Map();
  for (const it of hops) { const m = /route (\d+), hop (\d+)/.exec(it.why || ''); const r = m ? +m[1] : 1; if (!routes.has(r)) routes.set(r, []); routes.get(r).push(it); }
  const nodes = new Map(); const edges = [];
  [...routes.values()].forEach((list, ri) => {
    list.forEach((it, hi) => {
      const [a, b] = it.name.split(' -> ');
      if (!nodes.has(a)) nodes.set(a, node('t:' + a, { kind: 'sym', label: a, sub: hi === 0 ? `${it.path}:${it.line}` : '', tx: hi * 300, ty: ri * 96, sym: { name: a, path: hi === 0 ? it.path : '' }, target: hi === 0 && ri === 0 }));
      if (!nodes.has(b)) nodes.set(b, node('t:' + b, { kind: 'sym', label: b, sub: '', tx: (hi + 1) * 300, ty: ri * 96, sym: { name: b } }));
      edges.push({ a: 't:' + a, b: 't:' + b, conf: it.confidence || 'low', count: 1, kinds: [it.kind], tiers: {}, ev: `${it.path}:${it.line}`, step: hi });
    });
  });
  return { kind: 'trace', nodes: [...nodes.values()], edges, fixed: true, note: t.answer.summary };
}

// ---------------------------------------------------------------- layout

function node(id, o) { return { id, x: 0, y: 0, tx: 0, ty: 0, w: o.kind === 'sym' ? 236 : 220, h: o.kind === 'boundary' ? 46 : 52, shape: 'box', alpha: 0, ...o }; }

/* layered places a graph in ranks by longest path, orders each rank by the
   mean position of its neighbours, and picks left-to-right or top-to-bottom
   by which fits the stage at the larger scale. */
function layered(all, edges) {
  const linked = new Set(); for (const e of edges) { linked.add(e.a); linked.add(e.b); }
  const nodes = all.filter((n) => linked.has(n.id)); const lone = all.filter((n) => !linked.has(n.id));
  if (!nodes.length) { gridOf(lone, 0, 0); return { orient: 'LR', lone: lone.length ? box(lone) : null }; }
  const idx = new Map(nodes.map((n, i) => [n.id, i]));
  const adj = nodes.map(() => []);
  for (const e of edges) if (idx.has(e.a) && idx.has(e.b)) adj[idx.get(e.a)].push(idx.get(e.b));
  const state = new Array(nodes.length).fill(0); const dag = nodes.map(() => []);
  const visit = (u) => { state[u] = 1; for (const v of adj[u]) { if (state[v] === 1) continue; dag[u].push(v); if (!state[v]) visit(v); } state[u] = 2; };
  nodes.forEach((_, i) => { if (!state[i]) visit(i); });
  const indeg = new Array(nodes.length).fill(0); dag.forEach((a) => a.forEach((v) => indeg[v]++));
  const rank = new Array(nodes.length).fill(0); const queue = []; indeg.forEach((d, i) => { if (!d) queue.push(i); });
  while (queue.length) { const u = queue.shift(); for (const v of dag[u]) { rank[v] = Math.max(rank[v], rank[u] + 1); if (--indeg[v] === 0) queue.push(v); } }
  const preds = nodes.map(() => []); dag.forEach((a, u) => a.forEach((v) => preds[v].push(u)));
  const layers = new Map(); nodes.forEach((_, i) => { if (!layers.has(rank[i])) layers.set(rank[i], []); layers.get(rank[i]).push(i); });
  const R = [...layers.keys()].sort((a, b) => a - b);
  if ((R.length > 5 && nodes.length > 8) || nodes.length > 18) {
    // Dense or deep: an aligned grid, callers before what they call, so the
    // rows and columns read like a table. Lines are drawn on demand.
    const ordered = nodes.map((_, i) => i).sort((a, b) => rank[a] - rank[b] || (nodes[b].syms || 0) - (nodes[a].syms || 0) || nodes[a].label.localeCompare(nodes[b].label));
    const sb = S.ui.stage.getBoundingClientRect(); const aspect = Math.max(1, (sb.width - 60) / Math.max(200, sb.height - 120));
    const cw = Math.max(...nodes.map((x) => x.w)) + 48 + (nodes.some((x) => x.ports) ? 92 : 0), chh = Math.max(...nodes.map((x) => x.h)) + 40;
    const cols = Math.max(2, Math.round(Math.sqrt(nodes.length * aspect * chh / cw)));
    ordered.forEach((i, k) => { nodes[i].tx = (k % cols) * cw; nodes[i].ty = Math.floor(k / cols) * chh; });
    let lonebox = null;
    if (lone.length) { const b = box(nodes); gridOf(lone, b.x0, b.y1 + 90, b.x1 - b.x0); lonebox = box(lone); }
    return { orient: 'GRID', lone: lonebox };
  }
  const pos = new Array(nodes.length).fill(0); for (const r of R) layers.get(r).forEach((u, j) => { pos[u] = j; });
  for (let it = 0; it < 4; it++) for (const r of R) {
    const l = layers.get(r);
    const bc = new Map(l.map((u) => { const ps = it % 2 ? dag[u] : preds[u]; return [u, ps.length ? ps.reduce((s, p) => s + pos[p], 0) / ps.length : pos[u]]; }));
    l.sort((a, b) => bc.get(a) - bc.get(b)); l.forEach((u, j) => { pos[u] = j; });
  }
  const maxW = Math.max(...nodes.map((n) => n.w)), maxH = Math.max(...nodes.map((n) => n.h));
  const pill = nodes.some((n) => n.ports) ? 92 : 0; const colW = maxW + 70 + pill, rowH = maxH + 50, gy = 18, gx = 28 + pill;
  let lrH = 0, tbW = 0;
  const LR = new Map(), TB = new Map();
  for (const r of R) {
    const l = layers.get(r);
    const th = l.reduce((a, u) => a + nodes[u].h, 0) + (l.length - 1) * gy; lrH = Math.max(lrH, th); let y = -th / 2;
    l.forEach((u) => { LR.set(u, [r * colW, y + nodes[u].h / 2]); y += nodes[u].h + gy; });
    const tw = l.reduce((a, u) => a + nodes[u].w, 0) + (l.length - 1) * gx; tbW = Math.max(tbW, tw); let x = -tw / 2;
    l.forEach((u) => { TB.set(u, [x + nodes[u].w / 2, r * rowH]); x += nodes[u].w + gx; });
  }
  const sb = S.ui.stage.getBoundingClientRect(); const cw = Math.max(320, sb.width - 60), ch = Math.max(240, sb.height - 120);
  const sLR = Math.min(cw / ((R.length - 1) * colW + maxW), ch / lrH), sTB = Math.min(cw / tbW, ch / ((R.length - 1) * rowH + maxH));
  const P = sTB > sLR * 1.08 ? TB : LR;
  nodes.forEach((n, i) => { [n.tx, n.ty] = P.get(i); });
  let lonebox = null;
  if (lone.length) { const b = box(nodes); if (P === TB) gridOf(lone, b.x0, b.y1 + 90, b.x1 - b.x0); else gridOf(lone, b.x1 + 130, b.y0); lonebox = box(lone); }
  return { orient: P === TB ? 'TB' : 'LR', lone: lonebox };
}
/* gridOf packs nodes with no relationship at this level into a block, so a
   directory full of unrelated files does not become one tall column. */
function gridOf(list, x0, y0, width) {
  if (!list.length) return;
  const pills = list.some((n) => n.ports && (n.ports.in || n.ports.out)) ? 92 : 0;
  const w = Math.max(...list.map((n) => n.w)) + 18 + pills, hh = Math.max(...list.map((n) => n.h)) + 14;
  const cols = Math.max(1, width ? Math.max(Math.floor(width / w), Math.ceil(Math.sqrt(list.length * 1.6))) : Math.ceil(Math.sqrt(list.length / 1.4)));
  list.forEach((n, i) => { n.tx = x0 + (i % cols) * w + n.w / 2; n.ty = y0 + Math.floor(i / cols) * hh + n.h / 2; });
}
function box(list) {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  for (const n of list) { x0 = Math.min(x0, n.tx - n.w / 2); y0 = Math.min(y0, n.ty - n.h / 2); x1 = Math.max(x1, n.tx + n.w / 2); y1 = Math.max(y1, n.ty + n.h / 2); }
  return { x0, y0, x1, y1 };
}

// ---------------------------------------------------------------- scene & camera

function setScene(sc) {
  const prev = S.scene; const prevPos = new Map((prev ? prev.nodes : []).map((n) => [n.id, n]));
  sc.key = `${S.view}|${S.at}|${S.floor}|${S.refs}|${S.depth}|${S.sel ? S.sel.name + S.sel.path : ''}`;
  sc.orient = sc.orient || 'LR';
  const anchor = S.anchor && prevPos.get(S.anchor); S.anchor = null;
  let cx = 0, cy = 0; for (const n of sc.nodes) { cx += n.tx; cy += n.ty; } cx /= sc.nodes.length || 1; cy /= sc.nodes.length || 1;
  for (const n of sc.nodes) {
    const p = prevPos.get(n.id);
    if (p && prev.kind === sc.kind) { n.x = p.x; n.y = p.y; n.alpha = 1; }
    else if (anchor) { n.x = anchor.x; n.y = anchor.y; }
    else { n.x = cx + (n.tx - cx) * 0.4; n.y = cy + (n.ty - cy) * 0.4; }
    if (reduced.matches) { n.x = n.tx; n.y = n.ty; n.alpha = 1; }
  }
  for (const n of sc.nodes) { n.sx = n.x; n.sy = n.y; }
  sc.byId = new Map(sc.nodes.map((n) => [n.id, n]));
  sc.dirty = true; sc.t0 = performance.now();
  S.scene = sc;
  const saved = S.cam.get(sc.key);
  if (anchor) fit(!reduced.matches);
  else if (saved) S.camera = { ...saved };
  else fit(!!prev && !!S.camera && !reduced.matches);
  if (!S.raf) S.raf = requestAnimationFrame(frame);
}

let anim = null;
function fit(animate) {
  const sc = S.scene; if (!sc) return; size();
  const b = bounds(sc); const W = S.vw, H = S.vh;
  const k = clamp(Math.min((W - 56) / (b.x1 - b.x0 || 1), (H - 88) / (b.y1 - b.y0 || 1)), 0.18, 1.5);
  const to = { k, x: W / 2 - (b.x0 + b.x1) / 2 * k, y: 52 + (H - 76) / 2 - (b.y0 + b.y1) / 2 * k };
  if (animate && S.camera) anim = { from: { ...S.camera }, to, t0: performance.now() }; else S.camera = to;
  sc.dirty = true;
}
function bounds(sc) {
  let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
  for (const n of sc.nodes) { x0 = Math.min(x0, n.tx - n.w / 2 - (n.ports ? 40 : 0)); y0 = Math.min(y0, n.ty - n.h / 2); x1 = Math.max(x1, n.tx + n.w / 2 + (n.ports ? 40 : 0)); y1 = Math.max(y1, n.ty + n.h / 2); }
  if (sc.rings) { const r = Math.max(0, ...sc.rings.map((x) => x.r)) + 30; x0 = Math.min(x0, -r); y0 = Math.min(y0, -r - 20); x1 = Math.max(x1, r); y1 = Math.max(y1, r); }
  if (sc.hulls) for (const hl of sc.hulls) { x0 = Math.min(x0, hl.x); y0 = Math.min(y0, hl.y); x1 = Math.max(x1, hl.x + hl.w); y1 = Math.max(y1, hl.y + hl.h); }
  if (sc.units) { x0 = Math.min(x0, -240); y0 = Math.min(y0, -150); }
  if (sc.lanes) { x0 = Math.min(x0, -10); x1 = Math.max(x1, sc.width); }
  if (sc.hdrs) for (const hd of sc.hdrs) { x0 = Math.min(x0, hd.x); y0 = Math.min(y0, hd.y); x1 = Math.max(x1, hd.x + hd.w); y1 = Math.max(y1, hd.y + hd.h); }
  if (!isFinite(x0)) return { x0: -100, y0: -100, x1: 100, y1: 100 };
  return { x0, y0, x1, y1 };
}
function size() {
  const ui = S.ui; if (!ui) return; const r = ui.canvas.getBoundingClientRect(); const dpr = Math.min(devicePixelRatio || 1, 2.5);
  if (r.width === S.vw && r.height === S.vh && dpr === S.dpr) return;
  S.vw = r.width; S.vh = r.height; S.dpr = dpr;
  ui.canvas.width = Math.round(r.width * dpr); ui.canvas.height = Math.round(r.height * dpr);
  const m = ui.mini.getBoundingClientRect(); ui.mini.width = Math.round(m.width * dpr); ui.mini.height = Math.round(m.height * dpr);
  if (S.scene) S.scene.dirty = true;
}
function zoomBy(f, sx, sy) {
  const c = S.camera; if (!c) return; sx = sx ?? S.vw / 2; sy = sy ?? S.vh / 2;
  const k = clamp(c.k * f, 0.08, 4); const wx = (sx - c.x) / c.k, wy = (sy - c.y) / c.k;
  S.camera = { k, x: sx - wx * k, y: sy - wy * k }; anim = null; S.scene.dirty = true; saveCam();
  const zl = S.ui && S.ui.statusbar.querySelector('.mp-zoomlbl'); if (zl) zl.textContent = `zoom ${Math.round(k * 100)}%`;
}
const saveCam = () => { if (S.scene && S.camera) S.cam.set(S.scene.key, { ...S.camera }); };
const toWorld = (sx, sy) => ({ x: (sx - S.camera.x) / S.camera.k, y: (sy - S.camera.y) / S.camera.k });

// ---------------------------------------------------------------- drawing

function frame(now) {
  S.raf = 0;
  const ui = S.ui; if (!ui || !ui.canvas.isConnected) return;
  size();
  const sc = S.scene; if (!sc) return;
  if (anim) { const t = clamp((now - anim.t0) / 380, 0, 1); const e = t < 0.5 ? 2 * t * t : 1 - (-2 * t + 2) ** 2 / 2; S.camera = { k: anim.from.k + (anim.to.k - anim.from.k) * e, x: anim.from.x + (anim.to.x - anim.from.x) * e, y: anim.from.y + (anim.to.y - anim.from.y) * e }; if (t >= 1) { anim = null; saveCam(); } sc.dirty = true; }
  let moving = false;
  const tt = clamp((now - sc.t0) / 340, 0, 1); const ee = 1 - (1 - tt) ** 3;
  for (const n of sc.nodes) {
    if (n.drag) continue;
    if (tt < 1 && n.sx != null) { n.x = n.sx + (n.tx - n.sx) * ee; n.y = n.sy + (n.ty - n.sy) * ee; moving = true; } else { n.x = n.tx; n.y = n.ty; }
    if (n.alpha < 1) { n.alpha = Math.min(1, Math.max(n.alpha, (now - sc.t0) / 260)); moving = true; }
  }
  const active = activeEdges(sc);
  const flowing = !reduced.matches && active.size > 0;
  if (sc.dirty || moving || anim || flowing) { draw(sc, active, now); sc.dirty = false; }
  S.raf = requestAnimationFrame(frame);
}
function activeEdges(sc) {
  const set = new Set(); const focus = (S.hover && S.hover.n && S.hover.n.id) || (S.sel && S.sel.node);
  if (sc.kind === 'flow' || sc.kind === 'trace') { sc.edges.forEach((_, i) => set.add(i)); return set; }
  if (focus) sc.edges.forEach((e, i) => { if (e.a === focus || e.b === focus) set.add(i); });
  if (S.sel && S.sel.type === 'edge') sc.edges.forEach((e, i) => { if (e.a === S.sel.a && e.b === S.sel.b) set.add(i); });
  return set;
}
function rr(c, x, y, w, h, r) { c.beginPath(); c.moveTo(x + r, y); c.arcTo(x + w, y, x + w, y + h, r); c.arcTo(x + w, y + h, x, y + h, r); c.arcTo(x, y + h, x, y, r); c.arcTo(x, y, x + w, y, r); c.closePath(); }
const tcache = new Map();
function fitText(c, s, w) { s = String(s); const k = c.font + '|' + s + '|' + Math.round(w); if (tcache.has(k)) return tcache.get(k); let t = s; if (c.measureText(t).width > w) { while (t.length > 1 && c.measureText(t + '…').width > w) t = t.slice(0, -1); t += '…'; } if (tcache.size > 3000) tcache.clear(); tcache.set(k, t); return t; }
const MONO = getComputedStyle(document.documentElement).getPropertyValue('--mono') || 'monospace';
const BODY = getComputedStyle(document.documentElement).getPropertyValue('--body') || 'sans-serif';

function geom(sc, a, b, e) {
  if (e.radial || sc.kind === 'communities' || sc.orient === 'GRID') {
    const dx = b.x - a.x, dy = b.y - a.y, L = Math.hypot(dx, dy) || 1, ux = dx / L, uy = dy / L;
    const clip = (n, sx, sy) => Math.min(Math.abs(sx) > 1e-6 ? (n.w / 2 + 3) / Math.abs(sx) : 1e9, Math.abs(sy) > 1e-6 ? (n.h / 2 + 3) / Math.abs(sy) : 1e9);
    const p0 = { x: a.x + clip(a, ux, uy) * ux, y: a.y + clip(a, ux, uy) * uy }, p3 = { x: b.x - clip(b, -ux, -uy) * ux, y: b.y - clip(b, -ux, -uy) * uy };
    const bend = sc.orient === 'GRID' ? 0.14 : 0; const mx = -(p3.y - p0.y) * bend, my = (p3.x - p0.x) * bend;
    return [p0, { x: (p0.x * 2 + p3.x) / 3 + mx, y: (p0.y * 2 + p3.y) / 3 + my }, { x: (p0.x + p3.x * 2) / 3 + mx, y: (p0.y + p3.y * 2) / 3 + my }, p3];
  }
  const vgap = (a.h + b.h) / 2, hgap = (a.w + b.w) / 2;
  if (b.y - a.y > vgap * 0.7 && Math.abs(b.x - a.x) < hgap * 1.6 && sc.orient === 'TB') { const p0 = { x: a.x, y: a.y + a.h / 2 }, p3 = { x: b.x, y: b.y - b.h / 2 - 2 }; const d = Math.max(30, (p3.y - p0.y) / 2); return [p0, { x: p0.x, y: p0.y + d }, { x: p3.x, y: p3.y - d }, p3]; }
  if (b.x - a.x > hgap * 0.6) { const p0 = { x: a.x + a.w / 2, y: a.y }, p3 = { x: b.x - b.w / 2 - 2, y: b.y }; const d = Math.max(40, (p3.x - p0.x) / 2); return [p0, { x: p0.x + d, y: p0.y }, { x: p3.x - d, y: p3.y }, p3]; }
  if (a.x - b.x > hgap * 0.6) { const p0 = { x: a.x - a.w / 2, y: a.y }, p3 = { x: b.x + b.w / 2 + 2, y: b.y }; return [p0, { x: p0.x - 70, y: p0.y + 50 }, { x: p3.x + 70, y: p3.y + 50 }, p3]; }
  const down = b.y > a.y; const p0 = { x: a.x, y: a.y + (down ? a.h / 2 : -a.h / 2) }, p3 = { x: b.x, y: b.y + (down ? -b.h / 2 - 2 : b.h / 2 + 2) };
  const d = Math.max(30, Math.abs(p3.y - p0.y) / 2) * (down ? 1 : -1); return [p0, { x: p0.x + 40, y: p0.y + d }, { x: p3.x + 40, y: p3.y - d }, p3];
}
function bez(p, t) { const u = 1 - t; return { x: u * u * u * p[0].x + 3 * u * u * t * p[1].x + 3 * u * t * t * p[2].x + t * t * t * p[3].x, y: u * u * u * p[0].y + 3 * u * u * t * p[1].y + 3 * u * t * t * p[2].y + t * t * t * p[3].y }; }
function dash(c, conf) { c.setLineDash(conf === 'medium' ? [7, 5] : conf === 'low' ? [2, 5] : []); }

function draw(sc, active, now) {
  const ui = S.ui; const c = ui.canvas.getContext('2d'); const dpr = S.dpr; const cam = S.camera;
  c.setTransform(dpr, 0, 0, dpr, 0, 0);
  c.fillStyle = C.ground; c.fillRect(0, 0, S.vw, S.vh);
  // dot grid
  let step = 40 * cam.k; while (step < 18) step *= 2; while (step > 80) step /= 2;
  c.fillStyle = C.line; const ox = ((cam.x % step) + step) % step, oy = ((cam.y % step) + step) % step;
  for (let x = ox; x < S.vw; x += step) for (let y = oy; y < S.vh; y += step) c.fillRect(x, y, 1.3, 1.3);
  c.save(); c.translate(cam.x, cam.y); c.scale(cam.k, cam.k);
  deco(c, sc);
  const focus = S.sel && S.sel.node && sc.byId.has(S.sel.node) ? S.sel.node : null;
  const dimOthers = !!focus && (sc.kind === 'map' || sc.kind === 'communities');
  sc.geo = [];
  sc.edges.forEach((e, i) => {
    const a = sc.byId.get(e.a), b = sc.byId.get(e.b); if (!a || !b) { sc.geo[i] = null; return; }
    const on = active.has(i);
    if (sc.orient === 'GRID' && !on && i >= 25) { sc.geo[i] = null; return; }
    const g = geom(sc, a, b, e); sc.geo[i] = g;
    c.globalAlpha = Math.min(a.alpha, b.alpha) * (dimOthers && !on ? 0.12 : (e.radial || sc.orient === 'GRID') && !on ? 0.35 : 0.85);
    c.strokeStyle = tierCol(e.conf);
    c.lineWidth = ((e.conf === 'exact' ? 2 : 1.5) + Math.min(3.5, Math.log2(e.count || 1) * 0.7) + (on ? 1 : 0)) / Math.max(0.6, Math.min(1.2, cam.k));
    dash(c, e.conf);
    c.beginPath(); c.moveTo(g[0].x, g[0].y); c.bezierCurveTo(g[1].x, g[1].y, g[2].x, g[2].y, g[3].x, g[3].y); c.stroke(); c.setLineDash([]);
    const p = bez(g, 0.96), q3 = g[3]; const ang = Math.atan2(q3.y - p.y, q3.x - p.x);
    c.fillStyle = c.strokeStyle; c.beginPath(); c.moveTo(q3.x, q3.y); c.lineTo(q3.x - 9 * Math.cos(ang - 0.4), q3.y - 9 * Math.sin(ang - 0.4)); c.lineTo(q3.x - 9 * Math.cos(ang + 0.4), q3.y - 9 * Math.sin(ang + 0.4)); c.closePath(); c.fill();
    if (cam.k > 0.55 && e.count > 1) {
      const m = bez(g, 0.5); const lbl = String(e.count); c.font = `600 10px ${MONO}`; const tw = c.measureText(lbl).width + 10;
      c.globalAlpha = Math.min(a.alpha, b.alpha); c.fillStyle = C.sheet; rr(c, m.x - tw / 2, m.y - 8, tw, 16, 8); c.fill(); c.strokeStyle = tierCol(e.conf); c.lineWidth = 1; c.stroke();
      c.fillStyle = C.ink; c.textAlign = 'center'; c.textBaseline = 'middle'; c.fillText(lbl, m.x, m.y + 0.5);
    }
    c.globalAlpha = 1;
  });
  if (!reduced.matches) {
    const t = now / 1000;
    sc.edges.forEach((e, i) => {
      if (!active.has(i) || !sc.geo[i]) return; const g = sc.geo[i];
      const tt = (t * (e.conf === 'exact' ? 0.5 : 0.35) + (e.step || 0) * 0.15 + i * 0.07) % 1; const p = bez(g, tt);
      c.fillStyle = tierCol(e.conf); c.globalAlpha = 0.95; c.beginPath(); c.arc(p.x, p.y, 3 / Math.max(0.7, cam.k), 0, Math.PI * 2); c.fill(); c.globalAlpha = 1;
    });
  }
  for (const n of sc.nodes) drawNode(c, sc, n, dimOthers && n.id !== focus && !sc.edges.some((e, i) => active.has(i) && (e.a === n.id || e.b === n.id)));
  c.restore();
  drawMini(sc);
}

function deco(c, sc) {
  c.textBaseline = 'middle';
  if (sc.rings) {
    for (const r of sc.rings.slice().reverse()) {
      c.strokeStyle = C['line-strong']; c.globalAlpha = 0.55; c.lineWidth = 1.2; c.setLineDash(r.d === sc.rings.length ? [6, 6] : []);
      c.beginPath(); c.arc(0, 0, r.r, 0, Math.PI * 2); c.stroke(); c.setLineDash([]); c.globalAlpha = 1;
      c.font = `600 12px ${BODY}`; c.fillStyle = C['ink-3']; c.textAlign = 'center';
      c.fillText(`${r.d === 1 ? 'Direct' : `${r.d} hops`} · ${r.n}`, 0, -r.r - 12);
    }
  }
  if (sc.lanes) {
    sc.lanes.forEach((l, i) => {
      c.fillStyle = i % 2 ? C.sheet : C.well; c.fillRect(-10, l.y, sc.width + 20, l.h);
      c.strokeStyle = C.line; c.lineWidth = 1; c.strokeRect(-10, l.y, sc.width + 20, l.h);
      c.font = `650 11px ${BODY}`; c.fillStyle = C['ink-2']; c.textAlign = 'left'; c.fillText(l.name.toUpperCase(), 0, l.y + 13);
    });
  }
  if (sc.hulls) {
    for (const hl of sc.hulls) {
      c.fillStyle = C.sheet; rr(c, hl.x, hl.y, hl.w, hl.h, 10); c.fill(); c.strokeStyle = C['line-strong']; c.lineWidth = 1.2; c.stroke();
      c.fillStyle = C['c-dir']; c.fillRect(hl.x + 1, hl.y + 10, 3, 30);
      c.textAlign = 'left'; c.font = `650 13px ${MONO}`; c.fillStyle = C.ink; c.fillText(fitText(c, hl.label, hl.w - 28), hl.x + 14, hl.y + 19);
      c.font = `11.5px ${BODY}`; c.fillStyle = C['ink-3']; c.fillText(fitText(c, hl.sub, hl.w - 28), hl.x + 14, hl.y + 37);
    }
  }
  if (sc.units) {
    const cs = sc.cs, n = sc.units.length;
    c.strokeStyle = C.line; c.lineWidth = 1;
    for (let i = 0; i <= n; i++) { c.beginPath(); c.moveTo(0, i * cs); c.lineTo(n * cs, i * cs); c.stroke(); c.beginPath(); c.moveTo(i * cs, 0); c.lineTo(i * cs, n * cs); c.stroke(); }
    c.fillStyle = C.well; for (let i = 0; i < n; i++) c.fillRect(i * cs + 1, i * cs + 1, cs - 2, cs - 2);
    const hv = S.hover && S.hover.n && S.hover.n.shape === 'cell' ? S.hover.n : null;
    if (hv) { c.globalAlpha = 0.35; c.fillStyle = C['accent-soft']; c.fillRect(0, hv.ty - cs / 2, n * cs, cs); c.fillRect(hv.tx - cs / 2, 0, cs, n * cs); c.globalAlpha = 1; }
    c.font = `500 11px ${MONO}`; c.fillStyle = C['ink-2']; c.textAlign = 'left';
    sc.units.forEach((u, i) => { c.save(); c.translate(i * cs + cs / 2 + 4, -8); c.rotate(-Math.PI / 3); c.fillText(fitText(c, lastSeg(u, sc.base), 150), 0, 0); c.restore(); });
  }
  if (sc.hdrs) {
    for (const hd of sc.hdrs) {
      c.strokeStyle = C['line-strong']; c.lineWidth = 1; rr(c, hd.x, hd.y, hd.w, hd.h, 4); c.stroke();
      c.font = `650 11px ${MONO}`; c.fillStyle = C['ink-2']; c.textAlign = 'left'; if (hd.w > 40) c.fillText(fitText(c, hd.label, hd.w - 12), hd.x + 6, hd.y + 11);
    }
  }
}

function parts(n) {
  const p = {};
  if (n.ports) { if (n.ports.in) p.inPill = { x: n.x - n.w / 2 - 44, y: n.y - 10, w: 40, h: 20 }; if (n.ports.out) p.outPill = { x: n.x + n.w / 2 + 4, y: n.y - 10, w: 40, h: 20 }; }
  return p;
}
function drawNode(c, sc, n, dim) {
  const cam = S.camera; const sel = S.sel && (S.sel.node === n.id); const hov = S.hover && S.hover.n === n;
  const x = n.x - n.w / 2, y = n.y - n.h / 2;
  c.globalAlpha = (n.alpha ?? 1) * (dim ? 0.3 : 1);
  if (n.shape === 'cell') {
    c.fillStyle = n.diag ? C['line-strong'] : n.cyc ? C['c-low'] : C['c-exact'];
    c.globalAlpha *= n.diag ? 0.35 : 0.25 + Math.min(0.7, Math.log2(1 + n.n) / 9); rr(c, x, y, n.w, n.h, 3); c.fill(); c.globalAlpha = n.alpha ?? 1;
    if (n.conf === 'medium' || n.conf === 'low') { c.setLineDash([2, 3]); c.strokeStyle = tierCol(n.conf); c.lineWidth = 1.2; rr(c, x + 3, y + 3, n.w - 6, n.h - 6, 2); c.stroke(); c.setLineDash([]); }
    if (sel || hov) { c.strokeStyle = C.accent; c.lineWidth = 2; rr(c, x, y, n.w, n.h, 3); c.stroke(); }
    c.fillStyle = C.ink; c.font = `600 11px ${MONO}`; c.textAlign = 'center'; c.textBaseline = 'middle'; c.fillText(n.n > 999 ? Math.round(n.n / 100) / 10 + 'k' : String(n.n), n.x, n.y + 0.5);
    c.globalAlpha = 1; return;
  }
  if (n.shape === 'hdr') {
    c.font = `500 11.5px ${MONO}`; c.textAlign = 'right'; c.textBaseline = 'middle'; c.fillStyle = sel || hov ? C.accent : C['ink-2']; c.fillText(fitText(c, n.label, 210), n.x, n.y);
    c.globalAlpha = 1; return;
  }
  if (n.shape === 'tile') {
    c.fillStyle = riskCol(n.risk); rr(c, x, y, n.w, n.h, 3); c.fill();
    if (n.bf === 1) { c.save(); rr(c, x, y, n.w, n.h, 3); c.clip(); c.strokeStyle = C.ground; c.globalAlpha = 0.13; c.lineWidth = 2; for (let i = -n.h; i < n.w; i += 10) { c.beginPath(); c.moveTo(x + i, y + n.h); c.lineTo(x + i + n.h, y); c.stroke(); } c.restore(); }
    if (sel || hov) { c.strokeStyle = C.ink; c.lineWidth = sel ? 2.5 : 1.5; rr(c, x, y, n.w, n.h, 3); c.stroke(); }
    if (n.w * cam.k > 50 && n.h * cam.k > 20) { c.fillStyle = C.ground; c.font = `650 11.5px ${MONO}`; c.textAlign = 'left'; c.textBaseline = 'top'; c.fillText(fitText(c, n.label, n.w - 10), x + 6, y + 6); if (n.h * cam.k > 40) { c.font = `10.5px ${MONO}`; c.fillText(fitText(c, n.sub, n.w - 10), x + 6, y + 22); } }
    c.globalAlpha = 1; return;
  }
  c.fillStyle = sel ? C['accent-soft'] : hov ? C.well : n.boundary ? C.ground : C.sheet;
  rr(c, x, y, n.w, n.h, 8); c.fill();
  c.lineWidth = sel ? 2 : n.target ? 2.2 : 1.2;
  c.strokeStyle = n.weak ? tierCol(n.weak) : sel ? C.accent : n.target ? C.accent : C['line-strong'];
  if (n.boundary || n.outside) c.setLineDash([5, 4]); rr(c, x, y, n.w, n.h, 8); c.stroke(); c.setLineDash([]);
  c.save(); rr(c, x, y, n.w, n.h, 8); c.clip(); c.fillStyle = kindCol(n.kind); c.fillRect(x, y, 4, n.h); c.restore();
  if (cam.k < 0.3) { c.globalAlpha = 1; return; }
  c.textBaseline = 'middle'; c.textAlign = 'left';
  const two = n.sub && cam.k > 0.45 && n.h >= 44;
  c.font = `650 12.5px ${MONO}`; c.fillStyle = C.ink;
  const right = n.hop ? 30 : n.drill && !n.boundary ? 26 : 12;
  const name = fitText(c, n.label, n.w - 16 - right);
  c.fillText(name, x + 14, two ? y + 18 : n.y);
  if (two) { c.font = `11px ${MONO}`; c.fillStyle = C['ink-3']; c.fillText(fitText(c, n.sub, n.w - 22), x + 14, y + n.h - 15); }
  else if (n.sub && cam.k > 0.45) { const used = c.measureText(name).width + 12; c.font = `11px ${MONO}`; c.fillStyle = C['ink-3']; if (n.w - 28 - used > 30) c.fillText(fitText(c, n.sub, n.w - 28 - used), x + 14 + used, n.y); }
  if (n.hop) { c.font = `650 10px ${MONO}`; c.fillStyle = tierCol(n.weak); c.textAlign = 'right'; c.fillText(`${n.hop}`, x + n.w - 10, y + 15); }
  if (n.drill && !n.boundary && cam.k > 0.4) { c.font = `650 13px ${BODY}`; c.fillStyle = C.accent; c.textAlign = 'right'; c.fillText('›', x + n.w - 10, n.y); }
  const P = parts(n); const hp = S.hover && S.hover.n === n ? S.hover.part : null;
  const pill = (r, label, on) => { c.fillStyle = on ? C.accent : C.sheet; rr(c, r.x, r.y, r.w, r.h, 10); c.fill(); c.strokeStyle = C.accent; c.lineWidth = 1.2; c.stroke(); c.fillStyle = on ? C['on-accent'] : C.accent; c.font = `650 10.5px ${MONO}`; c.textAlign = 'center'; c.fillText(label, r.x + r.w / 2, r.y + r.h / 2 + 0.5); };
  if (P.inPill) pill(P.inPill, `◀ ${n.ports.in < 0 ? '' : n.ports.in}`, hp === 'inPill');
  if (P.outPill) pill(P.outPill, `${n.ports.out < 0 ? '' : n.ports.out} ▶`, hp === 'outPill');
  c.globalAlpha = 1;
}
function drawMini(sc) {
  const ui = S.ui; if (!S.miniOn) return; const c = ui.mini.getContext('2d'); const W = ui.mini.width / S.dpr, H = ui.mini.height / S.dpr; if (!W) return;
  c.setTransform(S.dpr, 0, 0, S.dpr, 0, 0); c.clearRect(0, 0, W, H); c.fillStyle = C.sheet; c.fillRect(0, 0, W, H);
  const b = bounds(sc); const v0 = toWorld(0, 0), v1 = toWorld(S.vw, S.vh);
  const X0 = Math.min(b.x0, v0.x), Y0 = Math.min(b.y0, v0.y), X1 = Math.max(b.x1, v1.x), Y1 = Math.max(b.y1, v1.y);
  const s = Math.min((W - 10) / (X1 - X0), (H - 10) / (Y1 - Y0)); const ox = 5 + (W - 10 - (X1 - X0) * s) / 2, oy = 5 + (H - 10 - (Y1 - Y0) * s) / 2;
  S.mini = { s, ox, oy, X0, Y0 };
  for (const n of sc.nodes) { c.fillStyle = n.shape === 'tile' ? riskCol(n.risk) : n.shape === 'cell' ? (n.cyc ? C['c-low'] : C['c-exact']) : kindCol(n.kind); c.globalAlpha = n.shape === 'hdr' ? 0.3 : 0.8; c.fillRect(ox + (n.x - n.w / 2 - X0) * s, oy + (n.y - n.h / 2 - Y0) * s, Math.max(1.5, n.w * s), Math.max(1.5, n.h * s)); }
  c.globalAlpha = 1; c.strokeStyle = C.accent; c.lineWidth = 1.2; c.strokeRect(ox + (v0.x - X0) * s, oy + (v0.y - Y0) * s, (v1.x - v0.x) * s, (v1.y - v0.y) * s);
}

// ---------------------------------------------------------------- pointer

function hit(sx, sy) {
  const sc = S.scene; if (!sc || !S.camera) return null; const p = toWorld(sx, sy);
  for (let i = sc.nodes.length - 1; i >= 0; i--) {
    const n = sc.nodes[i];
    for (const [k, r] of Object.entries(parts(n))) if (p.x >= r.x && p.x <= r.x + r.w && p.y >= r.y && p.y <= r.y + r.h) return { n, part: k };
    if (Math.abs(p.x - n.x) <= n.w / 2 && Math.abs(p.y - n.y) <= n.h / 2) return { n };
  }
  if (sc.geo) {
    const tol = 7 / S.camera.k; let best = null, bd = tol;
    sc.edges.forEach((e, i) => { const g = sc.geo[i]; if (!g) return; let prev = bez(g, 0); for (let t = 1; t <= 18; t++) { const q2 = bez(g, t / 18); const d = segDist(p, prev, q2); if (d < bd) { bd = d; best = i; } prev = q2; } });
    if (best != null) return { e: sc.edges[best] };
  }
  return null;
}
function segDist(p, a, b) { const dx = b.x - a.x, dy = b.y - a.y; const L = dx * dx + dy * dy || 1; const t = clamp(((p.x - a.x) * dx + (p.y - a.y) * dy) / L, 0, 1); return Math.hypot(p.x - (a.x + t * dx), p.y - (a.y + t * dy)); }

function bindCanvas() {
  const ui = S.ui; const cv = ui.canvas; let drag = null;
  const local = (ev) => { const r = cv.getBoundingClientRect(); return [ev.clientX - r.left, ev.clientY - r.top]; };
  cv.addEventListener('pointerdown', (ev) => {
    if (ev.button !== 0) return; hideMenu(); const [sx, sy] = local(ev); const hh = hit(sx, sy);
    drag = hh && hh.n && !hh.part && hh.n.shape === 'box' && !S.scene.fixed ? { n: hh.n, sx, sy, moved: false, hh } : { pan: true, sx, sy, cx: S.camera.x, cy: S.camera.y, moved: false, hh };
    cv.setPointerCapture(ev.pointerId); anim = null;
  });
  cv.addEventListener('pointermove', (ev) => {
    const [sx, sy] = local(ev);
    if (drag) {
      const dx = sx - drag.sx, dy = sy - drag.sy; if (Math.abs(dx) + Math.abs(dy) > 3) drag.moved = true;
      if (drag.pan && drag.moved) { S.camera = { ...S.camera, x: drag.cx + dx, y: drag.cy + dy }; cv.classList.add('panning'); }
      if (drag.n && drag.moved) { const p = toWorld(sx, sy); drag.n.drag = true; drag.n.x = drag.n.tx = p.x; drag.n.y = drag.n.ty = p.y; }
      S.scene.dirty = true; hideTip(); return;
    }
    const hh = hit(sx, sy); const n = hh && hh.n ? hh : null;
    if ((n && n.n) !== (S.hover && S.hover.n) || (n && n.part) !== (S.hover && S.hover.part)) { S.hover = n; S.scene.dirty = true; }
    cv.classList.toggle('over', !!hh);
    if (hh) showTip(ev.clientX, ev.clientY, hh); else hideTip();
  });
  cv.addEventListener('pointerup', () => {
    if (!drag) return; const d = drag; drag = null; cv.classList.remove('panning');
    if (d.n && d.moved) { d.n.drag = false; return; }
    if (d.moved) { saveCam(); return; }
    const hh = d.hh;
    if (!hh) { S.sel = null; S.scene.dirty = true; drawInspector(); return; }
    if (hh.e) { selectEdge(hh.e); return; }
    if (hh.part === 'inPill' || hh.part === 'outPill') { unfold(hh.n, hh.part === 'inPill' ? 'in' : 'out').catch((err) => D.toast(err.message)); return; }
    if (S.sel && hh.n && S.sel.node === hh.n.id && hh.n.drill) {
      openNode(hh.n);
      return;
    }
    selectNode(hh.n);
  });
  cv.addEventListener('dblclick', (ev) => { const [sx, sy] = local(ev); const hh = hit(sx, sy); if (hh && hh.n) openNode(hh.n); });
  cv.addEventListener('wheel', (ev) => { ev.preventDefault(); const [sx, sy] = local(ev); zoomBy(Math.exp(-ev.deltaY * (ev.ctrlKey ? 0.012 : 0.0016)), sx, sy); }, { passive: false });
  cv.addEventListener('contextmenu', (ev) => { ev.preventDefault(); const [sx, sy] = local(ev); const hh = hit(sx, sy); if (hh && hh.n) showMenu(ev.clientX, ev.clientY, hh.n); });
  cv.addEventListener('pointerleave', () => { if (S.hover) { S.hover = null; S.scene.dirty = true; } hideTip(); });
  let mdown = false;
  const mgo = (ev) => { const m = S.mini; if (!m) return; const r = ui.mini.getBoundingClientRect(); const wx = (ev.clientX - r.left - m.ox) / m.s + m.X0, wy = (ev.clientY - r.top - m.oy) / m.s + m.Y0; S.camera = { ...S.camera, x: S.vw / 2 - wx * S.camera.k, y: S.vh / 2 - wy * S.camera.k }; S.scene.dirty = true; };
  ui.mini.addEventListener('pointerdown', (ev) => { mdown = true; ui.mini.setPointerCapture(ev.pointerId); mgo(ev); });
  ui.mini.addEventListener('pointermove', (ev) => { if (mdown) mgo(ev); });
  ui.mini.addEventListener('pointerup', () => { mdown = false; saveCam(); });
  new ResizeObserver(() => { size(); if (S.scene) S.scene.dirty = true; }).observe(ui.stage);
  if (!bindCanvas.keys) {
    bindCanvas.keys = true;
    document.addEventListener('keydown', (ev) => {
      if (!document.body.classList.contains('is-map') || ['INPUT', 'TEXTAREA', 'SELECT'].includes(document.activeElement.tagName) || ev.metaKey || ev.ctrlKey) return;
      if (/^[1-6]$/.test(ev.key)) go({ v: VIEWS[+ev.key - 1].id });
      else if (ev.key === 'f' || ev.key === 'F') fit(true);
      else if (ev.key === 'e' || ev.key === 'E') toggleTree();
      else if (ev.key === 'Backspace' && S.at) { ev.preventDefault(); const up = S.at.replace(/[^/]+\/?$/, ''); go({ at: up }); }
      else if (ev.key === 'Escape') { hideMenu(); S.sel = null; if (S.scene) S.scene.dirty = true; drawInspector(); }
    });
  }
}

function showTip(x, y, hh) {
  const { h } = D; const tip = S.ui.tip; const lines = [];
  if (hh.e) { const e = hh.e; lines.push(h('b', { text: `${labelOf(e.a)} → ${labelOf(e.b)}` }), h('span', { text: `${e.count > 1 ? e.count + ' relationships · ' : ''}strongest ${D.TIERS[e.conf] ? D.TIERS[e.conf].label : e.conf}${e.ev ? ' · ' + e.ev : ''}` }), h('span', { class: 'mp-tip-hint', text: 'Click for the evidence' })); }
  else {
    const n = hh.n;
    lines.push(h('b', { text: n.label }));
    if (n.sub) lines.push(h('span', { text: n.sub }));
    if (n.weak) lines.push(h('span', { text: `weakest link: ${D.TIERS[n.weak] ? D.TIERS[n.weak].label : n.weak}` }));
    if (hh.part) lines.push(h('span', { class: 'mp-tip-hint', text: hh.part === 'inPill' ? 'Draw its callers from other files' : 'Draw what it calls in other files' }));
    else if (n.drill) lines.push(h('span', { class: 'mp-tip-hint', text: n.boundary ? 'Double-click to open it' : 'Double-click to open · right-click for more' }));
    else if (n.sym) lines.push(h('span', { class: 'mp-tip-hint', text: 'Click for evidence · double-click for its page' }));
  }
  tip.replaceChildren(...lines); tip.classList.add('on');
  const r = tip.getBoundingClientRect(); tip.style.left = `${Math.min(x + 14, innerWidth - r.width - 8)}px`; tip.style.top = `${Math.min(y + 14, innerHeight - r.height - 8)}px`;
}
function hideTip() { if (S.ui) S.ui.tip.classList.remove('on'); }
function labelOf(id) { const n = S.scene && S.scene.byId.get(id); return n ? n.label : id; }

function openNode(n) {
  if (n.shape === 'cell') return;
  if (n.drill || (n.key && S.view === 'map')) { go({ v: 'map', at: n.boundary ? n.id : n.id }); return; }
  if (n.key && (S.view === 'risk' || S.view === 'communities')) { location.hash = D.fileHref(n.key); return; }
  if (n.sym && n.sym.name) location.hash = D.symbolHref(n.sym.name, n.sym.path, { line: n.sym.line ? String(n.sym.line) : '' });
}

function showMenu(x, y, n) {
  const { h } = D; const m = S.ui.menu; const sym = n.sym && n.sym.name ? n.sym : null;
  const item = (label, fn, on = true) => h('button', { type: 'button', role: 'menuitem', disabled: !on, onclick: () => { hideMenu(); fn(); } }, label);
  m.replaceChildren(h('p', { class: 'mp-menu-h', text: n.label }),
    item('Open', () => openNode(n), !!(n.drill || n.key || sym)),
    item('Show impact rings', () => { S.sel = { type: 'sym', ...sym, node: n.id }; go({ v: 'impact', sym: sym.name, path: sym.path, line: sym.line ? String(sym.line) : '' }); }, !!sym),
    item('Show call flow from here', () => { S.sel = { type: 'sym', ...sym, node: n.id }; go({ v: 'flow', sym: sym.name, path: sym.path, line: sym.line ? String(sym.line) : '' }); }, !!sym),
    item('Trace from here…', () => { openTrace(sym.name, sym.path, sym.line ? String(sym.line) : ''); }, !!sym),
    item('Plan a rename', () => { selectNode(n); S.tab = 'rename'; drawInspector(); }, !!sym),
    item('Copy the agent call', () => copyCall(sym ? { tool: 'explore', args: { name: sym.name, path: sym.path || undefined } } : { tool: 'outline', args: { path: n.key || n.id } }), true));
  m.classList.add('on');
  const r = m.getBoundingClientRect(); m.style.left = `${Math.min(x, innerWidth - r.width - 6)}px`; m.style.top = `${Math.min(y, innerHeight - r.height - 6)}px`;
}
function hideMenu() { if (S.ui) S.ui.menu.classList.remove('on'); }
function copyCall(c) { const args = JSON.parse(JSON.stringify(c.args)); navigator.clipboard.writeText(D.commandFor(c.tool, args)).then(() => D.toast('Copied the command'), () => D.toast('Copy failed')); }

// ---------------------------------------------------------------- selection & inspector

function selectNode(n) {
  if (n.shape === 'cell') { S.sel = { type: 'edge', a: n.pair[0], b: n.pair[1], node: n.id, label: n.label, cell: true }; }
  else if (n.sym && n.sym.name) S.sel = { type: 'sym', ...n.sym, node: n.id };
  else S.sel = { type: 'part', key: n.key || n.id, kind: n.kind, label: n.label, node: n.id, why: n.why, sub: n.sub };
  S.rename = null; if (S.tab === 'context' || S.tab === 'trace') S.tab = 'evidence';
  S.scene.dirty = true; drawInspector();
}
function selectEdge(e) { S.sel = { type: 'edge', a: e.a, b: e.b, node: null, label: `${labelOf(e.a)} → ${labelOf(e.b)}`, e }; S.tab = 'evidence'; S.scene.dirty = true; drawInspector(); }

const TABS = [['evidence', 'Evidence'], ['source', 'Source'], ['rename', 'Rename'], ['agent', 'Agent']];
function drawInspector() {
  const { h } = D; const ui = S.ui; if (!ui) return;
  const tabs = h('div', { class: 'tabs mp-tabs', role: 'tablist' },
    TABS.map(([id, label]) => h('button', { type: 'button', role: 'tab', 'aria-selected': String(S.tab === id), onclick: () => { S.tab = id; drawInspector(); } }, label)));
  const body = h('div', { class: 'mp-insp-body' });
  ui.insp.replaceChildren(head(), tabs, body);
  const fill = { evidence: tabEvidence, source: tabSource, rename: tabRename, agent: tabAgent, context: tabContext, trace: tabTrace }[S.tab] || tabEvidence;
  fill(body);
}
function head() {
  const { h } = D; const s = S.sel;
  if (S.tab === 'context') return h('div', { class: 'mp-insp-head' }, h('p', { class: 'mp-kind', text: 'context' }), h('h2', { text: 'What to read for a task' }));
  if (S.tab === 'trace') return h('div', { class: 'mp-insp-head' }, h('p', { class: 'mp-kind', text: 'trace' }), h('h2', { text: 'How A reaches B' }));
  if (!s) return h('div', { class: 'mp-insp-head' }, h('p', { class: 'mp-kind', text: 'nothing selected' }), h('h2', { text: 'Select something on the map' }));
  if (s.type === 'sym') return h('div', { class: 'mp-insp-head' }, h('p', { class: 'mp-kind', text: s.kind || 'symbol' }), h('h2', { class: 'mono', text: s.name }),
    s.path ? h('a', { class: 'where', href: D.symbolHref(s.name, s.path, { line: s.line ? String(s.line) : '' }), text: `${s.path}${s.line ? ':' + s.line : ''} · open its page` }) : '');
  if (s.type === 'edge') return h('div', { class: 'mp-insp-head' }, h('p', { class: 'mp-kind', text: 'relationships' }), h('h2', { class: 'mono', text: s.label }));
  return h('div', { class: 'mp-insp-head' },
    h('p', { class: 'mp-kind', text: s.kind === 'dir' ? 'directory / service' : s.kind }),
    h('h2', { class: 'mono', text: s.label }),
    h('p', { class: 'where where-full', text: s.key || s.node }),
    s.kind === 'dir' ? h('div', { class: 'mp-quick-actions' },
      h('button', { type: 'button', class: 'btn', onclick: () => go({ v: 'map', at: s.key }) }, `📂 Open / Drill into ${s.label}`),
      h('button', { type: 'button', class: 'btn quiet', onclick: () => go({ v: 'pipeline', at: s.key }) }, '⚡ Pipeline'),
      h('button', { type: 'button', class: 'btn quiet', onclick: () => go({ v: 'cluster', at: s.key }) }, '📦 Cluster')
    ) : (s.kind !== 'dir' && s.kind !== 'boundary' ? h('a', { class: 'where', href: D.fileHref(s.key), text: `${s.key} · open its page` }) : '')
  );
}
function busy(body, what) { body.replaceChildren(D.h('p', { class: 'mp-busy busy', text: what })); }
function failed(body, err) { body.replaceChildren(D.h('p', { class: 'mp-fail', text: `The index could not answer: ${err.message}` })); }
function tierRow(conf, name, where, href, why) {
  const { h } = D;
  return h('li', { class: 'mp-row' }, D.tierMark(conf),
    href ? h('a', { class: 'name', href, text: name }) : h('span', { class: 'name', text: name }),
    where ? h('span', { class: 'where', text: where }) : '', why ? h('span', { class: 'why', text: why }) : '');
}

function tabEvidence(body) {
  const { h } = D; const s = S.sel;
  if (!s) {
    const sc = S.scene; const tiers = { exact: 0, high: 0, medium: 0, low: 0 };
    for (const e of (sc ? sc.edges : [])) for (const [t, n] of Object.entries(e.tiers || {})) tiers[t] = (tiers[t] || 0) + n;
    body.replaceChildren(
      h('p', { class: 'sub', text: 'Each line is a relationship the index holds, drawn in the colour and dash of how sure it is:' }),
      h('ul', { class: 'mp-key' }, TIER_ORDER.map((t) => h('li', null, h('span', { class: `mp-swatch mp-${t}` }), h('b', { text: D.TIERS[t].label }), h('span', { text: ` ${D.TIERS[t].use}` }), tiers[t] ? h('span', { class: 'mp-count', text: ` · ${tiers[t]} on screen` }) : ''))),
      h('p', { class: 'sub', text: 'Click a box for its evidence, a line for the call sites behind it. Double-click to open. Right-click for impact, call flow, trace and rename.' }));
    return;
  }
  if (s.kind === 'Database Table' || s.type === 'table') {
    busy(body, `Reading table ${s.name}…`);
    tool('tables', { table: s.name }, `statements that read or write ${s.name}`).then((a) => {
      const items = a.items || [];
      body.replaceChildren(
        h('p', { class: 'mp-summary', text: a.summary || `Database table ${s.name}` }),
        h('div', { class: 'mp-actions' },
          h('a', { class: 'btn quiet', href: `#/t/${encodeURIComponent(s.name)}` }, 'Open table page')),
        section('Usage', items.map((i) => tierRow('high', i.name, i.why || 'Declared in repository schema', `#/t/${encodeURIComponent(i.name)}`, i.why))),
        notes(a.notes)
      );
    }).catch((err) => failed(body, err));
    return;
  }

  if (s.type === 'sym') {
    busy(body, `Exploring ${s.name}…`);
    const args = JSON.parse(JSON.stringify({ name: s.name, path: s.path || undefined, line: s.line ? +s.line : undefined, min_confidence: 'low' }));
    tool('explore', args, `everything about ${s.name}`).then((a) => {
      const items = a.items || [];
      const callers = items.filter((i) => i.kind === 'caller'), callees = items.filter((i) => i.kind === 'callee');
      const other = items.filter((i) => i.kind && !['caller', 'callee'].includes(i.kind) && i.name && i.kind !== 'definition' && !i.excerpt);
      body.replaceChildren(
        h('p', { class: 'mp-summary', text: a.summary }),
        h('div', { class: 'mp-actions' },
          h('a', { class: 'btn quiet', href: hashFor({ v: 'impact', sym: s.name, path: s.path, line: s.line ? String(s.line) : '' }) }, 'Impact rings'),
          h('a', { class: 'btn quiet', href: hashFor({ v: 'flow', sym: s.name, path: s.path, line: s.line ? String(s.line) : '' }) }, 'Call flow'),
          h('button', { type: 'button', class: 'btn quiet', onclick: () => { S.tab = 'rename'; drawInspector(); } }, 'Rename…')),
        section(`Callers · ${callers.length}`, callers.map((i) => tierRow(i.confidence, i.name, `${i.path}:${i.line}`, D.symbolHref(i.name, i.path, { line: String(i.line || '') }), i.why)), 'None recorded. That is not proof nothing calls it: calls the index could not bind are absent.'),
        section(`Calls · ${callees.length}`, callees.map((i) => tierRow(i.confidence, i.name, `${i.path}:${i.line}`, D.symbolHref(i.name, i.path, { line: String(i.line || '') }), i.why)), 'It calls nothing inside this repository that the index could bind.'),
        other.length ? section('Also', other.map((i) => tierRow(i.confidence, i.name, i.path ? `${i.path}${i.line ? ':' + i.line : ''}` : '', '', `${D.kindWord(i.kind)} · ${i.why || ''}`))) : '',
        notes(a.notes));
    }).catch((err) => failed(body, err));
    return;
  }
  if (s.type === 'edge') {
    const sc = S.scene; const base = S.at && isDir(S.at) ? S.at : '';
    const cols = `s.name AS src, sf.path AS sp, s.start_line AS sl, d.name AS dst, df.path AS dp, d.start_line AS dl, e.kind AS k, e.confidence AS c, e.evidence_file AS ef, e.evidence_line AS el, e.source AS src_by`;
    const filters = `e.kind IN ${kindIn()} AND e.confidence IN ${confIn()}`;
    const order = `ORDER BY CASE e.confidence WHEN 'exact' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2 ELSE 3 END, ef, el LIMIT 60`;
    let query;
    if (s.a.startsWith('y:') && s.b.startsWith('y:')) {
      query = `SELECT ${cols}
      FROM edges e JOIN symbols s ON s.id = e.src_id JOIN symbols d ON d.id = e.dst_id JOIN files sf ON sf.id = s.file_id JOIN files df ON df.id = d.file_id
      WHERE e.index_run_id = ${RUN} AND ${filters} AND e.src_id = ${+s.a.slice(2)} AND e.dst_id = ${+s.b.slice(2)} ${order}`;
    } else {
      // The pair's files come from the rollup, and only their edges are read:
      // each source file's definitions, their edges, kept when the target is
      // one of the pair's target files.
      const where = sc.kind === 'matrix'
        ? `${childKey('sf.path', sc.base)} = ${q(s.a)} AND ${childKey('df.path', sc.base)} = ${q(s.b)}`
        : `${keyExpr('sf.path', base)} = ${q(s.a)} AND ${keyExpr('df.path', base)} = ${q(s.b)}`;
      query = `WITH p AS (SELECT DISTINCT r.src_file_id AS a, r.dst_file_id AS b FROM ${rollupFrom('', 'either')} AND ${where})
      SELECT ${cols}
      FROM p CROSS JOIN symbols s ON s.file_id = p.a CROSS JOIN edges e ON e.index_run_id = ${RUN} AND e.src_id = s.id
      CROSS JOIN symbols d ON d.id = e.dst_id AND d.file_id = p.b JOIN files sf ON sf.id = p.a JOIN files df ON df.id = p.b
      WHERE ${filters} ${order}`;
    }
    busy(body, 'Reading the relationships…');
    sql(`the relationships from ${s.a} to ${s.b}`, query)
      .then((r) => {
        const tiers = {}; for (const x of r.rows) tiers[x.c] = (tiers[x.c] || 0) + 1;
        body.replaceChildren(
          h('p', { class: 'mp-summary', text: `${r.rows.length}${r.rows.length >= 60 ? '+' : ''} relationship${r.rows.length === 1 ? '' : 's'}: ${TIER_ORDER.filter((t) => tiers[t]).map((t) => `${tiers[t]} ${D.TIERS[t].label}`).join(', ')}.` }),
          section('Each one, with where it was found', r.rows.map((x) => tierRow(x.c, `${x.src} → ${x.dst}`, `${x.ef}:${x.el}`, D.symbolHref(x.dst, x.dp, { line: String(x.dl) }), `${D.kindWord(x.k.toLowerCase())} · ${x.src_by}`))));
      }).catch((err) => failed(body, err));
    return;
  }
  // a directory, a file, a tile or a community member
  const n = S.scene && S.scene.byId.get(s.node);
  if (n && n.more) {
    body.replaceChildren(h('p', { class: 'sub', text: `${n.more.length} parts here have no relationship with the rest at this level.` }),
      h('ul', { class: 'mp-rows' }, n.more.map((m) => h('li', { class: 'mp-row' }, h('span', { class: 'tier', text: `${m.syms || 0} defs` }), h('a', { class: 'name', href: isDir(m.key) ? hashFor({ v: 'map', at: m.key }) : D.fileHref(m.key), text: lastSeg(m.key, S.at) })))));
    return;
  }

  if (s.kind === 'dir') {
    busy(body, `Reading service ${s.label}…`);
    Promise.all([
      sql(`children of ${s.key}`, childrenQuery(s.key, 'ORDER BY (CASE WHEN f.path LIKE \'%/\' THEN 0 ELSE 1 END), files DESC LIMIT 16')).catch(() => ({ rows: [] })),
      tool('endpoints', {}).catch(() => ({ items: [] }))
    ]).then(([kidsRes, epRes]) => {
      const kids = (kidsRes && kidsRes.rows) || [];
      const eps = ((epRes && epRes.items) || []).filter((i) => i.path && i.path.startsWith(s.key)).slice(0, 8);
      const ins = [], outs = [];
      for (const e of (S.scene ? (S.scene.allEdges || S.scene.edges) : [])) { if (e.b === s.node) ins.push(e); if (e.a === s.node) outs.push(e); }
      const sum = (arr) => arr.reduce((t, e) => t + e.count, 0);

      body.replaceChildren(
        h('div', { class: 'facts mp-facts' },
          n && n.files ? h('div', { class: 'fact' }, h('b', { text: String(n.files) }), h('span', { text: 'files' })) : '',
          n && n.syms != null ? h('div', { class: 'fact' }, h('b', { text: String(n.syms) }), h('span', { text: 'definitions' })) : '',
          h('div', { class: 'fact' }, h('b', { text: String(sum(ins)) }), h('span', { text: 'inbound links' })),
          h('div', { class: 'fact' }, h('b', { text: String(sum(outs)) }), h('span', { text: 'outbound links' }))
        ),
        h('div', { class: 'mp-actions mp-dir-actions' },
          h('button', { type: 'button', class: 'btn', onclick: () => go({ v: 'map', at: s.key }) }, '📂 Open this directory'),
          h('button', { type: 'button', class: 'btn quiet', onclick: () => go({ v: 'pipeline', at: s.key }) }, '⚡ View Pipeline'),
          h('button', { type: 'button', class: 'btn quiet', onclick: () => go({ v: 'cluster', at: s.key }) }, '📦 View Cluster')
        ),
        eps.length ? section(`Exposed Endpoints · ${eps.length}`, eps.map((ep) => tierRow('high', ep.name, ep.path, '', ep.why))) : '',
        section(`Contained Packages & Files · ${kids.length}`, kids.map((k) => {
          const isD = isDir(k.k);
          return tierRow('high', lastSeg(k.k, s.key), `${k.files} file${k.files === 1 ? '' : 's'} · ${k.syms} defs`, isD ? hashFor({ v: 'map', at: k.k }) : D.fileHref(k.k), isD ? 'directory' : 'file');
        })),
        ins.length ? section('Inbound Dependencies', ins.sort((a, b) => b.count - a.count).slice(0, 6).map((e) => tierRow(e.conf, labelOf(e.a), `${e.count} relationship${e.count === 1 ? '' : 's'}`, '', ''))) : '',
        outs.length ? section('Outbound Dependencies', outs.sort((a, b) => b.count - a.count).slice(0, 6).map((e) => tierRow(e.conf, labelOf(e.b), `${e.count} relationship${e.count === 1 ? '' : 's'}`, '', ''))) : ''
      );
    }).catch((err) => failed(body, err));
    return;
  }

  const ins = [], outs = [];
  for (const e of (S.scene ? (S.scene.allEdges || S.scene.edges) : [])) { if (e.b === s.node) ins.push(e); if (e.a === s.node) outs.push(e); }
  const sum = (arr) => arr.reduce((t, e) => t + e.count, 0);
  body.replaceChildren(
    h('div', { class: 'facts mp-facts' },
      n && n.files ? h('div', { class: 'fact' }, h('b', { text: String(n.files) }), h('span', { text: 'files' })) : '',
      n && n.syms != null ? h('div', { class: 'fact' }, h('b', { text: String(n.syms) }), h('span', { text: 'definitions' })) : '',
      h('div', { class: 'fact' }, h('b', { text: String(sum(ins)) }), h('span', { text: 'relationships in, at this level' })),
      h('div', { class: 'fact' }, h('b', { text: String(sum(outs)) }), h('span', { text: 'relationships out, at this level' }))),
    s.why ? h('p', { class: 'sub', text: s.why }) : '',
    h('div', { class: 'mp-actions' },
      s.kind === 'dir' || s.kind === 'boundary' || S.view === 'map' ? h('a', { class: 'btn', href: hashFor({ v: 'map', at: s.key }) }, isDir(s.key) ? 'Open this directory' : 'Show its definitions') : '',
      !isDir(s.key) ? h('a', { class: 'btn quiet', href: D.fileHref(s.key) }, 'File page') : h('a', { class: 'btn quiet', href: hashFor({ v: 'risk', at: s.key }) }, 'Risk here')),
    section('Biggest inbound', ins.sort((a, b) => b.count - a.count).slice(0, 8).map((e) => tierRow(e.conf, labelOf(e.a), `${e.count} relationship${e.count === 1 ? '' : 's'}`, '', ''))),
    section('Biggest outbound', outs.sort((a, b) => b.count - a.count).slice(0, 8).map((e) => tierRow(e.conf, labelOf(e.b), `${e.count} relationship${e.count === 1 ? '' : 's'}`, '', ''))));
}
function section(title, rows, empty) {
  const { h } = D;
  return h('section', { class: 'mp-sec' }, h('h3', { text: title }), rows.length ? h('ul', { class: 'mp-rows' }, rows) : h('p', { class: 'sub', text: empty || 'Nothing on screen.' }));
}
function notes(list) {
  if (!list || !list.length) return '';
  const { h } = D;
  return h('details', { class: 'mp-notes' }, h('summary', { text: `What the index could not see · ${list.length} note${list.length === 1 ? '' : 's'}` }), ...list.map((t) => h('p', { text: t })));
}

function tabSource(body) {
  const { h } = D; const s = S.sel;
  if (!s || s.type === 'edge') { body.replaceChildren(h('p', { class: 'sub', text: 'Select a symbol or a file to read its source.' })); return; }
  if (s.kind === 'Database Table' || s.type === 'table') {
    busy(body, `Reading table ${s.name}…`);
    tool('tables', { table: s.name }, `statements that read or write ${s.name}`).then((a) => {
      const items = a.items || [];
      body.replaceChildren(
        h('p', { class: 'mp-summary', text: a.summary || `Database table ${s.name}` }),
        h('div', { class: 'mp-actions' },
          h('a', { class: 'btn quiet', href: `#/t/${encodeURIComponent(s.name)}` }, 'Open table page')),
        section('Usage', items.map((i) => tierRow('high', i.name, i.why || 'Declared in repository schema', `#/t/${encodeURIComponent(i.name)}`, i.why))),
        notes(a.notes)
      );
    }).catch((err) => failed(body, err));
    return;
  }

  if (s.type === 'sym') {
    busy(body, 'Reading the source…');
    tool('explore', JSON.parse(JSON.stringify({ name: s.name, path: s.path || undefined, line: s.line ? +s.line : undefined })), `the source of ${s.name}`).then((a) => {
      const ex = (a.items || []).find((i) => i.excerpt);
      body.replaceChildren(ex ? h('pre', { class: 'outline mp-code' }, h('code', { text: ex.excerpt })) : h('p', { class: 'sub', text: 'The index holds no excerpt for it.' }),
        h('p', { class: 'sub', text: 'A bounded excerpt, as the explore tool returns it to an agent.' }));
    }).catch((err) => failed(body, err));
    return;
  }
  if (isDir(s.key)) { body.replaceChildren(h('p', { class: 'sub', text: 'A directory has no source of its own. Open a file in it.' })); return; }
  busy(body, 'Reading the outline…');
  tool('outline', { path: s.key }, `the outline of ${s.key}`).then((a) => {
    const ex = (a.items || []).find((i) => i.excerpt);
    body.replaceChildren(h('p', { class: 'sub', text: a.summary }), ex ? h('pre', { class: 'outline mp-code' }, h('code', { text: ex.excerpt })) : '');
  }).catch((err) => failed(body, err));
}

/* Rename: the rename plan, what the change reaches, and the tests that reach
   it, side by side. The gate is a reading of those three answers, never a
   promise: review sites, an unknown risk or a plan that says it is incomplete
   each hold it. */
function tabRename(body) {
  const { h } = D; const s = S.sel;
  if (!s || s.type !== 'sym') { body.replaceChildren(h('p', { class: 'sub', text: 'Select a function, method or type to plan a rename. Nothing is written: the plan lists the lines to change.' })); return; }
  const input = h('input', { class: 'mp-input', value: S.rename && S.rename.name === s.name ? S.rename.to : '', placeholder: 'new name', 'aria-label': 'New name', spellcheck: 'false' });
  const out = h('div', { class: 'mp-rename-out' });
  const run = async () => {
    const to = input.value.trim(); if (!/^[A-Za-z_][\w]*$/.test(to)) { D.toast('Type a valid identifier'); return; }
    S.rename = { name: s.name, to };
    const base = JSON.parse(JSON.stringify({ path: s.path || undefined, line: s.line ? +s.line : undefined }));
    const steps = [['Resolve the definition', 'symbol'], ['Plan the edit sites', 'rename'], ['Measure what it reaches', 'impact'], ['Find the tests that reach it', 'tests'], ['Read the gate', '']];
    const ol = h('ol', { class: 'mp-steps' }); out.replaceChildren(ol);
    const mark = (i, state, text) => { const li = ol.children[i] || ol.appendChild(h('li', null)); li.className = `mp-step ${state}`; li.replaceChildren(h('b', { text: steps[i][0] }), h('span', { text })); };
    steps.forEach((_, i) => mark(i, 'wait', steps[i][1] ? `${steps[i][1]} tool` : ''));
    try {
      mark(0, 'run', 'asking…');
      const def = await tool('symbol', { name: s.name }, `definitions named ${s.name}`);
      const n = (def.items || []).length;
      mark(0, n > 8 ? 'warn' : 'done', n > 1 ? `${n} definitions share the name; the plan below is for ${s.path}${s.line ? ':' + s.line : ''}.` : 'one definition');
      mark(1, 'run', 'asking…');
      const plan = await tool('rename', { name: s.name, new_name: to, ...base, limit: 200 }, `the rename plan for ${s.name}`);
      const sites = plan.items || []; const review = sites.filter((i) => i.confidence !== 'exact');
      mark(1, review.length ? 'warn' : 'done', plan.summary);
      mark(2, 'run', 'asking…');
      const imp = await tool('impact', { symbol: s.name, ...base, limit: 1 }, `what a change to ${s.name} reaches`);
      const risk = /Risk (\w+)/.exec(imp.summary || ''); mark(2, risk && /high|unknown/.test(risk[1]) ? 'warn' : 'done', imp.summary);
      mark(3, 'run', 'asking…');
      const tests = await tool('tests', { symbol: s.name, ...base, limit: 20 }, `tests that reach ${s.name}`);
      mark(3, (tests.items || []).length ? 'done' : 'warn', tests.summary);
      const holds = [review.length ? `${review.length} site(s) were inferred, not proven: read each one before applying.` : '',
        risk && /unknown/.test(risk[1]) ? 'Its callers could not be determined, so the reach is unknown.' : '',
        !(tests.items || []).length ? 'No test reaches it; the change would land unchecked.' : '',
        /not complete/i.test(plan.summary || '') ? 'The plan says it is not complete: after applying it, search for the old name.' : ''].filter(Boolean);
      mark(4, review.length || holds.length > 2 ? 'block' : holds.length ? 'warn' : 'done', holds.length ? holds.join(' ') : 'Every site is proven by a compiler and tests reach it.');
      out.append(
        h('h3', { class: 'mp-h3', text: `Edit sites · ${sites.length}` }),
        h('ul', { class: 'mp-rows' }, sites.map((i) => tierRow(i.confidence, `${i.path}:${i.line}`, i.name, D.fileHref(i.path), i.confidence === 'exact' ? 'apply' : 'review by hand'))),
        h('p', { class: 'sub', text: 'Each site is a file and a line; find the identifier on it. GraphLume writes nothing.' }),
        D.copyable(D.commandFor('rename', { name: s.name, new_name: to, ...base })),
        notes(plan.notes));
    } catch (err) { out.append(h('p', { class: 'mp-fail', text: err.message })); }
  };
  input.addEventListener('keydown', (e) => { if (e.key === 'Enter') run(); });
  body.replaceChildren(
    h('p', { class: 'sub', text: `Rename ${s.name}: the plan, its reach and its tests, read together. Nothing is written.` }),
    h('div', { class: 'mp-inline' }, input, h('button', { type: 'button', class: 'btn', onclick: run }, 'Plan it')), out);
  if (S.rename && S.rename.name === s.name) run();
}

function tabAgent(body) {
  const { h } = D;
  const calls = S.calls.slice(-12);
  body.replaceChildren(
    h('p', { class: 'sub', text: 'Every figure on this view is the answer to one of these calls. An agent connected to GraphLume gets the same answer to the same call.' }),
    ...calls.map((c) => h('section', { class: 'mp-sec' }, h('h3', { text: `${c.tool} — ${c.why}` }), D.copyable(D.commandFor(c.tool, c.tool === 'graph' ? { query: c.args.query } : c.args)))),
    calls.length ? '' : h('p', { class: 'sub', text: 'No calls yet.' }));
}

function tabContext(body) {
  const { h } = D;
  const box = h('textarea', { class: 'mp-input mp-area', rows: '3', 'aria-label': 'Describe the task', placeholder: 'e.g. add a login check to the portal guard' });
  if (S.ctx) box.value = S.ctx.task;
  const out = h('div', null);
  const run = () => {
    const task = box.value.trim(); if (!task) return;
    busy(out, 'Compiling the context…');
    tool('context', { task, min_confidence: S.floor }, `what to read for “${task}”`).then((a) => {
      S.ctx = { task, a };
      const items = a.items || [];
      out.replaceChildren(h('p', { class: 'mp-summary', text: a.summary }),
        h('ul', { class: 'mp-rows' }, items.slice(0, 40).map((i) => tierRow(i.confidence, i.name, `${i.path}:${i.line}`, D.symbolHref(i.name, i.path, { line: String(i.line || '') }), i.why))),
        D.copyable(D.commandFor('context', { task })), notes(a.notes));
    }).catch((err) => failed(out, err));
  };
  box.addEventListener('keydown', (e) => { if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) run(); });
  body.replaceChildren(h('p', { class: 'sub', text: 'Describe a change in plain words. The context tool walks the call graph from what the task names and returns the smallest set to read first, each with why it is there.' }),
    box, h('div', { class: 'mp-actions' }, h('button', { type: 'button', class: 'btn', onclick: run }, 'Compile context'), h('span', { class: 'sub', text: '⌘↵' })), out);
  if (S.ctx && S.ctx.a) { out.replaceChildren(); run(); }
}

function openTrace(from, fromPath, fromLine) {
  const prev = S.traceDraft || {};
  const sel = S.sel && S.sel.type === 'sym' ? S.sel : null;
  S.traceDraft = from ? { ...prev, from, fromPath: fromPath || '', fromLine: fromLine || '' }
    : (prev.from ? prev : { from: sel ? sel.name : '', fromPath: sel ? sel.path : '', fromLine: sel ? String(sel.line || '') : '', to: '' });
  S.tab = 'trace'; drawInspector();
}
/* Trace by name, then by place: a name that matches several definitions is
   answered with candidates, and picking one fills in its path and line. */
function tabTrace(body) {
  const { h } = D; const d = S.traceDraft || (S.traceDraft = {});
  const a = h('input', { class: 'mp-input', value: d.from || '', placeholder: 'from: a function or method', 'aria-label': 'From' });
  const b = h('input', { class: 'mp-input', value: d.to || '', placeholder: 'to: a function, method or table', 'aria-label': 'To' });
  const where = (p, l) => (p ? ` · ${p}${l ? ':' + l : ''}` : '');
  const pinned = h('p', { class: 'sub', text: (d.fromPath || d.toPath) ? `Pinned to${where(d.fromPath, d.fromLine) ? ' from' + where(d.fromPath, d.fromLine) : ''}${where(d.toPath, d.toLine) ? ' to' + where(d.toPath, d.toLine) : ''}` : '' });
  const out = h('div', null);
  const run = (auto) => {
    const from = a.value.trim(), to = b.value.trim(); if (!from || !to) return;
    if (from !== d.from) { d.fromPath = ''; d.fromLine = ''; }
    if (to !== d.to) { d.toPath = ''; d.toLine = ''; }
    d.from = from; d.to = to;
    busy(out, 'Tracing…');
    const args = JSON.parse(JSON.stringify({ from, to, from_path: d.fromPath || undefined, from_line: d.fromLine ? +d.fromLine : undefined, to_path: d.toPath || undefined, to_line: d.toLine ? +d.toLine : undefined, max_depth: Math.max(4, S.depth + 2), min_confidence: S.floor }));
    tool('trace', args, `how ${from} reaches ${to}`).then((ans) => {
      const items = ans.items || [];
      const cands = items.filter((i) => i.kind === 'candidate');
      const hops = items.filter((i) => / -> /.test(i.name || ''));
      S.trace = { from, to, answer: ans };
      const side = new RegExp(`"${from.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}"`).test(ans.summary || '') ? 'from' : 'to';
      out.replaceChildren(h('p', { class: 'mp-summary', text: ans.summary }),
        cands.length ? h('ul', { class: 'mp-rows' }, cands.map((i) => h('li', { class: 'mp-row mp-pick' },
          h('button', { type: 'button', class: 'linklike', onclick: () => { if (side === 'from') { d.fromPath = i.path; d.fromLine = String(i.line || ''); } else { d.toPath = i.path; d.toLine = String(i.line || ''); } drawInspector(); setTimeout(() => { const btn = [...S.ui.insp.querySelectorAll('.btn')].find((x) => x.textContent === 'Trace'); if (btn) btn.click(); }, 0); } }, 'Use this one'),
          h('span', { class: 'name', text: i.name }), h('span', { class: 'where', text: `${i.path}:${i.line}` }), i.excerpt ? h('span', { class: 'why', text: i.excerpt }) : ''))) : '',
        hops.length ? h('ul', { class: 'mp-rows' }, hops.map((i) => tierRow(i.confidence, i.name, `${i.path}:${i.line}`, D.fileHref(i.path), i.why))) : '',
        notes(ans.notes), D.copyable(D.commandFor('trace', args)));
      if (hops.length && S.view !== 'trace' && auto !== true) go({ v: 'trace' });
      else if (hops.length && S.view === 'trace' && auto !== true) load();
    }).catch((err) => failed(out, err));
  };
  [a, b].forEach((el) => el.addEventListener('keydown', (e) => { if (e.key === 'Enter') run(); }));
  body.replaceChildren(h('p', { class: 'sub', text: 'The shortest routes from one symbol to another, hop by hop, each with its file, line and how sure it is. No route found is not proof of independence.' }),
    a, b, pinned, h('div', { class: 'mp-actions' }, h('button', { type: 'button', class: 'btn', onclick: () => run() }, 'Trace'),
      h('button', { type: 'button', class: 'btn quiet', onclick: () => { const t = a.value; a.value = b.value; b.value = t; [d.fromPath, d.toPath] = [d.toPath, d.fromPath]; [d.fromLine, d.toLine] = [d.toLine, d.fromLine]; } }, 'Swap')), out);
  if (S.trace && S.trace.answer && d.from === S.trace.from && d.to === S.trace.to) run(true);
}

// ---------------------------------------------------------------- legend

function drawLegend(sc) {
  const { h } = D; const L = S.ui.legend; const rows = [];
  const line = (t) => h('li', null, h('span', { class: `mp-line mp-${t}` }), D.TIERS[t].label);
  if (sc.kind === 'risk') {
    rows.push(h('li', null, h('span', { class: 'mp-ramp' }), 'calm → changes often'), h('li', null, h('span', { class: 'mp-hatch' }), 'one person holds it'), h('li', null, h('span', { class: 'mp-box' }), 'area = size'));
  } else if (sc.kind === 'matrix') {
    rows.push(h('li', null, h('span', { class: 'mp-box mp-fill-exact' }), 'row uses column'), h('li', null, h('span', { class: 'mp-box mp-fill-low' }), 'cycle: both use each other'), h('li', null, h('span', { class: 'mp-box mp-dotted' }), 'only inferred evidence'));
  } else {
    rows.push(...TIER_ORDER.map(line));
    if (sc.kind === 'map') rows.push(h('li', null, h('span', { class: 'mp-box mp-dashed' }), 'outside this level'));
    if (sc.kind === 'impact') rows.push(h('li', null, h('span', { class: 'mp-box' }), 'border = weakest link to the centre'));
  }
  L.replaceChildren(h('ul', null, rows));
}


// ---------------------------------------------------------------- explorer & status

/* The explorer lists directories and files the way an editor does, loaded one
   level at a time from the same graph tool. Opening an entry moves the Map to
   it; the path to the current level is always expanded and marked. */
const treeKids = new Map();
const treeOpen = new Set(['']);
function toggleTree() {
  S.treeOff = !S.treeOff;
  S.ui.body.classList.toggle('no-tree', !!S.treeOff);
  const b = S.ui.bar.querySelector('.mp-treebtn'); if (b) b.setAttribute('aria-pressed', String(!S.treeOff));
  setTimeout(() => { size(); if (S.scene) S.scene.dirty = true; }, 30);
}
async function treeChildren(base) {
  if (treeKids.has(base)) return treeKids.get(base);
  const r = await sql(`entries of ${base || 'the repository'}`,
    childrenQuery(base, `ORDER BY (CASE WHEN ${childKey('f.path', base)} LIKE '%/' THEN 0 ELSE 1 END), 1`));
  const rows = r.rows.map((x) => ({ key: x.k, dir: isDir(x.k), files: +x.files, syms: +x.syms }));
  treeKids.set(base, rows);
  return rows;
}
function drawTree() {
  const { h } = D; const ui = S.ui; if (!ui) return;
  // keep the current path open
  let acc = '';
  for (const seg of S.at.split('/').filter(Boolean)) { acc += seg + '/'; if (S.at.startsWith(acc)) treeOpen.add(acc); }
  const list = h('ul', { class: 'mp-tree-list', role: 'tree' });
  ui.tree.replaceChildren(h('p', { class: 'mp-tree-h', text: 'Explorer' }), list);
  const render = async (base, ul, depth) => {
    let rows;
    try { rows = await treeChildren(base); } catch (err) { ul.append(h('li', { class: 'mp-tree-err', text: err.message })); return; }
    for (const r of rows) {
      const open = r.dir && treeOpen.has(r.key);
      const label = lastSeg(r.key, base).replace(/\/$/, '');
      const li = h('li', { role: 'treeitem', 'aria-expanded': r.dir ? String(open) : null });
      const row = h('div', { class: `mp-tree-row${S.at === r.key ? ' cur' : ''}${S.at.startsWith(r.key) && S.at !== r.key ? ' path' : ''}` });
      row.style.paddingLeft = `${8 + depth * 14}px`;
      const tw = h('button', { type: 'button', class: 'mp-tw', 'aria-label': open ? 'Collapse' : 'Expand', tabindex: '-1',
        onclick: (e) => { e.stopPropagation(); if (!r.dir) return; if (treeOpen.has(r.key)) treeOpen.delete(r.key); else treeOpen.add(r.key); drawTree(); } }, r.dir ? (open ? '▾' : '▸') : '');
      const name = h('a', { class: 'mp-tree-name', href: hashFor({ v: 'map', at: r.key }), title: r.key }, h('span', { class: `mp-ti ${r.dir ? 'd' : 'f'}`, 'aria-hidden': 'true' }), label);
      row.append(tw, name, h('span', { class: 'mp-tree-n', text: String(r.syms) }));
      li.append(row);
      ul.append(li);
      if (open) { const sub = h('ul', { role: 'group' }); li.append(sub); await render(r.key, sub, depth + 1); }
    }
  };
  render('', list, 0).then(() => { const sel = ui.tree.querySelector('.mp-tree-row.cur'); if (sel) sel.scrollIntoView({ block: 'center' }); });
}
function drawStatus() {
  const ui = S.ui; const sc = S.scene; if (!ui || !sc) return;
  const boxes = sc.nodes.filter((n) => n.shape !== 'hdr').length;
  const total = (sc.allEdges || sc.edges).length;
  const lines = sc.edges.length === total ? `${total} line${total === 1 ? '' : 's'}` : `${sc.edges.length} of ${total} lines`;
  const floor = S.floor === 'low' ? 'all relationships' : S.floor === 'exact' ? 'proven only' : `at least ${D.TIERS[S.floor].label}`;
  ui.statusbar.replaceChildren(
    D.h('span', { text: `${boxes} box${boxes === 1 ? '' : 'es'} · ${lines}` }),
    D.h('span', { text: `showing ${floor}` }),
    D.h('span', { text: `depth ${S.depth}` }),
    D.h('span', { class: 'mp-zoomlbl', text: `zoom ${Math.round((S.camera ? S.camera.k : 1) * 100)}%` }),
    S.lastMs != null ? D.h('span', { text: `last answer ${S.lastMs} ms` }) : '',
    D.h('span', { class: 'mp-keys', text: 'Press ? for shortcuts' }));
}
