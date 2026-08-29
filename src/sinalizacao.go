package main

// Servidor HTTP + WebSocket. As rotas e os corpos de resposta são os mesmos do
// signaling.js, para os painéis e scripts que já leem /rooms e /stats
// continuarem funcionando sem alteração.

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// intervaloPing é de quanto em quanto tempo o servidor cutuca cada conexão.
// O navegador responde pong sozinho; quem não responde estoura o prazo de
// leitura e é recolhido.
const intervaloPing = 3 * time.Second

type Servidor struct {
	hub  *Hub
	http *http.Server
	rede net.Listener
	Port int
}

// sfu pode ser nil: sem ele o servidor so apresenta as pessoas umas as outras
// e o video vai direto entre elas, que e o modo padrao.
func Iniciar(porta int, sfu *SFU) (*Servidor, error) {
	hub := NovoHub(sfu)
	s := &Servidor{hub: hub}

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.rotear)

	s.http = &http.Server{
		Handler: mux,
		// Sem prazo de leitura no servidor HTTP: uma conexão que vira WebSocket
		// fica aberta por horas, e o prazo por quadro é tratado no Conn.
		ReadHeaderTimeout: 10 * time.Second,
	}

	ouvinte, err := net.Listen("tcp", ":"+itoa(porta))
	if err != nil {
		return nil, err
	}
	s.rede = ouvinte
	s.Port = ouvinte.Addr().(*net.TCPAddr).Port

	go func() {
		if err := s.http.Serve(ouvinte); err != nil && !errors.Is(err, http.ErrServerClosed) {
			registrar("ERRO HTTP: %v", err)
			os.Exit(1)
		}
	}()

	registrar("Servidor de Sinalizacao GreenLabs rodando em http://0.0.0.0:%d", s.Port)
	return s, nil
}

func (s *Servidor) Fechar() {
	s.hub.Parar()
	ctx, cancelar := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancelar()
	_ = s.http.Shutdown(ctx)
}

func (s *Servidor) rotear(w http.ResponseWriter, r *http.Request) {
	if ehPedidoWebSocket(r) {
		s.atenderWebSocket(w, r)
		return
	}

	caminho := r.URL.Path
	switch {
	case strings.HasPrefix(caminho, "/rooms"):
		s.responderSalas(w)
	case strings.HasPrefix(caminho, "/stats"):
		s.responderEstatisticas(w)
	default:
		s.responderRaiz(w)
	}
}

func ehPedidoWebSocket(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		cabecalhoContem(r.Header.Get("Connection"), "upgrade")
}

// --------------------------------------------------------------- WebSocket

func (s *Servidor) atenderWebSocket(w http.ResponseWriter, r *http.Request) {
	conexao, err := AceitarWebSocket(w, r)
	if err != nil {
		http.Error(w, "handshake WebSocket invalido", http.StatusBadRequest)
		return
	}

	p := s.hub.NovoPeer(conexao)
	go conexao.Escritor()
	go baterCoracao(conexao)

	registrar("CONEXAO: id=%s ip=%s", p.id, enderecoRemoto(r))

	defer func() {
		s.hub.Sair(p)
		conexao.Fechar()
	}()

	for {
		dados, err := conexao.Ler()
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				registrar("DEAD PEER / CRASH DETECTADO: id=%s", p.id)
			} else {
				registrar("DESCONECTADO: id=%s sala=%s", p.id, ouTraco(p.Sala()))
			}
			return
		}
		s.hub.Tratar(p, dados)
	}
}

func baterCoracao(c *Conn) {
	tique := time.NewTicker(intervaloPing)
	defer tique.Stop()
	for {
		select {
		case <-c.Fechado():
			return
		case <-tique.C:
			if err := c.EnviarPing(); err != nil {
				return
			}
		}
	}
}

func enderecoRemoto(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		if r.RemoteAddr == "" {
			return "-"
		}
		return r.RemoteAddr
	}
	return host
}

func ouTraco(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// -------------------------------------------------------------- respostas

type participanteJSON struct {
	ID     string `json:"id"`
	Nome   string `json:"nome"`
	PingMs int64  `json:"pingMs"`
}

type salaJSON struct {
	Total         int                `json:"total"`
	Participantes []participanteJSON `json:"participantes"`
}

type respostaSalas struct {
	OK         bool                `json:"ok"`
	Aplicativo string              `json:"aplicativo"`
	Salas      map[string]salaJSON `json:"salas"`
}

type estatisticasJSON struct {
	IniciadoEm      string `json:"startedAt"`
	TotalConexoes   uint64 `json:"totalConnections"`
	TotalRepassadas uint64 `json:"totalMessagesRelayed"`
}

type respostaEstatisticas struct {
	OK           bool             `json:"ok"`
	Estatisticas estatisticasJSON `json:"estatisticas"`
	SalasAtivas  int              `json:"salasAtivas"`
}

type respostaRaiz struct {
	OK          bool   `json:"ok"`
	Mensagem    string `json:"mensagem"`
	SalasAtivas int    `json:"salasAtivas"`
}

func (s *Servidor) responderSalas(w http.ResponseWriter) {
	s.hub.mu.RLock()
	salas := make(map[string]salaJSON, len(s.hub.salas))
	for id, membros := range s.hub.salas {
		lista := make([]participanteJSON, 0, len(membros))
		for _, p := range membros {
			lista = append(lista, participanteJSON{ID: p.id, Nome: p.Nome(), PingMs: p.pingMs.Load()})
		}
		salas[id] = salaJSON{Total: len(membros), Participantes: lista}
	}
	s.hub.mu.RUnlock()

	escreverJSON(w, respostaSalas{
		OK:         true,
		Aplicativo: "Sinalizacao GreenLabs PT-BR",
		Salas:      salas,
	}, true)
}

func (s *Servidor) responderEstatisticas(w http.ResponseWriter) {
	escreverJSON(w, respostaEstatisticas{
		OK: true,
		Estatisticas: estatisticasJSON{
			IniciadoEm:      s.hub.iniciadoEm,
			TotalConexoes:   s.hub.totalConexoes.Load(),
			TotalRepassadas: s.hub.totalRepassadas.Load(),
		},
		SalasAtivas: s.hub.TotalSalas(),
	}, true)
}

func (s *Servidor) responderRaiz(w http.ResponseWriter) {
	escreverJSON(w, respostaRaiz{
		OK:          true,
		Mensagem:    "Servidor de Sinalizacao GreenLabs Ativo",
		SalasAtivas: s.hub.TotalSalas(),
	}, false)
}

func escreverJSON(w http.ResponseWriter, valor any, identado bool) {
	w.Header().Set("content-type", "application/json; charset=utf-8")
	codificador := json.NewEncoder(w)
	if identado {
		codificador.SetIndent("", "  ")
	}
	_ = codificador.Encode(valor)
}
