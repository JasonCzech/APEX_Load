# This standard-library-only command does not need the dashboard or application dependencies.
FROM golang:1.26.11-alpine3.24@sha256:ae52b5bc6ed045a9ed26ab03dc19327a802a9cc9aa8b27ed57f5c6b60579b0bb AS builder
WORKDIR /src
COPY cmd/apexfault-target/*.go ./
RUN CGO_ENABLED=0 GO111MODULE=off go build -trimpath -ldflags='-s -w' -o /apexfault-target .

FROM scratch
COPY --from=builder /apexfault-target /apexfault-target
USER 65532:65532
EXPOSE 8090
ENTRYPOINT ["/apexfault-target"]
