FROM node:22.22.0-bookworm-slim AS build

WORKDIR /opt/apexload/bruno-runner
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN corepack enable && pnpm install --frozen-lockfile --prod
COPY src ./src
COPY NOTICE.md README.md sbom.cdx.json ./

FROM gcr.io/distroless/nodejs22-debian13:nonroot

WORKDIR /opt/apexload/bruno-runner
COPY --from=build --chown=65532:65532 /opt/apexload/bruno-runner ./

ENV NODE_ENV=production
EXPOSE 8097
CMD ["src/server.js"]
