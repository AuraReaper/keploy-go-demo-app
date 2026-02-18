FROM golang:1.23-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY main.go .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /bin/multi-kind-app .

FROM alpine:latest
RUN apk add --no-cache ca-certificates curl
COPY --from=builder /bin/multi-kind-app /bin/multi-kind-app
EXPOSE 8080
ENTRYPOINT ["/bin/multi-kind-app"]
