const $ = (id) => document.getElementById(id);
const fmtId = (s) => s.replace(/(\d{3})(?=\d)/g, "$1 "); // 123 456 789
const ask = () => window.go.main.AskApp;

window.addEventListener("load", async () => {
  const p = await ask().Params();
  $("askId").textContent = fmtId(p.id);
  if (p.name) { $("askName").textContent = p.name; $("rowName").hidden = false; }
  $("askSub").textContent = p.control ? "Alguém quer ver e controlar este computador" : "Alguém quer ver este computador";
  $("askCan").textContent = p.control ? "Ver a tela, usar mouse e teclado" : "Somente ver a tela";

  const end = Date.now() + p.seconds * 1000;
  const tick = () => {
    const left = Math.max(0, end - Date.now());
    $("askSec").textContent = Math.ceil(left / 1000) + " s";
    $("askBar").style.width = (left / (p.seconds * 10)) + "%";
    if (left === 0) ask().Answer(false); // sem resposta no prazo: recusa
  };
  tick();
  setInterval(tick, 250);
  $("deny").focus(); // o foco inicial fica em Recusar: Enter sem querer nunca aceita
});

// A janela pode surgir debaixo do cursor, no meio de um clique do usuário em outra coisa.
// Por isso os botões só funcionam depois de 800 ms: um clique sem querer não aceita acesso.
const readyAt = Date.now() + 800;
const answer = (ok) => { if (Date.now() >= readyAt) ask().Answer(ok); };
$("allow").onclick = () => answer(true);
$("deny").onclick = () => answer(false);
document.addEventListener("keydown", (e) => { if (e.key === "Escape") ask().Answer(false); });
