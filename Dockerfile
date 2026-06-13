FROM golang:1.26.4-alpine AS builder

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
# PLAYWRIGHT_DRIVER_VERSION must match playwrightCliVersion in
# playwright-community/playwright-go (see go.mod) — playwright.Run() refuses
# to start with a mismatched driver.
ARG PLAYWRIGHT_DRIVER_VERSION=1.57.0
RUN apk add --no-cache ca-certificates \
	&& if [ "$BINARY" = "generator" ] || [ "$BINARY" = "controller" ]; then \
		apk add --no-cache chromium nodejs nss freetype harfbuzz ttf-freefont; \
		if [ -x /usr/bin/chromium-browser ]; then \
			ln -sf /usr/bin/chromium-browser /usr/bin/google-chrome; \
		elif [ -x /usr/bin/chromium ]; then \
			ln -sf /usr/bin/chromium /usr/bin/google-chrome; \
		fi; \
		# Playwright driver = the playwright-core npm package, laid out as
		# $PLAYWRIGHT_DRIVER_PATH/package/cli.js. Playwright's own driver zip
		# bundles a glibc Node that cannot run on musl, so we install the
		# package via npm and point PLAYWRIGHT_NODEJS_PATH at Alpine's Node.
		apk add --no-cache --virtual .driver-build npm; \
		mkdir -p /opt/playwright-driver; \
		npm install --prefix /opt/playwright-driver-npm "playwright-core@${PLAYWRIGHT_DRIVER_VERSION}"; \
		ln -s /opt/playwright-driver-npm/node_modules/playwright-core /opt/playwright-driver/package; \
		node /opt/playwright-driver/package/cli.js --version; \
		apk del .driver-build; \
	fi
COPY --from=builder /app /usr/local/bin/app
# Browser runtime wiring (controller and generator images; inert elsewhere):
# - the driver lives at a fixed path readable by any uid (readOnlyRootFilesystem-safe)
# - Playwright drives Alpine's musl-built Chromium instead of its own
#   glibc-only managed build (APEXLOAD_BROWSER_EXECUTABLE override)
# - HOME gives uid 65532 (no passwd entry) a writable scratch root under /tmp
ENV PLAYWRIGHT_DRIVER_PATH=/opt/playwright-driver \
	PLAYWRIGHT_NODEJS_PATH=/usr/bin/node \
	APEXLOAD_BROWSER_EXECUTABLE=/usr/bin/google-chrome \
	APEXLOAD_ADDR=0.0.0.0:8080 \
	HOME=/tmp
ENTRYPOINT ["/usr/local/bin/app"]
