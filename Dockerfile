FROM golang:1.26.2-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /delivery-service ./cmd/api

FROM alpine:3.22

WORKDIR /app

COPY --from=builder /delivery-service /app/delivery-service

EXPOSE 8080

CMD ["/app/delivery-service"]