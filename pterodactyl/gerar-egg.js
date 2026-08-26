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
  startup: './iniciar.sh',
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
      container: 'golang:1.24-bookworm',
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
      name: 'Versão',
      description:
        'Branch ou tag do repositório a compilar. Deixe em "main" para a mais recente. ' +
        'Mudar aqui só tem efeito ao reinstalar o servidor.',
      env_variable: 'VERSAO',
      default_value: 'main',
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
