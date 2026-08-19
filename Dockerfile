FROM golang:1.24-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/opencode2api ./

FROM alpine:3.22

RUN apk add --no-cache ca-certificates

WORKDIR /app

COPY --from=build /out/opencode2api /usr/local/bin/opencode2api
COPY docker-entrypoint.sh /usr/local/bin/docker-entrypoint
COPY config.example.json /app/config.example.json
RUN chmod 0755 /usr/local/bin/docker-entrypoint /usr/local/bin/opencode2api

EXPOSE 8080 8081

ENTRYPOINT ["/usr/local/bin/docker-entrypoint"]
