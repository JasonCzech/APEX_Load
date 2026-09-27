# This standard-library-only command does not need the dashboard or application dependencies.
FROM golang:1.26.5-alpine3.24@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder
WORKDIR /src
COPY cmd/apexfault-target/*.go ./
RUN CGO_ENABLED=0 GO111MODULE=off go build -trimpath -ldflags='-s -w' -o /apexfault-target .

FROM scratch
COPY --from=builder /apexfault-target /apexfault-target
USER 65532:65532
EXPOSE 8090
ENTRYPOINT ["/apexfault-target"]
