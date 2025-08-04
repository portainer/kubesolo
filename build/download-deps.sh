#!/bin/bash
# download-deps.sh

# Default OS and architecture
OS="linux"
ARCH="amd64"
# Set versions
CONTAINERD_VERSION="2.0.5"
RUNC_VERSION="v1.2.5"
CNI_VERSION="v1.3.0"
PORTAINER_AGENT_VERSION="2.32.0"
COREDNS_VERSION="1.12.2"
LOCAL_PATH_PROVISIONER_VERSION="v0.0.31"
PAUSE_IMAGE_VERSION="3.10"

# Process command line arguments
while [[ "$#" -gt 0 ]]; do
    case $1 in
        --os=*) OS="${1#*=}"; shift ;;
        --os) OS="$2"; shift 2 ;;
        --arch=*) ARCH="${1#*=}"; shift ;;
        --arch) ARCH="$2"; shift 2 ;;
        *) echo "Unknown parameter: $1"; exit 1 ;;
    esac
done

echo "Using OS: ${OS}, Architecture: ${ARCH}"

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

# Extract containerd binaries
tar -xzf internal/core/embedded/bin/containerd.tar.gz -C internal/core/embedded/bin/containerd
rm internal/core/embedded/bin/containerd.tar.gz

# Download runc - add error checking
# Map architecture to runc filename (arm -> armhf for runc releases)
RUNC_ARCH=${ARCH}
if [ "${ARCH}" = "arm" ]; then
    RUNC_ARCH="armhf"
fi

echo "Downloading runc ${RUNC_VERSION} for ${ARCH} (using runc.${RUNC_ARCH})..."
if ! curl -L -f --silent -o internal/core/embedded/bin/runc https://github.com/opencontainers/runc/releases/download/${RUNC_VERSION}/runc.${RUNC_ARCH}; then
    echo "Error downloading runc. Please check the version and URL."
    exit 1
fi
chmod +x internal/core/embedded/bin/runc

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

# Download container images
echo "Checking if Docker is available..."
if ! command -v docker &> /dev/null; then
    echo "Error: Docker is not installed or not in PATH. Docker is required to download container images for build-time embedding."
    exit 1
fi

# Download Portainer Agent (skip for riscv64 as it's not supported)
if [ "${ARCH}" != "riscv64" ]; then
    echo "Downloading Portainer Agent ${PORTAINER_AGENT_VERSION}..."
    PORTAINER_IMAGE="portainer/agent:${PORTAINER_AGENT_VERSION}"
    if ! docker image pull --platform ${OS}/${ARCH} ${PORTAINER_IMAGE}; then
        echo "Error pulling Portainer Agent image."
        exit 1
    fi
    echo "Saving Portainer Agent image to tar..."
    if ! docker save ${PORTAINER_IMAGE} | gzip > internal/core/embedded/bin/images/portainer-agent.tar.gz; then
        echo "Error saving Portainer Agent image."
        exit 1
    fi
    echo "Portainer Agent image saved successfully."
else
    echo "Skipping Portainer Agent download for ${ARCH} (not supported)"
fi

# Download CoreDNS
echo "Downloading CoreDNS ${COREDNS_VERSION}..."
COREDNS_IMAGE="coredns/coredns:${COREDNS_VERSION}"
if ! docker image pull --platform ${OS}/${ARCH} ${COREDNS_IMAGE}; then
    echo "Error pulling CoreDNS image."
    exit 1
fi
echo "Saving CoreDNS image to tar..."
if ! docker save ${COREDNS_IMAGE} | gzip > internal/core/embedded/bin/images/coredns.tar.gz; then
    echo "Error saving CoreDNS image."
    exit 1
fi
echo "CoreDNS image saved successfully."

# Download Local Path Provisioner
echo "Downloading Local Path Provisioner ${LOCAL_PATH_PROVISIONER_VERSION}..."
LOCAL_PATH_PROVISIONER_IMAGE="rancher/local-path-provisioner:${LOCAL_PATH_PROVISIONER_VERSION}"
if ! docker image pull --platform ${OS}/${ARCH} ${LOCAL_PATH_PROVISIONER_IMAGE}; then
    echo "Error pulling Local Path Provisioner image."
    exit 1
fi
echo "Saving Local Path Provisioner image to tar..."
if ! docker save ${LOCAL_PATH_PROVISIONER_IMAGE} | gzip > internal/core/embedded/bin/images/local-path-provisioner.tar.gz; then
    echo "Error saving Local Path Provisioner image."
    exit 1
fi
echo "Local Path Provisioner image saved successfully."

# Download Kubernetes pause image
echo "Downloading Portainer pause image..."
PAUSE_IMAGE="portainer/pause:latest"
if ! docker image pull --platform ${OS}/${ARCH} ${PAUSE_IMAGE}; then
    echo "Error pulling Kubernetes pause image."
    exit 1
fi
echo "Saving Kubernetes pause image to tar..."
if ! docker save ${PAUSE_IMAGE} | gzip > internal/core/embedded/bin/images/pause.tar.gz; then
    echo "Error saving Kubernetes pause image."
    exit 1
fi
echo "Kubernetes pause image saved successfully."

echo "Dependencies downloaded successfully"