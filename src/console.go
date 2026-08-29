package main

// Abertura no console: a marca e as perguntas de porta e túnel.
//
// Quem baixa o executável e clica duas vezes não passa argumento nenhum. Sem
// perguntar, o servidor subia na porta padrão sem túnel e a pessoa tinha que
// descobrir sozinha que existiam opções. Perguntar resolve isso sem tirar nada
// de quem prefere a linha de comando: com `--port` ou `--tunnel` o servidor não
// pergunta nada.

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Cores ANSI. O terminal do Windows entende desde a build 10586, e quando não
// entende as sequências saem como texto invisível — não quebra nada.
const (
	corReset = "\033[0m"
	corVerde = "\033[38;2;55;255;148m"
	corCinza = "\033[38;2;147;163;179m"
	corForte = "\033[1m"
)

func mostrarMarca(versao string) {
	fmt.Println()
	fmt.Println(corVerde + "   ▄████  ██▀███  ▓█████ ▓█████  ███▄    █ " + corReset)
	fmt.Println(corVerde + "  ██▒ ▀█▒▓██ ▒ ██▒▓█   ▀ ▓█   ▀  ██ ▀█   █ " + corReset)
	fmt.Println(corVerde + " ▒██░▄▄▄░▓██ ░▄█ ▒▒███   ▒███   ▓██  ▀█ ██▒" + corReset)
	fmt.Println(corVerde + " ░▓█  ██▓▒██▀▀█▄  ▒▓█  ▄ ▒▓█  ▄ ▓██▒  ▐▌██▒" + corReset)
	fmt.Println(corVerde + " ░▒▓███▀▒░██▓ ▒██▒░▒████▒░▒████▒▒██░   ▓██░" + corReset)
	fmt.Println(corCinza + "  ░▒   ▒ ░ ▒▓ ░▒▓░░░ ▒░ ░░░ ▒░ ░░ ▒░   ▒ ▒ " + corReset)
	fmt.Println()
	fmt.Println("  " + corForte + "GreenLabs" + corReset + corCinza + "  servidor de sinalizacao  " +
		versao + corReset)
	fmt.Println("  " + corCinza + "greencodes.com.br" + corReset)
	fmt.Println()
}

// perguntar mostra a pergunta com o valor padrão entre colchetes e devolve o
// que a pessoa digitou, ou o padrão quando ela só apertou Enter.
func perguntar(leitor *bufio.Reader, pergunta, padrao string) string {
	fmt.Printf("  %s%s%s [%s%s%s]: ", corForte, pergunta, corReset, corVerde, padrao, corReset)
	linha, err := leitor.ReadString('\n')
	if err != nil {
		return padrao
	}
	linha = strings.TrimSpace(linha)
	if linha == "" {
		return padrao
	}
	return linha
}

func perguntarSimNao(leitor *bufio.Reader, pergunta string, padrao bool) bool {
	dica := "s/N"
	if padrao {
		dica = "S/n"
	}
	fmt.Printf("  %s%s%s [%s%s%s]: ", corForte, pergunta, corReset, corVerde, dica, corReset)

	linha, err := leitor.ReadString('\n')
	if err != nil {
		return padrao
	}
	switch strings.ToLower(strings.TrimSpace(linha)) {
	case "":
		return padrao
	case "s", "sim", "y", "yes":
		return true
	default:
		return false
	}
}

// perguntarConfiguracao só roda quando ninguém passou opção na linha de comando
// e há um console de verdade para responder. Num serviço (systemd, Docker,
// Pterodactyl) a entrada padrão não é um terminal e ficar esperando resposta
// prenderia o servidor para sempre.
func perguntarConfiguracao(opts *opcoes) {
	if opts.porta != 0 || opts.tunel != "" {
		return // quem passou argumento já decidiu
	}
	if !entradaEhTerminal() {
		return
	}

	leitor := bufio.NewReader(os.Stdin)

	padrao := strconv.Itoa(ResolverPorta(0))
	resposta := perguntar(leitor, "Porta", padrao)
	if n, err := strconv.Atoi(resposta); err == nil && portaValida(n) {
		opts.porta = n
	} else if resposta != padrao {
		fmt.Printf("  %sporta invalida, usando %s%s\n", corCinza, padrao, corReset)
	}

	fmt.Println()
	fmt.Println("  " + corCinza + "O tunel cria um endereco publico (wss://) que funciona de" + corReset)
	fmt.Println("  " + corCinza + "qualquer lugar, sem abrir porta no roteador. Precisa do" + corReset)
	fmt.Println("  " + corCinza + "cloudflared ou do ngrok instalado." + corReset)
	if perguntarSimNao(leitor, "Abrir um tunel publico?", false) {
		opts.tunel = "auto"
	}
	fmt.Println()
}
