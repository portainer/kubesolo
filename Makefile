GOOS ?= linux
GOARCH ?= $(shell go env GOARCH)
OUTPUT ?= ./dist/kubesolo

VERSION ?= $(shell git describe --tags --always --dirty)
K8S_VERSION ?= $(shell awk '/k8s\.io\/kubernetes/ && !/>/ {gsub(/^[ \t]+|[ \t]+$$/, "", $$2); print $$2}' go.mod)
COMMIT ?= $(shell git rev-parse --short HEAD)
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

LDFLAGS_STRING = -s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE} -X k8s.io/component-base/version.gitVersion=${K8S_VERSION}+kubesolo-${VERSION}

# Cross-compilation settings
CC_arm64 = aarch64-linux-gnu-gcc
CC_amd64 = x86_64-linux-gnu-gcc
CC_arm = arm-linux-gnueabihf-gcc  # ARM Hard Float (ARMHF) - targets ARMv7+ with hardware FPU
CC_riscv64 = riscv64-linux-gnu-gcc

# musl cross-compilers (for Alpine Linux compatibility)
CC_arm64_musl = aarch64-linux-musl-gcc
CC_amd64_musl = x86_64-linux-musl-gcc
CC_arm_musl = arm-linux-musleabihf-gcc
CC_riscv64_musl = riscv64-linux-musl-gcc

# CGO flags - enable SQLite dbstat virtual table for kine db size reporting
CGO_CFLAGS_EXTRA = -DSQLITE_ENABLE_DBSTAT_VTAB

# Install cross-compilation toolchains
.PHONY: install-cross-compilers
install-cross-compilers:
	apt-get update
	apt-get install -y gcc-aarch64-linux-gnu gcc-x86-64-linux-gnu gcc-arm-linux-gnueabihf gcc-riscv64-linux-gnu

# Install musl cross-compilation toolchains
.PHONY: install-musl-cross-compilers
install-musl-cross-compilers:
	apt-get update
	apt-get install -y musl-tools
	# Install musl cross-compilers from musl.cc
	wget -q https://kubesolo-io-assets.sfo3.cdn.digitaloceanspaces.com/musl/aarch64-linux-musl-cross.tgz -O /tmp/aarch64-musl.tgz
	wget -q https://kubesolo-io-assets.sfo3.cdn.digitaloceanspaces.com/musl/x86_64-linux-musl-cross.tgz -O /tmp/x86_64-musl.tgz
	wget -q https://kubesolo-io-assets.sfo3.cdn.digitaloceanspaces.com/musl/arm-linux-musleabihf-cross.tgz -O /tmp/arm-musl.tgz
	wget -q https://kubesolo-io-assets.sfo3.cdn.digitaloceanspaces.com/musl/riscv64-linux-musl-cross.tgz -O /tmp/riscv64-musl.tgz
	cd /opt && tar -xzf /tmp/aarch64-musl.tgz && tar -xzf /tmp/x86_64-musl.tgz && tar -xzf /tmp/arm-musl.tgz && tar -xzf /tmp/riscv64-musl.tgz
	ln -sf /opt/aarch64-linux-musl-cross/bin/aarch64-linux-musl-gcc /usr/local/bin/aarch64-linux-musl-gcc
	ln -sf /opt/x86_64-linux-musl-cross/bin/x86_64-linux-musl-gcc /usr/local/bin/x86_64-linux-musl-gcc
	ln -sf /opt/arm-linux-musleabihf-cross/bin/arm-linux-musleabihf-gcc /usr/local/bin/arm-linux-musleabihf-gcc
	ln -sf /opt/riscv64-linux-musl-cross/bin/riscv64-linux-musl-gcc /usr/local/bin/riscv64-linux-musl-gcc

.PHONY: release-workflow-deps
release-workflow-deps: install-cross-compilers install-musl-cross-compilers

.PHONY: deps
deps:
	./build/download-deps.sh --os=$(GOOS) --arch=$(GOARCH)

.PHONY: deps-offline
deps-offline:
	./build/download-deps.sh --os=$(GOOS) --arch=$(GOARCH) --offline

# Generic build function that uses the correct cross-compiler based on GOARCH
.PHONY: build
build: lint deps
	@mkdir -p $(dir $(OUTPUT))
ifeq ($(GOARCH),arm64)
	CC=$(CC_arm64) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="${LDFLAGS_STRING}" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),amd64)
	CC=$(CC_amd64) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="${LDFLAGS_STRING}" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),riscv64)
	CC=$(CC_riscv64) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="${LDFLAGS_STRING}" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),arm)
	CC=$(CC_arm) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="${LDFLAGS_STRING}" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else
	@echo "Unsupported architecture: $(GOARCH)"
	@exit 1
endif

# Build offline variant with all OCI images embedded (air-gapped deployments)
.PHONY: build-offline
build-offline: lint deps-offline
	@mkdir -p $(dir $(OUTPUT))
ifeq ($(GOARCH),arm64)
	CC=$(CC_arm64) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-tags offline -ldflags="${LDFLAGS_STRING}" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),amd64)
	CC=$(CC_amd64) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-tags offline -ldflags="${LDFLAGS_STRING}" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),arm)
	CC=$(CC_arm) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-tags offline -ldflags="${LDFLAGS_STRING}" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),riscv64)
	CC=$(CC_riscv64) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-tags offline -ldflags="${LDFLAGS_STRING}" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else
	@echo "Unsupported architecture: $(GOARCH)"
	@exit 1
endif

# Build offline variant with musl for Alpine Linux compatibility (air-gapped deployments)
.PHONY: build-musl-offline
build-musl-offline: lint deps-offline
	@mkdir -p $(dir $(OUTPUT))
ifeq ($(GOARCH),arm64)
	CC=$(CC_arm64_musl) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-tags offline -ldflags="${LDFLAGS_STRING} -linkmode external -extldflags '-static'" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),amd64)
	CC=$(CC_amd64_musl) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-tags offline -ldflags="${LDFLAGS_STRING} -linkmode external -extldflags '-static'" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),arm)
	CC=$(CC_arm_musl) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) GOARM=7 go build \
		-tags offline -ldflags="${LDFLAGS_STRING} -linkmode external -extldflags '-static'" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),riscv64)
	CC=$(CC_riscv64_musl) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-tags offline -ldflags="${LDFLAGS_STRING} -linkmode external -extldflags '-static'" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else
	@echo "Unsupported architecture for musl offline build: $(GOARCH)"
	@exit 1
endif

# Build with musl for Alpine Linux compatibility
.PHONY: build-musl
build-musl: lint deps
	@mkdir -p $(dir $(OUTPUT))
ifeq ($(GOARCH),arm64)
	CC=$(CC_arm64_musl) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="${LDFLAGS_STRING} -linkmode external -extldflags '-static'" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),amd64)
	CC=$(CC_amd64_musl) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="${LDFLAGS_STRING} -linkmode external -extldflags '-static'" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),arm)
	CC=$(CC_arm_musl) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) GOARM=7 go build \
		-ldflags="${LDFLAGS_STRING} -linkmode external -extldflags '-static'" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),riscv64)
	CC=$(CC_riscv64_musl) CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="${LDFLAGS_STRING} -linkmode external -extldflags '-static'" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else
	@echo "Unsupported architecture for musl build: $(GOARCH)"
	@exit 1
endif

# ---------- Container runtime ----------
#
# Targets that shell out to a container engine work with either Apple
# Containers (`container`) or Docker. The engine is auto-detected, preferring
# `container` when it is on PATH; override with CONTAINER_RUNTIME=docker.
CONTAINER_RUNTIME ?= $(if $(shell command -v container 2>/dev/null),container,docker)

# Apple Containers points the guest at the vmnet gateway (192.168.64.1) as its
# resolver, which refuses queries on some hosts — every `apk add` and module
# fetch inside the container then fails with a DNS error. Pin a resolver for
# `container` invocations. Override CONTAINER_DNS to use your own resolver
# (needed behind a split-horizon or VPN DNS), or set it empty to fall back to
# the engine default. Docker resolves through its own daemon, so it is left
# alone.
CONTAINER_DNS ?= 1.1.1.1

# Apple Containers gives each container 1 GiB and 4 CPUs. That is not enough to
# compile this tree — the Go compiler gets OOM-killed part-way through with
# "compile: signal: killed" — so raise both. Docker shares one large VM sized by
# the user, so it is left alone. Set either variable empty to use the engine
# default.
CONTAINER_MEMORY ?= 8g
CONTAINER_CPUS ?= 8

ifeq ($(CONTAINER_RUNTIME),docker)
CONTAINER_DNS_FLAG =
CONTAINER_RESOURCE_FLAGS =
else
CONTAINER_DNS_FLAG = $(if $(CONTAINER_DNS),--dns $(CONTAINER_DNS))
CONTAINER_RESOURCE_FLAGS = $(if $(CONTAINER_MEMORY),--memory $(CONTAINER_MEMORY)) $(if $(CONTAINER_CPUS),--cpus $(CONTAINER_CPUS))
endif

CONTAINER_RUN_FLAGS = $(CONTAINER_DNS_FLAG) $(CONTAINER_RESOURCE_FLAGS)

# Docker needs buildx to honour --platform; `container build` takes it
# natively. Both engines spell the push the same way.
ifeq ($(CONTAINER_RUNTIME),docker)
IMAGE_BUILD = docker buildx build
else
IMAGE_BUILD = $(CONTAINER_RUNTIME) build $(CONTAINER_DNS_FLAG)
endif
IMAGE_PUSH = $(CONTAINER_RUNTIME) image push

.PHONY: build-using-image
build-using-image:
	mkdir -p $(HOME)/.go-cache/mod $(HOME)/.go-cache/build
	$(CONTAINER_RUNTIME) run $(CONTAINER_RUN_FLAGS) --platform $(GOOS)/$(GOARCH) --workdir /app --rm \
		-v ${PWD}:/app \
		-v ${HOME}/.go-cache/mod:/go/pkg/mod \
		-v ${HOME}/.go-cache/build:/root/.cache/go-build \
		-e GOCACHE=/root/.cache/go-build \
		-e GOMODCACHE=/go/pkg/mod \
		-e CGO_ENABLED=1 -e CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" -e GOOS=$(GOOS) -e GOARCH=$(GOARCH) -e VERSION=$(VERSION) \
		registry.k8s.io/build-image/kube-cross:v1.36.0-go1.26.2-bullseye.0 \
		make build

.PHONY: build-using-alpine
build-using-alpine:
	mkdir -p $(HOME)/.go-cache/mod $(HOME)/.go-cache/build
	$(CONTAINER_RUNTIME) run $(CONTAINER_RUN_FLAGS) --platform $(GOOS)/$(GOARCH) --workdir /app --rm \
		-v ${PWD}:/app \
		-v ${HOME}/.go-cache/mod:/go/pkg/mod \
		-v ${HOME}/.go-cache/build:/root/.cache/go-build \
		-e CGO_ENABLED=1 -e CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" -e GOOS=$(GOOS) -e GOARCH=$(GOARCH) \
		golang:1.26-alpine \
		sh -c "apk add --no-cache gcc musl-dev && go build -ldflags='${LDFLAGS_STRING} -linkmode external -extldflags \"-static\"' -o dist/kubesolo ./cmd/kubesolo/main.go"

.PHONY: lint
lint:
	go mod tidy
	go fmt ./...

# lint-ci runs the full static-analysis suite. Kept separate from `lint` because
# the build targets depend on `lint`, and we don't want every build to require
# golangci-lint to be installed. CI installs golangci-lint and calls this target.
.PHONY: lint-ci
lint-ci:
	golangci-lint run

# test compiles and runs the unit suite. It depends on `deps` because
# internal/core/embedded uses //go:embed on binaries that only exist after
# `make deps` has downloaded them — without it, `go test ./...` fails to compile.
# CGO is required for the SQLite (kine) dependency.
.PHONY: test
test: deps
	CGO_ENABLED=1 go test -covermode=atomic -coverprofile=coverage.out ./...

# test-race adds the race detector (slower, heavier compile). Use when touching
# concurrent code such as the service-startup orchestration.
.PHONY: test-race
test-race: deps
	CGO_ENABLED=1 go test -race -covermode=atomic -coverprofile=coverage.out ./...

# test-e2e boots KubeSolo as a container via kubesoloctl and runs the smoke
# suite. Requires Docker + kubectl and a Linux host (KubeSolo is Linux-only).
# Build kubesoloctl first, then point the harness at an image to test.
E2E_IMAGE ?= portainer/kubesolo:dev
.PHONY: test-e2e
test-e2e: build-kubesoloctl
	IMAGE=$(E2E_IMAGE) KUBESOLOCTL=$(KUBESOLOCTL_OUTPUT) test/e2e/run.sh

# bench boots KubeSolo and records boot time / idle RSS / image size / pod
# density. Reports only unless a test/perf/baseline.<arch>.json is committed.
PERF_IMAGE ?= portainer/kubesolo:dev
.PHONY: bench
bench: build-kubesoloctl
	IMAGE=$(PERF_IMAGE) KUBESOLOCTL=$(KUBESOLOCTL_OUTPUT) test/perf/bench.sh

# soak loops the manifest tiers and watches for container restarts and idle-RSS
# growth (a leak detector). Used by the nightly workflow.
.PHONY: soak
soak: build-kubesoloctl
	IMAGE=$(E2E_IMAGE) KUBESOLOCTL=$(KUBESOLOCTL_OUTPUT) test/e2e/soak.sh

.PHONY: run
run: build
	sudo $(OUTPUT)

.PHONY: dev
dev:
	CGO_ENABLED=1 CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" GOOS=$(GOOS) GOARCH=$(GOARCH) go run cmd/kubesolo/main.go

.PHONY: clean
clean:
	rm -rf ./dist/kubesolo*
	rm -rf ./internal/core/embedded/bin

# Build all supported architectures with musl
.PHONY: build-all-musl
build-all-musl:
	GOARCH=amd64 make build-musl
	GOARCH=arm64 make build-musl
	GOARCH=arm make build-musl
	GOARCH=riscv64 make build-musl

.PHONY: archive
archive:
	tar -czf dist/kubesolo.tar.gz dist/kubesolo install.sh

.PHONY: archive-musl
archive-musl:
	tar -czf dist/kubesolo-musl.tar.gz dist/kubesolo install.sh

# ── kubesoloctl binary ──────────────────────────────────────────────────────────
#
# kubesoloctl is built with CGO_ENABLED=0 (pure Go). A single binary per
# architecture runs on both glibc and musl systems, so there is no libc split.
#
# Supported targets: linux/amd64, linux/arm64, darwin/amd64, darwin/arm64

KUBESOLOCTL_LDFLAGS = -s -w \
	-X main.Version=$(VERSION) \
	-X main.Commit=$(COMMIT) \
	-X main.BuildDate=$(BUILD_DATE)

KUBESOLOCTL_OUTPUT ?= ./dist/kubesoloctl-$(GOOS)-$(GOARCH)

# Build kubesoloctl for the current GOOS/GOARCH.
# Pass GOARM=7 when targeting arm (armhf/ARMv7), e.g.: make build-kubesoloctl GOARCH=arm GOARM=7
.PHONY: build-kubesoloctl
build-kubesoloctl:
	@mkdir -p $(dir $(KUBESOLOCTL_OUTPUT))
	CGO_ENABLED=0 GOOS=$(GOOS) GOARCH=$(GOARCH) GOARM=$(GOARM) go build \
		-ldflags="$(KUBESOLOCTL_LDFLAGS)" \
		-o $(KUBESOLOCTL_OUTPUT) \
		./cmd/kubesoloctl

# Build kubesoloctl for all supported architectures
.PHONY: build-kubesoloctl-all
build-kubesoloctl-all:
	GOOS=linux GOARCH=amd64   KUBESOLOCTL_OUTPUT=./dist/kubesoloctl-linux-amd64    make build-kubesoloctl
	GOOS=linux GOARCH=arm64   KUBESOLOCTL_OUTPUT=./dist/kubesoloctl-linux-arm64    make build-kubesoloctl
	GOOS=darwin GOARCH=amd64 KUBESOLOCTL_OUTPUT=./dist/kubesoloctl-darwin-amd64 make build-kubesoloctl
	GOOS=darwin GOARCH=arm64 KUBESOLOCTL_OUTPUT=./dist/kubesoloctl-darwin-arm64 make build-kubesoloctl

# Clean kubesoloctl build artefacts
.PHONY: clean-kubesoloctl
clean-kubesoloctl:
	rm -f ./dist/kubesoloctl-*

# Include custom make targets
-include $(wildcard .dev/*.make)

# ---------- Container Image targets ----------

IMAGE_NAME ?= portainer/kubesolo
IMAGE_TAG ?= $(VERSION)

# Apple Containers will not push a reference without a registry host ("could
# not extract host from reference"); Docker assumes docker.io. Qualify
# IMAGE_NAME for `container` when it carries no host of its own — a name whose
# first segment holds a "." or ":" (ghcr.io/..., localhost:5000/...) already
# does and is left as-is.
IMAGE_NAME_HEAD = $(firstword $(subst /, ,$(IMAGE_NAME)))
ifeq ($(CONTAINER_RUNTIME),docker)
IMAGE_REF = $(IMAGE_NAME)
else
IMAGE_REF = $(if $(or $(findstring .,$(IMAGE_NAME_HEAD)),$(findstring :,$(IMAGE_NAME_HEAD))),$(IMAGE_NAME),docker.io/$(IMAGE_NAME))
endif

# Build the container image (downloads arch-specific deps, builds static binary via Alpine, then packages it)
.PHONY: image
image: deps build-using-alpine
	$(IMAGE_BUILD) \
		--platform $(GOOS)/$(GOARCH) \
		-t $(IMAGE_REF):$(IMAGE_TAG)-$(GOOS)-$(GOARCH) .

# Build and push the container image for $(GOOS)/$(GOARCH). buildx builds and
# pushes in one step; `container` builds one platform at a time and pushes
# afterwards, so multi-arch manifests need one invocation per architecture.
.PHONY: image-buildx
image-buildx:
ifeq ($(CONTAINER_RUNTIME),docker)
	docker buildx build --platform $(GOOS)/$(GOARCH) \
		-t $(IMAGE_REF):$(IMAGE_TAG) -t $(IMAGE_REF):latest \
		--push .
else
	$(IMAGE_BUILD) --platform $(GOOS)/$(GOARCH) \
		-t $(IMAGE_REF):$(IMAGE_TAG) .
	$(CONTAINER_RUNTIME) image tag $(IMAGE_REF):$(IMAGE_TAG) $(IMAGE_REF):latest
	$(IMAGE_PUSH) $(IMAGE_REF):$(IMAGE_TAG)
	$(IMAGE_PUSH) $(IMAGE_REF):latest
endif

# Push the container image
.PHONY: image-push
image-push:
	$(IMAGE_PUSH) $(IMAGE_REF):$(IMAGE_TAG)
	$(IMAGE_PUSH) $(IMAGE_REF):latest
