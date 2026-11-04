#!/bin/sh
# Instala o cdesk-server como serviço systemd. Uso: sudo ./install.sh /caminho/cdesk-server-linux-amd64 relay.exemplo.com
set -e
BIN=${1:?binário do servidor}; HOST=${2:?domínio ou IP público}
id cdesk >/dev/null 2>&1 || useradd --system --home /var/lib/cdesk --shell /usr/sbin/nologin cdesk
install -d /opt/cdesk && install -m 755 "$BIN" /opt/cdesk/cdesk-server
sed "s#relay.exemplo.com#$HOST#" "$(dirname "$0")/cdesk-server.service" > /etc/systemd/system/cdesk-server.service
systemctl daemon-reload && systemctl enable --now cdesk-server
echo "Aguarde 2 s e veja a senha inicial e o endereço para o app:"; sleep 2; journalctl -u cdesk-server -n 20 --no-pager
