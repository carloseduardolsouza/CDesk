# cdesk-server

Relay libp2p + diretório de IDs + painel web de limites para o CDesk. Módulo Go separado (não entra no build do app).

## Rodar

```sh
go build -o cdesk-server .
./cdesk-server -data /var/lib/cdesk -announce /dns4/relay.exemplo.com/tcp/4001
```

- Libere no firewall a porta **4001 tcp e udp** (`-port` muda).
- Painel: usuário `admin`, senha `admin123` (troque em **Conta** antes de expor o painel na internet).
- O log também mostra o endereço completo (`.../p2p/<PeerID>`) para colar no app em **Configurações > Rede > Relay**. O PeerID é estável enquanto `server.key` existir: faça backup dele.
- `server.db` (SQLite) guarda limites, bloqueios e uso.

## Painel

Por padrão escuta em `0.0.0.0:8443` (todas as interfaces) em HTTP puro. Libere a porta 8443 no firewall só para quem precisa. Para restringir ou proteger:

- Só local: `-admin 127.0.0.1:8443` e acesse por túnel SSH: `ssh -L 8443:127.0.0.1:8443 usuario@servidor` e abra `http://127.0.0.1:8443`.
- HTTPS direto: `-tls-cert cert.pem -tls-key key.pem`.
- Ou um proxy TLS (Caddy, nginx) na frente. Não exponha o painel em HTTP puro na internet.

Telas: **Ao vivo** (máquinas conectadas, banda, sessões, uso), **Limites** (padrão e por ID), **Bloqueios** (bloquear, ou permitir só IDs da lista), **Uso** (maiores consumidores) e **Conta**.

Limites por ID de máquina (0 = sem limite): banda MB/s, sessões simultâneas, franquia por dia e por mês. As mudanças valem na hora, sem reiniciar.

## Como funciona

- **Relay** sem corte de tempo/dados (diferente dos relays públicos do IPFS, que cortam em 2 min / 128 KB). O tráfego continua cifrado ponta a ponta: o servidor só vê bytes cifrados.
- **Diretório** (`/cdesk-dir/1.0.0`): o app registra seus endereços públicos e o viewer pergunta "onde está o ID X". Fica só em memória (TTL de 2 min). O ID vem do PeerID autenticado, então ninguém registra endereços de outro ID.
- **Fallback**: se o servidor estiver fora do ar, o app segue pela DHT, mDNS e hole punching.

## Limites conhecidos

- Banda: não há shaping. Quem passa do limite por 3 s seguidos é desconectado.
- A banda e a franquia contam entrada + saída de cada máquina; uma sessão cobra das duas pontas.
- O ID de 9 dígitos é derivado da chave; quem quiser burlar um bloqueio pode gerar outra chave. Para controle forte, use o modo "só permitidos".
- O filtro "só apps CDesk" usa o `UserAgent`; reduz abuso, não é segurança.

## Executáveis prontos

`dist/` (fora do git) tem os binários estáticos, sem dependências: `cdesk-server-linux-amd64`, `cdesk-server-linux-arm64` e `cdesk-server-windows-amd64.exe`. Para refazê-los:

```sh
CGO_ENABLED=0 GOOS=linux   GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/cdesk-server-linux-amd64 .
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o dist/cdesk-server-windows-amd64.exe .
```

## Instalar no Linux (systemd)

```sh
scp dist/cdesk-server-linux-amd64 deploy/cdesk-server.service deploy/install.sh usuario@servidor:
ssh usuario@servidor 'sudo ./install.sh cdesk-server-linux-amd64 relay.exemplo.com'
```

O script cria o usuário `cdesk`, instala em `/opt/cdesk`, grava os dados em `/var/lib/cdesk` e mostra no fim o endereço para colar no app (painel: `admin` / `admin123`). Libere a porta 4001 (tcp e udp). O painel fica em `http://IP_DO_SERVIDOR:8443`: libere a porta 8443 e troque a senha `admin123` em Conta. Para deixá-lo só local, adicione `-admin 127.0.0.1:8443` ao `ExecStart` e acesse por túnel SSH.

No Windows: `cdesk-server-windows-amd64.exe -data C:\cdesk -announce /ip4/SEU_IP/tcp/4001` e libere a porta 4001 no firewall.
