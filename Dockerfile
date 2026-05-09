FROM golang:1.22-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG BINARY=generator
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags "-s -w" -o /app ./cmd/${BINARY}

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /app /usr/local/bin/app
ENTRYPOINT ["/usr/local/bin/app"]
