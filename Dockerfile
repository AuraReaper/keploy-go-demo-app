# === Build Stage === (same pattern as gin-mongo sample)
FROM golang:1.22-bookworm AS build-stage

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./
RUN CGO_ENABLED=0 go build -o /main

# === Runtime Stage ===
FROM alpine:3.19

RUN addgroup -S keploy \
 && adduser -S keploy -G keploy -h /home/keploy \
 && mkdir -p /home/keploy/app \
 && chown -R keploy:keploy /home/keploy/app \
 && apk add --no-cache dumb-init \
 && rm -rf /var/cache/apk/*

WORKDIR /home/keploy/app
COPY --from=build-stage --chown=keploy:keploy /main /home/keploy/app/main

ENTRYPOINT ["dumb-init"]
USER keploy
EXPOSE 8080
CMD ["./main"]
