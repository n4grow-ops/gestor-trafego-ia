# Imagens pelo espelho do Google: sem o limite de downloads anônimos do Docker Hub.
FROM mirror.gcr.io/library/golang:1.26-alpine3.22 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/worker ./cmd/worker \
 && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/admin ./cmd/admin

FROM mirror.gcr.io/library/alpine:3.22
RUN apk add --no-cache ca-certificates tzdata && adduser -D -H app
COPY --from=build /out/ /app/
USER app
EXPOSE 8080
CMD ["/app/api"]
