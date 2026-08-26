package main

// CLI do servidor de sinalização.
//
//	greenlabs-server                  -> só a rede local
//	greenlabs-server --tunnel         -> detecta cloudflared ou ngrok
//	greenlabs-server --tunnel=ngrok   -> força um provedor
//	greenlabs-server --port 30000
//
// As opções são lidas à mão porque o pacote flag não aceita ao mesmo tempo
// "--tunnel" sem valor e "--tunnel=ngrok" com valor.

import (
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
)

// Preenchido na compilação com -ldflags "-X main.versao=vX.Y.Z". Ficava fixo
// aqui e não acompanhava a tag: o binário da release v0.2.2 se anunciava como
// v0.2.0, e não havia como saber qual versão estava no ar.
//
// O valor abaixo é o que aparece em build feito na mão, sem a flag.
var versao = "desenvolvimento"

type opcoes struct {
	porta int
	tunel string
	sfu   bool
}

func lerOpcoes(argumentos []string) opcoes {
	// porta fica em zero quando ninguém passa --port: aí quem resolve é o
	// ResolverPorta, via PORT/SERVER_PORT, sem repetir a regra em dois lugares.
	out := opcoes{}
	for i := 0; i < len(argumentos); i++ {
		arg := argumentos[i]
		switch {
		case arg == "--port" && i+1 < len(argumentos):
			if n, err := strconv.Atoi(argumentos[i+1]); err == nil {
				out.porta = n
			}
			i++
		case strings.HasPrefix(arg, "--port="):
			if n, err := strconv.Atoi(arg[len("--port="):]); err == nil {
				out.porta = n
			}
		case arg == "--sfu":
			out.sfu = true
		case arg == "--tunnel":
			out.tunel = "auto"
		case strings.HasPrefix(arg, "--tunnel="):
			out.tunel = strings.ToLower(arg[len("--tunnel="):])
		case arg == "-h" || arg == "--help":
			fmt.Println(ajuda)
			os.Exit(0)
		}
	}
	return out
}

const ajuda = `Servidor de sinalizacao GreenLabs (Go)

  --port N          porta a escutar (padrao: PORT, SERVER_PORT ou 25640)
  --sfu             o video passa por este servidor em vez de ir direto
                    entre as pessoas: resolve quem nao consegue conectar
                    por causa do roteador, e cobra banda daqui
  --tunnel          abre um tunel publico com cloudflared ou ngrok
  --tunnel=ngrok    força um provedor
  -h, --help        esta ajuda

Endpoints:
  /                 estado resumido
  /rooms            salas e participantes
  /stats            contadores desde a subida`

func main() {
	prepararConsole()

	// Antes de ler qualquer configuração: um .env na pasta do servidor deve
	// valer tanto para quem roda pelo terminal quanto para quem sobe por
	// systemd, Docker ou painel.
	CarregarEnv(".env")

	opts := lerOpcoes(os.Args[1:])

	mostrarMarca(versao)
	perguntarConfiguracao(&opts)

	var sfu *SFU
	if opts.sfu {
		sfu = NovoSFU()
		fmt.Println("  " + corVerde + "SFU ligado" + corReset + corCinza +
			": o video passa por este servidor em vez de ir direto entre as pessoas." + corReset)
		fmt.Println("  " + corCinza + "Isso resolve quem nao consegue se conectar por causa do roteador," + corReset)
		fmt.Println("  " + corCinza + "e cobra banda e CPU daqui em troca." + corReset)
		fmt.Println()
	}

	servidor, err := Iniciar(ResolverPorta(opts.porta), sfu)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nao foi possivel subir o servidor: %v\n", err)
		os.Exit(1)
	}

	enderecos := EnderecosLocais()
	fmt.Println()
	fmt.Println("  Enderecos para quem esta na mesma rede:")
	if len(enderecos) == 0 {
		fmt.Println("    nenhuma interface de rede encontrada")
	}
	for _, item := range enderecos {
		sufixo := ""
		if item.VPN {
			sufixo = " - VPN"
		}
		fmt.Printf("    ws://%s:%d   (%s%s)\n", item.IP, servidor.Port, item.Interface, sufixo)
	}
	fmt.Println()

	if opts.tunel != "" {
		provedor := ResolverProvedor(opts.tunel)
		if provedor == "" {
			fmt.Println("  Nenhum tunnel disponivel. Instale cloudflared ou ngrok,")
			fmt.Println("  ou use Radmin VPN / Hamachi com os enderecos acima.")
			fmt.Println()
		} else {
			fmt.Printf("  Abrindo tunnel via %s...\n", provedor)
			processo, err := IniciarTunel(provedor, servidor.Port, func(url string) {
				fmt.Println()
				fmt.Println("  Tunnel ativo. Compartilhe este endereco:")
				fmt.Printf("    %s\n", url)
				fmt.Println()
			})
			if err != nil {
				fmt.Fprintf(os.Stderr, "[host] tunnel falhou: %v\n", err)
			} else {
				defer func() {
					if processo.Process != nil {
						_ = processo.Process.Kill()
					}
				}()
			}
		}
	}

	parar := make(chan os.Signal, 1)
	signal.Notify(parar, os.Interrupt, syscall.SIGTERM)
	<-parar

	fmt.Println()
	registrar("Encerrando...")
	servidor.Fechar()
}
