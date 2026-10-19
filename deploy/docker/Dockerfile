FROM node:24-alpine AS web
WORKDIR /src
RUN corepack enable
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
COPY web/package.json web/
RUN pnpm install --frozen-lockfile
COPY web/ web/
RUN pnpm --filter @patchd/web build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist web/dist
RUN CGO_ENABLED=0 go build -trimpath -o /patchd ./cmd/patchd

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /patchd /patchd
ENTRYPOINT ["/patchd"]
CMD ["serve"]
