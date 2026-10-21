# This standard-library-only command does not need the dashboard or application dependencies.
FROM golang:1.26.10-alpine3.24@sha256:9ce231b28998b32e116927c61aa5c406183061d4f3f2d61b292c88e7f6bdf7ca AS builder
WORKDIR /src
COPY cmd/apexfault-target/*.go ./
RUN CGO_ENABLED=0 GO111MODULE=off go build -trimpath -ldflags='-s -w' -o /apexfault-target .

FROM scratch
COPY --from=builder /apexfault-target /apexfault-target
USER 65532:65532
EXPOSE 8090
ENTRYPOINT ["/apexfault-target"]
