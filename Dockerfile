FROM golang:1.22-alpine AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
RUN CGO_ENABLED=0 go build -o minio-gateway .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /app/minio-gateway .
EXPOSE 8080
ENV GATEWAY_PORT=8080
CMD ["./minio-gateway"]
