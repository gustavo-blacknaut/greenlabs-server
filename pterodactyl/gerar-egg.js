// Gera o egg do Pterodactyl a partir dos scripts ao lado.
//
// O egg é um JSON com shell script embutido como string. Manter os dois juntos
// num arquivo só significa editar bash escapado à mão, então os scripts ficam
// soltos, com destaque de sintaxe e possibilidade de rodar, e este arquivo os
// costura na hora.
//
//   node pterodactyl/gerar-egg.js
const fs = require('fs');
const path = require('path');

const aqui = __dirname;
const ler = (nome) => fs.readFileSync(path.join(aqui, nome), 'utf8').replace(/\r\n/g, '\n');

const egg = {
  _comment: 'GreenLabs - servidor de sinalizacao. https://github.com/gustavo-blacknaut/greenlabs-server',
  meta: { version: 'PTDL_v2', update_url: null },
  exported_at: new Date().toISOString(),
  name: 'GreenLabs — Servidor de Sinalização',
  author: 'gustavo.c.raichardt@gmail.com',
  description:
    'Servidor de sinalização do GreenLabs, para transmissão de tela e chamadas sem conta. ' +
    'Com o SFU ligado o vídeo passa por aqui e a travessia de roteador deixa de ser problema; ' +
    'desligado, o servidor só apresenta as pessoas umas às outras e gasta quase nada.',
  features: null,
  docker_images: {
    'Debian 12': 'ghcr.io/parkervcp/yolks:debian',
  },
  file_denylist: [],
  // Tudo num comando só, do jeito que os eggs de Node fazem: o servidor se
  // baixa se não estiver lá e sobe. Assim uma instalação que falhou não deixa
  // o servidor inutilizado — ele se resolve no boot.
  //
  // Ao contrário do Node, aqui não há nada para compilar na host: o Go entrega
  // um binário estático, completo. O npm install existe porque node_modules é
  // específico da máquina; o equivalente aqui simplesmente não existe.
  startup: [
    // Reaproveita o que já está na pasta. A checagem não é "o arquivo existe" e
    // sim "o arquivo roda": um download interrompido deixa um arquivo com
    // tamanho, que passaria num teste de existência e quebraria ao subir.
    'chmod +x greenlabs-server 2>/dev/null',
    'if [ -s greenlabs-server ] && bash -c "./greenlabs-server --help" >/dev/null 2>&1; then echo "GreenLabs pronto, reaproveitando o que ja esta aqui"; else ' +
      'A=amd64; case $(uname -m) in aarch64|arm64) A=arm64;; esac; ' +
      'R=https://github.com/gustavo-blacknaut/greenlabs-server/releases; ' +
      'N=greenlabs-server-linux-$A; ' +
      // Só tag de release serve aqui, e toda tag começa com "v". Nome de branch
      // ("main") virava /releases/download/main/... e voltava 404 - foi o que
      // aconteceu com quem tinha a variável do egg antigo, de quando ele
      // compilava do fonte e "main" fazia sentido.
      'V={{VERSAO}}; ' +
      'case "$V" in v*) U=$R/download/$V/$N;; *) U=$R/latest/download/$N;; esac; ' +
      'echo "GreenLabs baixando $N..."; rm -f greenlabs-server; ' +
      'B() { curl -fsSL -o greenlabs-server "$1" 2>/dev/null || wget -qO greenlabs-server "$1" 2>/dev/null; }; ' +
      // Segunda tentativa na mais recente: cobre tag apagada, tag digitada
      // errada e qualquer outro valor que não exista mais.
      'B "$U" || { echo "  nao achei nessa versao; tentando a mais recente"; B "$R/latest/download/$N"; } || { echo "ERRO: nao consegui baixar; confira a internet do host"; exit 1; }; ' +
      'chmod +x greenlabs-server; ' +
      'bash -c "./greenlabs-server --help" >/dev/null 2>&1 || { echo "ERRO: o arquivo baixado nao executa nesta maquina"; exit 1; }; ' +
      'echo "GreenLabs instalado"; fi',
    'P={{PORTA}}',
    '[ -z "$P" ] && P={{SERVER_PORT}}',
    'F=',
    '[ "{{SFU}}" = 1 ] && F=--sfu',
    'exec ./greenlabs-server --port $P $F',
  ].join('; '),
  config: {
    files: '{}',
    startup: JSON.stringify({ done: 'Servidor de Sinalizacao GreenLabs rodando' }),
    logs: '{}',
    // O servidor trata SIGINT e SIGTERM e fecha as conexões antes de sair.
    stop: '^C',
  },
  scripts: {
    installation: {
      script: ler('instalar.sh'),
      // Só precisa de curl ou wget: não há nada para compilar.
      container: 'ghcr.io/parkervcp/installers:debian',
      entrypoint: 'bash',
    },
  },
  variables: [
    {
      name: 'Retransmitir o vídeo pelo servidor (SFU)',
      description:
        'Ligado (1): cada pessoa mantém uma conexão só, com este servidor, que recebe o vídeo ' +
        'uma vez e reenvia para os outros. Quem transmite para de subir o vídeo várias vezes e ' +
        'ninguém precisa atravessar o roteador do outro. Custa banda e CPU daqui.\n\n' +
        'Desligado (0): o servidor só apresenta as pessoas e sai da frente. O vídeo vai direto ' +
        'entre elas e este servidor gasta quase nada — mas se a conexão direta falhar, todo ' +
        'mundo aparece na lista e a tela fica preta, sem mensagem de erro.\n\n' +
        'Hospedando num painel, deixe ligado.',
      env_variable: 'SFU',
      default_value: '1',
      user_viewable: true,
      user_editable: true,
      rules: 'required|string|in:0,1',
      field_type: 'text',
    },
    {
      name: 'Porta',
      description:
        'Deixe em branco para usar a porta que o painel alocou — é o caso normal.\n\n' +
        'Preencha só se quiser outra. Ela precisa estar liberada para este servidor, senão ' +
        'ele sobe e ninguém consegue chegar nele.',
      env_variable: 'PORTA',
      default_value: '',
      user_viewable: true,
      user_editable: true,
      rules: 'nullable|integer|between:1,65535',
      field_type: 'text',
    },
    {
      name: 'Versão',
      description:
        'Deixe em "latest" para a release mais recente. Para fixar uma versão, use a tag ' +
        'exata, com o "v" na frente: v0.2.0.\n\n' +
        'Qualquer outro valor cai na mais recente — nome de branch não vale aqui, porque o ' +
        'que se baixa é uma release, não o código.',
      env_variable: 'VERSAO',
      default_value: 'latest',
      user_viewable: true,
      user_editable: true,
      rules: 'required|string|max:40',
      field_type: 'text',
    },
  ],
};

const destino = path.join(aqui, 'egg-greenlabs.json');
fs.writeFileSync(destino, JSON.stringify(egg, null, 4) + '\n');
console.log('egg gerado:', path.relative(process.cwd(), destino));
