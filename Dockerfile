FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /karakeep-random-rss .

# Pre-create /out/data here so it can be COPY --chown'd into the distroless
# final stage below (which has no shell, so `RUN mkdir` isn't available
# there). See the final stage for why this directory needs to exist.
RUN mkdir -p /out/data

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /karakeep-random-rss /karakeep-random-rss

# store.go writes state.json via an atomic write-then-rename: it first
# writes state.json.tmp into STATE_FILE's directory, then renames it into
# place. Docker only carries ownership into a named volume mounted at
# /data on that volume's *first* use, via "copy-up" from an image
# directory that already exists at that exact path — if the mount point
# is absent from the image, Docker creates it fresh as root:root 0755
# instead, and the nonroot process (uid/gid 65532, this image's USER)
# can never write state.json.tmp there. Baking in an empty, nonroot-owned
# /data here is what makes the copy-up carry the right ownership.
COPY --from=build --chown=nonroot:nonroot /out/data /data

# State lives here; the named volume in compose.yaml / percival's
# docker-compose.yml is what makes the seen-set survive container
# rebuilds — see the COPY --chown above for why the mount point must
# already exist in the image, owned by nonroot.
ENV STATE_FILE=/data/state.json
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/karakeep-random-rss"]
