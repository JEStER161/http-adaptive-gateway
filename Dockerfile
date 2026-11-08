# syntax=docker/dockerfile:1
FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/bin

FROM alpine:3.21
RUN addgroup -S app && adduser -S -G app app
WORKDIR /app
COPY --from=build /out/gateway /app/gateway
COPY --from=build /src/configs /app/configs
COPY --from=build /src/docs /app/docs
USER app
EXPOSE 8080
ENTRYPOINT ["/app/gateway"]
CMD ["-config", "/app/configs/env.yaml"]
