# GreenLabs — Servidor de Sinalização

Servidor do [GreenLabs](https://github.com/gustavo-blacknaut/greenlabs-desktop):
junta as pessoas numa sala e mantém a chamada de pé. Um binário só, sem
instalador e sem runtime.

Roda de dois jeitos, e a escolha muda tudo — veja
[Ligado ou desligado?](#ligado-ou-desligado):

- **padrão** — só apresenta as pessoas e sai da frente; o vídeo vai direto
  entre elas e o servidor consome ~8 MB
- **`--sfu`** — o vídeo passa por aqui, o servidor recebe uma vez e reenvia

**Num painel ou VPS, use `--sfu`.** Tem [egg pronto para o
Pterodactyl](#pterodactyl-egg-pronto).

---

## Quanto ele aguenta

Medido nesta máquina (Ryzen 5 1600, 12 threads, Windows 10) contra a versão
anterior, em Node, que este servidor substituiu. Mesmo gerador de carga para os
dois: 100 clientes numa sala, cada
um mandando 300 mensagens de repasse por segundo (≈30.000 msg/s), corpo de 400
bytes, 10 segundos.

|                          | Go        | Node      |
| ------------------------ | --------- | --------- |
| Entregue                 | 100%      | 100%      |
| CPU consumida em 10s     | **1,6 s** | 10,4 s    |
| RAM sob carga            | **24 MB** | 160 MB    |
| RAM parado, sem ninguém  | **8 MB**  | 47 MB     |

Subindo a carga para 200 clientes × 500 msg/s (≈100.000 msg/s ofertados):

|                | Go              | Node                    |
| -------------- | --------------- | ----------------------- |
| Entregue       | **104.000 msg/s (100%)** | 57.000 msg/s (57%) |
| CPU em 10s     | 13,9 s          | 11,9 s                  |

O Node satura em torno de **55–57 mil mensagens por segundo** e a partir dali
começa a perder: ele roda tudo numa thread só, e quando ela enche não tem para
onde crescer. Os 11,9 s de CPU são uma thread inteira ocupada mais o coletor de
lixo ao lado. O Go espalha por todos os núcleos, então continuou entregando
100% enquanto consumia 13,9 s de CPU repartidos em 12 threads.

Não achei o teto do Go: acima de ~104 mil msg/s quem não deu conta foi o próprio
gerador de carga, rodando na mesma máquina. O número real é mais alto, só não
dá para medir daqui.

### Agora a parte honesta

**Nada disso é gargalo para o GreenLabs hoje.** Uma chamada de 30 pessoas gera
uns poucos milhares de mensagens no momento em que todo mundo entra, e depois
cai para 30 pings por segundo. Isso é 0,05% do que a versão anterior já aguentava.

O que muda de verdade na prática é o consumo parado: **8 MB contra 47 MB**, e
**24 MB contra 160 MB** com uma sala cheia. Em VPS barato, em contêiner com
limite de memória ou em slot de Pterodactyl, essa é a diferença entre caber
folgado e ficar apertado. O resto é margem que você provavelmente nunca vai usar
— mas está lá.

Para medir na sua máquina, veja [Medindo](#medindo) no fim.

---

## Rodando

A maneira rápida é não compilar nada: baixe o binário pronto em
[Releases](https://github.com/gustavo-blacknaut/greenlabs-server/releases)
e execute. É um arquivo só, sem instalador, sem runtime.

| Onde vai rodar                       | Arquivo                          |
| ------------------------------------ | -------------------------------- |
| VPS, Pterodactyl, a maioria do Linux | `greenlabs-server-linux-amd64`   |
| Oracle Cloud grátis, Raspberry Pi    | `greenlabs-server-linux-arm64`   |
| Windows                              | `greenlabs-server-windows-amd64.exe` |

No Linux, lembre de dar permissão antes:

```bash
chmod +x greenlabs-server-linux-amd64
./greenlabs-server-linux-amd64
```

Sem argumento nenhum ele pergunta a porta e se você quer túnel. Para deixar
rodando sozinho, passe as opções na linha de comando e ele não pergunta nada.

### Compilando você mesmo

Precisa do [Go 1.24 ou mais novo](https://go.dev/dl/) só na hora de compilar. A
máquina que roda não precisa de Go, nem de Node, nem de nada.

```bash
git clone https://github.com/gustavo-blacknaut/greenlabs-server
cd greenlabs-server
go build -o greenlabs-server .
./greenlabs-server
```

O modo de sinalização não usa biblioteca externa: o WebSocket é implementado
aqui mesmo. As dependências do `go.mod` são todas do
[pion](https://github.com/pion/webrtc) e só entram em jogo com `--sfu`, que
precisa mesmo falar WebRTC do lado do servidor.

### Opções

```
--port N          porta a escutar
--sfu             o vídeo passa pelo servidor em vez de ir direto entre as pessoas
--tunnel          abre um túnel público com cloudflared ou ngrok
--tunnel=ngrok    força um provedor
-h, --help        ajuda
```

O `--sfu` muda bastante coisa: veja [Ligado ou desligado?](#ligado-ou-desligado).

### Porta

Em ordem de prioridade:

1. `--port`
2. `PORT` (do ambiente ou do `.env`)
3. `SERVER_PORT` — é onde o Pterodactyl entrega a porta alocada
4. `25640`

Copie o `.env.example` para `.env` e ajuste. Variável já definida no ambiente
ganha do arquivo, então quem configura pelo painel ou pelo systemd não é
atropelado por um `.env` esquecido na pasta.

### Endpoints

| Rota     | O que devolve                                        |
| -------- | ---------------------------------------------------- |
| `/`      | estado resumido e quantas salas estão ativas          |
| `/rooms` | cada sala, com participantes e o ping de cada um      |
| `/stats` | desde quando está no ar, total de conexões e repasses |

```bash
curl http://localhost:25640/rooms
```

---

## Ligado ou desligado?

O `--sfu` é a decisão que mais muda o comportamento. Sala de 5 pessoas, 1
transmitindo em 1080p a 4 Mbps:

|                                | Desligado (padrão)        | Ligado                    |
| ------------------------------ | ------------------------- | ------------------------- |
| Upload de quem transmite       | 16 Mbps (4 cópias)        | 4 Mbps (1 cópia)          |
| Upload do servidor             | 0                         | 16 Mbps                   |
| Conexões por pessoa            | 4                         | 1                         |
| Se o roteador atrapalhar       | tela preta, sem aviso     | funciona                  |
| RAM do servidor                | ~8 MB                     | cresce com quem assiste   |
| Quem entra no meio             | precisa renegociar com todos | recebe do servidor     |

**Desligado** o servidor só apresenta as pessoas umas às outras e sai da frente.
Vídeo e áudio vão direto entre elas; aqui passa só texto. É o certo para quem
hospeda em casa, para a galera da mesma rede.

**Ligado** cada pessoa mantém uma conexão só, com o servidor, que recebe o vídeo
uma vez e reenvia. O upload de quem transmite para de crescer com o tamanho da
sala e a travessia de rede deixa de existir, porque o servidor tem endereço
público. Custa banda e CPU daqui.

**Hospedando num painel ou VPS, ligue.** É exatamente o caso em que compensa.

> Sem `--sfu`, quando a conexão direta falha **não aparece erro nenhum**: a
> sinalização funciona, todo mundo entra na sala, o ping mostra 30 ms e a tela
> fica preta. Se é isso que está acontecendo, é essa flag.

Para saber qual modo está no ar, entre com duas abas na mesma sala. Em modo SFU
a lista de participantes que o servidor manda na entrada vem vazia de propósito,
porque ninguém é apresentado a ninguém.

---

## Hospedando

### Pterodactyl sem egg (o jeito mais curto)

Não precisa importar nada. Em **Admin → Servers → [seu servidor] → Startup**,
troque o comando de inicialização por este e ligue:

```
chmod +x greenlabs-server 2>/dev/null; if [ -s greenlabs-server ] && bash -c "./greenlabs-server --help" >/dev/null 2>&1; then echo "GreenLabs pronto, reaproveitando o que ja esta aqui"; else A=amd64; case $(uname -m) in aarch64|arm64) A=arm64;; esac; U=https://github.com/gustavo-blacknaut/greenlabs-server/releases/latest/download/greenlabs-server-linux-$A; echo "GreenLabs baixando (linux-$A)..."; rm -f greenlabs-server; { curl -fsSL -o greenlabs-server $U || wget -qO greenlabs-server $U; } || { echo "ERRO: nao consegui baixar; confira a internet do host"; exit 1; }; chmod +x greenlabs-server; bash -c "./greenlabs-server --help" >/dev/null 2>&1 || { echo "ERRO: o arquivo baixado nao executa nesta maquina"; exit 1; }; echo "GreenLabs instalado"; fi; exec ./greenlabs-server --port {{SERVER_PORT}} --sfu
```

Funciona em qualquer servidor, mesmo criado com outro egg: só depende do
`{{SERVER_PORT}}`, que o Pterodactyl sempre fornece. Para desligar o SFU, tire
o `--sfu` do fim.

O que ele cobre, em ordem:

| Situação | O que faz |
| --- | --- |
| Pasta vazia | baixa e sobe |
| Já baixado | reaproveita, sem tocar na rede |
| Download interrompido | percebe e baixa de novo |
| Sem permissão de execução | ajusta, sem rebaixar 10 MB |
| Sem internet | erro claro, em vez de sumir |

A checagem não é "o arquivo existe" e sim **"o arquivo roda"** — um download
interrompido deixa um arquivo com tamanho, que passaria num teste de existência
e quebraria só na hora de subir, com uma mensagem que não ajuda em nada.

### Pterodactyl (egg pronto)### Pterodactyl (egg pronto)

Em [`pterodactyl/egg-greenlabs.json`](pterodactyl/egg-greenlabs.json).

1. **Admin → Nests → Import Egg** e envie o arquivo
2. Crie o servidor com esse egg
3. Ligue

O servidor se baixa sozinho. **A instalação só adianta o download** — se ela
falhar, o comando de inicialização baixa no boot e o servidor sobe igual. Não
existe estado em que o egg instale errado e você fique travado.

| Variável | Padrão   | O que faz                                                    |
| -------- | -------- | ------------------------------------------------------------ |
| `SFU`    | `1`      | `1` liga o retransmissor, `0` desliga                         |
| `PORTA`  | *branco* | em branco usa a porta alocada pelo painel; ou escolha uma     |
| `VERSAO` | `latest` | release a baixar; aceita uma tag (`v0.2.0`)                   |

**Sobre a porta.** Em branco usa a que o painel alocou, que é o caso normal.
Preencha só se quiser outra — e ela precisa estar liberada para este servidor,
senão ele sobe e ninguém consegue chegar nele.

#### Por que não tem "build", diferente do Node

Egg de Node roda `npm install` no boot porque `node_modules` é específico da
máquina e não dá para versionar pronto. Aqui não existe equivalente: o Go
entrega **um binário estático, completo**. Não há dependência para resolver na
host, nada para compilar, nada que possa quebrar por diferença de ambiente.

Por isso o comando de inicialização só precisa garantir que o arquivo está lá:

```
[ -f greenlabs-server ] || baixa; exec ./greenlabs-server --port ... --sfu
```

### Pterodactyl na mão

Se preferir não importar o egg, use qualquer egg genérico de binário:

```bash
# no console do servidor, depois de subir o arquivo
chmod +x greenlabs-server-linux-amd64
```

Comando de inicialização:

```
./greenlabs-server-linux-amd64 --port {{SERVER_PORT}} --sfu
```

O binário está em
[Releases](https://github.com/gustavo-blacknaut/greenlabs-server/releases) —
`-arm64` para Oracle Cloud grátis e Raspberry Pi. Confira se a release é nova o
bastante para conhecer o `--sfu`: rode `./greenlabs-server-linux-amd64 --help` e
veja se a flag aparece na lista.

### Linux com systemd

```bash
sudo useradd -r -s /usr/sbin/nologin greenlabs
sudo mkdir -p /opt/greenlabs
sudo cp greenlabs-server /opt/greenlabs/
sudo chown -R greenlabs: /opt/greenlabs
```

`/etc/systemd/system/greenlabs.service`:

```ini
[Unit]
Description=GreenLabs — sinalização
After=network.target

[Service]
Type=simple
User=greenlabs
WorkingDirectory=/opt/greenlabs
ExecStart=/opt/greenlabs/greenlabs-server --port 25640 --sfu
Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now greenlabs
sudo journalctl -u greenlabs -f
```

### Docker

```dockerfile
FROM golang:1.24 AS build
WORKDIR /src
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /greenlabs-server .

FROM gcr.io/distroless/static
COPY --from=build /greenlabs-server /greenlabs-server
EXPOSE 25640
ENTRYPOINT ["/greenlabs-server", "--sfu"]
```

```bash
docker build -t greenlabs-server .
docker run -d --restart unless-stopped -p 25640:25640 greenlabs-server
```

### Windows

Rode o `.exe` e deixe a janela aberta. Sem argumento nenhum ele pergunta a porta
e se você quer túnel.

Para expor a porta na rede local:

```powershell
New-NetFirewallRule -DisplayName "GreenLabs" -Direction Inbound -Protocol TCP -LocalPort 25640 -Action Allow
```

### Sem abrir porta no roteador

Três saídas, da mais simples para a mais trabalhosa:

- **`--tunnel`** — precisa do cloudflared ou do ngrok instalado. Entrega um
  endereço `wss://` público, que funciona até com o site em HTTPS.
- **Radmin VPN ou Hamachi** — todo mundo entra na mesma rede virtual e usa o IP
  que aparece na lista de endereços quando o servidor sobe.
- **Encaminhamento de porta no roteador** — o jeito clássico, e o que mais dá
  trabalho de explicar para os amigos.

---

## Compilando para outro sistema

Go compila para qualquer alvo a partir de qualquer máquina, sem precisar de nada
instalado do outro lado:

```bash
# Linux x64 (a maioria dos VPS e do Pterodactyl)
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o greenlabs-server .

# Linux ARM64 (Oracle Cloud gratuito, Raspberry Pi)
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -ldflags="-s -w" -o greenlabs-server-arm64 .

# Windows
GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o greenlabs-server.exe .
```

`-ldflags="-s -w"` tira a tabela de símbolos e o DWARF: o binário sai perto de
7 MB em vez de 10,5 MB. `CGO_ENABLED=0` garante que ele seja estático, o que é o
que permite rodar na imagem `scratch` e em qualquer distro.

---

## Protocolo

O cliente abre um WebSocket na raiz e manda JSON.

**Entrar numa sala**

```json
{ "type": "join", "roomId": "call1", "name": "Fulano" }
```

Resposta para quem entrou:

```json
{ "type": "joined", "peerId": "...", "peers": [{ "peerId": "...", "name": "...", "pingMs": 0 }], "count": 2 }
```

Os que já estavam recebem `{"type":"peer-joined","peerId":"...","name":"...","count":2}`,
e quando alguém sai, `{"type":"peer-left","peerId":"..."}`.

**Repasse ponto a ponto** — qualquer mensagem com `to` é entregue àquele
participante com o campo `from` acrescentado:

```json
{ "type": "offer", "to": "<peerId>", "sdp": "..." }
```

O `from` é sempre carimbado pelo servidor. Se o cliente mandar um `from`
próprio, o do servidor prevalece — não dá para forjar remetente.

**Ping**

```json
{ "type": "ping", "timestamp": 1700000000000, "rtt": 42 }
```

Volta `{"type":"pong","timestamp":<o mesmo>,"serverTime":<agora>}`. O tempo de
ida e volta é medido pelo cliente no relógio dele e informado no `rtt`; o
servidor só guarda e redistribui. Calcular a diferença aqui mediria descompasso
de relógio entre as máquinas, não latência.

Uma vez por segundo cada sala recebe
`{"type":"room-pings","pings":{"<peerId>": 42, ...}}`.

---

## Decisões

Três escolhas que valem uma explicação:

**Sem dependência.** O WebSocket (RFC 6455) é implementado aqui, em
[`websocket.go`](websocket.go) — handshake, quadros, fragmentação e controle.
São umas 300 linhas e evitam ter que baixar pacote para compilar. `git clone`
e `go build` bastam, offline inclusive.

**O repasse não desmonta o JSON.** Esse é o caminho mais percorrido do servidor.
O Node faz `JSON.parse` → espalha num objeto novo → `JSON.stringify`, três
voltas por mensagem só para incluir um campo. Aqui o `from` é acrescentado no
fim do JSON original, sem remontar nada. Vai no fim de propósito: com chave
repetida o `JSON.parse` do navegador fica com a última, que é exatamente o que
o spread do Node fazia.

**Fila de saída com teto.** Cada conexão tem uma fila de 256 mensagens. Quando
enche, o participante é desconectado em vez de o servidor continuar acumulando
na memória. A biblioteca `ws` do Node guarda sem limite — um cliente travado
pode ir empurrando o consumo para cima até derrubar o processo. Aqui ele cai
sozinho e o resto da sala segue. Aparece no log como `FILA CHEIA`.

Uma coisa continuou igual porque já estava certa: o ping é acumulado e
transmitido **uma vez por segundo por sala**. Na versão antiga cada ping
disparava um broadcast para a sala inteira, ou seja n×n mensagens por segundo —
30 pessoas geravam ~8 Mbps só de ping.

---

## Medindo

Os testes cobrem entrada e saída de sala, repasse com preservação de campos,
carimbo do `from`, ping/pong e leitura de `.env`. Eles falam com o servidor por
TCP de verdade, montando os quadros WebSocket na mão — é o caminho completo, não
uma chamada interna:

```bash
go test ./...
```

Para repetir a comparação de desempenho, tem um gerador de carga junto. Ele
serve para os dois servidores, já que o protocolo é o mesmo:

```bash
go build -o carga ./ferramentas/carga

# 100 clientes numa sala, 300 mensagens por segundo cada
./carga -addr 127.0.0.1:25640 -clientes 100 -taxa 300 -segundos 10
```

Deixe `-taxa 0` para mandar sem limite — mas aí o que você mede é o tamanho da
fila e a política de descarte, não a vazão. Taxa fixa responde a pergunta que
importa: *esse volume passa inteiro?*

`recebidas` passa de 100% porque o `room-pings` de cada segundo também conta
como mensagem recebida.

Para o consumo, olhe o processo enquanto a carga roda:

```powershell
Get-Process greenlabs-server | Select-Object WorkingSet64, CPU
```

```bash
ps -o rss,time -p $(pgrep greenlabs-server)
```

---

## Licença

Mesmo licenciamento do projeto principal.
