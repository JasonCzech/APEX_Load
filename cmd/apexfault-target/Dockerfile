# This standard-library-only command does not need the dashboard or application dependencies.
FROM golang:1.26.9-alpine3.24@sha256:3082400e369fa24d5fc60bca20edab3f6d604e0c5a690ec66b295eff4dd87ade AS builder
WORKDIR /src
COPY cmd/apexfault-target/*.go ./
RUN CGO_ENABLED=0 GO111MODULE=off go build -trimpath -ldflags='-s -w' -o /apexfault-target .

FROM scratch
COPY --from=builder /apexfault-target /apexfault-target
USER 65532:65532
EXPOSE 8090
ENTRYPOINT ["/apexfault-target"]
