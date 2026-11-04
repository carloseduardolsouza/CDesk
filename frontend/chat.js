// Chat da sessão, nos dois lados: o viewer abre pelo botão Chat da barra da sessão e quem está
// sendo controlado, pelo botão Chat ao lado de "Encerrar sessão". O histórico fica na memória,
// por ID do outro computador, enquanto o CDesk estiver aberto.

const chats = new Map(); // ID do outro computador -> [{ who: "me" | "them" | "sys", text, at }]
const unread = new Map();
let chatKey = ""; // ID da conversa aberta ("" = painel fechado)

const peerName = nameOf;
const hhmm = (ms) => new Date(ms).toLocaleTimeString("pt-BR", { hour: "2-digit", minute: "2-digit" });

function chatPush(id, who, text) {
  if (!id) return;
  if (!chats.has(id)) chats.set(id, []);
  chats.get(id).push({ who, text, at: Date.now() });
  if (chatKey === id) renderChat();
}
const chatNote = (id, text) => chatPush(id, "sys", text);

function onChat(id, text) {
  chatPush(id, "them", text);
  if (chatKey === id && !document.hidden) return;
  unread.set(id, (unread.get(id) || 0) + 1);
  renderChatBtns();
}

function renderChatBtns() {
  const n = unread.get(active) || 0;
  $("chatBadge").textContent = n;
  $("chatBadge").hidden = !n;
}

function renderChat() {
  const k = chatKey;
  const list = h("div", { class: "cmsgs", role: "log", "aria-live": "polite" },
    ...(chats.get(k) || []).map((m) => h("div", { class: "cmsg " + m.who },
      h("div", { class: "chead" }, h("span", {}, m.who === "me" ? "Eu" : m.who === "them" ? peerName(k) : "CDesk"), h("time", {}, hhmm(m.at))),
      h("div", { class: "ctext" }, m.text))));
  const box = $("chatPanel").querySelector(".cmsgs");
  if (!box) { // primeira vez: monta o painel inteiro
    const input = h("textarea", { id: "chatInput", rows: "2", maxlength: "2000", placeholder: "Escreva uma mensagem", "aria-label": "Mensagem" });
    const sendBtn = h("button", { class: "csend", "aria-label": "Enviar mensagem", title: "Enviar" }, "➤");
    const submit = async () => {
      const text = input.value.trim();
      if (!text) return;
      input.value = "";
      try {
        await app().SendChat(chatKey, text);
        chatPush(chatKey, "me", text);
      } catch (e) {
        chatNote(chatKey, errText(e));
      }
    };
    input.onkeydown = (e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); submit(); } };
    sendBtn.onclick = submit;
    $("chatPanel").replaceChildren(
      h("div", { class: "chatbar" }, h("strong", { id: "chatTitle" }), h("button", { class: "x", "aria-label": "Fechar chat", onclick: closeChat }, icon("close", 14))),
      list, h("div", { class: "cform" }, input, sendBtn));
  } else {
    box.replaceWith(list);
  }
  $("chatTitle").textContent = "Chat · " + peerName(k);
  list.scrollTop = list.scrollHeight;
}

function openChat(id) {
  if (!id) return;
  chatKey = id;
  unread.delete(id);
  $("chatPanel").hidden = false;
  $("chatPanel").replaceChildren();
  renderChat();
  renderChatBtns();
  $("chatInput").focus();
}

function closeChat() {
  chatKey = "";
  $("chatPanel").hidden = true;
  if (typeof active !== "undefined" && (sessions.get(active) || {}).state === "live") $("viewer").focus({ preventScroll: true });
}

$("chatBtn").onclick = () => (chatKey === active ? closeChat() : openChat(active));
