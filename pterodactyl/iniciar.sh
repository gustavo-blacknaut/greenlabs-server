#!/bin/bash
# Monta a linha de comando a partir das variáveis do painel.
#
# Existe porque o comando de inicialização do Pterodactyl não tem condicional:
# sem isto, ligar e desligar o SFU exigiria o usuário digitar "--sfu" numa
# caixa de texto, e um erro de digitação seria ignorado em silêncio pelo
# servidor — que é o pior tipo de falha, a que não avisa.
set -e

# No painel o diretorio ja e este, mas amarrar ao proprio script deixa o
# arquivo testavel fora do container.
cd "$(dirname "$0")"

ARGUMENTOS=(--port "${SERVER_PORT:-25640}")

case "${SFU,,}" in
  1 | true | sim | ligado)
    ARGUMENTOS+=(--sfu)
    MODO="SFU ligado — o vídeo passa por este servidor"
    ;;
  *)
    MODO="SFU desligado — o vídeo vai direto entre as pessoas"
    ;;
esac

echo "GreenLabs · porta ${SERVER_PORT:-25640} · ${MODO}"
echo

exec ./greenlabs-server "${ARGUMENTOS[@]}"
