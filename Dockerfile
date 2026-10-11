FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/desakita ./cmd/desakita

FROM alpine:3.22
RUN adduser -D -H -u 10001 app && mkdir -p /data/uploads && chown app /data/uploads
COPY --from=build /out/desakita /usr/local/bin/desakita
USER app
ENV APP_ENV=production HTTP_ADDR=:8080 UPLOAD_DIR=/data/uploads
EXPOSE 8080
CMD ["sh", "-c", "desakita migrate up && exec desakita serve"]
