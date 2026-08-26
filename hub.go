package main

// Salas, participantes e as regras de sinalização. É a tradução direta do
// signaling.js: mesmos nomes de mensagem, mesmos campos, mesma ordem de envio.

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const salaPadrao = "call1"
const tamanhoMaximoNome = 40

type Peer struct {
	id      string
	conexao *Conn

	mu   sync.RWMutex
	nome string
	sala string

	pingMs atomic.Int64
}

func (p *Peer) Nome() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.nome
}

func (p *Peer) Sala() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sala
}

func (p *Peer) enviar(dados []byte) {
	if err := p.conexao.EnviarTexto(dados); err == ErrFilaCheia {
		registrar("FILA CHEIA: id=%s derrubado por nao acompanhar o fluxo", p.id)
	}
}

type Hub struct {
	mu    sync.RWMutex
	salas map[string]map[string]*Peer

	// Cada cliente manda ping uma vez por segundo. No Node, cada ping disparava
	// um broadcast para a sala inteira: n pings/s x n destinatários, ou seja
	// tráfego crescendo com o quadrado da sala. Marcar a sala como suja e
	// esvaziar uma vez por segundo deixa isso linear sem perder informação —
	// os clientes não pingam mais rápido que isso de qualquer forma.
	pingMu    sync.Mutex
	pingSujas map[string]struct{}

	// Quando presente, os participantes não são apresentados uns aos outros:
	// cada um negocia só com o servidor, que recebe uma vez e reenvia. É o que
	// permite trocar malha por retransmissor sem mudar nenhum cliente.
	sfu *SFU

	iniciadoEm      string
	totalConexoes   atomic.Uint64
	totalRepassadas atomic.Uint64
	encerrar        chan struct{}
	encerrarUmaVez  sync.Once
}

func NovoHub(sfu *SFU) *Hub {
	h := &Hub{
		sfu:        sfu,
		salas:      make(map[string]map[string]*Peer),
		pingSujas:  make(map[string]struct{}),
		iniciadoEm: time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		encerrar:   make(chan struct{}),
	}
	go h.esvaziarPings()
	return h
}

func (h *Hub) Parar() {
	h.encerrarUmaVez.Do(func() { close(h.encerrar) })
}

func (h *Hub) NovoPeer(c *Conn) *Peer {
	p := &Peer{id: novoUUID(), conexao: c}
	h.totalConexoes.Add(1)
	return p
}

func (h *Hub) TotalSalas() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.salas)
}

// ---------------------------------------------------------------- mensagens

type mensagemEntrada struct {
	Tipo      string          `json:"type"`
	Para      string          `json:"to"`
	Sala      string          `json:"roomId"`
	Nome      string          `json:"name"`
	Timestamp json.RawMessage `json:"timestamp"`
	RTT       json.RawMessage `json:"rtt"`
}

func (h *Hub) Tratar(p *Peer, bruto []byte) {
	var m mensagemEntrada
	if err := json.Unmarshal(bruto, &m); err != nil {
		return // mensagem que não é JSON é ignorada, igual ao Node
	}

	switch m.Tipo {
	case "ping":
		h.responderPing(p, &m)
		return
	case "join":
		h.entrar(p, &m)
		return
	}

	// Qualquer outra coisa é repasse ponto a ponto: SDP, ICE, controles do app.
	sala := p.Sala()
	if sala == "" || m.Para == "" {
		return
	}

	// Endereçado ao servidor: é negociação de mídia com o retransmissor, não
	// recado para outra pessoa.
	if h.sfu != nil && m.Para == IDdoSFU {
		h.entregarAoSFU(sala, p.id, &m, bruto)
		return
	}
	h.mu.RLock()
	var alvo *Peer
	if membros, ok := h.salas[sala]; ok {
		alvo = membros[m.Para]
	}
	h.mu.RUnlock()
	if alvo == nil {
		return
	}
	h.totalRepassadas.Add(1)
	alvo.enviar(comCampoFrom(bruto, p.id))
}

// comCampoFrom acrescenta "from" ao JSON original sem desmontar e remontar o
// objeto. Esse é o caminho mais percorrido do servidor (cada candidato ICE
// passa por aqui), e reserializar só para incluir um campo custa uma volta
// inteira de parse + alloc por mensagem.
//
// O campo vai no fim de propósito: com chave repetida o JSON.parse do
// navegador fica com a última, que é exatamente o que o spread do Node fazia
// ({...message, from}). Assim um cliente não consegue forjar o remetente.
func comCampoFrom(bruto []byte, de string) []byte {
	corpo := bytes.TrimRight(bruto, " \t\r\n")
	if len(corpo) < 2 || corpo[0] != '{' || corpo[len(corpo)-1] != '}' {
		return bruto
	}
	sufixo := `,"from":"` + de + `"}`
	if len(bytes.TrimSpace(corpo[1:len(corpo)-1])) == 0 {
		sufixo = `"from":"` + de + `"}`
	}
	saida := make([]byte, 0, len(corpo)+len(sufixo))
	saida = append(saida, corpo[:len(corpo)-1]...)
	saida = append(saida, sufixo...)
	return saida
}

// ------------------------------------------------------------------- entrar

func (h *Hub) entrar(p *Peer, m *mensagemEntrada) {
	h.Sair(p)

	sala := strings.TrimSpace(m.Sala)
	if sala == "" {
		sala = salaPadrao
	}
	nome := m.Nome
	if nome == "" {
		nome = "Usuario"
	}
	if r := []rune(nome); len(r) > tamanhoMaximoNome {
		nome = string(r[:tamanhoMaximoNome])
	}

	p.mu.Lock()
	p.sala = sala
	p.nome = nome
	p.mu.Unlock()

	h.mu.Lock()
	membros, ok := h.salas[sala]
	if !ok {
		membros = make(map[string]*Peer)
		h.salas[sala] = membros
	}
	// A lista vai para quem entrou, então é tirada antes da própria inclusão.
	jaEstavam := make([]*Peer, 0, len(membros))
	for _, outro := range membros {
		jaEstavam = append(jaEstavam, outro)
	}
	membros[p.id] = p
	total := len(membros)
	h.mu.Unlock()

	registrar("ENTROU: sala=%s id=%s nome=%s total=%d", sala, p.id, nome, total)

	// Com SFU, quem entra negocia só com o servidor. A lista de pares vai
	// vazia de propósito: se fossem apresentados uns aos outros, cada cliente
	// abriria conexão direta com todos e o retransmissor não serviria para nada.
	if h.sfu != nil {
		jaEstavam = nil
		if err := h.sfu.Entrar(sala, p.id, func(bruto []byte) { p.enviar(comCampoFrom(bruto, IDdoSFU)) }); err != nil {
			registrar("[sfu] nao foi possivel abrir a midia para %s: %v", p.id, err)
		}
	}

	var b bytes.Buffer
	b.WriteString(`{"type":"joined","peerId":`)
	b.Write(textoJSON(p.id))
	b.WriteString(`,"peers":[`)
	for i, outro := range jaEstavam {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"peerId":`)
		b.Write(textoJSON(outro.id))
		b.WriteString(`,"name":`)
		b.Write(textoJSON(outro.Nome()))
		b.WriteString(`,"pingMs":`)
		b.WriteString(strconv.FormatInt(outro.pingMs.Load(), 10))
		b.WriteByte('}')
	}
	b.WriteString(`],"count":`)
	b.WriteString(strconv.Itoa(total))
	b.WriteByte('}')
	p.enviar(b.Bytes())

	var aviso bytes.Buffer
	aviso.WriteString(`{"type":"peer-joined","peerId":`)
	aviso.Write(textoJSON(p.id))
	aviso.WriteString(`,"name":`)
	aviso.Write(textoJSON(nome))
	aviso.WriteString(`,"count":`)
	aviso.WriteString(strconv.Itoa(total))
	aviso.WriteByte('}')
	pacote := aviso.Bytes()
	for _, outro := range jaEstavam {
		outro.enviar(pacote)
	}

	h.transmitirPings(sala)
}

// --------------------------------------------------------------------- sair

func (h *Hub) Sair(p *Peer) {
	p.mu.Lock()
	sala := p.sala
	p.sala = ""
	nome := p.nome
	p.mu.Unlock()
	if sala == "" {
		return
	}

	h.mu.Lock()
	membros, ok := h.salas[sala]
	if !ok {
		h.mu.Unlock()
		return
	}
	delete(membros, p.id)
	restantes := len(membros)
	sobreviventes := make([]*Peer, 0, restantes)
	for _, outro := range membros {
		sobreviventes = append(sobreviventes, outro)
	}
	if restantes == 0 {
		delete(h.salas, sala)
	}
	h.mu.Unlock()

	if h.sfu != nil {
		h.sfu.Sair(sala, p.id)
	}

	registrar("SAIDA: sala=%s id=%s nome=%s restantes=%d", sala, p.id, nome, restantes)

	var b bytes.Buffer
	b.WriteString(`{"type":"peer-left","peerId":`)
	b.Write(textoJSON(p.id))
	b.WriteByte('}')
	pacote := b.Bytes()
	for _, outro := range sobreviventes {
		outro.enviar(pacote)
	}

	if restantes == 0 {
		registrar("SALA VAZIA: removida=%s", sala)
		return
	}
	h.transmitirPings(sala)
}

// --------------------------------------------------------------------- ping

func (h *Hub) responderPing(p *Peer, m *mensagemEntrada) {
	agora := time.Now().UnixMilli()

	// O tempo de ida e volta é medido pelo próprio cliente, no relógio dele, e
	// devolvido aqui. Calcular (agoraServidor - horaCliente) mediria diferença
	// de relógio entre as máquinas, não latência.
	horaCliente := agora
	if v, ok := paraNumero(m.Timestamp); ok && v != 0 {
		horaCliente = int64(v)
	}
	if v, ok := paraNumero(m.RTT); ok && v >= 0 {
		ms := int64(math.Round(v))
		if ms < 1 {
			ms = 1
		}
		p.pingMs.Store(ms)
	}

	var b bytes.Buffer
	b.WriteString(`{"type":"pong","timestamp":`)
	b.WriteString(strconv.FormatInt(horaCliente, 10))
	b.WriteString(`,"serverTime":`)
	b.WriteString(strconv.FormatInt(agora, 10))
	b.WriteByte('}')
	p.enviar(b.Bytes())

	if sala := p.Sala(); sala != "" {
		h.pingMu.Lock()
		h.pingSujas[sala] = struct{}{}
		h.pingMu.Unlock()
	}
}

func (h *Hub) esvaziarPings() {
	tique := time.NewTicker(time.Second)
	defer tique.Stop()
	for {
		select {
		case <-h.encerrar:
			return
		case <-tique.C:
			h.pingMu.Lock()
			if len(h.pingSujas) == 0 {
				h.pingMu.Unlock()
				continue
			}
			pendentes := make([]string, 0, len(h.pingSujas))
			for sala := range h.pingSujas {
				pendentes = append(pendentes, sala)
			}
			h.pingSujas = make(map[string]struct{})
			h.pingMu.Unlock()

			for _, sala := range pendentes {
				h.transmitirPings(sala)
			}
		}
	}
}

func (h *Hub) transmitirPings(sala string) {
	h.mu.RLock()
	membros, ok := h.salas[sala]
	if !ok {
		h.mu.RUnlock()
		return
	}
	lista := make([]*Peer, 0, len(membros))
	for _, p := range membros {
		lista = append(lista, p)
	}
	h.mu.RUnlock()
	if len(lista) == 0 {
		return
	}

	var b bytes.Buffer
	b.WriteString(`{"type":"room-pings","pings":{`)
	for i, p := range lista {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(textoJSON(p.id))
		b.WriteByte(':')
		b.WriteString(strconv.FormatInt(p.pingMs.Load(), 10))
	}
	b.WriteString(`}}`)

	// Um pacote só, compartilhado por todos os destinatários: a fila de saída
	// guarda a referência, ninguém escreve nele depois daqui.
	pacote := b.Bytes()
	for _, p := range lista {
		p.enviar(pacote)
	}
}

// ------------------------------------------------------------------ auxiliar

func paraNumero(bruto json.RawMessage) (float64, bool) {
	s := strings.TrimSpace(string(bruto))
	if s == "" || s == "null" {
		return 0, false
	}
	s = strings.Trim(s, `"`)
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return v, true
}

func textoJSON(s string) []byte {
	b, err := json.Marshal(s)
	if err != nil {
		return []byte(`""`)
	}
	return b
}

// entregarAoSFU traduz a mensagem do cliente para o retransmissor.
//
// Os clientes falam em "sdp" (o web) ou "description" (o de Electron) para a
// mesma coisa, então os dois nomes são aceitos - o cliente não precisa saber
// com quem está falando.
func (h *Hub) entregarAoSFU(sala, peer string, m *mensagemEntrada, bruto []byte) {
	switch m.Tipo {
	case "offer", "answer":
		var corpo struct {
			SDP *struct {
				SDP string `json:"sdp"`
			} `json:"sdp"`
			Description *struct {
				SDP string `json:"sdp"`
			} `json:"description"`
		}
		if json.Unmarshal(bruto, &corpo) != nil {
			return
		}
		sdp := ""
		if corpo.SDP != nil {
			sdp = corpo.SDP.SDP
		} else if corpo.Description != nil {
			sdp = corpo.Description.SDP
		}
		if sdp != "" {
			h.sfu.Descricao(sala, peer, m.Tipo, sdp)
		}

	case "ice":
		var corpo struct {
			Candidate *struct {
				Candidate string `json:"candidate"`
				SDPMid    string `json:"sdpMid"`
			} `json:"candidate"`
		}
		if json.Unmarshal(bruto, &corpo) != nil || corpo.Candidate == nil {
			return
		}
		h.sfu.Candidato(sala, peer, corpo.Candidate.Candidate, corpo.Candidate.SDPMid)
	}
}
