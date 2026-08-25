package main

import (
	"bufio"
	"io"
	"net"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Radmin VPN e Hamachi entregam endereços nessas faixas. É como a maioria joga
// junto sem abrir porta no roteador, então esses aparecem primeiro na lista.
func ehFaixaVPN(ip string) bool {
	return strings.HasPrefix(ip, "26.") || strings.HasPrefix(ip, "25.")
}

type EnderecoLocal struct {
	Interface string
	IP        string
	VPN       bool
}

func EnderecosLocais() []EnderecoLocal {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var achados []EnderecoLocal
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		enderecos, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, endereco := range enderecos {
			rede, ok := endereco.(*net.IPNet)
			if !ok {
				continue
			}
			ip := rede.IP.To4()
			if ip == nil || ip.IsLoopback() {
				continue
			}
			achados = append(achados, EnderecoLocal{
				Interface: iface.Name,
				IP:        ip.String(),
				VPN:       ehFaixaVPN(ip.String()),
			})
		}
	}
	sort.SliceStable(achados, func(i, j int) bool {
		return achados[i].VPN && !achados[j].VPN
	})
	return achados
}

func existeNoPath(comando string) bool {
	_, err := exec.LookPath(comando)
	return err == nil
}

// ResolverProvedor devolve "cloudflared", "ngrok" ou "" quando não há nenhum.
func ResolverProvedor(pedido string) string {
	desejado := strings.ToLower(strings.TrimSpace(pedido))
	if desejado == "cloudflare" {
		desejado = "cloudflared"
	}
	if desejado != "" && desejado != "auto" {
		if existeNoPath(desejado) {
			return desejado
		}
		return ""
	}
	if existeNoPath("cloudflared") {
		return "cloudflared"
	}
	if existeNoPath("ngrok") {
		return "ngrok"
	}
	return ""
}

var (
	padraoCloudflare = regexp.MustCompile(`(?i)https://[a-z0-9-]+\.trycloudflare\.com`)
	padraoNgrok      = regexp.MustCompile(`(?i)https://[a-z0-9-]+\.ngrok[-a-z0-9.]*\.(app|io|dev)`)
)

// IniciarTunel sobe cloudflared ou ngrok e avisa quando o endereço público
// aparece. Os dois imprimem a URL na saída padrão, então basta acompanhar o
// texto — não precisa de API local nem de dependência.
func IniciarTunel(provedor string, porta int, aoAchar func(string)) (*exec.Cmd, error) {
	var argumentos []string
	var padrao *regexp.Regexp
	if provedor == "cloudflared" {
		argumentos = []string{"tunnel", "--url", "http://localhost:" + itoa(porta)}
		padrao = padraoCloudflare
	} else {
		argumentos = []string{"http", itoa(porta), "--log", "stdout"}
		padrao = padraoNgrok
	}

	comando := exec.Command(provedor, argumentos...)
	saida, err := comando.StdoutPipe()
	if err != nil {
		return nil, err
	}
	erro, err := comando.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := comando.Start(); err != nil {
		return nil, err
	}

	var umaVez sync.Once
	procurar := func(fonte io.Reader) {
		leitor := bufio.NewScanner(fonte)
		leitor.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for leitor.Scan() {
			achado := padrao.FindString(leitor.Text())
			if achado == "" {
				continue
			}
			umaVez.Do(func() {
				aoAchar("wss" + strings.TrimPrefix(achado, "https"))
			})
		}
	}
	go procurar(saida)
	go procurar(erro)

	return comando, nil
}
