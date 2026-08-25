//go:build !windows

package main

import "os"

// Em Linux e macOS: um terminal de verdade tem modo de caractere. Quando a
// entrada e um arquivo ou um cano (servico, contentor), nao da para perguntar
// nada e o servidor sobe direto com o padrao.
func entradaEhTerminal() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

// Nada a preparar: terminal de Unix ja entende ANSI e UTF-8.
func prepararConsole() {}
