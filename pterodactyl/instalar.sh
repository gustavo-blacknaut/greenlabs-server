#!/bin/bash
# Instalação do GreenLabs no Pterodactyl.
#
# Só adianta o download para o primeiro boot não esperar. Se falhar aqui, o
# comando de inicialização baixa sozinho — por isso este script não aborta a
# instalação por nada: um egg cuja instalação falha deixa o servidor inutilizado
# no painel, e aqui não há nada que justifique isso.
#
# Não usa a API do GitHub nem jq de propósito. A API tem limite de 60 chamadas
# por hora por IP quando não autenticada, e num host compartilhado esse limite
# é dividido com estranhos. O endereço /releases/latest/download/NOME
# redireciona sozinho para a release mais nova, sem chamada de API.

case "$(uname -m)" in
  aarch64 | arm64) ARQUIVO="greenlabs-server-linux-arm64" ;;
  *) ARQUIVO="greenlabs-server-linux-amd64" ;;
esac

VERSAO="${VERSAO:-latest}"
if [ "${VERSAO}" = "latest" ] || [ -z "${VERSAO}" ]; then
  BASE="https://github.com/gustavo-blacknaut/greenlabs-server/releases/latest/download"
else
  BASE="https://github.com/gustavo-blacknaut/greenlabs-server/releases/download/${VERSAO}"
fi

echo "GreenLabs — baixando ${ARQUIVO} (${VERSAO})"
echo "  ${BASE}/${ARQUIVO}"

# curl ou wget: qual existe varia com a imagem de instalação, e depender de um
# só faz a instalação morrer numa linha que não explica nada.
if command -v curl >/dev/null 2>&1; then
  curl -fsSL --retry 3 --retry-delay 2 -o /mnt/server/greenlabs-server "${BASE}/${ARQUIVO}"
elif command -v wget >/dev/null 2>&1; then
  wget -q --tries=3 -O /mnt/server/greenlabs-server "${BASE}/${ARQUIVO}"
else
  echo "  nem curl nem wget nesta imagem"
fi

if [ -s /mnt/server/greenlabs-server ]; then
  chmod +x /mnt/server/greenlabs-server
  echo "  ok, $(stat -c%s /mnt/server/greenlabs-server) bytes"
  /mnt/server/greenlabs-server --help 2>/dev/null | head -1
else
  # Arquivo vazio atrapalha mais que arquivo nenhum: o startup veria que existe
  # e não baixaria de novo.
  rm -f /mnt/server/greenlabs-server
  echo "  nao deu para baixar agora; o servidor baixa sozinho ao ligar"
fi

echo "GreenLabs — instalacao concluida"
exit 0
