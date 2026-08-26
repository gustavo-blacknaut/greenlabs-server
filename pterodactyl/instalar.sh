#!/bin/bash
# Instalação do GreenLabs no Pterodactyl.
#
# Baixa o binário pronto da release direto para a máquina que vai hospedar.
# É um arquivo estático de ~10 MB: nada de Go, nada de compilar, nada de
# runtime instalado na host.
#
# Antes de aceitar o binário, pergunta a ele quais flags conhece. Isso existe
# porque o servidor ignora flag desconhecida em silêncio: uma release velha
# aceitaria --sfu, não ligaria nada e não daria erro nenhum. Quando o binário
# não passa nessa conferência, o script compila do fonte em vez de instalar um
# servidor que ignoraria a escolha feita no painel.
set -e

DONO="gustavo-blacknaut"
REPO="greenlabs-server"
ALVO="/mnt/server/greenlabs-server"

apt-get update -qq
apt-get install -y -qq curl jq ca-certificates git >/dev/null

case "$(uname -m)" in
  x86_64 | amd64) ARQUITETURA="amd64" ;;
  aarch64 | arm64) ARQUITETURA="arm64" ;;
  *) echo "arquitetura $(uname -m) sem binário pronto; vai compilar"; ARQUITETURA="" ;;
esac

# VERSAO em branco ou "latest" pega a release mais nova; qualquer outra coisa é
# tratada como tag.
VERSAO="${VERSAO:-latest}"
if [ "${VERSAO}" = "latest" ]; then
  API="https://api.github.com/repos/${DONO}/${REPO}/releases/latest"
else
  API="https://api.github.com/repos/${DONO}/${REPO}/releases/tags/${VERSAO}"
fi

baixou=0
if [ -n "${ARQUITETURA}" ]; then
  echo "Procurando release (${VERSAO}, linux-${ARQUITETURA})..."
  URL=$(curl -fsSL "${API}" 2>/dev/null \
        | jq -r --arg a "linux-${ARQUITETURA}" \
            '.assets[]? | select(.name | endswith($a)) | .browser_download_url' \
        | head -1)

  if [ -n "${URL}" ] && [ "${URL}" != "null" ]; then
    echo "Baixando ${URL}"
    curl -fsSL -o "${ALVO}" "${URL}" && chmod +x "${ALVO}" && baixou=1
  else
    echo "Nenhum binário linux-${ARQUITETURA} nessa release."
  fi
fi

# O binário baixado só vale se conhecer as flags que o painel vai passar.
if [ "${baixou}" = "1" ]; then
  AJUDA="$("${ALVO}" --help 2>&1 || true)"
  if echo "${AJUDA}" | grep -q -- "--sfu"; then
    echo "Binário da release serve: conhece --sfu."
  else
    echo "A release baixada e antiga demais - nao conhece --sfu."
    echo "Compilando do fonte para o servidor nao subir sem o modo que voce escolheu."
    baixou=0
  fi
fi

if [ "${baixou}" != "1" ]; then
  REF="${VERSAO}"
  [ "${REF}" = "latest" ] && REF="main"

  echo "Compilando a partir de ${REF}..."
  rm -rf /tmp/fonte
  git clone --depth 1 --branch "${REF}" "https://github.com/${DONO}/${REPO}.git" /tmp/fonte 2>/dev/null \
    || git clone --depth 1 "https://github.com/${DONO}/${REPO}.git" /tmp/fonte

  cd /tmp/fonte
  echo "Commit: $(git rev-parse --short HEAD)"

  # CGO desligado: o binário sai estático e roda em qualquer imagem, sem
  # depender das bibliotecas do sistema em que foi compilado.
  CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "${ALVO}" .
  chmod +x "${ALVO}"
fi

# O iniciar.sh vem sempre do fonte: é ele que traduz as variáveis do painel.
rm -rf /tmp/scripts
git clone --depth 1 "https://github.com/${DONO}/${REPO}.git" /tmp/scripts >/dev/null 2>&1
install -m 0755 /tmp/scripts/pterodactyl/iniciar.sh /mnt/server/iniciar.sh

echo
echo "Instalado:"
"${ALVO}" --help | head -1
