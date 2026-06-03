FROM golang:1.26-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .

ARG BINARY=generator
ARG VERSION=dev
ARG COMMIT=
ARG BUILD_TIME=
ARG BI=github.com/jasonczech/apexload/pkg/buildinfo
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
	-ldflags "-s -w -X ${BI}.Version=${VERSION} -X ${BI}.Commit=${COMMIT} -X ${BI}.BuildTime=${BUILD_TIME}" \
	-o /app ./cmd/${BINARY}

FROM alpine:3.20
ARG BINARY=generator
RUN apk add --no-cache ca-certificates \
	&& if [ "$BINARY" = "generator" ]; then \
		apk add --no-cache chromium nss freetype harfbuzz ttf-freefont; \
		if [ -x /usr/bin/chromium-browser ]; then \
			ln -sf /usr/bin/chromium-browser /usr/bin/google-chrome; \
		elif [ -x /usr/bin/chromium ]; then \
			ln -sf /usr/bin/chromium /usr/bin/google-chrome; \
		fi; \
	fi
COPY --from=builder /app /usr/local/bin/app
ENTRYPOINT ["/usr/local/bin/app"]
