# --- build -----------------------------------------------------------------
FROM golang:1.26-alpine AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/hookrelay ./cmd/hookrelay

# --- runtime --------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/hookrelay /hookrelay
EXPOSE 8080
ENTRYPOINT ["/hookrelay"]
