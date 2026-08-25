package main

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// CarregarEnv lê um arquivo .env e joga o conteúdo no ambiente do processo.
//
// Variáveis já definidas no ambiente têm prioridade: quem passa PORT pelo
// painel ou pelo systemd não deve ser sobrescrito por um arquivo esquecido na
// pasta.
func CarregarEnv(caminho string) bool {
	arquivo, err := os.Open(caminho)
	if err != nil {
		return false // arquivo ausente é normal
	}
	defer arquivo.Close()

	leitor := bufio.NewScanner(arquivo)
	for leitor.Scan() {
		linha := strings.TrimSpace(leitor.Text())
		if linha == "" || strings.HasPrefix(linha, "#") {
			continue
		}
		igual := strings.Index(linha, "=")
		if igual == -1 {
			continue
		}
		chave := strings.TrimSpace(linha[:igual])
		valor := strings.TrimSpace(linha[igual+1:])
		if len(valor) >= 2 {
			primeiro, ultimo := valor[0], valor[len(valor)-1]
			if (primeiro == '"' && ultimo == '"') || (primeiro == '\'' && ultimo == '\'') {
				valor = valor[1 : len(valor)-1]
			}
		}
		if chave == "" {
			continue
		}
		if _, definida := os.LookupEnv(chave); !definida {
			_ = os.Setenv(chave, valor)
		}
	}
	return true
}

// ResolverPorta escolhe a porta, em ordem de prioridade:
//
//  1. o que veio por --port
//  2. PORT (o padrão)
//  3. SERVER_PORT (é onde o Pterodactyl entrega a porta alocada)
//  4. 25640
func ResolverPorta(preferida int) int {
	if portaValida(preferida) {
		return preferida
	}
	for _, chave := range []string{"PORT", "SERVER_PORT"} {
		if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv(chave))); err == nil && portaValida(n) {
			return n
		}
	}
	return 25640
}

func portaValida(n int) bool { return n > 0 && n < 65536 }
