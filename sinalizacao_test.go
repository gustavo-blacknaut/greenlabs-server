package main

// Teste de ponta a ponta: sobe o servidor de verdade numa porta livre e fala
// com ele por TCP, montando os quadros WebSocket à mão. Assim o handshake, o
// enquadramento e as regras de sala são exercitados juntos, do jeito que o
// navegador faz.

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

type clienteTeste struct {
	conexao net.Conn
	leitor  *bufio.Reader
}

func conectar(t *testing.T, endereco string) *clienteTeste {
	t.Helper()
	conexao, err := net.Dial("tcp", endereco)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	chave := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef"))
	pedido := fmt.Sprintf(
		"GET / HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n"+
			"Sec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", endereco, chave)
	if _, err := conexao.Write([]byte(pedido)); err != nil {
		t.Fatalf("handshake: %v", err)
	}

	leitor := bufio.NewReader(conexao)
	_ = conexao.SetReadDeadline(time.Now().Add(3 * time.Second))
	status, err := leitor.ReadString('\n')
	if err != nil {
		t.Fatalf("resposta do handshake: %v", err)
	}
	if !strings.Contains(status, "101") {
		t.Fatalf("esperava 101, veio %q", strings.TrimSpace(status))
	}
	for {
		linha, err := leitor.ReadString('\n')
		if err != nil {
			t.Fatalf("cabecalhos: %v", err)
		}
		if strings.TrimSpace(linha) == "" {
			break
		}
	}
	return &clienteTeste{conexao: conexao, leitor: leitor}
}

func (c *clienteTeste) fechar() { c.conexao.Close() }

func (c *clienteTeste) enviar(t *testing.T, texto string) {
	t.Helper()
	dados := []byte(texto)
	quadro := []byte{0x81}
	tamanho := len(dados)
	switch {
	case tamanho <= 125:
		quadro = append(quadro, byte(0x80|tamanho))
	case tamanho <= 0xFFFF:
		var ext [2]byte
		binary.BigEndian.PutUint16(ext[:], uint16(tamanho))
		quadro = append(quadro, 0x80|126)
		quadro = append(quadro, ext[:]...)
	default:
		var ext [8]byte
		binary.BigEndian.PutUint64(ext[:], uint64(tamanho))
		quadro = append(quadro, 0x80|127)
		quadro = append(quadro, ext[:]...)
	}
	mascara := []byte{0x11, 0x22, 0x33, 0x44}
	quadro = append(quadro, mascara...)
	for i, b := range dados {
		quadro = append(quadro, b^mascara[i&3])
	}
	if _, err := c.conexao.Write(quadro); err != nil {
		t.Fatalf("envio: %v", err)
	}
}

// receberTipo lê até chegar uma mensagem do tipo pedido, pulando quadros de
// controle e mensagens que não interessam a esse passo do teste.
func (c *clienteTeste) receberTipo(t *testing.T, tipo string) map[string]any {
	t.Helper()
	limite := time.Now().Add(4 * time.Second)
	for time.Now().Before(limite) {
		_ = c.conexao.SetReadDeadline(limite)

		var cabecalho [2]byte
		if _, err := io.ReadFull(c.leitor, cabecalho[:]); err != nil {
			t.Fatalf("leitura do cabecalho: %v", err)
		}
		opcode := cabecalho[0] & 0x0F
		if cabecalho[1]&0x80 != 0 {
			t.Fatal("servidor nao pode mascarar quadros")
		}
		tamanho := uint64(cabecalho[1] & 0x7F)
		switch tamanho {
		case 126:
			var ext [2]byte
			if _, err := io.ReadFull(c.leitor, ext[:]); err != nil {
				t.Fatalf("tamanho estendido: %v", err)
			}
			tamanho = uint64(binary.BigEndian.Uint16(ext[:]))
		case 127:
			var ext [8]byte
			if _, err := io.ReadFull(c.leitor, ext[:]); err != nil {
				t.Fatalf("tamanho estendido: %v", err)
			}
			tamanho = binary.BigEndian.Uint64(ext[:])
		}
		dados := make([]byte, tamanho)
		if _, err := io.ReadFull(c.leitor, dados); err != nil {
			t.Fatalf("corpo: %v", err)
		}
		if opcode != opTexto {
			continue // ping, pong ou close do servidor
		}

		var m map[string]any
		if err := json.Unmarshal(dados, &m); err != nil {
			t.Fatalf("json invalido do servidor: %s", dados)
		}
		if m["type"] == tipo {
			return m
		}
	}
	t.Fatalf("nao chegou mensagem do tipo %q no prazo", tipo)
	return nil
}

func subirServidorDeTeste(t *testing.T) *Servidor {
	t.Helper()
	s, err := Iniciar(0) // porta 0: o sistema escolhe uma livre
	if err != nil {
		t.Fatalf("subir servidor: %v", err)
	}
	t.Cleanup(s.Fechar)
	return s
}

func TestFluxoDeSala(t *testing.T) {
	servidor := subirServidorDeTeste(t)
	endereco := fmt.Sprintf("127.0.0.1:%d", servidor.Port)

	alice := conectar(t, endereco)
	defer alice.fechar()
	alice.enviar(t, `{"type":"join","roomId":"sala-teste","name":"Alice"}`)

	entradaAlice := alice.receberTipo(t, "joined")
	idAlice, _ := entradaAlice["peerId"].(string)
	if idAlice == "" {
		t.Fatal("joined sem peerId")
	}
	if n := entradaAlice["count"].(float64); n != 1 {
		t.Fatalf("primeira entrada devia contar 1, contou %v", n)
	}
	if pares := entradaAlice["peers"].([]any); len(pares) != 0 {
		t.Fatalf("sala nova devia vir sem pares, veio %d", len(pares))
	}

	bob := conectar(t, endereco)
	defer bob.fechar()
	bob.enviar(t, `{"type":"join","roomId":"sala-teste","name":"Bob"}`)

	entradaBob := bob.receberTipo(t, "joined")
	idBob, _ := entradaBob["peerId"].(string)
	if n := entradaBob["count"].(float64); n != 2 {
		t.Fatalf("segunda entrada devia contar 2, contou %v", n)
	}
	pares := entradaBob["peers"].([]any)
	if len(pares) != 1 {
		t.Fatalf("Bob devia ver 1 par, viu %d", len(pares))
	}
	if primeiro := pares[0].(map[string]any); primeiro["peerId"] != idAlice || primeiro["name"] != "Alice" {
		t.Fatalf("par listado errado: %v", primeiro)
	}

	aviso := alice.receberTipo(t, "peer-joined")
	if aviso["peerId"] != idBob || aviso["name"] != "Bob" {
		t.Fatalf("aviso de entrada errado: %v", aviso)
	}

	// Repasse: o servidor precisa manter todos os campos originais e carimbar
	// o remetente verdadeiro.
	bob.enviar(t, fmt.Sprintf(`{"type":"offer","to":%q,"sdp":"v=0 teste","from":"forjado"}`, idAlice))
	oferta := alice.receberTipo(t, "offer")
	if oferta["from"] != idBob {
		t.Fatalf("from devia ser o id real de Bob (%s), veio %v", idBob, oferta["from"])
	}
	if oferta["sdp"] != "v=0 teste" {
		t.Fatalf("sdp perdido no repasse: %v", oferta["sdp"])
	}

	// Ping: o carimbo do cliente volta igual, para o RTT ser medido no relógio
	// de quem perguntou.
	alice.enviar(t, `{"type":"ping","timestamp":1234567890,"rtt":42}`)
	pong := alice.receberTipo(t, "pong")
	if pong["timestamp"].(float64) != 1234567890 {
		t.Fatalf("pong devolveu outro timestamp: %v", pong["timestamp"])
	}

	pings := alice.receberTipo(t, "room-pings")
	mapa := pings["pings"].(map[string]any)
	if mapa[idAlice].(float64) != 42 {
		t.Fatalf("ping de Alice devia ser 42, veio %v", mapa[idAlice])
	}

	// Saída: quem fica precisa ser avisado.
	bob.fechar()
	saida := alice.receberTipo(t, "peer-left")
	if saida["peerId"] != idBob {
		t.Fatalf("peer-left com id errado: %v", saida["peerId"])
	}
}

func TestSalaPadraoQuandoNaoInformada(t *testing.T) {
	servidor := subirServidorDeTeste(t)
	endereco := fmt.Sprintf("127.0.0.1:%d", servidor.Port)

	cliente := conectar(t, endereco)
	defer cliente.fechar()
	cliente.enviar(t, `{"type":"join"}`)
	cliente.receberTipo(t, "joined")

	if n := servidor.hub.TotalSalas(); n != 1 {
		t.Fatalf("esperava 1 sala, tem %d", n)
	}
	servidor.hub.mu.RLock()
	_, existe := servidor.hub.salas[salaPadrao]
	servidor.hub.mu.RUnlock()
	if !existe {
		t.Fatalf("sem roomId a sala devia ser %q", salaPadrao)
	}
}

func TestComCampoFrom(t *testing.T) {
	casos := []struct {
		entrada  string
		esperado string
	}{
		{`{"type":"ice"}`, `{"type":"ice","from":"abc"}`},
		{`{}`, `{"from":"abc"}`},
		{`{"a":1}` + "\n", `{"a":1,"from":"abc"}`},
		// "from" do cliente fica antes do nosso: JSON.parse fica com o último.
		{`{"from":"forjado"}`, `{"from":"forjado","from":"abc"}`},
		// Não é objeto: devolve intacto em vez de gerar JSON quebrado.
		{`[1,2]`, `[1,2]`},
	}
	for _, caso := range casos {
		obtido := string(comCampoFrom([]byte(caso.entrada), "abc"))
		if obtido != caso.esperado {
			t.Errorf("comCampoFrom(%q) = %q, esperava %q", caso.entrada, obtido, caso.esperado)
		}
	}
}

func TestResolverPorta(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("SERVER_PORT", "")
	if p := ResolverPorta(0); p != 25640 {
		t.Errorf("sem nada configurado devia cair em 25640, deu %d", p)
	}
	if p := ResolverPorta(3001); p != 3001 {
		t.Errorf("--port devia ganhar, deu %d", p)
	}

	t.Setenv("SERVER_PORT", "27015")
	if p := ResolverPorta(0); p != 27015 {
		t.Errorf("SERVER_PORT devia valer, deu %d", p)
	}
	t.Setenv("PORT", "3001")
	if p := ResolverPorta(0); p != 3001 {
		t.Errorf("PORT tem prioridade sobre SERVER_PORT, deu %d", p)
	}
}

func TestCarregarEnvNaoSobrescreveAmbiente(t *testing.T) {
	caminho := t.TempDir() + string(os.PathSeparator) + ".env"
	conteudo := "# comentario\nPORT=9999\nOUTRA=\"com aspas\"\n"
	if err := os.WriteFile(caminho, []byte(conteudo), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PORT", "3001") // já definida: o arquivo não pode mandar
	t.Setenv("OUTRA", "")
	os.Unsetenv("OUTRA")

	if !CarregarEnv(caminho) {
		t.Fatal("CarregarEnv devia ter lido o arquivo")
	}
	if os.Getenv("PORT") != "3001" {
		t.Errorf("PORT do ambiente foi sobrescrita: %q", os.Getenv("PORT"))
	}
	if os.Getenv("OUTRA") != "com aspas" {
		t.Errorf("aspas nao foram removidas: %q", os.Getenv("OUTRA"))
	}
}
