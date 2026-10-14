# llama-server for KubeSolo's assistant: a static, CPU-only build against musl,
# so that the one binary runs on any glibc or musl host.
#
# Build with: docker build --platform linux/<arch> -f ./build/llama-server.Dockerfile -t llama-server-builder .
# Extract with:
#   docker create --name llama-server-extract llama-server-builder noop
#   docker cp llama-server-extract:/llama-server ./internal/core/embedded/bin/llama-server
#   docker rm llama-server-extract

FROM alpine:3.22 AS builder

ARG LLAMA_CPP_VERSION=v0.6.0

RUN apk add --no-cache build-base cmake git linux-headers

RUN git clone --depth 1 --branch ${LLAMA_CPP_VERSION} https://github.com/ggml-org/llama.cpp.git /src/llama.cpp
WORKDIR /src/llama.cpp

# GGML_NATIVE=OFF builds for the architecture baseline rather than the build
# machine's CPU, with the faster kernels picked at runtime. OpenMP is off
# because it cannot be linked statically; llama.cpp uses its own thread pool.
RUN cmake -B build \
        -DCMAKE_BUILD_TYPE=Release \
        -DBUILD_SHARED_LIBS=OFF \
        -DGGML_NATIVE=OFF \
        -DGGML_OPENMP=OFF \
        -DLLAMA_CURL=OFF \
        -DLLAMA_OPENSSL=OFF \
        -DLLAMA_BUILD_TESTS=OFF \
        -DLLAMA_BUILD_EXAMPLES=OFF \
        -DCMAKE_EXE_LINKER_FLAGS="-static" && \
    cmake --build build --target llama-server -j"$(nproc)" && \
    strip -o /llama-server build/bin/llama-server

FROM scratch
COPY --from=builder /llama-server /llama-server
