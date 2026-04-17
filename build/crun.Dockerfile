# crun build for arm/v7 using Debian Linux (glibc)
#
# Strategy: Build natively inside an arm/v7 Debian container via QEMU.
# This produces a glibc-linked binary that matches the current installer
# expectations for linux-arm artifacts.
#
# Build with: docker build --platform linux/arm/v7 -f ./build/crun.Dockerfile -t crun-arm32-builder .
# Extract with:
#   docker create --name crun-extract crun-arm32-builder noop
#   docker cp crun-extract:/crun ./internal/core/embedded/bin/crun
#   docker rm crun-extract

FROM --platform=linux/arm/v7 debian:12-slim AS builder

ARG CRUN_VERSION=1.26

# Install build dependencies
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
        autoconf \
        automake \
        build-essential \
        ca-certificates \
        git \
        go-md2man \
        gperf \
        libcap-dev \
        libseccomp-dev \
        libsystemd-dev \
        libtool \
        linux-libc-dev \
        pkg-config \
        python3 \
        && rm -rf /var/lib/apt/lists/*

# Clone crun repository
RUN git clone --recursive https://github.com/containers/crun.git /src/crun
WORKDIR /src/crun

# Checkout specific version
RUN git checkout ${CRUN_VERSION} && \
    git submodule update --init --recursive

# Generate configure script
RUN ./autogen.sh

# Configure for glibc build with systemd support to match upstream features
RUN ./configure \
    --enable-systemd \
    --enable-embedded-yajl \
    --with-cap \
    --with-seccomp

# Build
RUN make -j"$(nproc)"

# Package output
RUN mkdir -p /output/bin && \
    cp crun /output/bin/ && \
    strip /output/bin/crun

# Final stage: copy the built binary to a clean image
FROM scratch
COPY --from=builder /output/bin/crun /crun
