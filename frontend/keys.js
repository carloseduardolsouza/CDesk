// Teclado da sessão: converte o que o usuário digita no que o outro computador precisa receber.
// Texto digitado vai como caractere, então acentos (ç, ã, é) e teclados de layout diferente nos dois
// lados funcionam; o resto (Enter, setas, F1..F12, Ctrl+C...) vai como tecla do Windows.
// Usa `sessions`, `active` e `send` do app.js (só em tempo de execução, depois de tudo carregado).

const typedAsChar = new Set(); // teclas (e.code) enviadas como caractere: o keyup delas não vai
const heldKeys = new Set(); // códigos de tecla (VK) que estão apertados no computador remoto

// Devolve a mensagem para o outro computador, ou null se este evento não deve ser enviado.
function keyMessage(e, t) {
  if (t === "kdown") {
    const altGr = !!e.getModifierState && e.getModifierState("AltGraph"); // no Windows, AltGr chega como Ctrl+Alt
    const printable = [...e.key].length === 1 && !e.metaKey && (altGr || (!e.ctrlKey && !e.altKey));
    if (printable) {
      typedAsChar.add(e.code);
      return { t: "char", s: e.key };
    }
    if (e.keyCode === 229) return null; // tecla morta ou composição: o caractere vem no próximo evento
    return { t: "kdown", k: e.keyCode };
  }
  if (typedAsChar.delete(e.code)) return null; // o caractere já foi digitado inteiro
  return e.keyCode === 229 ? null : { t: "kup", k: e.keyCode };
}

// Com uma janela aberta (ex.: senha), as teclas são da janela, não do computador remoto.
function remoteKey(e, t) {
  if ((sessions.get(active) || {}).state !== "live" || document.querySelector("dialog[open]") || e.target.closest?.("#chatPanel, #dispPanel")) return;
  e.preventDefault();
  const m = keyMessage(e, t);
  if (!m) return;
  if (m.t === "kdown") heldKeys.add(m.k);
  else if (m.t === "kup") heldKeys.delete(m.k);
  send(m);
}

// Solta o que ficou apertado (trocou de aba, perdeu o foco com Alt/Ctrl apertado), senão a tecla
// fica "presa" no outro computador.
function releaseKeys() {
  for (const k of heldKeys) send({ t: "kup", k });
  heldKeys.clear();
  typedAsChar.clear();
}

// captura (true): chega antes de qualquer outro tratador da página
window.addEventListener("keydown", (e) => remoteKey(e, "kdown"), true);
window.addEventListener("keyup", (e) => remoteKey(e, "kup"), true);
window.addEventListener("blur", releaseKeys);
