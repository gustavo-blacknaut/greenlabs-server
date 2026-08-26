# GreenLabs — Servidor de Sinalização (Go)

Servidor de sinalização WebRTC do [GreenLabs](https://github.com/gustavo-blacknaut/greenlabs-desktop),
escrito em Go. A função dele é simples:
juntar as pessoas numa sala e passar recado entre elas até a conexão direta
fechar.

Ele funciona de dois jeitos, e a diferença importa:

**Sem `--sfu` (padrão).** O servidor só apresenta as pessoas umas às outras e
sai da frente. Vídeo e áudio vão direto entre elas, e aqui passa só texto: quem
entrou, quem saiu, ofertas SDP, candidatos ICE e o ping de cada um. Consome
quase nada — mas depende de os dois lados conseguirem se achar através dos
roteadores.

**Com `--sfu`.** O vídeo passa por aqui: o servidor recebe uma vez e reenvia
para todo mundo. Gasta banda e CPU, e em troca o upload de quem transmite para
de crescer com o tamanho da sala e a travessia de rede deixa de ser problema.

**Se você hospeda num painel ou numa VPS, use `--sfu`.** É exatamente o caso em
que compensa. Sem a flag, quando a conexão direta falha, a sinalização funciona,
todo mundo aparece na lista e a tela fica preta.

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

O `--sfu` muda bastante coisa: veja [Sobre as duas flags](#sobre-as-duas-flags).

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

## Hospedando

### Pterodactyl

É o caminho mais fácil: qualquer egg genérico de binário serve, porque não
precisa de Node, de Java nem de runtime nenhum — é um arquivo só.

**1. Suba o binário.** Baixe `greenlabs-server-linux-amd64` das
[Releases](https://github.com/gustavo-blacknaut/greenlabs-server/releases)
e envie pelo gerenciador de arquivos do painel. Em ARM (Oracle Cloud grátis,
por exemplo) use o `-arm64`.

**2. Dê permissão de execução.** Sem isso o painel responde `permission denied`.
No console do servidor:

```
chmod +x greenlabs-server-linux-amd64
```

**3. Comando de inicialização:**

```
./greenlabs-server-linux-amd64 --port {{SERVER_PORT}} --sfu
```

**4. Ligue o servidor** e confira a alocação — a porta que o painel mostra é a
que os seus amigos vão digitar.

Pronto. O endereço fica `ws://SEU-ENDERECO:PORTA`, igual ao que aparece na
alocação do painel.

#### Sobre as duas flags

**`--port {{SERVER_PORT}}`** — o painel substitui `{{SERVER_PORT}}` pela porta
alocada. O servidor também lê a variável `SERVER_PORT` sozinho, mas passar
explícito é melhor: fica visível no comando e não depende do egg exportar a
variável.

**`--sfu`** — esse muda o comportamento e vale entender. Sem ele, cada pessoa
manda o próprio vídeo direto para cada uma das outras. Numa sala de 5, quem
transmite sobe o vídeo **4 vezes** — e os dois lados ainda precisam se achar
através dos roteadores, o que nem sempre dá certo.

Com `--sfu`, cada pessoa mantém **uma** conexão, com o servidor, que recebe uma
vez e reenvia. O upload de quem transmite para de crescer com o tamanho da sala,
e o problema de travessia de rede some, porque o servidor tem endereço público.

O preço é real: o vídeo passa a consumir banda e CPU do servidor,
proporcionalmente a quantas pessoas estão assistindo.

**Se você hospeda num painel, deixe `--sfu` ligado.** É justamente o caso em que
o servidor tem endereço público e as pessoas estão espalhadas. Sem a flag, o
servidor só apresenta as pessoas umas às outras — e se a conexão direta falhar,
a sinalização funciona, todo mundo aparece na lista, e a tela fica preta.

Para conferir qual modo está no ar, entre com duas abas na mesma sala: em modo
SFU a lista de participantes que o servidor manda na entrada vem vazia de
propósito, porque ninguém é apresentado a ninguém.

### Linux com systemd

```bash
sudo useradd -r -s /usr/sbin/nologin greenlabs
sudo mkdir -p /opt/greenlabs
sudo cp greenlabs-server /opt/greenlabs/
sudo chown -R greenlabs:greenlabs /opt/greenlabs
```

`/etc/systemd/system/greenlabs.service`:

```ini
[Unit]
Description=Sinalizacao GreenLabs
After=network.target

[Service]
Type=simple
User=greenlabs
WorkingDirectory=/opt/greenlabs
Environment=PORT=25640
ExecStart=/opt/greenlabs/greenlabs-server
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl enable --now greenlabs
sudo journalctl -u greenlabs -f
```

### Docker

```bash
docker build -t greenlabs-server .
docker run -d --name greenlabs -p 25640:25640 --restart unless-stopped greenlabs-server
```

O `Dockerfile` compila numa etapa e publica noutra a partir do `scratch`: a
imagem final tem só o binário, sem shell nem gerenciador de pacotes. Dá em torno
de 11 MB.

### Windows

```powershell
.\greenlabs-server.exe --port 25640
```

Para deixar rodando sempre, crie uma tarefa no Agendador de Tarefas com gatilho
"ao iniciar o computador".

Lembre de liberar a porta:

```powershell
New-NetFirewallRule -DisplayName "GreenLabs" -Direction Inbound -LocalPort 25640 -Protocol TCP -Action Allow
```

### Sem abrir porta no roteador

**Radmin VPN ou Hamachi** — todo mundo entra na mesma rede virtual e usa o
endereço `26.x.x.x` que o servidor imprime ao subir (esses aparecem primeiro na
lista, marcados como VPN).

**Túnel** — `./greenlabs-server --tunnel` detecta cloudflared ou ngrok e imprime
um endereço `wss://` público. Serve para teste rápido; o endereço muda a cada
execução.

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
