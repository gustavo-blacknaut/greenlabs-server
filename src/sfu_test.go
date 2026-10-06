package main

import (
	"encoding/json"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

func TestICEAntesDoSDPTemLimite(t *testing.T) {
	pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	sessao := &sessaoSFU{conexao: pc}
	s := &SFU{salas: map[string]map[string]*sessaoSFU{"sala": {"peer": sessao}}}
	for i := 0; i < 1000; i++ {
		s.Candidato("sala", "peer", "candidate:teste", "0")
	}
	if len(sessao.candidatosPendentes) != 256 {
		t.Fatalf("fila ICE: %d", len(sessao.candidatosPendentes))
	}
}

// Pacotes sinteticos: exercita ICE/DTLS/SRTP e o encaminhamento, nao o codec
// ou a qualidade percebida de voz. Nao usa STUN nem servidores externos.
func TestSFUEncaminhaRTPAposSaidaDeEspectador(t *testing.T) {
	porta, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	if err != nil {
		t.Fatal(err)
	}
	numero := porta.LocalAddr().(*net.UDPAddr).Port
	porta.Close()
	s := NovoSFU(numero, "127.0.0.1")
	t.Cleanup(s.FecharMidia)
	s.config = webrtc.Configuration{}
	erros := make(chan error, 32)
	informar := func(err error) {
		if err != nil {
			select {
			case erros <- err:
			default:
			}
		}
	}
	faixa, err := webrtc.NewTrackLocalStaticRTP(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "audio", "publicador")
	if err != nil {
		t.Fatal(err)
	}
	var recebidos atomic.Int64
	parar := make(map[string]func())
	criar := func(id string, publicar bool) *webrtc.PeerConnection {
		var encerrado atomic.Bool
		reportar := func(err error) {
			if !encerrado.Load() {
				informar(err)
			}
		}
		pc, err := webrtc.NewPeerConnection(webrtc.Configuration{})
		if err != nil {
			t.Fatal(err)
		}
		if publicar {
			sender, err := pc.AddTrack(faixa)
			if err != nil {
				t.Fatal(err)
			}
			go func() {
				buffer := make([]byte, 1500)
				for {
					if _, _, err := sender.Read(buffer); err != nil {
						return
					}
				}
			}()
		}
		pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
			for {
				if _, _, err := track.ReadRTP(); err != nil {
					return
				}
				if id == "saudavel" {
					recebidos.Add(1)
				}
			}
		})
		pc.OnICECandidate(func(c *webrtc.ICECandidate) {
			if c == nil {
				return
			}
			init := c.ToJSON()
			mid := "0"
			if init.SDPMid != nil {
				mid = *init.SDPMid
			}
			s.Candidato("teste", id, init.Candidate, mid)
		})
		fila := make(chan []byte, 64)
		fim := make(chan struct{})
		var umaVez sync.Once
		parar[id] = func() { umaVez.Do(func() { encerrado.Store(true); close(fim); s.Sair("teste", id); pc.Close() }) }
		t.Cleanup(parar[id])
		go func() {
			for {
				select {
				case <-fim:
					return
				case dados := <-fila:
					var m struct {
						Tipo      string                    `json:"type"`
						SDP       webrtc.SessionDescription `json:"sdp"`
						Candidato webrtc.ICECandidateInit   `json:"candidate"`
					}
					if err := json.Unmarshal(dados, &m); err != nil {
						reportar(err)
						continue
					}
					switch m.Tipo {
					case "ice":
						reportar(pc.AddICECandidate(m.Candidato))
					case "offer":
						if err := pc.SetRemoteDescription(m.SDP); err != nil {
							reportar(err)
							continue
						}
						resposta, err := pc.CreateAnswer(nil)
						if err != nil {
							reportar(err)
							continue
						}
						if err := pc.SetLocalDescription(resposta); err != nil {
							reportar(err)
							continue
						}
						s.Descricao("teste", id, "answer", resposta.SDP)
					}
				}
			}
		}()
		if err := s.Entrar("teste", id, func(dados []byte) {
			select {
			case fila <- dados:
			case <-fim:
			}
		}); err != nil {
			t.Fatal(err)
		}
		return pc
	}
	criar("publicador", true)
	criar("saudavel", false)
	criar("extra", false)
	limite := time.Now().Add(15 * time.Second)
	var sequencia uint16
	enviar := func() {
		sequencia++
		informar(faixa.WriteRTP(&rtp.Packet{Header: rtp.Header{Version: 2, SequenceNumber: sequencia, Timestamp: uint32(sequencia) * 960, SSRC: 1234}, Payload: []byte{0xf8, 0xff, 0xfe}}))
		time.Sleep(20 * time.Millisecond)
		select {
		case err := <-erros:
			t.Fatal(err)
		default:
		}
	}
	for recebidos.Load() < 5 && time.Now().Before(limite) {
		enviar()
	}
	if recebidos.Load() < 5 {
		t.Fatal("RTP nao chegou ao espectador")
	}
	parar["extra"]()
	antes := recebidos.Load()
	for recebidos.Load() < antes+5 && time.Now().Before(limite) {
		enviar()
	}
	if recebidos.Load() < antes+5 {
		t.Fatal("saida de um espectador interrompeu os outros")
	}
}
