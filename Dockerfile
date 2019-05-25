FROM golang:1.12.5
COPY app /
ENTRYPOINT ["/app"]
