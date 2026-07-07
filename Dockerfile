FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS build

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build \
    -trimpath \
    -ldflags="-s -w -X github.com/yegor-usoltsev/magichop/internal/release.Version=${VERSION}" \
    -o /out/magichop .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tini tzdata

ENV MAGICHOP_HOST=0.0.0.0 \
    MAGICHOP_PORT=4222 \
    MAGICHOP_AUTH_TOKEN=

COPY --from=build /out/magichop /usr/local/bin/magichop

EXPOSE 4222
ENTRYPOINT ["tini", "--", "magichop"]
CMD ["server"]
