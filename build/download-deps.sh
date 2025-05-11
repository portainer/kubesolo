#!/bin/bash
# download-deps.sh

# Default OS and architecture
OS="linux"
ARCH="amd64"
# Set versions
CONTAINERD_VERSION="2.0.4"
RUNC_VERSION="v1.1.9"
CNI_VERSION="v1.3.0"
PORTAINER_AGENT_VERSION="2.29.2"
COREDNS_VERSION="1.12.1"

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

# Download containerd - add error checking
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

tar -xzf internal/core/embedded/bin/containerd.tar.gz -C internal/core/embedded/bin/containerd
rm internal/core/embedded/bin/containerd.tar.gz

# Download runc - add error checking
echo "Downloading runc ${RUNC_VERSION} for ${ARCH}..."
if ! curl -L -f --silent -o internal/core/embedded/bin/runc https://github.com/opencontainers/runc/releases/download/${RUNC_VERSION}/runc.${ARCH}; then
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
    echo "Docker is not installed or not in PATH. Cannot download container images."
    echo "Please install Docker to download container images, or run the script without image downloads."
    echo "Other dependencies downloaded successfully."
    exit 0
fi

# Download Portainer Agent
echo "Downloading Portainer Agent ${PORTAINER_AGENT_VERSION}..."
PORTAINER_IMAGE="portainer/agent:${PORTAINER_AGENT_VERSION}"
if ! docker image pull --platform ${OS}/${ARCH} ${PORTAINER_IMAGE}; then
    echo "Error pulling Portainer Agent image. Skipping."
else
    echo "Saving Portainer Agent image to tar..."
    if ! docker save ${PORTAINER_IMAGE} | gzip > internal/core/embedded/bin/images/portainer-agent.tar.gz; then
        echo "Error saving Portainer Agent image. Skipping."
    else
        echo "Portainer Agent image saved successfully."
    fi
fi

# Download CoreDNS
echo "Downloading CoreDNS ${COREDNS_VERSION}..."
COREDNS_IMAGE="coredns/coredns:${COREDNS_VERSION}"
if ! docker image pull --platform ${OS}/${ARCH} ${COREDNS_IMAGE}; then
    echo "Error pulling CoreDNS image. Skipping."
else
    echo "Saving CoreDNS image to tar..."
    if ! docker save ${COREDNS_IMAGE} | gzip > internal/core/embedded/bin/images/coredns.tar.gz; then
        echo "Error saving CoreDNS image. Skipping."
    else
        echo "CoreDNS image saved successfully."
    fi
fi

echo "Dependencies downloaded successfully"
