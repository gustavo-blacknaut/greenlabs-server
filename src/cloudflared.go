package main

// Baixar o cloudflared sozinho, quando ele não estiver na máquina.
//
// O `--tunnel` já existia, mas só funcionava para quem já tinha o cloudflared
// instalado; para todos os outros ele imprimia "instale cloudflared" e parava
// por aí. E justamente quem mais precisa do túnel é quem menos vai instalar
// coisa pela linha de comando: a pessoa que subiu o servidor em casa para
// jogar com os amigos.
//
// Isso importa mais do que parece. Uma página em HTTPS não consegue abrir
// `ws://` - o navegador bloqueia, e não há configuração que mude. Então quem
// hospeda sem certificado simplesmente não é alcançável pelo site, por mais
// que o servidor esteja no ar. O túnel resolve os dois problemas de uma vez:
// entrega um endereço `wss://` com certificado válido e atravessa NAT, sem
// abrir porta no roteador e sem conta em lugar nenhum.
//
// É um executável só, sem instalador e sem dependência - dá para baixar e
// rodar. O mesmo caminho que o aplicativo de desktop já usa.

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

const baseCloudflared = "https://github.com/cloudflare/cloudflared/releases/latest/download/"

// O arquivo certo para este sistema, ou "" quando não há um binário direto.
//
// macOS fica de fora de propósito: lá o cloudflared vem em .tgz, e descompactar
// para economizar um `brew install` não vale o código a mais.
func arquivoDoCloudflared() string {
	switch runtime.GOOS {
	case "windows":
		if runtime.GOARCH == "arm64" {
			return "cloudflared-windows-arm64.exe"
		}
		return "cloudflared-windows-amd64.exe"
	case "linux":
		switch runtime.GOARCH {
		case "arm64":
			return "cloudflared-linux-arm64"
		case "arm":
			return "cloudflared-linux-arm"
		case "386":
			return "cloudflared-linux-386"
		default:
			return "cloudflared-linux-amd64"
		}
	default:
		return ""
	}
}

// Onde a cópia baixada fica. Na pasta de cache do usuário, e não ao lado do
// servidor: o binário do servidor costuma viver em lugar sem permissão de
// escrita, e a pessoa que baixou um .exe não espera que ele crie arquivos ao
// lado dele.
func caminhoDoCloudflared() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	pasta := filepath.Join(base, "greenlabs")
	if err := os.MkdirAll(pasta, 0o755); err != nil {
		return "", err
	}
	nome := "cloudflared"
	if runtime.GOOS == "windows" {
		nome += ".exe"
	}
	return filepath.Join(pasta, nome), nil
}

// CloudflaredBaixado devolve o caminho da cópia local, se ela já existir.
func CloudflaredBaixado() string {
	caminho, err := caminhoDoCloudflared()
	if err != nil {
		return ""
	}
	info, err := os.Stat(caminho)
	if err != nil || info.Size() == 0 {
		return ""
	}
	return caminho
}

// BaixarCloudflared traz o executável e devolve onde ele ficou.
//
// `aoAndar` recebe quantos bytes já vieram e o total anunciado (0 quando o
// servidor não informa). Serve para a linha de progresso: são dezenas de
// megabytes, e sem sinal de vida parece que travou.
func BaixarCloudflared(aoAndar func(baixado, total int64)) (string, error) {
	arquivo := arquivoDoCloudflared()
	if arquivo == "" {
		return "", fmt.Errorf("sem binario pronto para %s/%s", runtime.GOOS, runtime.GOARCH)
	}

	destino, err := caminhoDoCloudflared()
	if err != nil {
		return "", err
	}

	cliente := &http.Client{Timeout: 10 * time.Minute}
	resp, err := cliente.Get(baseCloudflared + arquivo)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download devolveu %s", resp.Status)
	}

	// Escreve num arquivo temporário e só depois renomeia. Interromper no meio
	// deixaria um executável pela metade que, na próxima vez, seria encontrado
	// como se estivesse pronto - e falharia sem explicar por quê.
	temporario := destino + ".parcial"
	saida, err := os.OpenFile(temporario, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return "", err
	}

	_, err = io.Copy(saida, &leitorComProgresso{
		origem: resp.Body,
		total:  resp.ContentLength,
		aviso:  aoAndar,
	})
	fecharErr := saida.Close()
	if err == nil {
		err = fecharErr
	}
	if err != nil {
		_ = os.Remove(temporario)
		return "", err
	}

	if err := os.Rename(temporario, destino); err != nil {
		_ = os.Remove(temporario)
		return "", err
	}
	// Rename preserva o modo em Unix, mas não custa garantir o bit de execução.
	_ = os.Chmod(destino, 0o755)

	return destino, nil
}

type leitorComProgresso struct {
	origem   io.Reader
	total    int64
	baixado  int64
	aviso    func(baixado, total int64)
	ultimoEm time.Time
}

func (l *leitorComProgresso) Read(p []byte) (int, error) {
	n, err := l.origem.Read(p)
	l.baixado += int64(n)
	// No máximo um aviso a cada 200 ms: a cada leitura seriam milhares de
	// linhas por segundo no terminal.
	if l.aviso != nil && (err == io.EOF || time.Since(l.ultimoEm) > 200*time.Millisecond) {
		l.ultimoEm = time.Now()
		l.aviso(l.baixado, l.total)
	}
	return n, err
}
