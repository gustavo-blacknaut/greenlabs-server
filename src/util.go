package main

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"
)

// registrar imprime no mesmo formato do servidor em Node: carimbo ISO na
// frente, para os logs dos dois serem lidos pelas mesmas ferramentas.
type registroLog struct {
	texto     string
	concluido chan struct{}
}

var filaLogs = make(chan registroLog, 128)
var iniciarLogs sync.Once

func prepararLogs() {
	iniciarLogs.Do(func() {
		go func() {
			var ultimo string
			var ultimoInstante time.Time
			for registro := range filaLogs {
				if registro.concluido != nil {
					close(registro.concluido)
					continue
				}
				agora := time.Now()
				if registro.texto == ultimo && agora.Sub(ultimoInstante) < 5*time.Second {
					continue
				}
				ultimo, ultimoInstante = registro.texto, agora
				fmt.Fprintf(os.Stdout, "[%s] %s\n", agora.UTC().Format("2006-01-02T15:04:05.000Z"), registro.texto)
			}
		}()
	})
}

func registrar(formato string, args ...any) {
	prepararLogs()
	texto := fmt.Sprintf(formato, args...)
	if len(texto) > 2048 {
		texto = texto[:2048]
	}
	select {
	case filaLogs <- registroLog{texto: texto}:
	default: // Um console lento nunca bloqueia sinalização ou mídia.
	}
}

func registrarDetalhado(formato string, args ...any) {
	if os.Getenv("GREENLABS_DEBUG") == "1" {
		registrar(formato, args...)
	}
}

func concluirLogs() {
	prepararLogs()
	concluido := make(chan struct{})
	prazo := time.NewTimer(500 * time.Millisecond)
	defer prazo.Stop()
	select {
	case filaLogs <- registroLog{concluido: concluido}:
	case <-prazo.C:
		return
	}
	select {
	case <-concluido:
	case <-prazo.C:
	}
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
