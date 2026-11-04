// Gerenciador de arquivos em tela cheia, como no AnyDesk: este computador à esquerda, o remoto à
// direita. Arraste arquivos e pastas de um lado para o outro para transferir; o botão direito abre
// o menu de um explorador (abrir, copiar, recortar, colar, renomear, excluir, nova pasta,
// propriedades). O estado de cada sessão (pastas, seleção) fica guardado ao fechar e voltar.
// Usa `app`, `h`, `icon`, `ICONS`, `active`, `nameOf`, `fmtId`, `errText`, `myId` e `cfg`.

Object.assign(ICONS, {
  back: '<path d="M19 12H5M11 6l-6 6 6 6"/>',
  up: '<path d="M12 19V5M6 11l6-6 6 6"/>',
  home: '<path d="M3 11l9-8 9 8M5 10v10h5v-6h4v6h5V10"/>',
  refresh: '<path d="M21 12a9 9 0 1 1-3-6.7M21 4v5h-5"/>',
  upload: '<path d="M12 16V4M6 10l6-6 6 6M4 20h16"/>',
  download: '<path d="M12 4v12M6 10l6 6 6-6M4 20h16"/>',
});
const FOLDER = '<svg aria-hidden="true" width="16" height="14" viewBox="0 0 16 14"><path d="M1 2.5A1.5 1.5 0 0 1 2.5 1H6l1.5 2H13.5A1.5 1.5 0 0 1 15 4.5v7a1.5 1.5 0 0 1-1.5 1.5h-11A1.5 1.5 0 0 1 1 11.5z" fill="#F5B93C"/></svg>';
const FILE = '<svg aria-hidden="true" width="16" height="14" viewBox="0 0 16 14"><path d="M4 0.5h5.5L13 4v9a.5.5 0 0 1-.5.5h-8.5a.5.5 0 0 1-.5-.5z" fill="#D1D5DB"/><path d="M9.5.5V4H13z" fill="#9CA3AF"/></svg>';
const html = (s) => { const t = document.createElement("template"); t.innerHTML = s; return t.content.firstChild; }; // só com texto fixo, nunca nomes de arquivos

const SIDES = ["local", "remote"];
const fstate = new Map(); // ID da sessão -> { local, remote } (estado de cada lado); sobrevive a fechar e reabrir
let fid = ""; // sessão do gerenciador
let fcur = null; // fstate.get(fid)
let fui = null; // elementos de cada lado: { local: { list, path, err, rows }, remote: ... }
let fclip = null; // área de transferência de arquivos: { side, dir, names, cut }
let fdrag = null; // arraste em andamento: { side, dir, names }
let fdropEl = null;
let fside = "local"; // lado em foco, para as teclas
const other = (side) => (side === "local" ? "remote" : "local");

const newSide = () => ({ path: "", entries: [], sel: new Set(), anchor: "", hist: [], sort: "name", asc: true, err: "" });

const SEP = (p) => (p.includes("\\") ? "\\" : "/");
const joinPath = (dir, name) => (dir.endsWith("\\") || dir.endsWith("/") ? dir + name : dir + SEP(dir) + name);
// pai de uma pasta; na raiz de um disco ("C:\") vira a lista de discos
const parentPath = (p) => {
  if (p === "drives") return p;
  const t = p.replace(/[\\/]+$/, "");
  const i = Math.max(t.lastIndexOf("\\"), t.lastIndexOf("/"));
  if (i < 0) return "drives";
  return i === 0 ? "/" : t.slice(0, i) + (i === 2 && t[1] === ":" ? SEP(p) : "");
};
const canWrite = (s) => !!s.path && s.path !== "drives"; // na lista de discos não há onde gravar

const nf = (n) => n.toLocaleString("pt-BR", { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const fmtSize = (n) => (n < 1024 ? `${n} B` : n < 1 << 20 ? `${nf(n / 1024)} KiB` : n < 2 ** 30 ? `${nf(n / (1 << 20))} MiB` : `${nf(n / 2 ** 30)} GiB`);
const fmtDate = (ms) => (ms ? new Date(ms).toLocaleString("pt-BR") : "");
const typeOf = (e) => (e.dir ? "Pasta" : /\.([^.]+)$/.test(e.name) ? RegExp.$1.toUpperCase() : "Arquivo");

const fop = (side, q) => app().FilesOp(fid, side === "remote", q);

// ---------- avisos e transferências em andamento ----------

const xfers = new Map(); // chave -> { label, done, total, state: run | ok | err, msg }
let xseq = 0;

function renderXfers() {
  $("xfers").replaceChildren(...[...xfers].map(([key, x]) => {
    const pct = x.total ? Math.min(100, Math.floor((x.done / x.total) * 100)) : 0;
    const bar = h("div", { class: "xbar" }, h("i"));
    bar.firstChild.style.width = (x.state === "run" ? pct : 100) + "%";
    const text = x.state === "run"
      ? (x.total ? `${pct}% · ${fmtSize(x.done)} de ${fmtSize(x.total)}` : "Preparando...")
      : x.msg;
    const close = h("button", { "aria-label": x.state === "run" ? "Cancelar transferência" : "Fechar aviso", title: x.state === "run" ? "Cancelar" : "Fechar",
      onclick: () => { if (x.state === "run") app().FilesCancel(key); else { xfers.delete(key); renderXfers(); } } }, icon("close", 14));
    return h("div", { class: "xfer " + x.state }, h("div", { class: "xhead" }, h("b", {}, x.label), close), ...(x.total || x.state === "run" ? [bar] : []), h("div", { class: "xtext" }, text));
  }));
}

function toast(label, msg, state) {
  const key = "t" + ++xseq;
  xfers.set(key, { label, msg, state, done: 0, total: 0 });
  renderXfers();
  setTimeout(() => { xfers.delete(key); renderXfers(); }, 8000);
}

function onFileProgress(id, key, label, done, total) {
  const x = xfers.get(key);
  if (!x) return;
  Object.assign(x, { label, done, total });
  renderXfers();
}

// Transfere nomes de srcDir (em from) para toDir (no outro computador). Com cut, apaga a origem depois.
async function transfer(from, names, srcDir, toDir, cut) {
  const id = fid, key = "x" + ++xseq;
  const label = names.length === 1 ? names[0] : `${names.length} itens`;
  xfers.set(key, { label, done: 0, total: 0, state: "run", msg: "" });
  renderXfers();
  const to = other(from);
  try {
    await (from === "local" ? app().FilesUpload(id, key, srcDir, names, toDir) : app().FilesDownload(id, key, srcDir, names, toDir));
    if (cut) await app().FilesOp(id, from === "remote", { op: "delete", path: srcDir, names });
    Object.assign(xfers.get(key), { state: "ok", msg: "Transferência concluída." });
    setTimeout(() => { xfers.delete(key); renderXfers(); }, 5000);
  } catch (e) {
    const m = errText(e);
    Object.assign(xfers.get(key), { state: "err", msg: m === "CANCELADO" ? "Transferência cancelada." : m });
  }
  renderXfers();
  if (fid === id) { fload(to, fcur[to].path, false); if (cut) fload(from, fcur[from].path, false); }
}

// ---------- janelinhas (nome, confirmação, propriedades) ----------

const askBox = (title, text, input, withCancel = true) => new Promise((resolve) => {
  const dlg = $("fAsk");
  $("fAskTitle").textContent = title;
  $("fAskText").textContent = text;
  $("fAskInput").hidden = input === undefined;
  $("fAskInput").value = input || "";
  $("fAskCancel").hidden = !withCancel;
  dlg.returnValue = "";
  dlg.addEventListener("close", () => resolve(dlg.returnValue === "ok" ? ($("fAskInput").hidden ? true : $("fAskInput").value.trim()) : null), { once: true });
  dlg.showModal();
  if (input !== undefined) { $("fAskInput").focus(); $("fAskInput").select(); }
});
$("fAskCancel").onclick = () => $("fAsk").close("cancel");

// ---------- listagem e navegação ----------

async function fload(side, path, push = true) {
  const id = fid, s = fcur[side];
  try {
    const r = await app().FilesOp(id, side === "remote", { op: "list", path });
    if (fid !== id) return;
    const l = r.list;
    if (l.path !== s.path) {
      if (push && s.path) s.hist.push(s.path);
      s.sel = new Set();
      s.anchor = "";
    } else {
      s.sel = new Set([...s.sel].filter((n) => l.entries.some((e) => e.name === n)));
    }
    Object.assign(s, { path: l.path, entries: l.entries || [], err: "" });
  } catch (e) {
    if (fid !== id) return;
    s.err = errText(e);
  }
  frender(side);
}

const sorted = (s) => {
  const key = { name: (e) => e.name.toLowerCase(), type: (e) => typeOf(e), size: (e) => e.size, date: (e) => e.mod }[s.sort];
  return [...s.entries].sort((a, b) => (a.dir !== b.dir ? (a.dir ? -1 : 1) : (key(a) > key(b) ? 1 : key(a) < key(b) ? -1 : 0) * (s.asc ? 1 : -1)));
};

function paintSel(side) {
  const s = fcur[side];
  for (const [name, row] of fui[side].rows) {
    row.classList.toggle("on", s.sel.has(name));
    row.setAttribute("aria-selected", String(s.sel.has(name)));
    row.classList.toggle("cut", !!fclip && fclip.cut && fclip.side === side && fclip.dir === s.path && fclip.names.includes(name));
  }
  fui[side].act.disabled = !s.sel.size || !canWrite(fcur[other(side)]);
}

function frender(side) {
  if (!fui) return;
  const s = fcur[side], ui = fui[side];
  ui.path.value = s.path === "drives" ? "Este computador" : s.path;
  ui.err.textContent = s.err;
  ui.err.hidden = !s.err;
  ui.back.disabled = !s.hist.length;
  ui.rows = new Map();
  ui.list.replaceChildren(...(s.path !== "drives" && s.path ? [h("div", { class: "frow", ondblclick: () => fload(side, parentPath(s.path)) }, h("div", { class: "n" }, html(FOLDER), h("span", {}, "..")))] : []),
    ...sorted(s).map((e) => frow(side, s, e)));
  for (const [key, b] of Object.entries(ui.cols)) b.textContent = { name: "Nome", type: "Tipo", size: "Tamanho", date: "Data de modificação" }[key] + (s.sort === key ? (s.asc ? " ▴" : " ▾") : "");
  paintSel(side);
}

function frow(side, s, e) {
  const dirOf = () => (e.dir ? (s.path === "drives" ? e.name : joinPath(s.path, e.name)) : s.path);
  const row = h("div", { class: "frow", role: "row", draggable: "true" },
    h("div", { class: "n" }, html(e.dir ? FOLDER : FILE), h("span", { title: e.name }, e.name)),
    h("div", { class: "t" }, e.dir ? "" : typeOf(e)),
    h("div", { class: "s" }, e.dir ? "" : fmtSize(e.size)),
    h("div", { class: "d" }, fmtDate(e.mod)));
  fui[side].rows.set(e.name, row);
  row.onclick = (ev) => {
    ev.stopPropagation();
    fside = side;
    const names = sorted(s).map((x) => x.name);
    if (ev.shiftKey && s.anchor) {
      const [a, b] = [names.indexOf(s.anchor), names.indexOf(e.name)].sort((x, y) => x - y);
      s.sel = new Set(names.slice(a, b + 1));
    } else if (ev.ctrlKey || ev.metaKey) {
      s.sel.has(e.name) ? s.sel.delete(e.name) : s.sel.add(e.name);
      s.anchor = e.name;
    } else {
      s.sel = new Set([e.name]);
      s.anchor = e.name;
    }
    paintSel(side);
  };
  row.ondblclick = () => fopen(side, e);
  row.oncontextmenu = (ev) => {
    ev.preventDefault();
    ev.stopPropagation();
    fside = side;
    if (!s.sel.has(e.name)) { s.sel = new Set([e.name]); s.anchor = e.name; paintSel(side); }
    showMenu(ev, side, true);
  };
  // arrastar: leva a seleção (ou só este item, se ele não estava selecionado)
  row.ondragstart = (ev) => {
    if (!s.sel.has(e.name)) { s.sel = new Set([e.name]); paintSel(side); }
    fdrag = { side, dir: s.path, names: [...s.sel] };
    ev.dataTransfer.effectAllowed = "copyMove";
    ev.dataTransfer.setData("text/plain", fdrag.names.join("\n"));
  };
  row.ondragend = () => { fdrag = null; setDrop(null); };
  if (e.dir) wireDrop(row, side, dirOf);
  return row;
}

function setDrop(el) {
  if (fdropEl === el) return;
  if (fdropEl) fdropEl.classList.remove("drop");
  fdropEl = el;
  if (el) el.classList.add("drop");
}

// soltar em `el` leva para a pasta que dirOf() devolver (a pasta aberta, ou a pasta sob o mouse)
function wireDrop(el, side, dirOf) {
  el.ondragover = (ev) => {
    if (!fdrag) return;
    ev.preventDefault();
    ev.stopPropagation();
    ev.dataTransfer.dropEffect = fdrag.side === side ? "move" : "copy";
    setDrop(el);
  };
  el.ondrop = (ev) => {
    ev.preventDefault();
    ev.stopPropagation();
    const src = fdrag;
    fdrag = null;
    setDrop(null);
    const dir = dirOf();
    if (!src || !dir || dir === "drives") return;
    if (src.side !== side) return transfer(src.side, src.names, src.dir, dir, false);
    if (dir === src.dir) return;
    fop(side, { op: "move", path: src.dir, names: src.names, to: dir }).then(() => fload(side, fcur[side].path, false)).catch((e) => toast("Não foi possível mover", errText(e), "err"));
  };
}

async function fopen(side, e) {
  const s = fcur[side];
  if (e.dir) return fload(side, s.path === "drives" ? e.name : joinPath(s.path, e.name));
  try { await fop(side, { op: "open", path: s.path, name: e.name }); } catch (err) { toast("Não foi possível abrir", errText(err), "err"); }
}

// ---------- operações do menu ----------

const failed = (what) => (e) => toast(what, errText(e), "err");
const selNames = (side) => [...fcur[side].sel];
const refresh = (side) => fload(side, fcur[side].path, false);

function sendSel(side) {
  const s = fcur[side], to = fcur[other(side)];
  if (s.sel.size && canWrite(to)) transfer(side, selNames(side), s.path, to.path, false);
}

function copySel(side, cut) {
  fclip = { side, dir: fcur[side].path, names: selNames(side), cut };
  SIDES.forEach(paintSel);
}

async function paste(side, dir) {
  const c = fclip;
  if (!c || !dir || dir === "drives") return;
  if (c.cut) fclip = null;
  if (c.side !== side) return transfer(c.side, c.names, c.dir, dir, c.cut);
  try {
    await fop(side, { op: c.cut ? "move" : "copy", path: c.dir, names: c.names, to: dir });
  } catch (e) { failed("Não foi possível colar")(e); }
  refresh(side);
}

async function rename(side) {
  const [name] = selNames(side);
  const to = name && await askBox("Renomear", "", name);
  if (!to || to === name) return;
  try { await fop(side, { op: "rename", path: fcur[side].path, name, to }); } catch (e) { return failed("Não foi possível renomear")(e); }
  fcur[side].sel = new Set([to]);
  refresh(side);
}

async function remove(side) {
  const names = selNames(side);
  if (!names.length) return;
  const what = names.length === 1 ? `"${names[0]}"` : `${names.length} itens`;
  if (!await askBox("Excluir", `Excluir ${what} de ${side === "local" ? "este computador" : "o computador remoto"}? Isso não pode ser desfeito.`)) return;
  try { await fop(side, { op: "delete", path: fcur[side].path, names }); } catch (e) { failed("Não foi possível excluir")(e); }
  refresh(side);
}

async function newFolder(side) {
  const name = await askBox("Nova pasta", "", "Nova pasta");
  if (!name) return;
  try { await fop(side, { op: "mkdir", path: fcur[side].path, name }); } catch (e) { return failed("Não foi possível criar a pasta")(e); }
  fcur[side].sel = new Set([name]);
  refresh(side);
}

async function props(side) {
  const names = selNames(side);
  try {
    const p = (await fop(side, { op: "props", path: fcur[side].path, names })).props;
    await askBox("Propriedades", `${names.length === 1 ? names[0] : names.length + " itens"}\nTamanho: ${fmtSize(p.size)} (${p.size.toLocaleString("pt-BR")} bytes)\nArquivos: ${p.files}\nPastas: ${p.dirs}${names.length === 1 ? "\nModificado: " + fmtDate(p.mod) : ""}`, undefined, false);
  } catch (e) { failed("Não foi possível ler as propriedades")(e); }
}

// ---------- menu do botão direito ----------

function showMenu(ev, side, onItem) {
  const s = fcur[side], to = fcur[other(side)];
  const one = s.sel.size === 1 ? s.entries.find((e) => e.name === [...s.sel][0]) : null;
  const into = onItem && one && one.dir ? joinPath(s.path, one.name) : s.path; // colar dentro da pasta selecionada
  const w = canWrite(s);
  const items = [
    ...(onItem ? [
      ["Abrir", () => fopen(side, one), !one],
      [side === "local" ? "Enviar ao computador remoto" : "Baixar para este computador", () => sendSel(side), !canWrite(to)],
      "-",
      ["Copiar", () => copySel(side, false)],
      ["Recortar", () => copySel(side, true), !w],
    ] : []),
    ["Colar", () => paste(side, into), !fclip || !(w || into !== s.path)],
    ...(onItem ? [["Renomear", () => rename(side), !one || !w], ["Excluir", () => remove(side), !w], "-"] : ["-"]),
    ["Nova pasta", () => newFolder(side), !w],
    ["Atualizar", () => refresh(side)],
    ...(onItem ? [["Propriedades", () => props(side)]] : []),
  ];
  const m = $("fmenu");
  m.replaceChildren(...items.map((it) => (it === "-" ? h("hr") : h("button", { role: "menuitem", ...(it[2] ? { disabled: "" } : {}), onclick: () => { hideMenu(); it[1](); } }, it[0]))));
  m.hidden = false;
  m.style.left = Math.min(ev.clientX, innerWidth - m.offsetWidth - 4) + "px";
  m.style.top = Math.min(ev.clientY, innerHeight - m.offsetHeight - 4) + "px";
}
const hideMenu = () => { $("fmenu").hidden = true; };
document.addEventListener("click", hideMenu);
document.addEventListener("scroll", hideMenu, true);
window.addEventListener("blur", hideMenu);

// ---------- montagem ----------

function buildPane(side) {
  const s = fcur[side];
  const ui = {};
  const ibtn = (name, label, onclick) => h("button", { class: "fbtn", "aria-label": label, title: label, onclick }, icon(name, 16));
  ui.back = ibtn("back", "Voltar", () => { const p = s.hist.pop(); if (p) fload(side, p, false); });
  ui.path = h("input", { class: "fpath", "aria-label": "Caminho", spellcheck: "false" });
  ui.path.onkeydown = (e) => { if (e.key === "Enter") fload(side, ui.path.value.trim()); e.stopPropagation(); };
  ui.err = h("p", { class: "ferr", role: "alert", hidden: "" });
  ui.act = h("button", { class: "fbtn", onclick: () => sendSel(side) }, icon(side === "local" ? "upload" : "download", 16), side === "local" ? "Upload" : "Baixar");
  ui.cols = {};
  const colBtn = (key) => (ui.cols[key] = h("button", { onclick: () => { s.asc = s.sort === key ? !s.asc : true; s.sort = key; frender(side); } }));
  ui.list = h("div", { class: "flist", role: "grid", tabindex: "-1" });
  ui.list.onclick = () => { fside = side; s.sel = new Set(); paintSel(side); };
  ui.list.oncontextmenu = (e) => { e.preventDefault(); fside = side; showMenu(e, side, false); };
  wireDrop(ui.list, side, () => s.path);
  const dev = side === "local"
    ? [(cfg && cfg.name) || "Este computador", fmtId(myId)]
    : [nameOf(fid), fmtId(fid)];
  const badge = h("div", { class: "fbadge" }, icon("monitor", 22));
  const head = h("div", { class: "fhead" },
    ...(side === "local" ? [h("div", { class: "fdev" }, badge, h("div", {}, h("b", {}, dev[0]), h("span", {}, "Este dispositivo"))), ui.act]
      : [ui.act, h("div", { class: "fact" }, h("div", { class: "fdev" }, h("div", { style: "text-align:right" }, h("b", {}, dev[0]), h("span", {}, "Dispositivo remoto")), badge),
        h("button", { class: "fbtn", "aria-label": "Fechar gerenciador de arquivos", title: "Fechar", onclick: closeFilesView }, icon("close", 16)))]));
  fui[side] = Object.assign(ui, { rows: new Map() });
  return h("section", { class: "fpane", "aria-label": dev[0], onpointerdown: () => { fside = side; } },
    head,
    h("div", { class: "fnav" }, ui.back, ibtn("up", "Pasta acima", () => fload(side, parentPath(s.path || ""))), ibtn("home", "Pasta inicial", () => fload(side, "")), ibtn("refresh", "Atualizar", () => refresh(side)), ui.path),
    ui.err,
    h("div", { class: "fcols" }, colBtn("name"), colBtn("type"), colBtn("size"), colBtn("date")),
    ui.list);
}

function openFiles(id) {
  fid = id;
  if (!fstate.has(id)) fstate.set(id, { local: newSide(), remote: newSide() });
  fcur = fstate.get(id);
  fui = {};
  $("filesDlg").replaceChildren(...SIDES.map(buildPane));
  SIDES.forEach(frender); // mostra na hora onde o usuário parou; a atualização vem logo depois
  $("filesDlg").show();
  $("filesDlg").focus();
  SIDES.forEach((side) => fload(side, fcur[side].path, false));
}

function closeFilesView() { hideMenu(); $("filesDlg").close(); }
function hideFiles(id) { if ($("filesDlg").open && fid !== id) closeFilesView(); } // trocou de aba: some, mas guarda o estado
function closeFiles(id) { // sessão encerrada: esquece o estado
  fstate.delete(id);
  if (fid === id) { if ($("filesDlg").open) closeFilesView(); fid = ""; fcur = null; fui = null; }
}

$("filesBtn").onclick = () => ($("filesDlg").open && fid === active ? closeFilesView() : openFiles(active));
$("filesDlg").addEventListener("close", () => { if (active !== "home") $("viewer").focus({ preventScroll: true }); });

// teclas do explorador no lado em foco
$("filesDlg").addEventListener("keydown", (e) => {
  if (e.target.tagName === "INPUT" || !fcur) return;
  const side = fside, s = fcur[side], ctrl = e.ctrlKey || e.metaKey, k = e.key.toLowerCase();
  const one = s.sel.size === 1 ? s.entries.find((x) => x.name === [...s.sel][0]) : null;
  if (e.key === "Delete" && canWrite(s)) remove(side);
  else if (e.key === "F2" && one) rename(side);
  else if (e.key === "F5") refresh(side);
  else if (e.key === "Enter" && one) fopen(side, one);
  else if (ctrl && k === "a") { s.sel = new Set(s.entries.map((x) => x.name)); paintSel(side); }
  else if (ctrl && k === "c" && s.sel.size) copySel(side, false);
  else if (ctrl && k === "x" && s.sel.size && canWrite(s)) copySel(side, true);
  else if (ctrl && k === "v") paste(side, one && one.dir ? joinPath(s.path, one.name) : s.path);
  else return;
  e.preventDefault();
});
