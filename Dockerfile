# syntax=docker/dockerfile:1
FROM golang:1.23-alpine AS builder
WORKDIR /src

COPY go.mod go.sum* ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -o /out/server ./cmd/server

# distroless static (not scratch) so we get a CA bundle for HTTPS calls to
# api.radiofrance.fr, plus a non-root user, at no extra image-build cost.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=builder /out/server /server
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/server"]
