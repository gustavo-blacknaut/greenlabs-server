#!/bin/bash
# Instalação do GreenLabs no Pterodactyl.
#
# Compila do fonte de propósito. Baixar o binário da release parecia mais
# rápido, mas o servidor ignora flag que não conhece em silêncio: uma release
# antiga aceitaria --sfu, não ligaria nada, e não haveria erro nenhum para
# indicar isso. Compilando, o binário e as flags são sempre da mesma versão.
set -e

REPO="https://github.com/gustavo-blacknaut/greenlabs-server.git"
REF="${VERSAO:-main}"

echo "GreenLabs — instalando a partir de ${REF}"

apt-get update -qq
apt-get install -y -qq git ca-certificates >/dev/null

rm -rf /tmp/fonte
git clone --depth 1 --branch "${REF}" "${REPO}" /tmp/fonte 2>/dev/null \
  || git clone --depth 1 "${REPO}" /tmp/fonte

cd /tmp/fonte
echo "Commit: $(git rev-parse --short HEAD)"

# CGO desligado: o binário sai estático e roda em qualquer imagem, sem depender
# das bibliotecas do sistema em que foi compilado.
CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /mnt/server/greenlabs-server .

install -m 0755 pterodactyl/iniciar.sh /mnt/server/iniciar.sh
chmod +x /mnt/server/greenlabs-server

echo
echo "Pronto. Versão instalada:"
/mnt/server/greenlabs-server --help | head -1
