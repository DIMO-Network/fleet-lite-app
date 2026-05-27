FROM golang:1.26 AS build

ARG COMMIT_HASH=dev

RUN useradd -u 10001 dimo

WORKDIR /go/src/github.com/DIMO-Network/fleet-lite-app/
# Order matters: /web is copied first so /api/Makefile wins on collision.
# (web/Makefile only has lint targets; the Go build needs api/Makefile's `install`.)
COPY /web /go/src/github.com/DIMO-Network/fleet-lite-app/
COPY /api /go/src/github.com/DIMO-Network/fleet-lite-app/

ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOFLAGS=-mod=vendor

RUN apt-get clean && apt-get update
RUN curl -fsSL https://deb.nodesource.com/setup_22.x | bash -
RUN apt-get install -y nodejs
RUN go mod download
RUN go mod tidy
RUN go mod vendor
RUN make install COMMIT=${COMMIT_HASH}
RUN npm install && npm run build

FROM busybox AS package

LABEL maintainer="DIMO <hello@dimo.zone>"

WORKDIR /

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /etc/passwd /etc/passwd
COPY --from=build /go/src/github.com/DIMO-Network/fleet-lite-app/target/bin/fleet-lite-app .
COPY --from=build /go/src/github.com/DIMO-Network/fleet-lite-app/internal/db/migrations /internal/db/migrations
COPY --from=build /go/src/github.com/DIMO-Network/fleet-lite-app/dist /dist

USER dimo

EXPOSE 8084
EXPOSE 8085

CMD ["/fleet-lite-app"]
