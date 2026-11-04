const $ = (id) => document.getElementById(id);
const app = () => window.go.main.App;

let myId = "", online = false, hostingWith = "", last = 0;
let savedIds = new Set(); // computadores com senha lembrada
const sessions = new Map(); // ID remoto -> { state: connecting|failed|live, step, error, logs, blob (último quadro), viewOnly }
let active = "home"; // "home", "settings" ou o ID de uma sessão
let settingsOpen = false;

// ---------- utilidades ----------

const fmtId = (s) => s.replace(/(\d{3})(?=\d)/g, "$1 "); // 123 456 789
const errText = (e) => String(e).replace(/^Error: /, "");

const ICONS = { // conteúdo fixo, nunca vem do usuário
  monitor: '<rect x="2" y="4" width="20" height="13" rx="2"/><path d="M8 21h8M12 17v4"/>',
  close: '<path d="M6 6l12 12M18 6L6 18"/>',
  star: '<path d="M12 3l2.8 5.7 6.2.9-4.5 4.4 1.1 6.2L12 17.3 6.4 20.2l1.1-6.2L3 9.6l6.2-.9z"/>',
  lock: '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/>',
  dots: '<circle cx="12" cy="5" r="1.8"/><circle cx="12" cy="12" r="1.8"/><circle cx="12" cy="19" r="1.8"/>',
  gear: '<circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M4.9 4.9l2.1 2.1M17 17l2.1 2.1M2 12h3M19 12h3M4.9 19.1L7 17M17 7l2.1-2.1"/>',
};
const icon = (name, size, fill = "none") => {
  const s = document.createElement("span");
  s.style.display = "inline-grid";
  s.innerHTML = `<svg aria-hidden="true" width="${size}" height="${size}" viewBox="0 0 24 24" fill="${fill}" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${ICONS[name]}</svg>`;
  return s;
};
const h = (tag, props = {}, ...kids) => {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "class") e.className = v;
    else if (k.startsWith("on")) e[k] = v;
    else e.setAttribute(k, v);
  }
  e.append(...kids);
  return e;
};

const say = (text, isError) => {
  $("msg").textContent = text;
  $("msg").hidden = !text;
  $("msg").classList.toggle("error", !!isError);
};

const ago = (ms) => {
  const min = Math.floor((Date.now() - ms) / 60000);
  if (min < 1) return "agora";
  if (min < 60) return `há ${min} min`;
  const hr = Math.floor(min / 60);
  if (hr < 24) return `há ${hr} h`;
  const d = Math.floor(hr / 24);
  return d === 1 ? "ontem" : `${d} dias atrás`;
};

// ---------- computadores salvos (só neste PC, no armazenamento do navegador) ----------

const KEY = "cdesk.machines";
let machines = [];
try { machines = JSON.parse(localStorage.getItem(KEY)) || []; } catch { /* sem armazenamento: lista vazia */ }
let listTab = "recent", showAll = false, rows = false;
try { rows = localStorage.getItem("cdesk.rows") === "1"; } catch { /* idem */ }

const persist = () => { try { localStorage.setItem(KEY, JSON.stringify(machines)); } catch { /* cota cheia: ignora */ } };
const nameOf = (id) => (machines.find((m) => m.id === id) || {}).name || fmtId(id);
const hasMachineName = (id) => !!(machines.find((m) => m.id === id) || {}).name;

const upsert = (id, patch) => {
  let m = machines.find((x) => x.id === id);
  if (!m) { m = { id, name: "", last: Date.now(), fav: false, thumb: "" }; machines.push(m); app().CheckOnline([id]); }
  Object.assign(m, patch);
  machines.sort((a, b) => b.last - a.last);
  machines = machines.slice(0, 30);
  persist();
  renderList();
  renderTabs();
};

// usado pela tela Configurações > Geral
const clearHistory = () => { machines = []; persist(); renderList(); renderTabs(); };

// miniatura da última imagem da sessão, para o cartão da lista
const makeThumb = (blob) => new Promise((done) => {
  const url = URL.createObjectURL(blob);
  const im = new Image();
  im.onload = () => {
    const c = document.createElement("canvas");
    c.width = 320;
    c.height = Math.round((320 * im.height) / im.width);
    c.getContext("2d").drawImage(im, 0, 0, c.width, c.height);
    URL.revokeObjectURL(url);
    done(c.toDataURL("image/jpeg", 0.6));
  };
  im.onerror = () => { URL.revokeObjectURL(url); done(""); };
  im.src = url;
});
const saveThumb = (id, s) => {
  if (s && s.blob) makeThumb(s.blob).then((t) => { if (t) upsert(id, { thumb: t }); });
};

// ---------- abas ----------

// O Go só avisa que chegou um quadro; aqui ele é buscado em binário (sem base64). Só a aba ativa
// busca, e no máximo um pedido por vez: se chegar outro aviso no meio, busca de novo depois.
const setScreen = (blob) => {
  const old = $("screen").src;
  $("screen").src = blob ? URL.createObjectURL(blob) : "";
  if (old.startsWith("blob:")) URL.revokeObjectURL(old);
};

async function pull(id) {
  const s = sessions.get(id);
  if (!s) return;
  if (s.fetching) { s.dirty = true; return; }
  s.fetching = true;
  try {
    do {
      s.dirty = false;
      const r = await fetch(`/frame/${id}`, { cache: "no-store" });
      if (!r.ok) break;
      s.blob = await r.blob();
      if (id === active) setScreen(s.blob);
      if (!s.thumbAt || Date.now() - s.thumbAt > 15000) { s.thumbAt = Date.now(); saveThumb(id, s); }
    } while (s.dirty && sessions.has(id) && id === active);
  } catch { /* o próximo quadro tenta de novo */ }
  s.fetching = false;
}

const renderTabs = () => {
  const label = (id, ...kids) => h("button", { class: "label", onclick: () => show(id), ...(id === active ? { "aria-current": "page" } : {}) }, ...kids);
  const home = h("div", { class: "tab home" + (active === "home" ? " active" : "") }, label("home", icon("monitor", 15), "Nova sessão"));
  const tabs = [...sessions.keys()].map((id) => {
    const x = h("button", { class: "x", "aria-label": "Fechar sessão " + nameOf(id), onclick: () => closeTab(id) }, icon("close", 12));
    return h("div", { class: "tab" + (id === active ? " active" : "") },
      h("span", { class: "dot " + (sessions.get(id).state || "live"), "aria-hidden": "true" }), label(id, nameOf(id)), x);
  });
  if (settingsOpen) {
    const x = h("button", { class: "x", "aria-label": "Fechar configurações", onclick: closeSettings }, icon("close", 12));
    tabs.push(h("div", { class: "tab" + (active === "settings" ? " active" : "") }, label("settings", icon("gear", 15), "Configurações"), x));
  }
  $("tabList").replaceChildren(home, ...tabs);
};

const show = (id) => {
  releaseKeys(); // solta o que ficou apertado na sessão que está saindo de cena
  document.exitPointerLock(); // o mouse capturado volta para a janela
  if (chatKey && chatKey !== id && chatKey !== hostingWith) closeChat(); // o chat é da sessão em que foi aberto
  closeDisp();
  closeOpts();
  hideFiles(id);
  active = id;
  const s = sessions.get(id);
  const live = !!s && s.state === "live";
  $("home").hidden = id !== "home";
  $("settings").hidden = id !== "settings";
  $("connecting").hidden = !s || live;
  $("viewer").hidden = !live;
  $("sessbar").hidden = !live;
  app().SetActiveSession(live ? id : ""); // só a aba em uso troca área de transferência
  if (live) {
    setScreen(s.blob);
    renderBar();
    pull(id);
    $("viewer").focus({ preventScroll: true }); // o teclado vai para a sessão
  } else if (s) {
    renderConn();
  }
  renderTabs();
};

const closeTab = (id) => {
  saveThumb(id, sessions.get(id));
  sessions.delete(id); // antes do Disconnect, para o evento "closed" ser ignorado
  closeFiles(id);
  audioStop(id);
  app().Cancel(id); // se ainda estava conectando
  app().Disconnect(id);
  if (active === id) show("home"); else renderTabs();
};
$("newTab").onclick = () => show("home");

const closeSettings = () => {
  settingsOpen = false;
  if (active === "settings") show("home"); else renderTabs();
};

// Barra da sessão: botões Chat, Arquivos e Exibição (monitores ficam na janela Exibição, display.js).
const renderBar = () => {
  const s = sessions.get(active);
  if (!s) return;
  $("sessbar").hidden = false;
  $("filesBtn").hidden = !!s.viewOnly;
  $("viewOnly").hidden = !s.viewOnly;
  applyDisp(s);
  if (!s.fxInit) { s.fxInit = true; sendFeat(s); } // pede ao host o cursor e a janela em foco
  placeCursor();
  renderDispPanel();
  renderChatBtns();
};

// ---------- lista de computadores ----------

// Quem está online: true/false por ID; sem entrada = ainda verificando (ponto cinza).
const presence = new Map();
let lanIds = []; // computadores com CDesk achados na rede local

const presenceOf = (m) => (m.lan ? true : presence.get(m.id)); // achado na rede local = online

const paintPresence = (el, m) => {
  const st = presenceOf(m);
  const dot = el.querySelector(".pdot");
  dot.className = "pdot " + (st === true ? "on" : st === false ? "off" : "wait");
  dot.setAttribute("aria-label", st === true ? "Online" : st === false ? "Offline" : "Verificando status");
  el.querySelector(".offline").hidden = st !== false;
};

const card = (m) => {
  const hue = parseInt(m.id, 10) % 360;
  const thumb = h("button", { class: "thumb", "aria-label": "Conectar a " + (m.name || fmtId(m.id)), onclick: () => openSession(m.id) },
    m.thumb ? h("img", { src: m.thumb, alt: "" }) : h("span", { class: "ph", "aria-hidden": "true" }, icon("monitor", 32)),
    h("span", { class: "offline", "aria-hidden": "true" }, h("b", {}, "Offline")));
  if (!m.thumb) thumb.style.background = `linear-gradient(160deg, hsl(${hue} 55% 30%), hsl(${(hue + 40) % 360} 60% 14%))`;
  const fav = h("button", {
    class: "fav", "aria-pressed": String(m.fav),
    "aria-label": m.fav ? "Remover dos favoritos" : "Adicionar aos favoritos",
    onclick: () => upsert(m.id, { fav: !m.fav }),
  }, icon("star", 16));
  const info = h("button", { class: "info", onclick: () => openSession(m.id) },
    h("span", { class: "name" }, m.name || fmtId(m.id)),
    h("span", { class: "sub" }, ...(m.name ? [h("span", { class: "mono" }, fmtId(m.id)), " · "] : []), m.last ? ago(m.last) : "Na rede local",
      ...(savedIds.has(m.id) ? [" · ", h("span", { class: "lockic", title: "Senha lembrada" }, icon("lock", 12))] : [])));
  const more = h("button", { class: "more", "aria-label": "Mais opções", onclick: () => editMachine(m.id) }, icon("dots", 16, "currentColor"));
  const el = h("article", { class: "card", "data-id": m.id }, thumb, h("span", { class: "pdot", role: "img" }), fav, h("div", { class: "meta" }, info, more));
  paintPresence(el, m);
  return el;
};

const TAB_TITLES = { recent: "Sessões recentes", fav: "Favoritos", lan: "Encontrados na rede" };
const EMPTY_TEXT = {
  recent: "Nenhuma sessão ainda. Digite o ID de um computador acima e clique em Conectar.",
  fav: "Marque um computador com a estrela para ele aparecer aqui.",
  lan: "Nenhum computador com CDesk foi encontrado na rede local. Confira se a descoberta na rede local está ligada em Configurações > Rede.",
};

// itens da aba atual; os da rede local carregam o nome e a miniatura do histórico, se houver
const listItems = () => {
  if (listTab === "lan") {
    return lanIds.map((id) => ({ ...(machines.find((m) => m.id === id) || { id, name: "", last: 0, fav: false, thumb: "" }), lan: true }));
  }
  return machines.filter((m) => listTab !== "fav" || m.fav);
};

const renderList = () => {
  const list = listItems();
  const shown = showAll ? list : list.slice(0, 6);
  $("listTitle").textContent = `${TAB_TITLES[listTab]} · ${list.length}`;
  $("showAll").hidden = list.length <= 6;
  $("showAll").textContent = showAll ? "Mostrar menos" : "Mostrar tudo";
  $("empty").hidden = list.length > 0;
  $("empty").textContent = EMPTY_TEXT[listTab];
  $("grid").classList.toggle("rows", rows);
  $("grid").replaceChildren(...shown.map(card));
  for (const [id, key] of [["tabRecent", "recent"], ["tabFav", "fav"], ["tabLan", "lan"]]) {
    $(id).setAttribute("aria-selected", String(listTab === key));
  }
  for (const [id, on] of [["viewGrid", !rows], ["viewList", rows]]) {
    $(id).classList.toggle("on", on);
    $(id).setAttribute("aria-pressed", String(on));
  }
};

// chegou o resultado de um computador: só repinta o cartão dele (a lista inteira piscaria)
const applyPresence = (id) => {
  const el = $("grid").querySelector(`[data-id="${id}"]`);
  if (el) paintPresence(el, { ...(machines.find((m) => m.id === id) || { id }), lan: listTab === "lan" });
};

const refreshPresence = () => {
  const ids = machines.map((m) => m.id);
  if (ids.length) app().CheckOnline(ids);
};
const refreshLan = async () => {
  lanIds = await app().LanPeers();
  if (listTab === "lan" && active === "home") renderList();
};

$("tabRecent").onclick = () => { listTab = "recent"; renderList(); };
$("tabFav").onclick = () => { listTab = "fav"; renderList(); };
$("tabLan").onclick = () => { listTab = "lan"; renderList(); refreshLan(); };
$("refreshPresence").onclick = () => {
  machines.forEach((m) => presence.delete(m.id)); // volta ao cinza ("verificando") até chegar o resultado
  renderList();
  refreshPresence();
  if (listTab === "lan") refreshLan();
};
$("showAll").onclick = () => { showAll = !showAll; renderList(); };
const setRows = (v) => { rows = v; try { localStorage.setItem("cdesk.rows", v ? "1" : "0"); } catch { /* idem */ } renderList(); };
$("viewGrid").onclick = () => setRows(false);
$("viewList").onclick = () => setRows(true);

let editing = "";
const editMachine = (id) => {
  editing = id;
  $("editId").textContent = fmtId(id);
  $("editName").value = hasMachineName(id) ? nameOf(id) : "";
  $("editForget").hidden = !savedIds.has(id);
  $("editDlg").showModal();
};
$("editForm").onsubmit = (e) => { e.preventDefault(); upsert(editing, { name: $("editName").value.trim() }); $("editDlg").close(); };
$("editCancel").onclick = () => $("editDlg").close();
$("editForget").onclick = async () => { await app().ForgetPassword(editing); await refreshSaved(); $("editDlg").close(); };
$("editRemove").onclick = () => {
  machines = machines.filter((m) => m.id !== editing);
  persist();
  renderList();
  $("editDlg").close();
};

// ---------- estado deste computador ----------

const renderState = () => {
  const ok = online || !!hostingWith;
  $("net").classList.toggle("off", !ok);
  $("net").setAttribute("aria-label", ok ? "Online, pronto para conexões" : "Conectando à rede");
  $("stateDot").className = "sdot" + (hostingWith ? " busy" : online ? " ok" : "");
  $("end").hidden = !hostingWith;
  renderChatBtns();
  $("stateText").textContent = hostingWith
    ? `${fmtId(hostingWith)} está controlando este computador.`
    : online ? "Pronto. Este computador pode receber conexões." : "Conectando à rede...";
};

let net = { online: false, known: 0 }; // lido também pela tela Configurações > Rede
const pollStatus = async () => {
  net = await app().Status();
  online = net.online;
  renderState();
  if (typeof updateNetStatus === "function") updateNetStatus();
};

// ---------- menu ----------

const setMenu = (open) => {
  $("menu").hidden = !open;
  $("menuBtn").setAttribute("aria-expanded", String(open));
};
$("menuBtn").onclick = (e) => { e.stopPropagation(); setMenu($("menu").hidden); };
document.addEventListener("click", (e) => { if (!$("menu").contains(e.target)) setMenu(false); });
document.addEventListener("keydown", (e) => { if (e.key === "Escape") setMenu(false); });
const menuTo = (section) => () => { setMenu(false); openSettings(section); };
$("miSettings").onclick = menuTo("geral");
$("miSec").onclick = menuTo("seguranca");
$("miNet").onclick = menuTo("rede");
$("miAbout").onclick = menuTo("sobre");
$("miQuit").onclick = () => app().Quit();

$("end").onclick = () => app().EndSession();

$("copyId").onclick = async () => {
  await window.runtime.ClipboardSetText(fmtId(myId));
  const label = $("copyId").querySelector("span");
  label.textContent = "Copiado";
  setTimeout(() => { label.textContent = "Copiar"; }, 1500);
};

// ---------- conectar ----------

const refreshSaved = async () => { savedIds = new Set(await app().SavedPasswords()); renderList(); };

// Pede a senha do outro computador. Devolve { pw, remember, askPermission } ou null se cancelar.
const askPassword = (id, wrong) => new Promise((resolve) => {
  const dlg = $("pwAsk");
  $("pwAskName").textContent = nameOf(id);
  $("pwAskInput").value = "";
  $("pwAskRemember").checked = false;
  $("pwAskErr").hidden = !wrong;
  dlg.returnValue = "";
  dlg.addEventListener("close", () => {
    const v = dlg.returnValue;
    resolve(v === "ok" ? { pw: $("pwAskInput").value, remember: $("pwAskRemember").checked, askPermission: false }
      : v === "perm" ? { pw: "", remember: false, askPermission: true } : null);
  }, { once: true });
  dlg.showModal();
  $("pwAskInput").focus();
});

// Tela de carregamento da aba (ou o erro, com "Tentar novamente").
const renderConn = () => {
  const s = sessions.get(active);
  if (!s || s.state === "live") return;
  const failed = s.state === "failed";
  $("cTitle").textContent = nameOf(active);
  $("cSub").textContent = "ID " + fmtId(active);
  $("cFill").style.width = failed ? "100%" : Math.round(((s.step || 0) / 3) * 100) + "%";
  $("cFill").classList.toggle("bad", failed);
  $("cLeft").className = "node left " + (failed ? "bad" : "spin");
  $("cStatus").textContent = failed ? s.error : s.text || "Preparando a conexão...";
  $("cStatus").classList.toggle("error", failed);
  $("cRetry").hidden = !failed;
  $("cLogs").textContent = s.logs.join("\n");
};
$("cLogsBtn").onclick = () => {
  const open = $("cLogs").hidden;
  $("cLogs").hidden = !open;
  $("cLogsBtn").setAttribute("aria-expanded", String(open));
  $("cLogsBtn").textContent = open ? "Ocultar detalhes" : "Ver detalhes";
};
$("cClose").onclick = () => closeTab(active);
$("cRetry").onclick = () => openSession(active);

// A aba abre na hora, com a tela de carregamento. Sem senha digitada, o Go usa a lembrada.
async function openSession(id, pw = "", askPermission = false, remember = false) {
  let s = sessions.get(id);
  if (!s) { s = { logs: [] }; sessions.set(id, s); }
  if (s.state === "live") { show(id); return; }
  Object.assign(s, { state: "connecting", step: 0, text: "", error: "" });
  Object.assign(s, { disp: undefined, opts: undefined, fxInit: false, cur: null }); // o host novo começa do zero: as escolhas da sessão anterior não valem
  chatNote(id, "Pedido de sessão enviado");
  show(id);
  try {
    await app().Connect(id, pw, askPermission);
    chatNote(id, "Sessão iniciada");
    if (remember && pw) { await app().SavePassword(id, pw); refreshSaved(); }
    s.state = "live";
    upsert(id, { last: Date.now() });
    if (active === id) show(id);
  } catch (err) {
    if (sessions.get(id) !== s) return; // aba fechada durante a conexão
    const msg = errText(err);
    if (msg === "CANCELADO") return;
    if (msg === "SENHA_NECESSARIA" || msg === "SENHA_INCORRETA") {
      const wrong = msg === "SENHA_INCORRETA";
      if (wrong) app().ForgetPassword(id).then(refreshSaved); // a senha lembrada não vale mais
      s.text = wrong ? "Senha incorreta." : "Esse computador pede uma senha.";
      s.logs.push(s.text);
      if (active === id) renderConn();
      const r = await askPassword(id, wrong);
      if (sessions.get(id) !== s) return;
      if (r) return openSession(id, r.pw, r.askPermission, r.remember);
      s.error = "Conexão cancelada: a senha não foi informada.";
    } else {
      s.error = msg;
    }
    s.logs.push("Falha: " + s.error);
    s.state = "failed";
    if (active === id) renderConn();
  }
}

$("connectForm").onsubmit = (e) => {
  e.preventDefault();
  const id = $("rid").value.replace(/\D/g, "");
  if (id.length !== 9) { say("ID inválido. O ID tem 9 números.", true); return; }
  if (id === myId) { say("Esse é o ID deste computador. Digite o ID do outro computador.", true); return; }
  say("");
  $("rid").value = "";
  openSession(id);
};

// ---------- eventos do Go ----------

window.addEventListener("load", async () => {
  const i = await app().Info();
  if (i.err) {
    $("myId").textContent = "Indisponível";
    say(i.err, true);
  } else {
    myId = i.id;
    $("myId").textContent = fmtId(i.id);
  }
  await loadSettings(); // settings.js
  await refreshSaved();
  pollStatus();
  setInterval(pollStatus, 3000);
  refreshPresence();
  refreshLan();
  // com a janela à vista na tela inicial, atualiza quem está online (a cada 30 s) e a lista da rede local (5 s)
  setInterval(() => { if (active === "home" && !document.hidden) refreshPresence(); }, 30000);
  setInterval(() => { if (active === "home" && !document.hidden) refreshLan(); }, 5000);

  const ev = window.runtime;
  ev.EventsOn("frame", (id) => { if (id === active) pull(id); });
  ev.EventsOn("closed", (id) => {
    const s = sessions.get(id);
    if (!s) return; // já fechada pelo usuário
    saveThumb(id, s);
    sessions.delete(id);
    closeFiles(id);
    audioStop(id);
    if (active === id) show("home"); else renderTabs();
    say(`Sessão com ${nameOf(id)} encerrada.`);
  });
  ev.EventsOn("mode", (id, mode) => {
    const s = sessions.get(id);
    if (s && mode === "view") { s.viewOnly = true; if (id === active) renderBar(); }
  });
  ev.EventsOn("fileProgress", onFileProgress);
  ev.EventsOn("chat", onChat);
  ev.EventsOn("cursor", onRemoteCursor);
  ev.EventsOn("focus", onRemoteFocus);
  ev.EventsOn("fileClip", onFileClip);
  ev.EventsOn("update", (v, url) => say(`Versão ${v} do CDesk disponível: ${url}`));
  ev.EventsOn("presence", (id, isOnline) => { presence.set(id, isOnline); applyPresence(id); });
  ev.EventsOn("monitors", (id, count, current) => { // o computador remoto diz quantos monitores tem
    const s = sessions.get(id);
    if (!s) return;
    s.monitors = count;
    s.monitor = current;
    if (id === active) renderBar();
  });
  ev.EventsOn("status", (id, step, text) => {
    const s = sessions.get(id);
    if (!s) return;
    s.step = step;
    s.text = text;
    s.logs.push(new Date().toLocaleTimeString("pt-BR") + "  " + text);
    if (id === active) renderConn();
  });
  ev.EventsOn("hosting", (id) => { hostingWith = id; renderState(); });
  ev.EventsOn("hostingEnded", () => { hostingWith = ""; renderState(); });
});

// ---------- mouse e teclado da sessão ativa ----------

const INPUT = new Set(["move", "down", "up", "wheel", "kdown", "char", "rel", "rdown", "rup"]); // ficam de fora quando "Controlar teclado e mouse" está desligado (options.js); kup passa para soltar teclas
const send = (ev) => {
  const o = (sessions.get(active) || {}).opts;
  if (o && !o.control && INPUT.has(ev.t)) return;
  app().SendInput(active, JSON.stringify(ev));
};
const s = $("screen");
const clamp = (v) => Math.min(1, Math.max(0, v));
// object-fit: contain deixa faixas pretas, então desconta a sobra para mapear o pixel certo.
const pos = (e) => {
  const r = s.getBoundingClientRect();
  if (viewMode() !== "reduzido") return { x: clamp((e.clientX - r.left) / r.width), y: clamp((e.clientY - r.top) / r.height) }; // a imagem ocupa o elemento inteiro
  const k = Math.min(r.width / s.naturalWidth, r.height / s.naturalHeight);
  const w = s.naturalWidth * k, hh = s.naturalHeight * k;
  return {
    x: clamp((e.clientX - r.left - (r.width - w) / 2) / w),
    y: clamp((e.clientY - r.top - (r.height - hh) / 2) / hh),
  };
};
// Lembra onde o viewer mexeu o mouse por último, para o cursor "Automático" distinguir quem mexeu.
const noteSent = (p) => {
  const ss = sessions.get(active);
  if (ss) { ss.sentX = p.x; ss.sentY = p.y; ss.sentAt = Date.now(); }
  return p;
};
const captured = () => document.pointerLockElement === s;
// pixels do computador remoto por pixel desta tela, para o movimento relativo ter o mesmo tamanho lá
const remoteScale = () => { const r = shownRect(); return s.naturalWidth ? s.naturalWidth / r.width : 1; };
const wantCapture = () => { const ss = sessions.get(active); return !!(ss && ss.disp && ss.disp.capture); };
s.onmousemove = (e) => {
  if (captured()) { const k = remoteScale(); send({ t: "rel", x: e.movementX * k, y: e.movementY * k }); return; }
  if (Date.now() - last > 30) { last = Date.now(); send({ t: "move", ...noteSent(pos(e)) }); }
};
s.onmousedown = (e) => {
  e.preventDefault();
  $("viewer").focus({ preventScroll: true });
  if (captured()) { send({ t: "rdown", b: e.button }); return; }
  if (wantCapture()) { s.requestPointerLock(); return; } // o primeiro clique só prende o mouse na tela
  send({ t: "down", b: e.button, ...noteSent(pos(e)) });
};
s.onmouseup = (e) => send(captured() ? { t: "rup", b: e.button } : { t: "up", b: e.button, ...pos(e) });
s.oncontextmenu = (e) => e.preventDefault();
s.onwheel = (e) => send({ t: "wheel", d: -Math.sign(e.deltaY) * 120 });
// teclado: veja keys.js

renderTabs();
renderList();
