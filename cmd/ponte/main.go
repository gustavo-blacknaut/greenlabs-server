// greenlabs-ponte: dá TLS a quem não tem.
//
// O problema que isto resolve: uma página em HTTPS não consegue abrir `ws://`.
// O navegador bloqueia, e não existe configuração, header ou framework que
// mude - é regra dele. Quem hospeda o servidor em casa quase nunca tem
// certificado, então o site simplesmente não alcança essa pessoa, por mais que
// o servidor dela esteja no ar.
//
// A ponte fica no meio: o navegador fala `wss://` com ela, ela fala `ws://`
// com o servidor de destino, e copia bytes de um lado para o outro. Nada é
// interpretado - é um cano.
//
//	wss://ponte.seudominio.com.br/ws?alvo=meuservidor.com:25640
//
// ---------------------------------------------------------------------------
//
// O PERIGO, e por que o código abaixo é do jeito que é.
//
// Um proxy que aceita destino pela URL é, por padrão, um proxy ABERTO: quem
// souber o endereço manda a máquina abrir conexão para onde quiser. Isso não é
// um detalhe, são duas portas escancaradas:
//
//   - a rede de dentro. `alvo=127.0.0.1:6379` alcança o Redis local;
//     `alvo=169.254.169.254:80` alcança o serviço de metadados do provedor, que
//     em várias nuvens entrega credencial da máquina. O firewall não protege
//     disso, porque quem abre a conexão é um processo de dentro.
//   - o resto do mundo. Sem limite, a ponte vira ferramenta de terceiros para
//     bater em outros hosts com o IP da sua VPS na frente - e a reclamação
//     chega para você.
//
// Daí as três regras, nesta ordem: resolve o nome primeiro e recusa qualquer IP
// que não seja público; recusa porta fora da faixa esperada; e limita conexões
// simultâneas por IP de origem. A primeira é a que importa - sem ela, o resto
// é enfeite.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	endereco    = flag.String("addr", "127.0.0.1:4070", "onde escutar (deixe no loopback: o nginx termina o TLS)")
	porLimite   = flag.Int("por-ip", 4, "conexoes simultaneas por IP de origem")
	totalMaximo = flag.Int("total", 200, "conexoes simultaneas no total")
	portasOk    = flag.String("portas", "1024-65535", "faixa de portas de destino permitida")
	ocioso      = flag.Duration("ocioso", 5*time.Minute, "derruba a conexao sem trafego por este tempo")
)

func main() {
	flag.Parse()

	minima, maxima, err := lerFaixa(*portasOk)
	if err != nil {
		log.Fatalf("faixa de portas invalida: %v", err)
	}

	p := &ponte{
		portaMinima: minima,
		portaMaxima: maxima,
		porIP:       map[string]int{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/ws", p.atender)
	mux.HandleFunc("/saude", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"ok":true,"abertas":%d}`, p.abertas())
	})

	servidor := &http.Server{
		Addr:              *endereco,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("ponte ouvindo em %s (portas de destino %d-%d, %d por IP)",
		*endereco, minima, maxima, *porLimite)
	if err := servidor.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

type ponte struct {
	portaMinima int
	portaMaxima int

	mu    sync.Mutex
	porIP map[string]int
	total int
}

func (p *ponte) abertas() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.total
}

func (p *ponte) reservar(ip string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.total >= *totalMaximo || p.porIP[ip] >= *porLimite {
		return false
	}
	p.porIP[ip]++
	p.total++
	return true
}

func (p *ponte) devolver(ip string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.porIP[ip]--
	if p.porIP[ip] <= 0 {
		delete(p.porIP, ip)
	}
	p.total--
}

func (p *ponte) atender(w http.ResponseWriter, r *http.Request) {
	alvo := r.URL.Query().Get("alvo")
	if alvo == "" {
		http.Error(w, "falta ?alvo=host:porta", http.StatusBadRequest)
		return
	}

	destino, err := p.validar(alvo)
	if err != nil {
		// O motivo vai para o cliente de proposito: sem ele, quem esta tentando
		// usar a ponte de boa-fe nao tem como saber o que corrigir. Nenhuma das
		// mensagens revela nada sobre a rede de dentro alem do "nao".
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}

	origem, _, _ := net.SplitHostPort(r.RemoteAddr)
	if xff := r.Header.Get("X-Real-IP"); xff != "" {
		origem = xff
	}
	if !p.reservar(origem) {
		http.Error(w, "muitas conexoes; tente daqui a pouco", http.StatusTooManyRequests)
		return
	}
	defer p.devolver(origem)

	// Sequestra o socket do cliente e repassa o handshake cru. A ponte não
	// interpreta WebSocket - não precisa: o handshake é HTTP, e o resto é fluxo
	// de bytes. Sem parser, não há bug de parser.
	sequestrador, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "servidor sem suporte a upgrade", http.StatusInternalServerError)
		return
	}

	deLa, err := net.DialTimeout("tcp", destino, 10*time.Second)
	if err != nil {
		http.Error(w, "nao consegui alcancar o destino", http.StatusBadGateway)
		return
	}
	defer deLa.Close()

	daqui, buffer, err := sequestrador.Hijack()
	if err != nil {
		return
	}
	defer daqui.Close()

	// Reescreve a requisição para o destino: mesmo caminho e mesmos cabeçalhos
	// de upgrade, com o Host trocado pelo de lá.
	pedido := r.Clone(context.Background())
	pedido.URL.Scheme = "http"
	pedido.URL.Host = destino
	pedido.Host = destino
	pedido.RequestURI = ""
	pedido.URL.RawQuery = removerAlvo(r.URL.Query())

	if err := pedido.Write(deLa); err != nil {
		return
	}

	log.Printf("%s -> %s", origem, destino)

	// Se o cliente já mandou bytes junto do handshake, eles estão no buffer.
	if buffer != nil && buffer.Reader.Buffered() > 0 {
		if _, err := io.CopyN(deLa, buffer, int64(buffer.Reader.Buffered())); err != nil {
			return
		}
	}

	copiar(daqui, deLa)
}

// copiar liga os dois lados e volta quando qualquer um fecha.
func copiar(a, b net.Conn) {
	pronto := make(chan struct{}, 2)
	umLado := func(destino, origem net.Conn) {
		defer func() { pronto <- struct{}{} }()
		buf := make([]byte, 32*1024)
		for {
			_ = origem.SetReadDeadline(time.Now().Add(*ocioso))
			n, err := origem.Read(buf)
			if n > 0 {
				_ = destino.SetWriteDeadline(time.Now().Add(30 * time.Second))
				if _, errW := destino.Write(buf[:n]); errW != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}
	go umLado(a, b)
	go umLado(b, a)
	<-pronto
}

// validar é onde o proxy deixa de ser aberto.
func (p *ponte) validar(alvo string) (string, error) {
	alvo = strings.TrimSpace(alvo)
	alvo = strings.TrimPrefix(alvo, "ws://")
	alvo = strings.TrimPrefix(alvo, "http://")
	alvo = strings.TrimSuffix(alvo, "/")

	host, portaTexto, err := net.SplitHostPort(alvo)
	if err != nil {
		return "", fmt.Errorf("use host:porta")
	}
	porta, err := strconv.Atoi(portaTexto)
	if err != nil || porta < p.portaMinima || porta > p.portaMaxima {
		return "", fmt.Errorf("porta fora da faixa permitida (%d-%d)", p.portaMinima, p.portaMaxima)
	}

	// Resolve ANTES de conectar e olha TODOS os endereços.
	//
	// Olhar só o primeiro deixaria passar um nome que devolve um IP público e
	// um privado: bastaria a sorte do resolvedor. E recusar por texto ("começa
	// com 192.168") não serve de nada, porque quem quer entrar registra um nome
	// qualquer apontando para onde quiser.
	ips, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("nao consegui resolver o endereco")
	}
	for _, ip := range ips {
		if !ehPublico(ip.IP) {
			return "", fmt.Errorf("destino aponta para endereco interno; a ponte so alcanca a internet")
		}
	}

	return net.JoinHostPort(host, portaTexto), nil
}

// ehPublico recusa tudo que não seja internet aberta.
func ehPublico(ip net.IP) bool {
	if ip == nil || ip.IsLoopback() || ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsInterfaceLocalMulticast() || ip.IsMulticast() ||
		ip.IsUnspecified() {
		return false
	}
	// 169.254.169.254 já cai em link-local, mas a faixa de teste e a de
	// documentacao tambem nao tem por que ser alcancaveis.
	for _, faixa := range []string{
		"100.64.0.0/10", // CGNAT
		"192.0.0.0/24",  // IETF
		"192.0.2.0/24",  // documentacao
		"198.18.0.0/15", // benchmark
		"198.51.100.0/24",
		"203.0.113.0/24",
		"240.0.0.0/4", // reservado
	} {
		_, rede, err := net.ParseCIDR(faixa)
		if err == nil && rede.Contains(ip) {
			return false
		}
	}
	return true
}

func removerAlvo(q url.Values) string {
	q.Del("alvo")
	return q.Encode()
}

func lerFaixa(texto string) (int, int, error) {
	partes := strings.SplitN(texto, "-", 2)
	if len(partes) != 2 {
		return 0, 0, fmt.Errorf("use minima-maxima")
	}
	a, err1 := strconv.Atoi(strings.TrimSpace(partes[0]))
	b, err2 := strconv.Atoi(strings.TrimSpace(partes[1]))
	if err1 != nil || err2 != nil || a < 1 || b > 65535 || a > b {
		return 0, 0, fmt.Errorf("faixa invalida")
	}
	return a, b, nil
}
