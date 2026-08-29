//go:build windows

package main

import (
	"os"
	"syscall"
	"unsafe"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetConsoleMode           = kernel32.NewProc("GetConsoleMode")
	procSetConsoleMode           = kernel32.NewProc("SetConsoleMode")
	procSetConsoleOutputCP       = kernel32.NewProc("SetConsoleOutputCP")
	enableVirtualTerminalProcess = uint32(0x0004)
)

// entradaEhTerminal diz se dá para perguntar alguma coisa.
//
// GetConsoleMode só funciona em handle de console de verdade: num serviço, num
// contêiner ou com a saída redirecionada para arquivo ele falha, e é assim que
// o servidor sabe que não deve ficar esperando resposta que nunca vem.
func entradaEhTerminal() bool {
	var modo uint32
	r, _, _ := procGetConsoleMode.Call(os.Stdin.Fd(), uintptr(unsafe.Pointer(&modo)))
	return r != 0
}

// prepararConsole liga o processamento de sequências ANSI e põe a saída em
// UTF-8. Sem isso a marca sai como um monte de caractere estranho e as cores
// aparecem como texto cru.
func prepararConsole() {
	const cpUtf8 = 65001
	procSetConsoleOutputCP.Call(uintptr(cpUtf8))

	var modo uint32
	if r, _, _ := procGetConsoleMode.Call(os.Stdout.Fd(), uintptr(unsafe.Pointer(&modo))); r == 0 {
		return
	}
	procSetConsoleMode.Call(os.Stdout.Fd(), uintptr(modo|enableVirtualTerminalProcess))
}
