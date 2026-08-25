# GreenLabs — Servidor de Sinalização (Go)

Servidor de sinalização WebRTC do [GreenLabs](https://github.com/gustavo-blacknaut/greenlabs-live-streaming),
escrito em Go. É a mesma função do [servidor em Node](https://github.com/gustavo-blacknaut/greenlabs-live-streaming-server):
juntar as pessoas numa sala e passar recado entre elas até a conexão direta
fechar.

**Vídeo e áudio não passam por aqui.** Quem conversa são os navegadores, ponto a
ponto. Este servidor só carrega texto: quem entrou, quem saiu, ofertas SDP,
candidatos ICE e o ping de cada um. Por isso ele cabe em qualquer lugar.

Protocolo idêntico ao do servidor em Node — mesmos tipos de mensagem, mesmos
campos, mesmos endpoints HTTP. Dá para trocar um pelo outro sem mexer no app,
no site nem no Android.

---

## Vale a pena trocar?

Medido nesta máquina (Ryzen 5 1600, 12 threads, Windows 10), os dois servidores
rodando lado a lado, com o mesmo gerador de carga: 100 clientes numa sala, cada
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
cai para 30 pings por segundo. Isso é 0,05% do que o Node já aguenta.

O que muda de verdade na prática é o consumo parado: **8 MB contra 47 MB**, e
**24 MB contra 160 MB** com uma sala cheia. Em VPS barato, em contêiner com
limite de memória ou em slot de Pterodactyl, essa é a diferença entre caber
folgado e ficar apertado. O resto é margem que você provavelmente nunca vai usar
— mas está lá.

E tem o lado chato de trocar: são duas bases de código para manter, e a versão
em Node continua sendo a de referência. Se você não tem problema de memória, não
tem por que mexer.

Para medir na sua máquina, veja [Medindo](#medindo) no fim.

---

## Rodando

Precisa do [Go 1.21 ou mais novo](https://go.dev/dl/) só para compilar. Depois é
um binário sozinho — a máquina que roda não precisa de Go, de Node, nem de nada.

```bash
git clone https://github.com/gustavo-blacknaut/greenlabs-live-streaming-server-go
cd greenlabs-live-streaming-server-go
go build -o greenlabs-server .
./greenlabs-server
```

Sem dependência externa nenhuma: o `go.mod` não lista um pacote sequer, então o
build não baixa nada e funciona offline.

### Opções

```
--port N          porta a escutar
--tunnel          abre um túnel público com cloudflared ou ngrok
--tunnel=ngrok    força um provedor
-h, --help        ajuda
```

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

Use um egg genérico de binário. O painel entrega a porta em `SERVER_PORT`, e o
servidor lê essa variável sozinho — não precisa configurar porta em lugar
nenhum.

1. Compile para Linux (veja [Compilando para outro sistema](#compilando-para-outro-sistema))
2. Suba `greenlabs-server` para a pasta do servidor
3. Comando de inicialização: `./greenlabs-server`
4. Marque a porta como aberta nas alocações

Como é um binário estático, não precisa de imagem com Node nem instalar nada.

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

## O que é diferente do servidor em Node

Mesmo protocolo, mas três decisões mudaram:

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
30 pessoas geravam ~8 Mbps só de ping. Isso foi corrigido no servidor em Node e
está do mesmo jeito aqui.

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
