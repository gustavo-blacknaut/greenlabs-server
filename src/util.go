package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"time"
)

// registrar imprime no mesmo formato do servidor em Node: carimbo ISO na
// frente, para os logs dos dois serem lidos pelas mesmas ferramentas.
func registrar(formato string, args ...any) {
	fmt.Fprintf(os.Stdout, "[%s] %s\n",
		time.Now().UTC().Format("2006-01-02T15:04:05.000Z"),
		fmt.Sprintf(formato, args...))
}

func itoa(n int) string { return strconv.Itoa(n) }

// novoUUID devolve um UUID v4. É o mesmo formato do randomUUID() do Node, e
// escrever à mão evita uma dependência para dezesseis bytes aleatórios.
func novoUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand falhando é praticamente impossível, mas um id repetido
		// quebraria o roteamento, então cai para algo ainda distinto.
		binary.BigEndian.PutUint64(b[0:8], uint64(time.Now().UnixNano()))
		binary.BigEndian.PutUint64(b[8:16], uint64(os.Getpid()))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // versão 4
	b[8] = (b[8] & 0x3f) | 0x80 // variante RFC 4122
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
