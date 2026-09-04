FROM golang:1.25.1 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /app ./cmd/web
FROM scratch
COPY --from=build /app /app
ENV PORT=8080 DATABASE_PATH=/data/noten.db TZ=Europe/Berlin
EXPOSE 8080
ENTRYPOINT ["/app"]
