FROM golang:1.18-bullseye as build-env

WORKDIR /go/src/app

COPY go.mod .
COPY go.sum .
RUN go mod download

COPY cmd cmd
COPY lib lib
COPY Makefile .

RUN make all

FROM gcr.io/distroless/static
USER nonroot
COPY --from=build-env /go/src/app/dist/tcplb /
CMD ["/tcplb"]
