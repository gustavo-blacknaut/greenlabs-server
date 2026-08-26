#!/bin/bash
# Monta a linha de comando a partir das variáveis do painel.
#
# Existe porque o comando de inicialização do Pterodactyl não tem condicional:
# sem isto, ligar e desligar o SFU exigiria o usuário digitar "--sfu" numa
# caixa de texto, e um erro de digitação seria ignorado em silêncio pelo
# servidor — que é o pior tipo de falha, a que não avisa.
set -e

# No painel o diretório já é este, mas amarrar ao próprio script deixa o
# arquivo testável fora do container.
cd "$(dirname "$0")"

# PORTA em branco significa "usa a que o painel alocou". Escolher outra só
# funciona se ela também estiver nas alocações do servidor: o que não está
# alocado não é encaminhado até aqui, e o servidor sobe sem ninguém conseguir
# chegar nele.
PORTA_ESCOLHIDA="${PORTA//[^0-9]/}"
if [ -n "${PORTA_ESCOLHIDA}" ] && [ "${PORTA_ESCOLHIDA}" -ge 1 ] && [ "${PORTA_ESCOLHIDA}" -le 65535 ]; then
  PORTA_FINAL="${PORTA_ESCOLHIDA}"
  if [ -n "${SERVER_PORT}" ] && [ "${PORTA_FINAL}" != "${SERVER_PORT}" ]; then
    AVISO="porta ${PORTA_FINAL} escolhida na mão (a alocação principal é ${SERVER_PORT})"
  fi
else
  PORTA_FINAL="${SERVER_PORT:-25640}"
  [ -n "${PORTA}" ] && AVISO="PORTA='${PORTA}' não é um número válido; usando ${PORTA_FINAL}"
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
[ -n "${AVISO}" ] && echo "         ${AVISO}"
echo

exec ./greenlabs-server "${ARGUMENTOS[@]}"
