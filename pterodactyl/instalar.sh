#!/bin/bash
# Instalação do GreenLabs no Pterodactyl.
#
# Baixa o binário pronto da release direto para a máquina que vai hospedar.
# É um arquivo estático de ~10 MB: nada de runtime instalado na host.
#
# Não usa a API do GitHub nem jq de propósito. A API tem limite de 60 chamadas
# por hora por IP quando não autenticada, e num host compartilhado esse limite
# é de outra pessoa também — a instalação falharia sem motivo aparente. O
# endereço /releases/latest/download/NOME redireciona sozinho para a release
# mais nova, sem chamada de API e sem depender de nenhum programa a mais.
set -euo pipefail

DONO="gustavo-blacknaut"
REPO="greenlabs-server"
ALVO="/mnt/server/greenlabs-server"

VERSAO="${VERSAO:-latest}"

passo() { echo; echo "==> $*"; }

passo "Ambiente"
echo "    arquitetura: $(uname -m)"
echo "    versao pedida: ${VERSAO}"

case "$(uname -m)" in
  x86_64 | amd64) ARQUIVO="greenlabs-server-linux-amd64" ;;
  aarch64 | arm64) ARQUIVO="greenlabs-server-linux-arm64" ;;
  *) ARQUIVO="" ;;
esac

if [ "${VERSAO}" = "latest" ] || [ -z "${VERSAO}" ]; then
  BASE="https://github.com/${DONO}/${REPO}/releases/latest/download"
else
  BASE="https://github.com/${DONO}/${REPO}/releases/download/${VERSAO}"
fi

baixou=0
if [ -n "${ARQUIVO}" ]; then
  passo "Baixando ${ARQUIVO}"
  echo "    ${BASE}/${ARQUIVO}"
  # -L segue o redirecionamento do /latest/ para a tag de verdade.
  if curl -fsSL --retry 3 --retry-delay 2 -o "${ALVO}" "${BASE}/${ARQUIVO}"; then
    chmod +x "${ALVO}"
    echo "    ok, $(stat -c%s "${ALVO}") bytes"
    baixou=1
  else
    echo "    nao deu para baixar"
  fi
else
  echo "    sem binario pronto para $(uname -m)"
fi

# O binário só vale se conhecer as flags que o painel vai passar. O servidor
# ignora flag desconhecida em silêncio: uma release velha aceitaria --sfu, não
# ligaria nada e não daria erro nenhum.
if [ "${baixou}" = "1" ]; then
  passo "Conferindo as flags"
  AJUDA="$("${ALVO}" --help 2>&1 || true)"
  if echo "${AJUDA}" | grep -q -- "--sfu"; then
    echo "    ok: conhece --sfu"
  else
    echo "    release antiga demais, nao conhece --sfu"
    baixou=0
  fi
fi

if [ "${baixou}" != "1" ]; then
  passo "Compilando do fonte"
  REF="${VERSAO}"
  { [ "${REF}" = "latest" ] || [ -z "${REF}" ]; } && REF="main"

  if ! command -v go >/dev/null 2>&1; then
    echo "ERRO: sem binario pronto e sem Go nesta imagem para compilar."
    echo "      Use a imagem de instalacao golang:1.24-bookworm."
    exit 1
  fi

  rm -rf /tmp/fonte
  git clone --depth 1 --branch "${REF}" "https://github.com/${DONO}/${REPO}.git" /tmp/fonte 2>/dev/null \
    || git clone --depth 1 "https://github.com/${DONO}/${REPO}.git" /tmp/fonte

  cd /tmp/fonte
  echo "    commit $(git rev-parse --short HEAD)"

  # CGO desligado: binário estático, roda em qualquer imagem sem depender das
  # bibliotecas de onde foi compilado.
  CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o "${ALVO}" .
  chmod +x "${ALVO}"
fi

# O iniciar.sh traduz as variáveis do painel em linha de comando. Vem embutido
# aqui, e não clonado, para a instalação não depender do repositório uma
# segunda vez depois de já ter o binário.
passo "Instalando o iniciador"
cat > /mnt/server/iniciar.sh <<'INICIADOR'
#!/bin/bash
set -e
cd "$(dirname "$0")"

# Sem o binário, o bash responde só "No such file or directory" e quem lê acha
# que o iniciar.sh é que sumiu — ele existe, quem falta é o servidor. Isso
# acontece quando a instalação não terminou: reiniciar não resolve, tem que
# reinstalar, porque é a instalação que baixa o arquivo.
if [ ! -f ./greenlabs-server ]; then
  echo
  echo "  O servidor nao esta instalado nesta pasta."
  echo
  echo "  O arquivo greenlabs-server nao existe em $(pwd)."
  echo "  Isso quer dizer que a instalacao nao terminou."
  echo
  echo "  Va em Settings -> Reinstall Server no painel. Reiniciar nao resolve:"
  echo "  e a instalacao que baixa o binario."
  echo
  exit 1
fi

if [ ! -x ./greenlabs-server ]; then
  echo "  Sem permissao de execucao no greenlabs-server; ajustando."
  chmod +x ./greenlabs-server || true
fi

PORTA_ESCOLHIDA="${PORTA//[^0-9]/}"
if [ -n "${PORTA_ESCOLHIDA}" ] && [ "${PORTA_ESCOLHIDA}" -ge 1 ] && [ "${PORTA_ESCOLHIDA}" -le 65535 ]; then
  PORTA_FINAL="${PORTA_ESCOLHIDA}"
else
  PORTA_FINAL="${SERVER_PORT:-25640}"
  [ -n "${PORTA:-}" ] && AVISO="PORTA='${PORTA}' não é um número válido; usando ${PORTA_FINAL}"
fi

ARGUMENTOS=(--port "${PORTA_FINAL}")

case "${SFU,,}" in
  1 | true | sim | ligado)
    ARGUMENTOS+=(--sfu)
    MODO="SFU ligado — o vídeo passa por este servidor"
    ;;
  *)
    MODO="SFU desligado — o vídeo vai direto entre as pessoas"
    ;;
esac

echo "GreenLabs · porta ${PORTA_FINAL} · ${MODO}"
[ -n "${AVISO:-}" ] && echo "         ${AVISO}"
echo

exec ./greenlabs-server "${ARGUMENTOS[@]}"
INICIADOR
chmod +x /mnt/server/iniciar.sh

passo "Pronto"
"${ALVO}" --help | head -1
ls -la /mnt/server/greenlabs-server /mnt/server/iniciar.sh
