# Containerd cross-compilation for arm32 on native host
FROM golang:1.24-bullseye

# Install cross-compilation toolchain and dependencies
RUN dpkg --add-architecture armhf && \
    apt-get update && \
    apt-get install -y \
        git \
        make \
        pkg-config \
        gcc-arm-linux-gnueabihf \
        libc6-dev-armhf-cross \
        libseccomp-dev:armhf \
        libbtrfs-dev:armhf \
        libdevmapper-dev:armhf \
        crossbuild-essential-armhf && \
    rm -rf /var/lib/apt/lists/*

# Set up cross-compilation environment
ENV GOPATH=/go
ENV PATH=$PATH:/go/bin

# Cross-compilation settings
ENV CC=arm-linux-gnueabihf-gcc
ENV CXX=arm-linux-gnueabihf-g++
ENV CGO_ENABLED=1
ENV GOOS=linux
ENV GOARCH=arm
ENV GOARM=7

# Set pkg-config to find armhf libraries
ENV PKG_CONFIG_PATH=/usr/lib/arm-linux-gnueabihf/pkgconfig
ENV PKG_CONFIG_LIBDIR=/usr/lib/arm-linux-gnueabihf/pkgconfig

# Clone containerd repository
ARG CONTAINERD_VERSION=v2.0.5
RUN git clone https://github.com/containerd/containerd.git /go/src/github.com/containerd/containerd
WORKDIR /go/src/github.com/containerd/containerd

# Checkout specific version
RUN git checkout ${CONTAINERD_VERSION}

# Build with full features
ENV BUILDTAGS="seccomp selinux apparmor btrfs_noversion"
RUN make binaries

# Copy only the required binaries to output directory
RUN mkdir -p /output/bin && \
    cp bin/containerd /output/bin/ && \
    cp bin/containerd-shim-runc-v2 /output/bin/ && \
    cp bin/containerd-stress /output/bin/ && \
    cp bin/ctr /output/bin/

# Create tar.gz archive
RUN cd /output && \
    tar -czf /containerd-${CONTAINERD_VERSION}-linux-arm32.tar.gz bin/

# Final stage - just the tar.gz
FROM scratch
COPY --from=0 /containerd-*.tar.gz /
CMD ["/bin/sh"]