package main

import (
	"fmt"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSalaCheiaNaoAfetaOutraSala(t *testing.T) {
	t.Setenv("GREENLABS_MAX_POR_SALA", "1")
	h := NovoHub(nil)
	defer h.Parar()
	novo := func() *Peer {
		return h.NovoPeer(&Conn{saida: make(chan quadroSaida, tamanhoFilaSaida), fechado: make(chan struct{})})
	}
	primeiro, excedente, outra := novo(), novo(), novo()
	h.entrar(primeiro, &mensagemEntrada{Sala: "a"})
	h.entrar(excedente, &mensagemEntrada{Sala: "a"})
	h.entrar(outra, &mensagemEntrada{Sala: "b"})
	if primeiro.Sala() != "a" || outra.Sala() != "b" || excedente.Sala() != "" {
		t.Fatal("admissao incorreta")
	}
	select {
	case <-excedente.conexao.Fechado():
	default:
		t.Fatal("excedente nao rejeitado")
	}
	if h.TotalSalas() != 2 {
		t.Fatal("sala legitima afetada")
	}
}

func TestLimiteGlobalEEncerramento(t *testing.T) {
	t.Setenv("GREENLABS_MAX_CONEXOES", "1")
	s := subirServidorDeTeste(t)
	c := conectar(t, fmt.Sprintf("127.0.0.1:%d", s.Port))
	defer c.fechar()
	c.enviar(t, `{"type":"join","roomId":"teste"}`)
	c.receberTipo(t, "joined")
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Upgrade", "websocket")
	r.Header.Set("Connection", "upgrade")
	w := httptest.NewRecorder()
	s.rotear(w, r)
	if w.Code != 503 || w.Header().Get("Retry-After") != "5" {
		t.Fatalf("resposta: %d", w.Code)
	}
	s.Fechar()
	limite := time.Now().Add(time.Second)
	for len(s.vagas) != 0 && time.Now().Before(limite) {
		time.Sleep(time.Millisecond)
	}
	if len(s.vagas) != 0 {
		t.Fatal("conexao sequestrada nao liberada")
	}
}

// Opt-in: abre sockets reais somente contra o servidor local criado pelo teste.
// Mede sinalizacao, nao conexoes WebRTC nem capacidade de retransmitir video.
func TestCargaSinalizacao(t *testing.T) {
	quantos, _ := strconv.Atoi(os.Getenv("GREENLABS_TEST_CLIENTES"))
	if quantos == 0 {
		t.Skip("defina GREENLABS_TEST_CLIENTES para executar a carga local")
	}
	if quantos < 1 || quantos > 10000 {
		t.Fatal("use entre 1 e 10000 clientes")
	}
	s := subirServidorDeTeste(t)
	endereco := fmt.Sprintf("127.0.0.1:%d", s.Port)
	clientes := make([]*clienteTeste, 0, quantos)
	var leitores sync.WaitGroup
	var falhas atomic.Int64
	encerrar := make(chan struct{})
	defer func() {
		close(encerrar)
		for _, c := range clientes {
			c.fechar()
		}
		leitores.Wait()
	}()
	inicio := time.Now()
	for i := 0; i < quantos; i++ {
		c := conectar(t, endereco)
		clientes = append(clientes, c)
		c.enviar(t, fmt.Sprintf(`{"type":"join","roomId":"carga-%d","name":"teste"}`, i/100))
		c.receberTipo(t, "joined")
		// Descarrega os avisos continuamente, como faz um cliente saudavel.
		leitores.Add(1)
		go func() {
			defer leitores.Done()
			for {
				_ = c.conexao.SetReadDeadline(time.Now().Add(10 * time.Second))
				if _, err := c.leitor.ReadByte(); err != nil {
					select {
					case <-encerrar:
					default:
						falhas.Add(1)
					}
					return
				}
			}
		}()
		leitores.Add(1)
		go func() {
			defer leitores.Done()
			pulso := time.NewTicker(time.Second)
			defer pulso.Stop()
			for {
				select {
				case <-encerrar:
					return
				case <-pulso.C:
					c.enviar(t, `{"type":"ping","timestamp":1,"rtt":10}`)
				}
			}
		}()
	}
	time.Sleep(3 * time.Second)
	s.hub.mu.RLock()
	ativos := 0
	for _, sala := range s.hub.salas {
		ativos += len(sala)
	}
	s.hub.mu.RUnlock()
	if ativos != quantos || falhas.Load() != 0 {
		t.Fatalf("ativos=%d/%d, falhas=%d", ativos, quantos, falhas.Load())
	}
	t.Logf("%d sockets ativos em %d salas; entrada e sustentacao: %s (sem midia)", ativos, s.hub.TotalSalas(), time.Since(inicio))
}

func TestPedidosDeChaveConcorrentes(t *testing.T) {
	f := &faixaEncaminhada{}
	agora := time.Now()
	var aceitos atomic.Int64
	var grupo sync.WaitGroup
	for i := 0; i < 10000; i++ {
		grupo.Add(1)
		go func() {
			defer grupo.Done()
			if f.permitirChave(agora) {
				aceitos.Add(1)
			}
		}()
	}
	grupo.Wait()
	if aceitos.Load() != 1 {
		t.Fatalf("pedidos aceitos: %d", aceitos.Load())
	}
	if !f.permitirChave(agora.Add(500 * time.Millisecond)) {
		t.Fatal("recuperacao posterior bloqueada")
	}
}

func TestFilaLimitadaPorBytes(t *testing.T) {
	c := &Conn{saida: make(chan quadroSaida, tamanhoFilaSaida), fechado: make(chan struct{})}
	dados := make([]byte, 1<<20)
	for i := 0; i < 2; i++ {
		if err := c.EnviarTexto(dados); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.EnviarTexto([]byte{1}); err != ErrFilaCheia {
		t.Fatalf("erro: %v", err)
	}
	if c.bytesNaFila.Load() != bytesMaximosFilaSaida {
		t.Fatal("contagem de bytes incorreta")
	}
	select {
	case <-c.Fechado():
	default:
		t.Fatal("cliente lento nao foi encerrado")
	}
}

func BenchmarkCoalescerChave(b *testing.B) {
	f := &faixaEncaminhada{}
	agora := time.Now()
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			f.permitirChave(agora)
		}
	})
}
