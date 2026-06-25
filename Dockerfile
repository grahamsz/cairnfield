FROM node:20-alpine AS frontend

WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci
COPY tsconfig.json vite.config.ts ./
COPY frontend ./frontend
RUN npm run build

FROM golang:1.25-alpine AS build
RUN apk add --no-cache build-base

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/cairnfield ./cmd/cairnfield

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
	&& addgroup -S -g 10001 cairnfield \
	&& adduser -S -D -H -u 10001 -G cairnfield -s /sbin/nologin cairnfield \
	&& mkdir -p /data /app/frontend/dist \
	&& chown -R cairnfield:cairnfield /data

WORKDIR /app
COPY --from=build /out/cairnfield /usr/local/bin/cairnfield
COPY --from=frontend /src/frontend/dist /app/frontend/dist

USER cairnfield
EXPOSE 8080
VOLUME ["/data"]

ENV CAIRNFIELD_ADDR=:8080
ENV CAIRNFIELD_DATA_DIR=/data

ENTRYPOINT ["/usr/local/bin/cairnfield"]
