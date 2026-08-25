package main

// Implementação de WebSocket (RFC 6455) do lado servidor, só o que a
// sinalização usa: handshake, quadros de texto, fragmentação e os quadros de
// controle (ping, pong, close).
//
// É feito à mão de propósito. O servidor inteiro fica sem dependência externa,
// então "git clone && go build" produz um binário único sem baixar nada — o que
// importa em painel de hospedagem e em container mínimo.

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const guidWebSocket = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"

const (
	opContinuacao = 0x0
	opTexto       = 0x1
	opBinario     = 0x2
	opFechar      = 0x8
	opPing        = 0x9
	opPong        = 0xA
)

const (
	// Sinalização é SDP e ICE: alguns KB no pior caso. O limite existe para um
	// cliente hostil não conseguir pedir um alloc gigante.
	tamanhoMaximoMensagem = 1 << 20 // 1 MiB

	// Sem nada chegando por esse tempo a conexão é dada como morta. O servidor
	// manda ping a cada 3s e o navegador responde pong sozinho, então um par
	// vivo renova bem antes disso.
	tempoLimiteLeitura = 15 * time.Second

	tempoLimiteEscrita = 10 * time.Second

	// Fila de saída por conexão. Cheia significa par que não acompanha: melhor
	// derrubar do que acumular memória sem teto.
	tamanhoFilaSaida = 256
)

var (
	ErrConexaoFechada = errors.New("conexão fechada")
	ErrFilaCheia      = errors.New("fila de saída cheia")
)

type quadroSaida struct {
	opcode byte
	dados  []byte
}

// Conn é uma conexão WebSocket já estabelecida.
//
// Escrita acontece só na goroutine escritor(); qualquer outro lugar enfileira.
// Isso evita quadros intercalados sem precisar de mutex no caminho quente.
type Conn struct {
	rede    net.Conn
	leitor  *bufio.Reader
	escrita *bufio.Writer

	saida   chan quadroSaida
	fechado chan struct{}
	umaVez  sync.Once

	// AoPong é chamado quando o cliente responde a um ping nosso.
	AoPong func()

	// montagem guarda os pedaços de uma mensagem fragmentada.
	montagem []byte
	montando bool
}

// AceitarWebSocket faz o handshake e assume o socket.
func AceitarWebSocket(w http.ResponseWriter, r *http.Request) (*Conn, error) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("cabeçalho Upgrade ausente")
	}
	if !cabecalhoContem(r.Header.Get("Connection"), "upgrade") {
		return nil, errors.New("cabeçalho Connection sem upgrade")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, errors.New("versão de WebSocket não suportada")
	}
	chave := r.Header.Get("Sec-WebSocket-Key")
	if chave == "" {
		return nil, errors.New("Sec-WebSocket-Key ausente")
	}

	sequestrador, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("servidor HTTP não permite assumir a conexão")
	}
	rede, buf, err := sequestrador.Hijack()
	if err != nil {
		return nil, err
	}

	// Sinalização é feita de mensagens pequenas onde latência é tudo: agrupar
	// pacotes por Nagle só adiciona espera.
	if tcp, ok := rede.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	soma := sha1.Sum([]byte(chave + guidWebSocket))
	resposta := "HTTP/1.1 101 Switching Protocols\r\n" +
		"Upgrade: websocket\r\n" +
		"Connection: Upgrade\r\n" +
		"Sec-WebSocket-Accept: " + base64.StdEncoding.EncodeToString(soma[:]) + "\r\n\r\n"

	_ = rede.SetWriteDeadline(time.Now().Add(tempoLimiteEscrita))
	if _, err := buf.WriteString(resposta); err != nil {
		rede.Close()
		return nil, err
	}
	if err := buf.Flush(); err != nil {
		rede.Close()
		return nil, err
	}
	_ = rede.SetWriteDeadline(time.Time{})

	// O leitor precisa ser o do Hijack: ele já pode ter lido bytes do cliente
	// que vieram grudados no fim do handshake.
	return &Conn{
		rede:    rede,
		leitor:  buf.Reader,
		escrita: bufio.NewWriterSize(rede, 4096),
		saida:   make(chan quadroSaida, tamanhoFilaSaida),
		fechado: make(chan struct{}),
	}, nil
}

func cabecalhoContem(valor, procurado string) bool {
	for _, parte := range strings.Split(valor, ",") {
		if strings.EqualFold(strings.TrimSpace(parte), procurado) {
			return true
		}
	}
	return false
}

// Fechado devolve um canal que fecha quando a conexão cai.
func (c *Conn) Fechado() <-chan struct{} { return c.fechado }

// Fechar encerra a conexão. Pode ser chamado quantas vezes for.
func (c *Conn) Fechar() {
	c.umaVez.Do(func() { close(c.fechado) })
}

// EnviarTexto enfileira uma mensagem de texto.
func (c *Conn) EnviarTexto(dados []byte) error {
	return c.enfileirar(quadroSaida{opcode: opTexto, dados: dados})
}

// EnviarPing enfileira um ping de controle.
func (c *Conn) EnviarPing() error {
	return c.enfileirar(quadroSaida{opcode: opPing})
}

func (c *Conn) enfileirar(q quadroSaida) error {
	select {
	case <-c.fechado:
		return ErrConexaoFechada
	default:
	}
	select {
	case c.saida <- q:
		return nil
	case <-c.fechado:
		return ErrConexaoFechada
	default:
		c.Fechar()
		return ErrFilaCheia
	}
}

// Escritor roda em goroutine própria e é o único ponto que escreve no socket.
func (c *Conn) Escritor() {
	defer c.rede.Close()
	for {
		select {
		case q := <-c.saida:
			if err := c.escreverQuadro(q.opcode, q.dados); err != nil {
				c.Fechar()
				return
			}
		case <-c.fechado:
			c.drenar()
			return
		}
	}
}

// drenar tenta despachar o que ficou na fila antes de desligar, para um
// "peer-left" no fim da conexão não se perder.
func (c *Conn) drenar() {
	for {
		select {
		case q := <-c.saida:
			if err := c.escreverQuadro(q.opcode, q.dados); err != nil {
				return
			}
		default:
			return
		}
	}
}

func (c *Conn) escreverQuadro(opcode byte, dados []byte) error {
	var cabecalho [10]byte
	cabecalho[0] = 0x80 | opcode // FIN ligado: não fragmentamos na saída
	tamanho := len(dados)
	n := 2
	switch {
	case tamanho <= 125:
		cabecalho[1] = byte(tamanho)
	case tamanho <= 0xFFFF:
		cabecalho[1] = 126
		binary.BigEndian.PutUint16(cabecalho[2:4], uint16(tamanho))
		n = 4
	default:
		cabecalho[1] = 127
		binary.BigEndian.PutUint64(cabecalho[2:10], uint64(tamanho))
		n = 10
	}

	if err := c.rede.SetWriteDeadline(time.Now().Add(tempoLimiteEscrita)); err != nil {
		return err
	}
	// Cabeçalho e corpo passam pelo bufio para saírem numa syscall só quando a
	// mensagem é pequena, que é o caso de quase tudo aqui.
	if _, err := c.escrita.Write(cabecalho[:n]); err != nil {
		return err
	}
	if tamanho > 0 {
		if _, err := c.escrita.Write(dados); err != nil {
			return err
		}
	}
	return c.escrita.Flush()
}

// Ler devolve a próxima mensagem completa do cliente. Quadros de controle são
// tratados aqui dentro e não chegam a quem chama.
func (c *Conn) Ler() ([]byte, error) {
	for {
		fim, opcode, dados, err := c.lerQuadro()
		if err != nil {
			return nil, err
		}

		switch opcode {
		case opPing:
			// Responder ping do cliente é obrigação do servidor pela RFC.
			if err := c.enfileirar(quadroSaida{opcode: opPong, dados: dados}); err != nil {
				return nil, err
			}
			continue

		case opPong:
			if c.AoPong != nil {
				c.AoPong()
			}
			continue

		case opFechar:
			_ = c.enfileirar(quadroSaida{opcode: opFechar})
			return nil, io.EOF

		case opTexto, opBinario:
			if c.montando {
				return nil, errors.New("mensagem nova antes do fim da anterior")
			}
			c.montagem = dados
			c.montando = true

		case opContinuacao:
			if !c.montando {
				return nil, errors.New("quadro de continuação sem início")
			}
			if len(c.montagem)+len(dados) > tamanhoMaximoMensagem {
				return nil, errors.New("mensagem fragmentada grande demais")
			}
			c.montagem = append(c.montagem, dados...)

		default:
			return nil, errors.New("opcode desconhecido")
		}

		if fim {
			mensagem := c.montagem
			c.montagem = nil
			c.montando = false
			return mensagem, nil
		}
	}
}

func (c *Conn) lerQuadro() (bool, byte, []byte, error) {
	if err := c.rede.SetReadDeadline(time.Now().Add(tempoLimiteLeitura)); err != nil {
		return false, 0, nil, err
	}

	var cabecalho [2]byte
	if _, err := io.ReadFull(c.leitor, cabecalho[:]); err != nil {
		return false, 0, nil, err
	}

	fim := cabecalho[0]&0x80 != 0
	if cabecalho[0]&0x70 != 0 {
		// Não negociamos extensão nenhuma (nem permessage-deflate), então
		// qualquer bit RSV ligado é erro de protocolo.
		return false, 0, nil, errors.New("bits RSV ligados sem extensão negociada")
	}
	opcode := cabecalho[0] & 0x0F
	mascarado := cabecalho[1]&0x80 != 0
	tamanho := uint64(cabecalho[1] & 0x7F)

	controle := opcode >= 0x8
	if controle && (!fim || tamanho > 125) {
		return false, 0, nil, errors.New("quadro de controle inválido")
	}

	switch tamanho {
	case 126:
		var ext [2]byte
		if _, err := io.ReadFull(c.leitor, ext[:]); err != nil {
			return false, 0, nil, err
		}
		tamanho = uint64(binary.BigEndian.Uint16(ext[:]))
	case 127:
		var ext [8]byte
		if _, err := io.ReadFull(c.leitor, ext[:]); err != nil {
			return false, 0, nil, err
		}
		tamanho = binary.BigEndian.Uint64(ext[:])
	}

	// A RFC exige máscara em tudo que vem do cliente.
	if !mascarado {
		return false, 0, nil, errors.New("quadro do cliente sem máscara")
	}
	if tamanho > tamanhoMaximoMensagem {
		return false, 0, nil, errors.New("quadro grande demais")
	}

	var mascara [4]byte
	if _, err := io.ReadFull(c.leitor, mascara[:]); err != nil {
		return false, 0, nil, err
	}

	dados := make([]byte, tamanho)
	if tamanho > 0 {
		if _, err := io.ReadFull(c.leitor, dados); err != nil {
			return false, 0, nil, err
		}
		for i := range dados {
			dados[i] ^= mascara[i&3]
		}
	}

	return fim, opcode, dados, nil
}
