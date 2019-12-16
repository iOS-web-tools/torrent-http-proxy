FROM golang:latest

# copy the source files
COPY . /go/src/bitbucket.org/vintikzzzz/torrent-http-proxy

WORKDIR /go/src/bitbucket.org/vintikzzzz/torrent-http-proxy

# enable modules
ENV GO111MODULE=on

# disable crosscompiling 
ENV CGO_ENABLED=0

# compile linux only
ENV GOOS=linux

# build the binary with debug information removed
RUN go build -mod=vendor -ldflags '-w -s' -a -installsuffix cgo -o server

FROM scratch

# copy our static linked library
COPY --from=0 /go/src/bitbucket.org/vintikzzzz/torrent-http-proxy/server .

# tell we are exposing our service on port 8080
EXPOSE 8080

# run it!
CMD ["./server"]