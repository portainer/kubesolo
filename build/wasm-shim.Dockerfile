# WASM shim (containerd-shim-wasmtime-v1) cross-compilation for arm32 on native host
FROM rust:bullseye

# Add armhf architecture and install cross-compilation toolchain and dependencies
RUN dpkg --add-architecture armhf && \
    apt-get update && \
    apt-get install -y \
        git \
        pkg-config \
        gcc-arm-linux-gnueabihf \
        libc6-dev-armhf-cross \
        crossbuild-essential-armhf \
        libseccomp-dev:armhf && \
    rm -rf /var/lib/apt/lists/*

# Add the armv7 Rust target
RUN rustup target add armv7-unknown-linux-gnueabihf

# Set linker for the armv7 target
ENV CARGO_TARGET_ARMV7_UNKNOWN_LINUX_GNUEABIHF_LINKER=arm-linux-gnueabihf-gcc

# Clone runwasi repository
ARG RUNWASI_VERSION=v0.6.0
RUN git clone https://github.com/containerd/runwasi.git /runwasi
WORKDIR /runwasi

# Checkout specific version
RUN git checkout containerd-shim-wasmtime/${RUNWASI_VERSION}

# Build the wasmtime shim for armv7
RUN cargo build --target armv7-unknown-linux-gnueabihf --release -p containerd-shim-wasmtime

# Copy binary to output path
RUN mkdir -p /output && \
    cp target/armv7-unknown-linux-gnueabihf/release/containerd-shim-wasmtime-v1 /output/containerd-shim-wasmtime-v1

# Final stage - just the binary
FROM scratch
COPY --from=0 /output/containerd-shim-wasmtime-v1 /containerd-shim-wasmtime-v1
