# pre build
FROM tdewolff/minify:latest as pre

# FROM balenalib/armv7hf-alpine-golang:1.18-3.15 as builder
FROM golang:1.19-alpine as builder
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