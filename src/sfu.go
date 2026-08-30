package main

// SFU: um retransmissor de mídia.
//
// Sem ele o GreenLabs é uma malha — cada pessoa manda o próprio vídeo direto
// para cada uma das outras. Isso tem dois problemas que aparecem juntos:
//
//   1. O upload de quem transmite é multiplicado pelo tamanho da sala. Numa
//      chamada de 10 pessoas em 1080p são 40 Mbps saindo da máquina dele.
//
//   2. Os dois lados precisam se achar através dos roteadores. Quando a
//      travessia de NAT falha — e falha com frequência — não há vídeo nenhum,
//      mesmo com a sinalização funcionando perfeitamente.
//
// Com o SFU cada pessoa mantém UMA conexão, com o servidor. Ele recebe uma vez
// e reenvia para todos. O upload de quem transmite para de crescer com a sala, e
// o problema de NAT some: o servidor tem endereço público, então todo mundo
// alcança ele mesmo quando não alcançaria uns aos outros.
//
// O preço é o servidor passar a tocar mídia: banda e CPU proporcionais ao
// número de espectadores. Por isso ele é opcional, ligado por --sfu.

import (
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"net"
	"sync"
	"time"

	"github.com/pion/rtcp"
	"github.com/pion/webrtc/v4"
)

// Identificador do participante virtual que representa o servidor dentro da
// sala. Os clientes veem o SFU como se fosse mais uma pessoa, e é por isso que
// nenhum deles precisou mudar para funcionar com ele.
const IDdoSFU = "sfu"

// faixaEncaminhada é uma faixa que alguém publicou e que o servidor reenvia.
type faixaEncaminhada struct {
	dono   string // peerId de quem publicou
	origem *webrtc.TrackRemote
	saida  *webrtc.TrackLocalStaticRTP
}

type sessaoSFU struct {
	peer    string
	conexao *webrtc.PeerConnection
	enviar  func([]byte)

	mu      sync.Mutex
	saidas  map[string]*webrtc.TrackLocalStaticRTP // faixaID -> o que mandamos para ele
	fechada bool

	// Uma oferta nossa esperando resposta. Enquanto houver, não dá para fazer
	// outra: o WebRTC recusa com "have-local-offer -> SetLocal(offer)". Quando
	// aparece faixa nova nesse meio-tempo, fica anotado aqui e a renegociação
	// sai assim que a resposta chegar.
	renegociarDepois bool
}

// SFU guarda as sessões por sala e faz o encaminhamento entre elas.
type SFU struct {
	mu     sync.RWMutex
	salas  map[string]map[string]*sessaoSFU // sala -> peerId -> sessão
	faixas map[string][]*faixaEncaminhada   // sala -> faixas publicadas
	api    *webrtc.API
	config webrtc.Configuration
}

// NovoSFU monta o retransmissor.
//
// portaMidia é a porta UDP em que toda a mídia entra e sai. Fixar uma só
// importa muito em painel de jogos: lá se aloca um punhado de portas, e o
// resto não é encaminhado. Sem isso o pion sorteia uma porta efêmera por
// conexão, nenhuma delas chega de fora, e o participante fica com "connecting"
// até estourar o tempo.
//
// enderecoPublico é o endereço que o servidor anuncia. Num container ele só
// enxerga o próprio IP privado (172.18.0.x) e é isso que sai nos candidatos -
// inalcançável para quem está na internet. Em branco, o servidor descobre
// sozinho pelo STUN.
func NovoSFU(portaMidia int, enderecoPublico string) *SFU {
	// MediaEngine só com o que os clientes do GreenLabs falam. Registrar todos
	// os codecs padrão faria o servidor aceitar VP8 de um lado e H.264 de outro
	// dentro da mesma sala, e aí o reenvio não funcionaria: o SFU repassa
	// pacotes, não transcodifica.
	motor := &webrtc.MediaEngine{}
	if err := motor.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:     webrtc.MimeTypeH264,
			ClockRate:    90000,
			SDPFmtpLine:  "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
			RTCPFeedback: feedbackDeVideo(),
		},
		PayloadType: 102,
	}, webrtc.RTPCodecTypeVideo); err != nil {
		erroSFU("nao foi possivel registrar H.264: %v", err)
	}
	if err := motor.RegisterCodec(webrtc.RTPCodecParameters{
		RTPCodecCapability: webrtc.RTPCodecCapability{
			MimeType:  webrtc.MimeTypeOpus,
			ClockRate: 48000,
			Channels:  2,
		},
		PayloadType: 111,
	}, webrtc.RTPCodecTypeAudio); err != nil {
		erroSFU("nao foi possivel registrar Opus: %v", err)
	}

	// A extensao de cabecalho "mid" no RTP.
	//
	// Sem ela o servidor so consegue associar um pacote a uma faixa se o SSRC
	// tiver sido anunciado no SDP. O navegador que comeca a transmitir com
	// replaceTrack numa m-line ja negociada NAO anuncia SSRC nenhum: ele
	// simplesmente comeca a mandar. Sem a extensao, esses pacotes chegam e sao
	// descartados - a tela "nao aparecia para ninguem", sem erro em lugar
	// nenhum.
	//
	// Ela nao vem de graca porque nao chamamos RegisterDefaultCodecs: registrar
	// tudo faria o servidor aceitar VP8 de um lado e H.264 de outro, e o SFU
	// repassa pacote, nao transcodifica.
	const midURI = "urn:ietf:params:rtp-hdrext:sdes:mid"
	for _, tipo := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		if err := motor.RegisterHeaderExtension(
			webrtc.RTPHeaderExtensionCapability{URI: midURI}, tipo); err != nil {
			erroSFU("nao foi possivel registrar a extensao mid: %v", err)
		}
	}

	ajustes := webrtc.SettingEngine{}

	// O papel do DTLS deste lado fica FIXO em servidor.
	//
	// Quem oferece primeiro e o servidor, com setup:actpass; o cliente responde
	// "active" e vira o cliente do DTLS, o que faz este lado ser o servidor.
	// Mas quando e o CLIENTE que oferece depois - e ele oferece, ao comecar a
	// transmitir - o Pion escolhia o papel de novo, do zero, e podia escolher o
	// oposto. Trocar de papel no meio de uma conexao ja estabelecida e
	// justamente o que o navegador recusa com:
	//
	//   Failed to set remote answer sdp: Failed to apply the description for
	//   m= section with mid='0': Failed to set SSL role for the transport
	//
	// Compartilhar tela pelo site e pelo celular nao aparecia para ninguem por
	// causa disto. Fixar o papel faz a renegociacao vinda do cliente valer.
	ajustes.SetAnsweringDTLSRole(webrtc.DTLSRoleServer)

	if portaMidia > 0 {
		conexao, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4zero, Port: portaMidia})
		if err != nil {
			erroSFU("nao consegui abrir a porta de midia %d: %v", portaMidia, err)
			erroSFU("seguindo com porta sorteada; quem estiver fora da rede pode nao conectar")
		} else {
			ajustes.SetICEUDPMux(webrtc.NewICEUDPMux(nil, conexao))
			infoSFU("midia em udp/%d", portaMidia)
		}
	}

	if enderecoPublico == "" {
		enderecoPublico = descobrirIPPublico()
	}
	if enderecoPublico != "" {
		// Host e não Srflx. Tentei srflx primeiro, para o candidato público ser
		// ACRESCENTADO e o local continuar valendo em rede caseira. Só que o
		// candidato srflx carrega o endereço privado do contêiner como
		// endereço relacionado, e nem toda implementação de ICE casa o par: o
		// navegador conectava em um segundo e o cliente em C++ ficava trinta
		// segundos tentando até desistir.
		//
		// Como host o endereço público SUBSTITUI o privado. Quem hospeda em
		// casa e chama gente da própria rede perde o caminho curto e passa a
		// sair e voltar pelo roteador - custa um pouco de latência, e é o
		// preço de funcionar para todo mundo.
		ajustes.SetNAT1To1IPs([]string{enderecoPublico}, webrtc.ICECandidateTypeHost)
		infoSFU("anunciando o endereco %s", enderecoPublico)
	} else {
		erroSFU("nao descobri o endereco publico; quem estiver fora da rede pode nao conectar")
	}

	return &SFU{
		salas:  make(map[string]map[string]*sessaoSFU),
		faixas: make(map[string][]*faixaEncaminhada),
		api: webrtc.NewAPI(webrtc.WithMediaEngine(motor),
			webrtc.WithSettingEngine(ajustes)),
		config: webrtc.Configuration{
			ICEServers: []webrtc.ICEServer{
				{URLs: []string{"stun:stun.l.google.com:19302"}},
			},
		},
	}
}

// descobrirIPPublico pergunta a um servidor STUN qual endereço o mundo vê.
//
// É uma requisição STUN mínima, escrita à mão: a biblioteca só expõe isso
// dentro de uma conexão, e aqui a resposta é necessária antes de existir
// conexão nenhuma.
func descobrirIPPublico() string {
	conexao, err := net.Dial("udp", "stun.l.google.com:19302")
	if err != nil {
		return ""
	}
	defer conexao.Close()

	// Binding Request: tipo 0x0001, sem atributos, com o cookie mágico.
	pedido := make([]byte, 20)
	binary.BigEndian.PutUint16(pedido[0:2], 0x0001)
	binary.BigEndian.PutUint32(pedido[4:8], 0x2112A442)
	if _, err := rand.Read(pedido[8:20]); err != nil {
		return ""
	}
	if _, err := conexao.Write(pedido); err != nil {
		return ""
	}

	_ = conexao.SetReadDeadline(time.Now().Add(3 * time.Second))
	resposta := make([]byte, 512)
	n, err := conexao.Read(resposta)
	if err != nil || n < 20 {
		return ""
	}

	// Percorre os atributos até achar XOR-MAPPED-ADDRESS (0x0020).
	corpo := resposta[20:n]
	for len(corpo) >= 4 {
		tipo := binary.BigEndian.Uint16(corpo[0:2])
		tamanho := int(binary.BigEndian.Uint16(corpo[2:4]))
		if len(corpo) < 4+tamanho {
			return ""
		}
		valor := corpo[4 : 4+tamanho]

		if tipo == 0x0020 && len(valor) >= 8 && valor[1] == 0x01 {
			// IPv4, com os bytes em XOR com o cookie.
			ip := make(net.IP, 4)
			for i := 0; i < 4; i++ {
				ip[i] = valor[4+i] ^ pedido[4+i]
			}
			return ip.String()
		}

		// Atributos são alinhados em 4 bytes.
		avanco := 4 + tamanho
		if resto := avanco % 4; resto != 0 {
			avanco += 4 - resto
		}
		if avanco > len(corpo) {
			return ""
		}
		corpo = corpo[avanco:]
	}
	return ""
}

func feedbackDeVideo() []webrtc.RTCPFeedback {
	// nack: peça o pacote que se perdeu. pli: peça um quadro-chave inteiro
	// quando não der para recuperar. Sem os dois, um pacote perdido quebra a
	// imagem até o próximo IDR natural.
	return []webrtc.RTCPFeedback{
		{Type: "goog-remb"},
		{Type: "ccm", Parameter: "fir"},
		{Type: "nack"},
		{Type: "nack", Parameter: "pli"},
	}
}

// Entrar cria a conexão do participante com o servidor e devolve a oferta.
//
// enviar é como o SFU manda mensagens de volta para aquele participante, pela
// mesma sinalização que os clientes já usam.
func (s *SFU) Entrar(sala, peer string, enviar func([]byte)) error {
	conexao, err := s.api.NewPeerConnection(s.config)
	if err != nil {
		return err
	}

	sessao := &sessaoSFU{
		peer:    peer,
		conexao: conexao,
		enviar:  enviar,
		saidas:  make(map[string]*webrtc.TrackLocalStaticRTP),
	}

	// Transceivers de recepção: sem eles o participante não teria onde publicar.
	// Um de vídeo e um de áudio dão conta de tela mais som do sistema.
	for _, tipo := range []webrtc.RTPCodecType{webrtc.RTPCodecTypeVideo, webrtc.RTPCodecTypeAudio} {
		if _, err := conexao.AddTransceiverFromKind(tipo,
			webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionRecvonly}); err != nil {
			conexao.Close()
			return err
		}
	}

	conexao.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return // fim da coleta
		}
		enviar(mensagemDeCandidato(c.ToJSON()))
	})

	conexao.OnTrack(func(faixa *webrtc.TrackRemote, receptor *webrtc.RTPReceiver) {
		s.aoReceberFaixa(sala, peer, faixa)
	})

	conexao.OnConnectionStateChange(func(estado webrtc.PeerConnectionState) {
		infoSFU("midia com %s: %s", curto(peer), estado.String())

		// Quadro-chave só faz sentido depois que o transporte deste
		// participante existe. Pedir antes - que era o que acontecia, junto com
		// a oferta - produzia o quadro cedo demais: ele era repassado enquanto
		// o destinatário ainda estava em "connecting", se perdia, e ninguém
		// pedia outro. A faixa aparecia na lista e nunca decodificava.
		if estado == webrtc.PeerConnectionStateConnected {
			s.pedirChaveDeTudo(sala, peer)
		}

		if estado == webrtc.PeerConnectionStateFailed ||
			estado == webrtc.PeerConnectionStateClosed {
			s.Sair(sala, peer)
		}
	})

	s.mu.Lock()
	if s.salas[sala] == nil {
		s.salas[sala] = make(map[string]*sessaoSFU)
	}
	s.salas[sala][peer] = sessao
	faixasExistentes := append([]*faixaEncaminhada(nil), s.faixas[sala]...)
	s.mu.Unlock()

	// Quem chega já recebe o que os outros estão publicando. Sem isto a pessoa
	// entra numa sala com gente transmitindo e vê tela preta até alguém
	// recomeçar a transmissão.
	assinadas := 0
	for _, f := range faixasExistentes {
		if f.dono == peer {
			continue
		}
		s.assinar(sessao, f)
		assinadas++
	}
	if assinadas > 0 {
		infoSFU("%s entrou numa sala com %d faixa(s) em andamento; ja inscrito",
			curto(peer), assinadas)
	}

	// Uma oferta só, com tudo dentro. Oferecer por faixa deixava a conexão em
	// have-local-offer e a segunda tentativa era recusada - o participante
	// entrava e não recebia nada.
	// O quadro-chave é pedido quando a conexão ficar de pé, em
	// OnConnectionStateChange, e não aqui.
	return s.oferecer(sessao)
}

// pedirChaveDeTudo pede um quadro-chave de cada transmissão que este
// participante está recebendo. É chamado quando a conexão dele fica de pé:
// antes disso o quadro se perderia no caminho.
func (s *SFU) pedirChaveDeTudo(sala, peer string) {
	s.mu.RLock()
	sessao := s.salas[sala][peer]
	faixas := append([]*faixaEncaminhada(nil), s.faixas[sala]...)
	s.mu.RUnlock()
	if sessao == nil {
		return
	}

	pedidas := 0
	for _, f := range faixas {
		if f.dono == peer {
			continue
		}
		sessao.mu.Lock()
		inscrito := sessao.saidas[f.saida.ID()] != nil
		sessao.mu.Unlock()
		if !inscrito {
			continue
		}
		s.insistirNaChave(sala, f)
		pedidas++
	}
	if pedidas > 0 {
		infoSFU("%s conectou; pedi quadro-chave de %d transmissao(oes)", curto(peer), pedidas)
	}
}

func (s *SFU) oferecer(sessao *sessaoSFU) error {
	// Já há oferta esperando resposta: anota e sai. Insistir agora seria
	// recusado, e o participante ficaria sem receber a faixa nova.
	if sessao.conexao.SignalingState() != webrtc.SignalingStateStable {
		sessao.mu.Lock()
		sessao.renegociarDepois = true
		sessao.mu.Unlock()
		return nil
	}

	oferta, err := sessao.conexao.CreateOffer(nil)
	if err != nil {
		return err
	}
	if err := sessao.conexao.SetLocalDescription(oferta); err != nil {
		return err
	}
	sessao.enviar(mensagemDeDescricao("offer", sessao.conexao.LocalDescription().SDP))
	return nil
}

// aoReceberFaixa cria a cópia que será reenviada e a entrega a todo mundo que
// já está na sala.
func (s *SFU) aoReceberFaixa(sala, dono string, origem *webrtc.TrackRemote) {
	// O primeiro argumento é o id da FAIXA e precisa ser único; o segundo é o id
	// da STREAM e precisa ser o mesmo para as faixas da mesma pessoa, para o
	// outro lado juntar áudio e vídeo num card só.
	//
	// Os dois eram iguais, e aí áudio e vídeo do mesmo dono tinham o mesmo id.
	// O assinar() guarda o que já entregou em saidas[id], via que aquele id já
	// estava lá e pulava a segunda faixa - sempre. Quem publicava áudio antes
	// perdia o vídeo, e vice-versa. Na oferta isso aparecia como a m-line de
	// vídeo ficando em recvonly enquanto só a de áudio virava sendrecv.
	saida, err := webrtc.NewTrackLocalStaticRTP(origem.Codec().RTPCodecCapability,
		"greenlabs-"+curto(dono)+"-"+origem.Kind().String(), "greenlabs-"+curto(dono))
	if err != nil {
		erroSFU("nao foi possivel criar a faixa de saida: %v", err)
		return
	}

	f := &faixaEncaminhada{dono: dono, origem: origem, saida: saida}

	s.mu.Lock()
	s.faixas[sala] = append(s.faixas[sala], f)
	destinos := make([]*sessaoSFU, 0)
	for id, sessao := range s.salas[sala] {
		if id != dono {
			destinos = append(destinos, sessao)
		}
	}
	s.mu.Unlock()

	infoSFU("%s publicou %s; reenviando para %d", curto(dono), origem.Kind().String(),
		len(destinos))

	for _, destino := range destinos {
		s.assinarComChave(sala, destino, f)
	}

	// Bombeia os pacotes da origem para a cópia. É aqui que o vídeo de fato
	// atravessa o servidor - sem decodificar nada, só repassando RTP.
	buffer := make([]byte, 1500)
	for {
		n, _, err := origem.Read(buffer)
		if err != nil {
			break
		}
		if _, err := saida.Write(buffer[:n]); err != nil {
			break
		}
	}

	s.removerFaixa(sala, f)
}

// pedirChave manda um PLI para quem publica.
//
// Sob demanda, nao em intervalo fixo. Pedir de tempos em tempos parecia
// inofensivo e nao e: um quadro-chave e cerca de dez vezes maior que um normal,
// entao pedir a cada tres segundos joga banda fora o tempo todo para resolver um
// problema que so existe quando alguem acaba de chegar.
func (s *SFU) pedirChave(sala string, f *faixaEncaminhada) {
	if f.origem.Kind() != webrtc.RTPCodecTypeVideo {
		return
	}
	s.mu.RLock()
	sessao := s.salas[sala][f.dono]
	s.mu.RUnlock()
	if sessao == nil {
		return
	}
	_ = sessao.conexao.WriteRTCP([]rtcp.Packet{
		&rtcp.PictureLossIndication{MediaSSRC: uint32(f.origem.SSRC())},
	})
}

// assinar entrega a faixa a um participante e renegocia com ele.
func (s *SFU) assinar(destino *sessaoSFU, f *faixaEncaminhada) {
	destino.mu.Lock()
	if destino.fechada {
		destino.mu.Unlock()
		return
	}
	if _, jaTem := destino.saidas[f.saida.ID()]; jaTem {
		destino.mu.Unlock()
		return
	}
	destino.saidas[f.saida.ID()] = f.saida
	destino.mu.Unlock()

	// AddTransceiverFromTrack, e nao AddTrack.
	//
	// O AddTrack REAPROVEITA um transceiver que ja exista com direcao
	// compativel - e o unico que existe e o recvonly que abrimos para a pessoa
	// publicar. O resultado e a m-line de envio dela virando sendrecv e passando
	// a carregar tambem a imagem de OUTRA pessoa.
	//
	// O Chrome lida com isso: ele dispara ontrack quando a direcao passa a
	// receber. O libdatachannel nao - para ele a faixa daquele mid ja existe,
	// nenhuma faixa nova aparece, e o cliente nativo ficava com a lista de
	// transmissoes vazia enquanto o Electron, na mesma sala, via tudo. Dava para
	// ver no SDP: "video mid 0 sendrecv".
	//
	// Cada faixa encaminhada ganhando o proprio transceiver sendonly e o
	// desenho certo de qualquer jeito: misturar o que a pessoa publica com o que
	// ela recebe na mesma m-line e o tipo de coisa que funciona ate parar de
	// funcionar.
	if _, err := destino.conexao.AddTransceiverFromTrack(f.saida,
		webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendonly}); err != nil {
		erroSFU("nao foi possivel entregar a faixa a %s: %v", curto(destino.peer), err)
	}
}

// assinarComChave entrega a faixa e pede um quadro-chave a quem publica: sem
// ele o assinante novo fica com tela preta ate o proximo IDR natural.
func (s *SFU) assinarComChave(sala string, destino *sessaoSFU, f *faixaEncaminhada) {
	s.assinar(destino, f)
	if err := s.oferecer(destino); err != nil {
		erroSFU("renegociacao com %s falhou: %v", curto(destino.peer), err)
	}
	s.insistirNaChave(sala, f)
}

// insistirNaChave pede o quadro-chave algumas vezes, espaçado.
//
// Um pedido só é frágil: ele pode sair antes da renegociação terminar, ou
// simplesmente se perder - PLI vai por RTCP, sem garantia de entrega. Quando
// isso acontece, quem está recebendo fica esperando um quadro-chave que nunca
// vem, e a tela fica parada até alguém recomeçar a transmissão.
func (s *SFU) insistirNaChave(sala string, f *faixaEncaminhada) {
	go func() {
		for _, espera := range []time.Duration{0, 500 * time.Millisecond, 1500 * time.Millisecond} {
			if espera > 0 {
				time.Sleep(espera)
			}
			// Faixa que saiu do ar no meio da espera: nada a pedir.
			s.mu.RLock()
			viva := false
			for _, atual := range s.faixas[sala] {
				if atual == f {
					viva = true
					break
				}
			}
			s.mu.RUnlock()
			if !viva {
				return
			}
			s.pedirChave(sala, f)
		}
	}()
}

func (s *SFU) removerFaixa(sala string, f *faixaEncaminhada) {
	s.mu.Lock()
	restantes := s.faixas[sala][:0]
	for _, atual := range s.faixas[sala] {
		if atual != f {
			restantes = append(restantes, atual)
		}
	}
	s.faixas[sala] = restantes
	destinos := make([]*sessaoSFU, 0)
	for id, sessao := range s.salas[sala] {
		if id != f.dono {
			destinos = append(destinos, sessao)
		}
	}
	s.mu.Unlock()

	for _, destino := range destinos {
		destino.mu.Lock()
		delete(destino.saidas, f.saida.ID())
		destino.mu.Unlock()

		for _, remetente := range destino.conexao.GetSenders() {
			if remetente.Track() == f.saida {
				_ = destino.conexao.RemoveTrack(remetente)
				_ = s.oferecer(destino)
				break
			}
		}
	}
}

// Descricao aplica a resposta que o participante mandou.
func (s *SFU) Descricao(sala, peer, tipo, sdp string) {
	s.mu.RLock()
	sessao := s.salas[sala][peer]
	s.mu.RUnlock()
	if sessao == nil {
		return
	}

	var tipoSDP webrtc.SDPType
	switch tipo {
	case "answer":
		tipoSDP = webrtc.SDPTypeAnswer
	case "offer":
		tipoSDP = webrtc.SDPTypeOffer
	default:
		return
	}

	if err := sessao.conexao.SetRemoteDescription(webrtc.SessionDescription{
		Type: tipoSDP, SDP: sdp,
	}); err != nil {
		erroSFU("descricao de %s recusada: %v", curto(peer), err)
		return
	}

	if tipoSDP == webrtc.SDPTypeAnswer {
		// Chegou a resposta: se apareceu faixa nova enquanto esperávamos, é
		// agora que ela é anunciada.
		sessao.mu.Lock()
		pendente := sessao.renegociarDepois
		sessao.renegociarDepois = false
		sessao.mu.Unlock()
		if pendente {
			if err := s.oferecer(sessao); err != nil {
				erroSFU("renegociacao adiada com %s falhou: %v", curto(peer), err)
			}
		}
		return
	}

	// Oferta vinda do cliente: responder é obrigação nossa.
	if tipoSDP == webrtc.SDPTypeOffer {
		resposta, err := sessao.conexao.CreateAnswer(nil)
		if err != nil {
			return
		}
		if err := sessao.conexao.SetLocalDescription(resposta); err != nil {
			return
		}
		sessao.enviar(mensagemDeDescricao("answer", sessao.conexao.LocalDescription().SDP))
	}
}

// Candidato acrescenta um candidato ICE que o participante enviou.
func (s *SFU) Candidato(sala, peer, candidato, mid string) {
	s.mu.RLock()
	sessao := s.salas[sala][peer]
	s.mu.RUnlock()
	if sessao == nil || candidato == "" {
		return
	}

	indice := uint16(0)
	init := webrtc.ICECandidateInit{
		Candidate:     candidato,
		SDPMid:        &mid,
		SDPMLineIndex: &indice,
	}
	if err := sessao.conexao.AddICECandidate(init); err != nil {
		// Candidato inválido acontece o tempo todo; não é motivo para derrubar.
		return
	}
}

// Sair fecha a conexão do participante e tira as faixas dele do ar.
func (s *SFU) Sair(sala, peer string) {
	s.mu.Lock()
	sessao := s.salas[sala][peer]
	if sessao != nil {
		delete(s.salas[sala], peer)
	}
	if len(s.salas[sala]) == 0 {
		delete(s.salas, sala)
		delete(s.faixas, sala)
	}
	s.mu.Unlock()

	if sessao == nil {
		return
	}
	sessao.mu.Lock()
	sessao.fechada = true
	sessao.mu.Unlock()
	_ = sessao.conexao.Close()
}

// ------------------------------------------------------------- auxiliares

func curto(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func infoSFU(formato string, args ...any) {
	registrar("[sfu] "+formato, args...)
}

func erroSFU(formato string, args ...any) {
	registrar("[sfu] ERRO "+formato, args...)
}

// As mensagens saem no mesmo formato que os clientes ja falam. O campo aparece
// tanto como "sdp" quanto como "description" porque o cliente web le um e o de
// Electron le o outro - mandar os dois faz o mesmo pacote servir para ambos.
func mensagemDeDescricao(tipo, sdp string) []byte {
	corpo := map[string]string{"type": tipo, "sdp": sdp}
	msg := map[string]any{
		"type":        tipo,
		"sdp":         corpo,
		"description": corpo,
	}
	bruto, err := json.Marshal(msg)
	if err != nil {
		return nil
	}
	return bruto
}

func mensagemDeCandidato(c webrtc.ICECandidateInit) []byte {
	mid := ""
	if c.SDPMid != nil {
		mid = *c.SDPMid
	}
	indice := uint16(0)
	if c.SDPMLineIndex != nil {
		indice = *c.SDPMLineIndex
	}
	msg := map[string]any{
		"type": "ice",
		"candidate": map[string]any{
			"candidate":     c.Candidate,
			"sdpMid":        mid,
			"sdpMLineIndex": indice,
		},
	}
	bruto, err := json.Marshal(msg)
	if err != nil {
		return nil
	}
	return bruto
}
