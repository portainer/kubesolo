#!/bin/bash
# download-deps.sh

# Default OS and architecture
OS="linux"
ARCH="amd64"
# Set versions
CONTAINERD_VERSION="2.1.5"
CRUN_VERSION="1.26"
CNI_VERSION="v1.9.0"
PORTAINER_AGENT_VERSION="2.39.0"
COREDNS_VERSION="1.14.1"
LOCAL_PATH_PROVISIONER_VERSION="v0.0.34"
PAUSE_IMAGE_VERSION="3.10"

# Offline mode embeds all OCI images; online mode (default) skips optional images
OFFLINE=false

# Process command line arguments
while [[ "$#" -gt 0 ]]; do
    case $1 in
        --os=*) OS="${1#*=}"; shift ;;
        --os) OS="$2"; shift 2 ;;
        --arch=*) ARCH="${1#*=}"; shift ;;
        --arch) ARCH="$2"; shift 2 ;;
        --offline) OFFLINE=true; shift ;;
        *) echo "Unknown parameter: $1"; exit 1 ;;
    esac
done

echo "Using OS: ${OS}, Architecture: ${ARCH}, Offline: ${OFFLINE}"

# Create bin directories
mkdir -p internal/core/embedded/bin/containerd
mkdir -p internal/core/embedded/bin/cni
mkdir -p internal/core/embedded/bin/images

# Get containerd binaries
if [ "${ARCH}" = "arm" ]; then
    # Build containerd for ARM using Docker cross-compilation
    echo "Building containerd ${CONTAINERD_VERSION} for ${OS}-${ARCH} using Docker..."
    
    # Check if Docker is available
    if ! command -v docker &> /dev/null; then
        echo "Docker is required to build containerd for ARM but is not installed."
        echo "Please install Docker or use pre-built binaries."
        exit 1
    fi
    
    # Build the containerd image with the specified version
    if ! docker build -f build/containerd.Dockerfile --build-arg CONTAINERD_VERSION=v${CONTAINERD_VERSION} -t containerd-arm32-cross .; then
        echo "Error building containerd Docker image."
        exit 1
    fi
    
    # Extract the compiled archive
    echo "Extracting containerd binaries..."
    if ! docker create --name temp-containerd containerd-arm32-cross; then
        echo "Error creating temporary container."
        exit 1
    fi
    
    if ! docker cp temp-containerd:/containerd-v${CONTAINERD_VERSION}-linux-arm32.tar.gz internal/core/embedded/bin/containerd.tar.gz; then
        echo "Error extracting containerd archive from container."
        docker rm temp-containerd 2>/dev/null
        exit 1
    fi
    
    docker rm temp-containerd
    
    # Verify the archive
    if ! tar -tf internal/core/embedded/bin/containerd.tar.gz >/dev/null 2>&1; then
        echo "Built containerd archive is not valid."
        exit 1
    fi
    
    echo "Successfully built containerd for ARM."
else
    # Download containerd for other architectures
    echo "Downloading containerd ${CONTAINERD_VERSION} for ${OS}-${ARCH}..."
    if ! curl -L -f --silent -o internal/core/embedded/bin/containerd.tar.gz https://github.com/containerd/containerd/releases/download/v${CONTAINERD_VERSION}/containerd-${CONTAINERD_VERSION}-${OS}-${ARCH}.tar.gz; then
        echo "Error downloading containerd. Please check the version and URL."
        exit 1
    fi

    # Verify the download is a valid tar file
    if ! tar -tf internal/core/embedded/bin/containerd.tar.gz >/dev/null 2>&1; then
        echo "Downloaded containerd archive is not valid. Check URL or try again."
        exit 1
    fi
fi

# Extract containerd binaries and zstd-compress the shim for embedding
tar -xzf internal/core/embedded/bin/containerd.tar.gz -C internal/core/embedded/bin/containerd
rm internal/core/embedded/bin/containerd.tar.gz
zstd -19 --rm -q internal/core/embedded/bin/containerd/bin/containerd-shim-runc-v2

# Download crun - add error checking
# crun does not publish 32-bit ARM binaries; fail early for unsupported architectures
if [ "${ARCH}" = "arm" ]; then
    echo "Error: crun does not provide pre-built binaries for 32-bit ARM (armhf)."
    echo "Please build crun from source or use a supported architecture (amd64, arm64, riscv64)."
    exit 1
fi

echo "Downloading crun ${CRUN_VERSION} for ${ARCH}..."
if ! curl -L -f --silent -o internal/core/embedded/bin/crun https://github.com/containers/crun/releases/download/${CRUN_VERSION}/crun-${CRUN_VERSION}-linux-${ARCH}; then
    echo "Error downloading crun. Please check the version and URL."
    exit 1
fi
zstd -19 --rm -q internal/core/embedded/bin/crun

# Download CNI plugins - add error checking
echo "Downloading CNI plugins ${CNI_VERSION} for ${OS}-${ARCH}..."
if ! curl -L -f --silent -o internal/core/embedded/bin/cni/cni-plugins.tgz https://github.com/containernetworking/plugins/releases/download/${CNI_VERSION}/cni-plugins-${OS}-${ARCH}-${CNI_VERSION}.tgz; then
    echo "Error downloading CNI plugins. Please check the version and URL."
    exit 1
fi

# Verify the download is a valid tar file
if ! tar -tf internal/core/embedded/bin/cni/cni-plugins.tgz >/dev/null 2>&1; then
    echo "Downloaded CNI plugins archive is not valid. Check URL or try again."
    exit 1
fi

tar -xzf internal/core/embedded/bin/cni/cni-plugins.tgz -C internal/core/embedded/bin/cni
rm internal/core/embedded/bin/cni/cni-plugins.tgz

# zstd-compress CNI plugin binaries for embedding
for plugin in bridge host-local portmap loopback; do
    zstd -19 --rm -q "internal/core/embedded/bin/cni/${plugin}"
done

# Download container images
echo "Checking if Crane is available..."
if ! command -v crane &> /dev/null; then
    VERSION=$(curl -s "https://api.github.com/repos/google/go-containerregistry/releases/latest" | jq -r '.tag_name')
    curl -sL "https://github.com/google/go-containerregistry/releases/download/${VERSION}/go-containerregistry_Linux_x86_64.tar.gz" > go-containerregistry.tar.gz
    tar -zxvf go-containerregistry.tar.gz -C /usr/local/bin/ crane
    rm -f go-containerregistry.tar.gz
fi

# Download CoreDNS (always embedded)
echo "Downloading CoreDNS ${COREDNS_VERSION}..."
COREDNS_IMAGE="coredns/coredns:${COREDNS_VERSION}"
if ! crane pull --platform ${OS}/${ARCH} ${COREDNS_IMAGE} internal/core/embedded/bin/images/coredns.tar; then
    echo "Error pulling CoreDNS image."
    exit 1
fi
if ! gzip -f internal/core/embedded/bin/images/coredns.tar; then
    echo "Error compressing CoreDNS image."
    exit 1
fi
echo "CoreDNS image saved successfully."

# Download optional images only for offline builds
if [ "${OFFLINE}" = "true" ]; then
    # Download Portainer Agent (skip for riscv64 as it's not supported)
    if [ "${ARCH}" != "riscv64" ]; then
        echo "Downloading Portainer Agent ${PORTAINER_AGENT_VERSION}..."
        PORTAINER_IMAGE="portainer/agent:${PORTAINER_AGENT_VERSION}"
        if ! crane pull --platform ${OS}/${ARCH} ${PORTAINER_IMAGE} internal/core/embedded/bin/images/portainer-agent.tar; then
            echo "Error pulling Portainer Agent image."
            exit 1
        fi
        if ! gzip -f internal/core/embedded/bin/images/portainer-agent.tar; then
            echo "Error compressing Portainer Agent image."
            exit 1
        fi
        echo "Portainer Agent image saved and compressed successfully."
    else
        echo "Skipping Portainer Agent download for ${ARCH} (not supported)"
    fi

    # Download Local Path Provisioner
    echo "Downloading Local Path Provisioner ${LOCAL_PATH_PROVISIONER_VERSION}..."
    LOCAL_PATH_PROVISIONER_IMAGE="rancher/local-path-provisioner:${LOCAL_PATH_PROVISIONER_VERSION}"
    if ! crane pull --platform ${OS}/${ARCH} ${LOCAL_PATH_PROVISIONER_IMAGE} internal/core/embedded/bin/images/local-path-provisioner.tar; then
        echo "Error pulling Local Path Provisioner image."
        exit 1
    fi
    if ! gzip -f internal/core/embedded/bin/images/local-path-provisioner.tar; then
        echo "Error compressing Local Path Provisioner image."
        exit 1
    fi
    echo "Local Path Provisioner image saved successfully."
else
    echo "Skipping optional image downloads (online build). Images will be pulled at runtime."
fi

# Download Kubernetes pause image
echo "Downloading Portainer pause image..."
PAUSE_IMAGE="portainer/pause:latest"
if ! crane pull --platform ${OS}/${ARCH} ${PAUSE_IMAGE} internal/core/embedded/bin/images/pause.tar; then
    echo "Error pulling Kubernetes pause image."
    exit 1
fi
# Compress it to save space
if ! gzip -f internal/core/embedded/bin/images/pause.tar; then
    echo "Error compressing Kubernetes pause image."
    exit 1
fi
echo "Kubernetes pause image saved successfully."

echo "Dependencies downloaded successfully"