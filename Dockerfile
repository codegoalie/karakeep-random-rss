FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /karakeep-random-rss .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /karakeep-random-rss /karakeep-random-rss
# State lives here; mount a volume so the seen-set survives container rebuilds.
ENV STATE_FILE=/data/state.json
VOLUME ["/data"]
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/karakeep-random-rss"]
