FROM balenalib/armv7hf-alpine-golang:1.18-3.15 as builder

RUN apk --no-cache add build-base git mercurial gcc
WORKDIR /nothingtodo
ADD . /nothingtodo
RUN go mod download
RUN CGO_ENABLED=0 GOOS=linux go build -a -o nothingtodo .

FROM scratch
COPY --from=builder /nothingtodo .
ENTRYPOINT ["./nothingtodo"]