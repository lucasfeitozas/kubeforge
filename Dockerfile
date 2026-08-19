FROM golang:1.23-alpine AS builder
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/kubeforge ./cmd/kubeforge

FROM alpine:3.20
RUN adduser -D -u 10001 kubeforge
COPY --from=builder /out/kubeforge /usr/local/bin/kubeforge
USER kubeforge
EXPOSE 8080
ENTRYPOINT ["kubeforge"]
