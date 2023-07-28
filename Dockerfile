# pre build
FROM tdewolff/minify:latest as pre

FROM golang:1.19.11-alpine3.18 as builder
RUN apk --no-cache add git make
COPY --from=pre /usr/bin/minify /usr/bin/minify
WORKDIR /nothingtodo
ADD . /nothingtodo
RUN go mod download \
    && make minify \
    && make build

# runtime
FROM scratch as runtime
COPY --from=builder /nothingtodo/dist/nothingtodo .
ENTRYPOINT ["./nothingtodo"]