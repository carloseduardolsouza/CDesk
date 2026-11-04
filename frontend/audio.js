// Áudio do computador remoto: o host manda PCM de 16 bits, estéreo, a 24 kHz (audio_windows.go);
// aqui o que chegou é buscado em /audio/<id> a cada 100 ms e tocado em sequência pelo Web Audio.
// shortcut: sem compressão e com ~120 ms de atraso fixo; subir para Opus se a rede apertar.

const AUDIO_RATE = 24000;
const audios = new Map(); // ID da sessão -> { ctx, timer, next }

function audioStart(id) {
  if (audios.has(id)) return;
  const st = { ctx: new AudioContext({ sampleRate: AUDIO_RATE }), timer: 0, next: 0 };
  st.timer = setInterval(async () => {
    try {
      const r = await fetch(`/audio/${id}`, { cache: "no-store" });
      if (!r.ok) return;
      const b = await r.arrayBuffer();
      if (b.byteLength >= 4) audioPlay(st, b);
    } catch { /* a próxima busca tenta de novo */ }
  }, 100);
  audios.set(id, st);
}

function audioPlay(st, b) {
  const pcm = new Int16Array(b, 0, Math.floor(b.byteLength / 4) * 2);
  const frames = pcm.length / 2;
  const buf = st.ctx.createBuffer(2, frames, AUDIO_RATE);
  const l = buf.getChannelData(0), r = buf.getChannelData(1);
  for (let i = 0; i < frames; i++) { l[i] = pcm[2 * i] / 32768; r[i] = pcm[2 * i + 1] / 32768; }
  const src = st.ctx.createBufferSource();
  src.buffer = buf;
  src.connect(st.ctx.destination);
  const now = st.ctx.currentTime;
  if (st.next < now + 0.03) st.next = now + 0.12; // ficou sem som (ou é o começo): recomeça com um pouco de folga
  src.start(st.next);
  st.next += buf.duration;
}

function audioStop(id) {
  const st = audios.get(id);
  if (!st) return;
  clearInterval(st.timer);
  st.ctx.close();
  audios.delete(id);
}
