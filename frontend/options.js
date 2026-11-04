// Menu de opções da sessão (botão de escudo na barra): o que o viewer faz com o computador remoto.
// Controlar teclado e mouse e sincronizar o texto copiado valem só para a aba; o resto pede ao host
// (áudio, arquivos copiados, bloquear a tela ao fim, bloquear as entradas de quem está lá e o modo
// privacidade). Os recursos que mexem no host exigem que o controle esteja permitido lá.
// Usa `sessions`, `active`, `h` e `app()` do app.js e `closeDisp` do display.js.

const defaultOpts = () => ({ control: true, clip: true, clipFiles: false, lockEnd: false, audio: false, block: false, privacy: false });
const optsOf = (s) => (s.opts = s.opts || defaultOpts());

function renderOpts() {
  const s = sessions.get(active);
  if ($("optsPanel").hidden || !s) return;
  const o = optsOf(s);
  const view = !!s.viewOnly; // só visualização: o host não deixa mexer nele
  const row = (text, on, onchange, { sub = false, off = false } = {}) => {
    const i = h("input", { type: "checkbox" });
    i.checked = on && !off;
    i.disabled = off;
    i.onchange = () => { onchange(i.checked); renderOpts(); };
    return h("label", { class: "dcheck" + (sub ? " sub" : "") }, i, text);
  };
  const ev = (ev) => app().SendInput(active, JSON.stringify(ev));
  const lockNote = o.block || o.privacy ? [h("p", { class: "dnote" }, "Quem está no outro computador desbloqueia com Ctrl+Alt+Shift+F12.")] : [];
  $("optsPanel").replaceChildren(
    row("Transmitir áudio", o.audio, (v) => { o.audio = v; v ? audioStart(active) : audioStop(active); ev({ t: "audio", k: v ? 1 : 0 }); }, { off: view }),
    row("Controlar teclado e mouse", o.control, (v) => { o.control = v; releaseKeys(); }, { off: view }),
    row("Sincronizar área de transferência de texto", o.clip, (v) => {
      o.clip = v;
      app().SetSessionOption(active, "clip", v);
      if (v) app().SetActiveSession(active);
      else if (o.clipFiles) { o.clipFiles = false; app().SetSessionOption(active, "clipFiles", false); }
    }),
    row("Sincronizar área de transferência de arquivos", o.clipFiles, (v) => { o.clipFiles = v; app().SetSessionOption(active, "clipFiles", v); }, { sub: true, off: view || !o.clip }),
    row("Bloquear entradas do usuário", o.block || o.privacy, (v) => { o.block = v; ev({ t: "block", k: v ? 1 : 0 }); }, { off: view || o.privacy }),
    row("Bloquear dispositivo remoto ao final da sessão", o.lockEnd, (v) => { o.lockEnd = v; app().SetSessionOption(active, "lockEnd", v); }, { off: view }),
    row("Ativar Modo Privacidade (a tela do outro computador fica preta para quem está lá)", o.privacy, (v) => { o.privacy = v; o.block = v; ev({ t: "privacy", k: v ? 1 : 0 }); }, { off: view }),
    ...lockNote);
}

function closeOpts() {
  $("optsPanel").hidden = true;
  $("optsBtn").setAttribute("aria-expanded", "false");
}

$("optsBtn").onclick = (e) => {
  e.stopPropagation();
  closeDisp();
  const open = $("optsPanel").hidden;
  $("optsPanel").hidden = !open;
  $("optsBtn").setAttribute("aria-expanded", String(open));
  renderOpts();
};
document.addEventListener("click", (e) => { if (!$("optsPanel").hidden && !$("optsPanel").contains(e.target)) closeOpts(); });

// avisos da área de transferência de arquivos (files.js tem o toast)
function onFileClip(id, kind, msg) {
  if (kind === "ready") toast("Arquivos do computador remoto", "Copiados. Cole com Ctrl+V.", "ok");
  else if (kind === "sent") toast("Arquivos para o computador remoto", "Enviados. Cole lá com Ctrl+V.", "ok");
  else toast("Área de transferência de arquivos", msg, "err");
}
