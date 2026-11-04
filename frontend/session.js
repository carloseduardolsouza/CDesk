// Janela da sessão no host: lê o estado do app principal a cada 500 ms (Poll) e manda de volta
// o chat, as permissões e o pedido de desconectar. Veja session_win.go.
const $ = (id) => document.getElementById(id);
const sess = () => window.go.main.SessionApp;
const fmtId = (s) => s.replace(/(\d{3})(?=\d)/g, "$1 ");
const hhmm = (ms) => new Date(ms).toLocaleTimeString("pt-BR", { hour: "2-digit", minute: "2-digit" });
const pad = (n) => String(n).padStart(2, "0");

const ICON_WAIT = '<svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="#F5B93C" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><path d="M8 7h8M8 17h8M9 7c0 4 6 4 6 10M15 7c0 4-6 4-6 10"/></svg>';
const ICON_OK = '<svg aria-hidden="true" width="16" height="16" viewBox="0 0 24 24"><rect x="2" y="3" width="20" height="14" rx="2" fill="#22A559"/><path d="M7 10l3 3 6-6" fill="none" stroke="#fff" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/><path d="M8 21h8" stroke="#22A559" stroke-width="2" stroke-linecap="round"/></svg>';

let p = { id: "", name: "", since: Date.now() };
let chatN = 0, logN = 0, unread = 0, tab = "chat", busy = false;

const el = (tag, cls, ...kids) => {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  e.append(...kids);
  return e;
};
const svg = (s) => { const t = document.createElement("template"); t.innerHTML = s; return t.content.firstChild; }; // só ícones fixos

const peerName = () => p.name || "-";

function addChat(m) {
  const box = $("swMsgs");
  const stick = box.scrollTop + box.clientHeight >= box.scrollHeight - 8;
  const body = el("div", "cbody");
  if (m.who === "sys") body.append(svg(m.text.startsWith("Pedido") ? ICON_WAIT : ICON_OK));
  body.append(el("span", "", m.text)); // textContent: o texto vem do outro computador
  box.append(el("div", "cmsg " + m.who, el("div", "chead", el("span", "", m.who === "me" ? "Eu" : peerName()), el("time", "", hhmm(m.at))), body));
  if (stick || m.who === "me") box.scrollTop = box.scrollHeight;
}

function addLog(m) {
  const box = $("swLog");
  const empty = box.querySelector(".empty");
  if (empty) empty.remove();
  box.append(el("div", "cmsg", el("div", "chead", el("span", "", "CDesk"), el("time", "", hhmm(m.at))), el("div", "cbody", el("span", "", m.text))));
}

function setTab(t) {
  tab = t;
  $("tabChat").setAttribute("aria-selected", String(t === "chat"));
  $("tabFiles").setAttribute("aria-selected", String(t === "files"));
  $("paneChat").hidden = t !== "chat";
  $("paneFiles").hidden = t !== "files";
  if (t === "chat") { unread = 0; $("swUnread").hidden = true; $("swInput").focus(); }
}
$("tabChat").onclick = () => setTab("chat");
$("tabFiles").onclick = () => setTab("files");

async function poll() {
  if (busy) return;
  busy = true;
  try {
    const r = await sess().Poll(chatN, logN);
    let incoming = 0;
    for (const m of r.chat) { addChat(m); if (m.who === "them") incoming++; }
    for (const m of r.log) addLog(m);
    chatN += r.chat.length;
    logN += r.log.length;
    if (incoming) {
      if (tab !== "chat") { unread += incoming; $("swUnread").textContent = unread; $("swUnread").hidden = false; }
      if (!document.hasFocus()) { window.runtime.WindowUnminimise(); window.runtime.WindowShow(); } // chegou mensagem com a janela escondida
    }
    for (const b of document.querySelectorAll(".perm")) {
      const on = r[b.dataset.key];
      b.classList.toggle("on", on);
      b.setAttribute("aria-pressed", String(on));
    }
    if (!r.active) {
      $("swEnd").disabled = true;
      $("swEnd").textContent = "Sessão encerrada";
    }
  } catch { /* o Go fecha a janela se o app principal sumir */ }
  busy = false;
}

async function send() {
  const text = $("swInput").value.trim();
  if (!text) return;
  $("swInput").value = "";
  try { await sess().Send(text); } catch { $("swInput").value = text; }
  poll();
}
$("swSend").onclick = send;
$("swInput").onkeydown = (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); send(); } };

for (const b of document.querySelectorAll(".perm")) {
  b.onclick = async () => { await sess().SetPerm(b.dataset.key, b.getAttribute("aria-pressed") !== "true"); poll(); };
}
$("swEnd").onclick = () => sess().End();

window.addEventListener("load", async () => {
  p = await sess().Params();
  $("swName").textContent = peerName();
  $("swId").textContent = "(" + fmtId(p.id) + ")";
  document.title = "CDesk - " + fmtId(p.id);
  $("swLog").append(el("p", "empty", "Nenhum arquivo foi transferido nesta sessão."));
  const tick = () => {
    const s = Math.max(0, Math.floor((Date.now() - p.since) / 1000));
    $("swTime").textContent = `Conectado ${pad(Math.floor(s / 3600))}:${pad(Math.floor(s / 60) % 60)}:${pad(s % 60)}`;
  };
  tick();
  setInterval(tick, 1000);
  poll();
  setInterval(poll, 500);
  $("swInput").focus();
});
