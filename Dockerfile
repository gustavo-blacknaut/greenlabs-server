# Duas etapas: compila com o Go completo, publica só o binário.
# A imagem final não tem shell, gerenciador de pacotes nem runtime — é o
# executável e mais nada, por volta de 11 MB.

FROM golang:1.23-alpine AS compilacao
WORKDIR /src
COPY go.mod go.sum ./
COPY src/ ./src/
# CGO desligado: gera binário estático, que roda no scratch sem libc.
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /greenlabs-server ./src

FROM scratch
COPY --from=compilacao /greenlabs-server /greenlabs-server

ENV PORT=25640
EXPOSE 25640

ENTRYPOINT ["/greenlabs-server"]
