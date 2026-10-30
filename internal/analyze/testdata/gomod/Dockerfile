FROM golang:1.22 AS build
COPY . .
RUN go build -o /shop ./cmd/shop
ENTRYPOINT ["/shop"]
CMD ["serve"]
