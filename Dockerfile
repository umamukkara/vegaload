# syntax=docker/dockerfile:1

# ---- build stage ----
FROM golang:1.22-alpine AS build
WORKDIR /src

# Cache module downloads separately from source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/vegaload \
    ./cmd/vegaload

# ---- final stage ----
# scratch, not a distro base image: VegaLoad's core promise (see
# AGENTS.md) is a single static binary, and this image is that promise
# carried into containers. It has no shell, no package manager, and no
# python3 — a scenario using the Python scripting driver
# (internal/scripting/python) needs a python3 on PATH, which this image
# deliberately does not carry, to keep the default image as small as the
# binary itself. If you need Python scenarios in a container, build your
# own image FROM this one (or FROM a distro base copying the binary in)
# and add a python3 package for your distro of choice.
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /out/vegaload /usr/local/bin/vegaload
ENTRYPOINT ["/usr/local/bin/vegaload"]
CMD ["help"]
