// Telas de Configurações (Geral, Segurança, Rede, Exibição, Sobre). Usa os utilitários do app.js.
// Cada alteração é enviada ao Go e salva sozinha; só a senha de acesso tem botão de salvar.

let cfg = null, hasPw = false, section = "geral", saveTimer = 0, flashTimer = 0;

Object.assign(ICONS, {
  shield: '<path d="M12 3l8 3v6c0 5-3.5 8-8 9-4.5-1-8-4-8-9V6z"/>',
  wifi: '<path d="M5 12.5a10 10 0 0 1 14 0M8.5 16a5 5 0 0 1 7 0M2 9a15 15 0 0 1 20 0M12 20h.01"/>',
  screen: '<path d="M2 5h20v12H2zM8 21h8M12 17v4"/>',
  info: '<path d="M12 22a10 10 0 1 0 0-20 10 10 0 0 0 0 20zM12 16v-4M12 8h.01"/>',
  trash: '<path d="M3 6h18M8 6V4h8v2M6 6l1 14h10l1-14"/>',
  warn: '<path d="M12 3l10 18H2z"/><path d="M12 10v4M12 18h.01"/>',
});
const SECTIONS = [["geral", "Geral", "gear"], ["seguranca", "Segurança", "shield"], ["rede", "Rede", "wifi"], ["exibicao", "Exibição", "screen"], ["sobre", "Sobre", "info"]];
const SAVED_NOTE = "As alterações são salvas automaticamente.";

// símbolo da logo do CDesk (monitor em "C" com cursor), para fundo escuro
const logo = (size) => {
  const s = document.createElement("span");
  s.style.display = "inline-grid";
  s.innerHTML = `<svg width="${size}" height="${size}" viewBox="0 0 64 64"><path d="M48 12H19a9 9 0 0 0-9 9v16a9 9 0 0 0 9 9h29" fill="none" stroke="#3B82F6" stroke-width="8" stroke-linecap="round" stroke-linejoin="round"/><path d="M23 56h16" fill="none" stroke="#3B82F6" stroke-width="6" stroke-linecap="round"/><path d="M33 19L49 28.5L42 30.5L46.5 38.5L43 40.3L38.6 32.4L33 36.5Z" fill="#fff" stroke="#fff" stroke-width="1.5" stroke-linejoin="round"/></svg>`;
  return s;
};

async function loadSettings() {
  cfg = await app().GetSettings();
  hasPw = !!(await app().Info()).hasPw;
}

function openSettings(sec) {
  if (sec) section = sec;
  settingsOpen = true;
  show("settings");
  renderSettings();
}

// ---------- salvar ----------

const flash = (text, isError) => {
  const f = $("sfoot");
  if (!f) return;
  f.textContent = text;
  f.classList.toggle("error", !!isError);
  clearTimeout(flashTimer);
  flashTimer = setTimeout(() => { const g = $("sfoot"); if (g) { g.textContent = SAVED_NOTE; g.classList.remove("error"); } }, 2500);
};

const save = () => {
  clearTimeout(saveTimer);
  saveTimer = setTimeout(async () => {
    try {
      await app().SaveSettings(cfg);
      flash("Alterações salvas.");
    } catch (err) { // valor recusado: volta ao que está salvo
      cfg = await app().GetSettings();
      renderSettings();
      flash(errText(err), true);
    }
  }, 250);
};
const set = (key, value) => { cfg[key] = value; save(); };

// ---------- peças ----------

const shead = (title, sub) => h("div", { class: "shead" }, h("h1", {}, title), ...(sub ? [h("p", {}, sub)] : []));
const panel = (title, ...rows) => h("section", { class: "scard" }, ...(title ? [h("h2", {}, title)] : []), ...rows);
const srow = (title, desc, control, forId) => h("div", { class: "srow" },
  h("div", { class: "stext" }, h(forId ? "label" : "span", forId ? { for: forId } : {}, title), ...(desc ? [h("span", { class: "desc" }, desc)] : [])),
  control);
const btn = (text, cls, onclick) => h("button", { class: "sbtn " + cls, onclick }, text);

// o estado fica no próprio atributo, então dá para mudá-lo por fora (ex.: valor lido do Windows)
const toggle = (on, label, onchange) => {
  const b = h("button", { class: "switch", role: "switch", "aria-checked": String(on), "aria-label": label });
  b.onclick = () => {
    const next = b.getAttribute("aria-checked") !== "true";
    b.setAttribute("aria-checked", String(next));
    onchange(next);
  };
  return b;
};

const select = (id, options, value, onchange) => {
  const el = h("select", { class: "sinput", id }, ...options.map(([v, label]) => h("option", { value: String(v) }, label)));
  el.value = String(value);
  el.onchange = () => onchange(el.value);
  return el;
};

// ---------- seções ----------

function secGeral() {
  const upd = h("span", { class: "desc", role: "status" }, "Versão atual: ...");
  app().Version().then((v) => { upd.textContent = `Versão atual: ${v}`; });
  const checkNow = async () => {
    upd.textContent = "Verificando...";
    try {
      const u = await app().CheckUpdate();
      upd.textContent = u.newer ? `Versão ${u.latest} disponível: ${u.url}` : `Você está na versão mais recente (${u.current}).`;
    } catch (e) { upd.textContent = errText(e); }
  };
  const name = h("input", { class: "sinput", id: "nome-pc", value: cfg.name, placeholder: "Meu computador", maxlength: "40" });
  name.onchange = () => set("name", name.value.trim());

  const copy = btn("Copiar", "", async () => {
    await window.runtime.ClipboardSetText(fmtId(myId));
    copy.textContent = "Copiado";
    setTimeout(() => { copy.textContent = "Copiar"; }, 1500);
  });

  const lang = select("idioma", [["pt-BR", "Português (Brasil)"]], "pt-BR", () => {});
  lang.disabled = true;

  const boot = toggle(false, "Iniciar com o Windows", async (on) => {
    try { await app().SetStartup(on); flash(on ? "O CDesk vai iniciar com o Windows." : "O CDesk não inicia mais com o Windows."); }
    catch (err) { boot.setAttribute("aria-checked", String(!on)); flash(errText(err), true); }
  });
  app().StartupEnabled().then((v) => boot.setAttribute("aria-checked", String(v)));

  const path = h("span", { class: "desc mono" }, "...");
  app().ConfigDir().then((p) => { path.textContent = p; });

  let armed = false;
  const clear = btn("Limpar histórico", "danger", () => {
    if (!armed) {
      armed = true;
      clear.textContent = "Clique de novo para apagar";
      setTimeout(() => { armed = false; clear.textContent = "Limpar histórico"; }, 3000);
      return;
    }
    armed = false;
    clear.textContent = "Limpar histórico";
    clearHistory();
    flash("Histórico apagado.");
  });

  return [
    shead("Geral", "Como este computador aparece e como o CDesk inicia."),
    panel("Este computador",
      srow("Nome deste computador", "Aparece para quem recebe o seu pedido de acesso", name, "nome-pc"),
      srow("ID", "", h("span", { class: "inline" }, h("span", { class: "mono id" }, fmtId(myId)), copy)),
      srow("Idioma", "Outros idiomas chegam numa próxima versão", lang, "idioma")),
    panel("Inicialização",
      srow("Iniciar com o Windows", "Para receber conexões assim que o computador ligar", boot),
      srow("Minimizar para a bandeja ao fechar", "O CDesk continua rodando perto do relógio e recebendo conexões; para sair de vez, use o ícone da bandeja",
        toggle(cfg.trayOnClose, "Minimizar para a bandeja ao fechar", (on) => set("trayOnClose", on))),
      srow("Verificar atualizações ao abrir", "Consulta o GitHub por uma versão nova do CDesk; só avisa, não instala nada",
        toggle(cfg.checkUpdates, "Verificar atualizações ao abrir", (on) => set("checkUpdates", on))),
      srow("Versão", upd, h("span", { class: "inline" }, btn("Verificar agora", "", checkNow)))),
    panel("Dados",
      h("div", { class: "srow" }, h("div", { class: "stext" }, h("span", {}, "Pasta de configuração"), path), btn("Abrir pasta", "", () => app().OpenConfigDir())),
      srow("Histórico de sessões", "Apaga a lista de recentes e as miniaturas das telas", clear)),
  ];
}

function secSeguranca() {
  const tempo = select("tempo", [[15, "15 segundos"], [30, "30 segundos"], [60, "60 segundos"]], cfg.askSeconds, (v) => set("askSeconds", Number(v)));

  const ctl = h("input", { type: "checkbox", id: "perm-ctl" });
  ctl.checked = cfg.allowControl;
  ctl.onchange = () => set("allowControl", ctl.checked);
  const perm = (control, text, badge) => h("label", { class: "perm" }, control, text, ...(badge ? [badge] : []));
  const seeOnly = h("input", { type: "checkbox", checked: "", disabled: "" });
  const clip = h("input", { type: "checkbox", id: "perm-clip" });
  clip.checked = cfg.allowClipboard;
  clip.onchange = () => set("allowClipboard", clip.checked);
  const files = h("input", { type: "checkbox", id: "perm-files" });
  files.checked = cfg.allowFiles;
  files.onchange = () => set("allowFiles", files.checked);

  // senha de acesso não supervisionado
  const pill = h("span", { class: "pill" });
  const p1 = h("input", { class: "sinput full", id: "s1", type: "password", placeholder: "Mínimo de 6 caracteres", autocomplete: "new-password" });
  const p2 = h("input", { class: "sinput full", id: "s2", type: "password", autocomplete: "new-password" });
  const pwMsg = h("p", { class: "pwmsg", role: "status" });
  const removePw = btn("Remover senha", "danger", async () => {
    await app().SetPassword("");
    hasPw = false; refreshPw(); say2("Senha removida.");
  });
  const say2 = (t, isError) => { pwMsg.textContent = t; pwMsg.classList.toggle("error", !!isError); };
  const refreshPw = () => {
    pill.textContent = hasPw ? "Ativado" : "Desativado";
    pill.className = "pill " + (hasPw ? "ok" : "soon");
    removePw.disabled = !hasPw;
  };
  refreshPw();
  const savePw = btn("Salvar senha", "primary", async () => {
    if (p1.value !== p2.value) { say2("As senhas não são iguais.", true); return; }
    if (!p1.value) { say2("Digite a nova senha.", true); return; }
    try {
      await app().SetPassword(p1.value);
      p1.value = p2.value = "";
      hasPw = true; refreshPw(); say2("Senha salva.");
    } catch (err) { say2(errText(err), true); }
  });
  const warn = h("div", { class: "warn" }, icon("warn", 18),
    h("span", {}, "Use uma senha que você não usa em nenhum outro lugar. Nesta versão a senha viaja até o outro computador durante a conexão."));

  // só computadores permitidos
  const only = toggle(cfg.onlyAllowed, "Só computadores permitidos", (on) => set("onlyAllowed", on));
  const list = cfg.allowed.map((p) => h("div", { class: "srow allowed" },
    h("span", { class: "grow" }, p.name || fmtId(p.id)),
    h("span", { class: "mono desc" }, fmtId(p.id)),
    h("button", {
      class: "icon-btn", "aria-label": "Remover " + (p.name || fmtId(p.id)),
      onclick: () => { cfg.allowed = cfg.allowed.filter((x) => x.id !== p.id); save(); renderSettings(); },
    }, icon("trash", 15))));
  const newId = h("input", { class: "sinput mono grow", id: "novo-id", inputmode: "numeric", placeholder: "ID do computador", "aria-label": "ID para permitir" });
  const add = () => {
    const id = newId.value.replace(/\D/g, "");
    if (id.length !== 9) { flash("O ID tem 9 números.", true); return; }
    if (cfg.allowed.some((p) => p.id === id)) { flash("Esse ID já está na lista.", true); return; }
    cfg.allowed.push({ name: hasMachineName(id) ? nameOf(id) : "", id });
    save();
    renderSettings();
  };
  newId.onkeydown = (e) => { if (e.key === "Enter") add(); };

  return [
    shead("Segurança", "Quem pode pedir acesso, o que pode fazer e como entrar sem ninguém aceitar."),
    panel("Pedidos de acesso",
      srow("Recusar automaticamente após", "", tempo, "tempo"),
      srow("Trazer o aviso para a frente", "A janela do pedido fica sobre as outras, mesmo com o CDesk minimizado",
        toggle(cfg.askOnTop, "Trazer o aviso para a frente", (on) => set("askOnTop", on))),
      srow("Tocar som ao receber pedido", "Um toque curto quando alguém pede acesso",
        toggle(cfg.askSound, "Tocar som ao receber pedido", (on) => set("askSound", on)))),
    panel(null,
      h("div", { class: "scardhead" }, h("h2", {}, "Permissões padrão"), h("span", { class: "desc" }, "O que quem se conecta pode fazer. Sem controle, a pessoa só vê a tela.")),
      perm(seeOnly, "Ver a tela", h("span", { class: "desc right" }, "Sempre")),
      perm(ctl, "Controlar mouse e teclado"),
      perm(clip, "Copiar e colar texto entre os computadores", h("span", { class: "desc right" }, "Precisa do controle ligado")),
      perm(files, "Transferir arquivos", h("span", { class: "desc right" }, "Precisa do controle ligado"))),
    panel(null,
      h("div", { class: "scardhead row" },
        h("div", {}, h("h2", {}, "Acesso não supervisionado"), h("span", { class: "desc" }, "Com a senha certa, quem tem o ID entra sem ninguém aceitar.")), pill),
      h("div", { class: "fields" },
        h("div", { class: "field" }, h("label", { for: "s1" }, "Nova senha"), p1),
        h("div", { class: "field" }, h("label", { for: "s2" }, "Repita a senha"), p2)),
      h("div", { class: "btnrow" }, savePw, removePw),
      pwMsg, warn),
    panel(null,
      srow("Só computadores permitidos", "Desligado: qualquer ID pode pedir acesso. Ligado: só os IDs da lista abaixo.", only),
      ...list,
      h("div", { class: "srow addrow" }, newId, btn("Adicionar", "", add))),
  ];
}

// atualizada a cada 3 s pelo app.js enquanto a tela de Rede está aberta
function updateNetStatus() {
  const set3 = (id, text, kind) => {
    const el = $(id);
    if (!el) return;
    el.textContent = text;
    el.className = "val " + kind;
  };
  if (!cfg) return;
  set3("netP2P", net.online ? "Conectado" : "Conectando...", net.online ? "ok" : "warn");
  set3("netLocal", cfg.mdns ? "Ativa" : "Desativada", cfg.mdns ? "ok" : "off");
  set3("netKnown", net.known === 1 ? "1 computador" : `${net.known} computadores`, net.known > 0 ? "ok" : "off");
  const srv = { off: ["Desativado (só P2P)", "off"], none: ["Endereço do servidor inválido", "warn"], down: ["Fora do ar, usando P2P", "warn"], up: ["Conectado", "ok"] }[net.server] || ["...", "off"];
  set3("netServer", srv[0], srv[1]);
}

function secRede() {
  const row = (label, id) => h("div", { class: "srow status" }, h("span", {}, label), h("span", { id, class: "val" }));
  const port = h("input", { class: "sinput mono", id: "porta", inputmode: "numeric", placeholder: "Automática", value: cfg.port || "" });
  port.onchange = () => {
    const v = port.value.trim();
    set("port", v === "" ? 0 : Number(v.replace(/\D/g, "")));
  };
  const relay = h("input", { class: "sinput mono full", id: "relay", value: cfg.relay || "", placeholder: "Vazio: usa o servidor padrão do CDesk", spellcheck: "false" });
  relay.onchange = () => set("relay", relay.value.trim());
  const mode = h("div", { class: "seg", role: "radiogroup", "aria-label": "Como conectar" },
    ...[[true, "Servidor CDesk"], [false, "Somente P2P"]].map(([v, label]) => h("button", {
      role: "radio", "aria-checked": String(!!cfg.useServer === v),
      onclick: () => { set("useServer", v); renderSettings(); },
    }, label)));
  relay.disabled = !cfg.useServer;
  const sec = [
    shead("Rede", "Como este computador encontra os outros e abre a conexão direta. As mudanças desta tela valem depois de reiniciar o CDesk."),
    panel("Status",
      row("Rede P2P", "netP2P"),
      row("Servidor CDesk", "netServer"),
      row("Descoberta na rede local", "netLocal"),
      row("Computadores que a rede já conhece", "netKnown")),
    panel("Conexão",
      srow("Como conectar", "Servidor CDesk: tenta o servidor primeiro, que conecta mais rápido e funciona mesmo atrás de CGNAT; se ele estiver fora do ar, o CDesk usa P2P sozinho. Somente P2P: nunca usa o servidor.", mode)),
    panel("Descoberta e NAT",
      srow("Encontrar computadores na rede local", "Usa mDNS para achar outros CDesk sem passar pela internet",
        toggle(cfg.mdns, "Encontrar computadores na rede local", (on) => { set("mdns", on); updateNetStatus(); })),
      srow("Abrir portas automaticamente (UPnP)", "Ajuda a receber conexões atrás do roteador",
        toggle(cfg.upnp, "Abrir portas automaticamente (UPnP)", (on) => set("upnp", on))),
      srow("Atravessar NAT (hole punching)", "Tenta abrir a conexão direta mesmo com os dois atrás de roteadores",
        toggle(cfg.holePunch, "Atravessar NAT (hole punching)", (on) => set("holePunch", on))),
      srow("Porta de escuta", "Deixe vazio para escolher automaticamente (1024 a 65535)", port, "porta")),
    panel(null,
      h("div", { class: "scardhead row" },
        h("div", {}, h("h2", {}, "Servidor personalizado (opcional)"), h("span", { class: "desc" }, "Deixe vazio para usar o servidor padrão do CDesk. Só preencha se você mantém um servidor próprio (endereço completo, com /p2p/ID).")), h("span", { class: "pill soon" }, "Vale após reiniciar")),
      h("div", { class: "fields single" }, h("label", { for: "relay", class: "desc" }, "Endereço do servidor personalizado"), relay)),
  ];
  setTimeout(updateNetStatus);
  return sec;
}

function secExibicao() {
  const prios = [["velocidade", "Velocidade"], ["equilibrada", "Equilibrada"], ["qualidade", "Qualidade"]];
  const seg = h("div", { class: "seg", role: "radiogroup", "aria-labelledby": "qual" },
    ...prios.map(([v, label]) => h("button", {
      role: "radio", "aria-checked": String(cfg.priority === v),
      onclick: () => { set("priority", v); renderSettings(); },
    }, label)));

  const fpsVal = h("span", { class: "mono fpsval" }, `${cfg.fps} qps`);
  const fps = h("input", { type: "range", id: "fps", min: "5", max: "30", value: String(cfg.fps), class: "range" });
  fps.oninput = () => { fpsVal.textContent = `${fps.value} qps`; };
  fps.onchange = () => set("fps", Number(fps.value));

  return [
    shead("Exibição", "Qualidade da imagem e comportamento da tela durante a sessão."),
    panel("Imagem",
      srow("Prioridade", "Velocidade usa menos internet; Qualidade deixa o texto mais nítido", seg),
      srow("Quadros por segundo", "Limite de atualizações da tela por segundo", h("span", { class: "inline" }, fps, fpsVal), "fps"),
      srow("Monitor mostrado", "Se o outro computador tiver mais de um monitor, escolha qual ver pelos botões numerados no topo da sessão",
        h("span", { class: "desc" }, "Na sessão"))),
    panel("Durante a sessão",
      srow("Mostrar o cursor do computador remoto", "Útil quando alguém do outro lado também mexe no mouse. Vale para as próximas sessões; dá para mudar na janela Exibição",
        toggle(cfg.showCursor, "Mostrar o cursor do computador remoto", (on) => set("showCursor", on))),
      srow("Ocultar o papel de parede remoto", "Deixa a imagem mais leve em conexões lentas; o papel de parede volta quando a sessão acaba",
        toggle(cfg.hideWallpaper, "Ocultar o papel de parede remoto", (on) => set("hideWallpaper", on)))),
  ];
}

function secSobre() {
  const ver = h("span", { class: "desc" }, "Versão ... · Windows");
  app().Version().then((v) => { ver.textContent = `Versão ${v} · Windows`; });
  return [
    shead("Sobre"),
    h("section", { class: "scard about" },
      h("div", { class: "aboutHead" },
        h("div", { class: "logo", "aria-hidden": "true" }, logo(34)),
        h("div", { class: "stext" }, h("span", { class: "appname" }, "CDesk"), ver)),
      h("p", {}, "Controle remoto sem servidor central. Os computadores se encontram pela rede P2P e conversam por uma conexão direta e criptografada.")),
  ];
}

const RENDER = { geral: secGeral, seguranca: secSeguranca, rede: secRede, exibicao: secExibicao, sobre: secSobre };

function renderSettings() {
  if (!cfg) return;
  const nav = h("nav", { class: "snav", "aria-label": "Seções das configurações" },
    h("h2", {}, "Configurações"),
    ...SECTIONS.map(([key, label, ic]) => h("button", {
      ...(key === section ? { "aria-current": "page" } : {}),
      onclick: () => { section = key; renderSettings(); },
    }, icon(ic, 18), label)));
  const top = $("settings").scrollTop;
  $("settings").replaceChildren(nav,
    h("main", { class: "smain" }, h("div", { class: "sinner" }, ...RENDER[section](), h("p", { id: "sfoot", class: "sfoot" }, SAVED_NOTE))));
  $("settings").scrollTop = top;
}
