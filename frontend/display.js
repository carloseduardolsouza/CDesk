// Janela "Exibição" da sessão (botão na barra do topo): modo de trabalho, mouse capturado, monitores,
// modo de visualização, tela inteira, resolução, modo noturno, qualidade e ajudantes visuais
// (ponteiro remoto, seguir cursor/janela, assistente de janelas). As escolhas valem só para a aba aberta.
// Usa `sessions`, `active`, `cfg` (configurações) e `app()` do app.js e settings.js.

const WORK = { eficiencia: ["Eficiência", 0], video: ["Vídeo", 30], jogos: ["Jogos", 30] }; // [nome, quadros/s (0 = o das Configurações)]
const defaultDisp = () => ({
  work: "eficiencia", view: "reduzido", night: false, prio: (typeof cfg === "object" && cfg.priority) || "equilibrada", adapt: true, detail: false,
  capture: false, autoRes: false, cursor: typeof cfg === "object" && cfg.showCursor ? "show" : "auto", followCursor: false, followWin: false, assist: false,
});
let fullscreen = false;

const dispOf = (s) => (s.disp = s.disp || defaultDisp());
const viewMode = () => ((sessions.get(active) || {}).disp || {}).view || "reduzido";
const sendEv = (ev) => app().SendInput(active, JSON.stringify(ev));

// aplica na imagem o que é só visual
function applyDisp(s) {
  const d = dispOf(s);
  $("screen").dataset.view = d.view;
  $("screen").classList.toggle("night", d.night);
}

// avisa o host da qualidade escolhida (prioridade, adaptação automática, detalhes e quadros/s)
function sendQuality(s) {
  const d = dispOf(s);
  const fps = WORK[d.work][1] || (typeof cfg === "object" && cfg.fps) || 15;
  sendEv({ t: "quality", s: d.prio, adapt: d.adapt, detail: d.detail, fps });
}

// diz ao host o que mandar de volta: o cursor (bit 1) e a janela em foco (bit 2)
function sendFeat(s) {
  const d = dispOf(s);
  sendEv({ t: "feat", k: (d.cursor !== "off" || d.followCursor ? 1 : 0) | (d.followWin ? 2 : 0) });
}

// pede ao host a resolução mais parecida com a janela daqui (0 x 0 = volta à resolução dele)
function sendRes(s) {
  const d = dispOf(s);
  const v = $("viewer");
  if (d.autoRes && !v.clientWidth) return;
  sendEv(d.autoRes ? { t: "res", x: Math.round(v.clientWidth * devicePixelRatio), y: Math.round(v.clientHeight * devicePixelRatio) } : { t: "res", x: 0, y: 0 });
}
let resTimer = 0;
new ResizeObserver(() => {
  const s = sessions.get(active);
  if (!s || !s.disp || !s.disp.autoRes || s.state !== "live") return;
  clearTimeout(resTimer);
  resTimer = setTimeout(() => sendRes(s), 600); // espera a janela parar de mudar de tamanho
}).observe($("viewer"));

const seg = (opts, value, onpick) => h("div", { class: "seg", role: "radiogroup" },
  ...opts.map(([v, text, beta]) => h("button", { class: "segb" + (v === value ? " on" : ""), role: "radio", "aria-checked": String(v === value), onclick: () => onpick(v) },
    text, ...(beta ? [h("span", { class: "beta" }, "BETA")] : []))));
const check = (text, on, onchange, disabled) => {
  const i = h("input", { type: "checkbox" });
  i.checked = on;
  if (disabled) i.disabled = true;
  i.onchange = () => onchange(i.checked);
  return h("label", { class: "dcheck" }, i, text);
};

function renderDispPanel() {
  const s = sessions.get(active);
  if ($("dispPanel").hidden || !s) return;
  const d = dispOf(s);
  const again = (fn) => (v) => { fn(v); applyDisp(s); renderDispPanel(); };
  const mons = [];
  for (let i = 0; i < (s.monitors || 1); i++) mons.push([i, String(i + 1)]);
  const follow = (key, v) => {
    d[key] = v;
    if (v) d.view = "original"; // seguir só faz sentido com a imagem no tamanho real, rolando
    sendFeat(s);
    applyDisp(s);
    renderDispPanel();
  };
  $("dispPanel").replaceChildren(
    h("h3", {}, "Modo de trabalho"),
    seg([["eficiencia", "Eficiência"], ["video", "Vídeo", 1], ["jogos", "Jogos", 1]], d.work, again((v) => { d.work = v; sendQuality(s); })),
    check("Captura do mouse (para jogos: o mouse fica preso à tela; Esc solta)", d.capture, again((v) => { d.capture = v; if (!v) document.exitPointerLock(); }), !!s.viewOnly),
    h("h3", {}, "Monitores"),
    seg(mons, s.monitor || 0, (i) => { s.monitor = i; renderDispPanel(); app().SetMonitor(active, i); }),
    h("h3", {}, "Modo de visualização"),
    seg([["original", "Original"], ["reduzido", "Reduzido"], ["esticado", "Esticado"]], d.view, again((v) => { d.view = v; })),
    check("Modo de tela inteira", fullscreen, (on) => { fullscreen = on; on ? window.runtime.WindowFullscreen() : window.runtime.WindowUnfullscreen(); }),
    check("Auto-adaptar resolução (muda a resolução do outro computador para caber nesta janela)", d.autoRes, again((v) => { d.autoRes = v; sendRes(s); }), !!s.viewOnly),
    check("Modo noturno", d.night, again((v) => { d.night = v; })),
    h("hr"),
    h("h3", {}, "Qualidade"),
    seg([["qualidade", "Melhor qualidade de imagem"], ["equilibrada", "Equilibrado"], ["velocidade", "Otimizar tempo de reação"]], d.prio, again((v) => { d.prio = v; sendQuality(s); })),
    check("Preservar detalhes", d.detail, again((v) => { d.detail = v; sendQuality(s); })),
    check("Detectar velocidade da conexão", d.adapt, again((v) => { d.adapt = v; sendQuality(s); })),
    h("hr"),
    h("h3", {}, "Ajudantes visuais"),
    h("p", { class: "dnote" }, "Exibir ponteiro do mouse remoto"),
    seg([["off", "Desabilitado"], ["show", "Mostrar"], ["auto", "Automático"]], d.cursor, (v) => { d.cursor = v; sendFeat(s); renderDispPanel(); placeCursor(); }),
    check("Seguir cursor remoto", d.followCursor, (v) => follow("followCursor", v)),
    check("Seguir janela em foco do dispositivo remoto", d.followWin, (v) => follow("followWin", v)),
    check("Assistente para mover janelas (traz para a tela as janelas que ficaram fora dela)", d.assist, (v) => { d.assist = v; sendEv({ t: "winassist", k: v ? 1 : 0 }); }, !!s.viewOnly));
}

// ---------- cursor e janela do computador remoto ----------

// área da tela onde a imagem aparece (em coordenadas da janela)
function shownRect() {
  const im = $("screen"), r = im.getBoundingClientRect();
  if (viewMode() !== "reduzido" || !im.naturalWidth) return r;
  const k = Math.min(r.width / im.naturalWidth, r.height / im.naturalHeight);
  const w = im.naturalWidth * k, h2 = im.naturalHeight * k;
  return { left: r.left + (r.width - w) / 2, top: r.top + (r.height - h2) / 2, width: w, height: h2 };
}

let cursorHideTimer = 0;
function placeCursor() {
  const s = sessions.get(active), el = $("rcursor");
  const c = s && s.cur;
  if (!s || s.state !== "live" || !c || dispOf(s).cursor === "off" || c.x < 0 || c.x > 1 || c.y < 0 || c.y > 1) { el.hidden = true; return; }
  if (dispOf(s).cursor === "auto") { // só aparece quando quem está no outro computador mexe no mouse dele
    const left = 2500 - (Date.now() - (s.hostMovedAt || 0));
    el.hidden = left <= 0;
    if (left <= 0) return;
    clearTimeout(cursorHideTimer);
    cursorHideTimer = setTimeout(placeCursor, left + 50);
  } else {
    el.hidden = false;
  }
  const r = shownRect();
  el.style.left = r.left + c.x * r.width + "px";
  el.style.top = r.top + c.y * r.height + "px";
}

// rola a imagem (tamanho real) para trazer o ponto (x, y), de 0 a 1, para perto do centro
function followTo(x, y) {
  const v = $("viewer"), im = $("screen");
  if (viewMode() !== "original" || !im.naturalWidth) return;
  const px = x * im.naturalWidth, py = y * im.naturalHeight;
  const m = 0.2; // só rola quando o ponto chega perto da borda
  if (px < v.scrollLeft + v.clientWidth * m || px > v.scrollLeft + v.clientWidth * (1 - m)) v.scrollLeft = px - v.clientWidth / 2;
  if (py < v.scrollTop + v.clientHeight * m || py > v.scrollTop + v.clientHeight * (1 - m)) v.scrollTop = py - v.clientHeight / 2;
}

function onRemoteCursor(id, json) {
  const s = sessions.get(id);
  if (!s) return;
  let c;
  try { c = JSON.parse(json); } catch { return; }
  const moved = !s.cur || Math.abs(c.x - s.cur.x) + Math.abs(c.y - s.cur.y) > 0.002;
  // o cursor mexeu sem o viewer ter mexido o mouse há pouco: foi quem está no outro computador
  if (moved && s.cur && Date.now() - (s.sentAt || 0) > 400 && Math.abs(c.x - (s.sentX ?? -1)) + Math.abs(c.y - (s.sentY ?? -1)) > 0.01) s.hostMovedAt = Date.now();
  s.cur = c;
  if (id !== active) return;
  placeCursor();
  if (dispOf(s).followCursor && moved) followTo(c.x, c.y);
}

function onRemoteFocus(id, json) {
  const s = sessions.get(id);
  let f;
  try { f = JSON.parse(json); } catch { return; }
  if (s && id === active && dispOf(s).followWin) followTo(f.x + f.w / 2, f.y + f.h / 2);
}

window.addEventListener("resize", placeCursor);
$("viewer").addEventListener("scroll", placeCursor);

function closeDisp() {
  $("dispPanel").hidden = true;
  $("dispBtn").setAttribute("aria-expanded", "false");
}

$("dispBtn").onclick = (e) => {
  e.stopPropagation();
  closeOpts();
  const open = $("dispPanel").hidden;
  $("dispPanel").hidden = !open;
  $("dispBtn").setAttribute("aria-expanded", String(open));
  renderDispPanel();
};
document.addEventListener("click", (e) => { if (!$("dispPanel").hidden && !$("dispPanel").contains(e.target)) closeDisp(); });
