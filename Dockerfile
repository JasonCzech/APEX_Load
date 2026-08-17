ARG PLAYWRIGHT_DRIVER_VERSION=1.61.1

FROM node:26.7.0-alpine3.23@sha256:ce3cc39fe3b8b2602d3b1c4d63d301e46b48c550ecb627869853ddcdda418b63 AS dashboard-builder

ARG PLAYWRIGHT_DRIVER_VERSION
ARG VERSION=dev
ARG BUILD_TIME=

WORKDIR /src
COPY dashboard/package.json dashboard/package-lock.json ./dashboard/
COPY dashboard/scripts/check-node-version.mjs ./dashboard/scripts/check-node-version.mjs
# check-node-version.mjs resolves the pin from ../../.nvmrc, i.e. /src/.nvmrc.
# It must be copied alongside the script: without it the preinstall gate below
# throws ENOENT and the image build fails before npm ci ever runs.
COPY .nvmrc ./.nvmrc
RUN npm --prefix dashboard run check:node
RUN npm --prefix dashboard ci --ignore-scripts
RUN actual="$(node -p "require('./dashboard/node_modules/playwright-core-go/package.json').version")" \
	&& test "${actual}" = "${PLAYWRIGHT_DRIVER_VERSION}" \
	|| { echo "locked playwright-core driver ${actual} does not match ${PLAYWRIGHT_DRIVER_VERSION}" >&2; exit 1; }
COPY docs/API_USER_GUIDE.md ./docs/API_USER_GUIDE.md
COPY docs/APPLICATION_USER_GUIDE.md ./docs/APPLICATION_USER_GUIDE.md
COPY docs/ADMIN_USER_GUIDE.md ./docs/ADMIN_USER_GUIDE.md
COPY dashboard/ ./dashboard/
# The dashboard build deliberately writes to /src/pkg/api/static, matching the
# go:embed path used by the controller. Building it here makes image contents
# independent from any locally generated or committed frontend bundle.
RUN APEXLOAD_UX_VERSION="${VERSION}" APEXLOAD_UX_BUILD_TIME="${BUILD_TIME}" \
	npm --prefix dashboard run build

FROM golang:1.26.5-alpine3.24@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=dashboard-builder /src/pkg/api/static/ ./pkg/api/static/

ARG BINARY=generator
ARG GO_BUILD_TAGS=standard
ARG VERSION=dev
ARG COMMIT=
ARG BUILD_TIME=
ARG BI=github.com/jasonczech/apexload/pkg/buildinfo
RUN case "${GO_BUILD_TAGS}" in \
		standard) build_tags="" ;; \
		enterprise) build_tags="jms_solace_real,jms_servicebus_real,kafka_real,eventhubs_real" ;; \
		*) echo "unsupported GO_BUILD_TAGS profile: ${GO_BUILD_TAGS} (expected standard or enterprise)" >&2; exit 2 ;; \
	esac \
	&& if [ -n "${build_tags}" ]; then set -- -tags "${build_tags}"; else set --; fi \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath "$@" \
		-ldflags "-s -w -X ${BI}.Version=${VERSION} -X ${BI}.Commit=${COMMIT} -X ${BI}.BuildTime=${BUILD_TIME}" \
		-o /app ./cmd/${BINARY}

FROM alpine:3.24@sha256:28bd5fe8b56d1bd048e5babf5b10710ebe0bae67db86916198a6eec434943f8b
ARG BINARY=generator
ARG PLAYWRIGHT_DRIVER_VERSION
# PLAYWRIGHT_DRIVER_VERSION must match playwrightCliVersion in
# playwright-community/playwright-go (see go.mod) — playwright.Run() refuses
# to start with a mismatched driver.
RUN apk add --no-cache ca-certificates \
	&& if [ "$BINARY" = "generator" ] || [ "$BINARY" = "controller" ]; then \
		apk add --no-cache chromium nodejs nss freetype harfbuzz ttf-freefont; \
		if [ -x /usr/bin/chromium-browser ]; then \
			ln -sf /usr/bin/chromium-browser /usr/bin/google-chrome; \
		elif [ -x /usr/bin/chromium ]; then \
			ln -sf /usr/bin/chromium /usr/bin/google-chrome; \
		fi; \
	fi
COPY --from=dashboard-builder /src/dashboard/node_modules/playwright-core-go/ /opt/playwright-driver/package/
RUN if [ "$BINARY" = "generator" ] || [ "$BINARY" = "controller" ]; then \
		actual="$(node /opt/playwright-driver/package/cli.js --version)"; \
		test "${actual}" = "Version ${PLAYWRIGHT_DRIVER_VERSION}" \
		|| { echo "runtime Playwright driver ${actual} does not match Version ${PLAYWRIGHT_DRIVER_VERSION}" >&2; exit 1; }; \
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
RUN mkdir -p /data /tmp /opt/playwright-driver /var/lib/apexload-bruno \
	&& chown -R 65532:65532 /data /tmp /var/lib/apexload-bruno
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/app"]
