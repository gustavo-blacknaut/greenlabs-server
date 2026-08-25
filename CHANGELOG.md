# Changelog

Mudanças do servidor de sinalização em Go, por versão. Formato livre, em
português.

## [0.1.0] — 2026-08-25

Primeira versão. Reescreve em Go o servidor de sinalização que existia em Node,
com o mesmo protocolo: os mesmos tipos de mensagem, os mesmos campos e as mesmas
rotas HTTP (`/`, `/rooms`, `/stats`). Dá para trocar um pelo outro sem tocar no
app, no site nem no Android.

Medido lado a lado, os dois no ar ao mesmo tempo, com 100 clientes numa sala a
300 mensagens por segundo cada (≈30.000 msg/s): os dois entregaram tudo, mas o
Go gastou 1,6 s de CPU contra 10,4 s, e 24 MB de RAM contra 160 MB. Parado, sem
ninguém conectado, 8 MB contra 47 MB.

Subindo para ≈100.000 msg/s o Node entregou 57% — satura perto de 55–57 mil
mensagens por segundo, porque roda numa thread só. O Go continuou entregando
100%. O teto dele não foi encontrado: acima disso quem não deu conta foi o
gerador de carga na mesma máquina.

Vale registrar que **nada disso é gargalo no uso real**: uma chamada de 30
pessoas fica em torno de 30 mensagens por segundo depois que todo mundo entrou.
O que muda na prática é a memória, que importa em VPS pequeno e em slot de
Pterodactyl com limite.

O que mudou de decisão em relação ao servidor em Node:

- **Zero dependência.** O WebSocket é implementado no próprio repositório
  (`websocket.go`): handshake, quadros, fragmentação e controle. `go build` não
  baixa nada e funciona offline, e a imagem Docker sai do `scratch` com ~11 MB.

- **O repasse não desmonta o JSON.** É o caminho mais percorrido do servidor. Em
  vez de `parse` → objeto novo → `stringify` por mensagem, o campo `from` é
  acrescentado ao fim do JSON original. Vai no fim de propósito: com chave
  repetida o `JSON.parse` do navegador fica com a última, igual ao que o spread
  do Node fazia, então continua não sendo possível forjar remetente.

- **Fila de saída com teto (256 mensagens).** Quem não acompanha é desconectado
  em vez de o servidor acumular memória sem limite, que é o comportamento do
  `ws` no Node. Sai no log como `FILA CHEIA`.

Continua igual porque já estava certo: o ping é acumulado e transmitido uma vez
por segundo por sala, em vez de um broadcast por ping (que crescia com o
quadrado da sala).

Também acompanha um gerador de carga (`ferramentas/carga`) que fala com os dois
servidores, para a comparação poder ser refeita em vez de acreditada.
