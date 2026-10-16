FROM --platform=$BUILDPLATFORM golang:1.26.8-bookworm AS build
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /product-service .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /product-service /product-service
ENV PORT=8080
EXPOSE 8080
ENTRYPOINT ["/product-service"]
