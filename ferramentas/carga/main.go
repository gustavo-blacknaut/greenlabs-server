package main

// Medidor de carga da sinalização. Conecta N clientes numa sala, cada um
// mandando mensagens de repasse para o vizinho num anel, e mede quantas o
// servidor consegue entregar por segundo.
//
//	go run ./ferramentas/carga -addr 127.0.0.1:25640 -clientes 30 -segundos 10
//
// Serve para os dois servidores (Go e Node): o protocolo é o mesmo.

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type cliente struct {
	conexao net.Conn
	leitor  *bufio.Reader
	id      string
}

func conectar(endereco string) (*cliente, error) {
	conexao, err := net.Dial("tcp", endereco)
	if err != nil {
		return nil, err
	}
	if tcp, ok := conexao.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	var bruto [16]byte
	_, _ = rand.Read(bruto[:])
	chave := base64.StdEncoding.EncodeToString(bruto[:])
	pedido := fmt.Sprintf("GET / HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
		"Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", endereco, chave)
	if _, err := conexao.Write([]byte(pedido)); err != nil {
		return nil, err
	}
	leitor := bufio.NewReaderSize(conexao, 16*1024)
	status, err := leitor.ReadString('\n')
	if err != nil {
		return nil, err
	}
	if !strings.Contains(status, "101") {
		return nil, fmt.Errorf("handshake recusado: %s", strings.TrimSpace(status))
	}
	for {
		linha, err := leitor.ReadString('\n')
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(linha) == "" {
			break
		}
	}
	return &cliente{conexao: conexao, leitor: leitor}, nil
}

func (c *cliente) enviar(texto []byte) error {
	quadro := make([]byte, 0, len(texto)+14)
	quadro = append(quadro, 0x81)
	switch tamanho := len(texto); {
	case tamanho <= 125:
		quadro = append(quadro, byte(0x80|tamanho))
	case tamanho <= 0xFFFF:
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(tamanho))
		quadro = append(quadro, 0x80|126)
		quadro = append(quadro, ext[:]...)
	default:
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(len(texto)))
		quadro = append(quadro, 0x80|127)
		quadro = append(quadro, ext[:]...)
	}
	mascara := []byte{0x11, 0x22, 0x33, 0x44}
	quadro = append(quadro, mascara...)
	for i, b := range texto {
		quadro = append(quadro, b^mascara[i&3])
	}
	_, err := c.conexao.Write(quadro)
	return err
}

// receber devolve o corpo da próxima mensagem de texto, pulando controle.
func (c *cliente) receber() ([]byte, error) {
	for {
		var cabecalho [2]byte
		if _, err := io.ReadFull(c.leitor, cabecalho[:]); err != nil {
			return nil, err
		}
		opcode := cabecalho[0] & 0x0F
		tamanho := uint64(cabecalho[1] & 0x7F)
		switch tamanho {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(c.leitor, ext[:]); err != nil {
				return nil, err
			}
			tamanho = uint64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(c.leitor, ext[:]); err != nil {
				return nil, err
			}
			tamanho = binary.BigEndian.Uint64(ext[:])
		}
		dados := make([]byte, tamanho)
		if _, err := io.ReadFull(c.leitor, dados); err != nil {
			return nil, err
		}
		if opcode == 0x1 {
			return dados, nil
		}
		if opcode == 0x8 {
			return nil, io.EOF
		}
	}
}

func main() {
	endereco := flag.String("addr", "127.0.0.1:25640", "host:porta do servidor")
	quantos := flag.Int("clientes", 30, "quantos clientes entram na sala")
	segundos := flag.Int("segundos", 10, "duracao da medicao")
	sala := flag.String("sala", "carga", "nome da sala")
	tamanhoCorpo := flag.Int("corpo", 400, "bytes de carga util por mensagem (um candidato ICE tipico)")
	taxa := flag.Int("taxa", 200, "mensagens por segundo por cliente (0 = sem limite)")
	flag.Parse()

	clientes := make([]*cliente, 0, *quantos)
	for i := 0; i < *quantos; i++ {
		c, err := conectar(*endereco)
		if err != nil {
			fmt.Fprintf(os.Stderr, "cliente %d nao conectou: %v\n", i, err)
			os.Exit(1)
		}
		clientes = append(clientes, c)
		_ = c.enviar([]byte(fmt.Sprintf(`{"type":"join","roomId":%q,"name":"carga-%d"}`, *sala, i)))
	}

	// Fase de acordo: cada um lê o próprio "joined" para descobrir seu id.
	for i, c := range clientes {
		for {
			dados, err := c.receber()
			if err != nil {
				fmt.Fprintf(os.Stderr, "cliente %d caiu antes de entrar: %v\n", i, err)
				os.Exit(1)
			}
			var m struct {
				Tipo string `json:"type"`
				ID   string `json:"peerId"`
			}
			if json.Unmarshal(dados, &m) == nil && m.Tipo == "joined" {
				c.id = m.ID
				break
			}
		}
	}
	fmt.Printf("%d clientes na sala %q\n", len(clientes), *sala)

	var enviadas, recebidas atomic.Uint64
	fim := make(chan struct{})
	var grupo sync.WaitGroup

	// Cada cliente lê sem parar: sem isso a fila de saída do servidor enche e
	// a medida vira o tamanho do buffer, não a vazão.
	for _, c := range clientes {
		grupo.Add(1)
		go func(c *cliente) {
			defer grupo.Done()
			for {
				dados, err := c.receber()
				if err != nil {
					return
				}
				// Avisos da sala nao sao repasses da carga. Inclui-los inflava
				// a taxa de entrega e escondia mensagens realmente perdidas.
				var mensagem struct {
					Tipo string `json:"type"`
				}
				if json.Unmarshal(dados, &mensagem) == nil && mensagem.Tipo == "candidate" {
					recebidas.Add(1)
				}
			}
		}(c)
	}

	corpo := strings.Repeat("x", *tamanhoCorpo)
	inicio := time.Now()
	for i, c := range clientes {
		destino := clientes[(i+1)%len(clientes)].id
		mensagem := []byte(fmt.Sprintf(`{"type":"candidate","to":%q,"candidate":%q}`, destino, corpo))
		grupo.Add(1)
		go func(c *cliente, mensagem []byte) {
			defer grupo.Done()

			// Com taxa fixa a medida vira "esse volume passa inteiro?", que e a
			// pergunta real. Sem limite, o que se mede e o tamanho da fila do
			// servidor e a politica de descarte, nao a vazao dele.
			var pulso <-chan time.Time
			if *taxa > 0 {
				tique := time.NewTicker(time.Second / time.Duration(*taxa))
				defer tique.Stop()
				pulso = tique.C
			}

			for {
				if pulso != nil {
					select {
					case <-fim:
						return
					case <-pulso:
					}
				} else {
					select {
					case <-fim:
						return
					default:
					}
				}
				if err := c.enviar(mensagem); err != nil {
					return
				}
				enviadas.Add(1)
			}
		}(c, mensagem)
	}

	time.Sleep(time.Duration(*segundos) * time.Second)
	close(fim)
	decorrido := time.Since(inicio).Seconds()

	// Um instante para o que está em voo terminar de chegar.
	time.Sleep(300 * time.Millisecond)
	for _, c := range clientes {
		c.conexao.Close()
	}
	grupo.Wait()

	e, r := enviadas.Load(), recebidas.Load()
	fmt.Printf("enviadas:  %d  (%.0f msg/s)\n", e, float64(e)/decorrido)
	fmt.Printf("recebidas: %d  (%.0f msg/s)\n", r, float64(r)/decorrido)
	fmt.Printf("entregues: %.1f%%\n", 100*float64(r)/float64(e))
	fmt.Printf("vazao:     %.1f MB/s de carga util\n",
		float64(r)*float64(*tamanhoCorpo)/decorrido/(1024*1024))
}
