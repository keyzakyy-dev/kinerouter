# syntax=docker/dockerfile:1

# ---- web: build the Vite dashboard into web/dist (embedded by go:embed) ----
FROM node:24-alpine AS web
WORKDIR /src/web-app
COPY web-app/package.json web-app/package-lock.json ./
RUN npm ci
COPY web-app/ ./
RUN npm run build

# ---- go: static binary with the dashboard embedded ----
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web /src/web/dist ./web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /kinerouter ./cmd/kinerouter

# ---- runtime ----
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /kinerouter /kinerouter
# MIT requires the copyright line to travel with the binary.
COPY --from=build /src/LICENSE /licenses/LICENSE
COPY --from=build /src/NOTICE /licenses/NOTICE
# Pre-create the data dir owned by nonroot so a fresh named volume inherits it.
COPY --from=build --chown=nonroot:nonroot --chmod=700 /src/go.mod /data/.keep
ENV DATA_DIR=/data PORT=20128 HOST=0.0.0.0
VOLUME /data
EXPOSE 20128
USER nonroot:nonroot
ENTRYPOINT ["/kinerouter"]
