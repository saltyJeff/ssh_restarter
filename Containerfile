FROM docker.io/golang:1.25.1-alpine3.21
ADD *.go go.mod go.sum .
RUN go build -o /bin/ssh_restarter .

FROM docker.io/amazoncorretto:24-alpine3.22
COPY --from=0 /bin/ssh_restarter /bin/ssh_restarter
