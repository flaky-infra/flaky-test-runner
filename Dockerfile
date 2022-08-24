FROM golang:1.17 as builder
WORKDIR /app

COPY go.mod .

ENV GOPROXY https://proxy.golang.org,direct
RUN go mod download

COPY . .

ENV CGO_ENABLED=0
RUN GOOS=linux go build -o main main.go

FROM alpine:3.13.1
WORKDIR /app
COPY --from=builder /app/* ./
ENV FLAKY_TEST_RUN_CMD=/cnb/process/web
ENV FLAKY_APP_PATH=/workspace
CMD ["/app/main"]
