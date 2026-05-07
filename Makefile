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

.PHONY: build-using-image
build-using-image:
	mkdir -p $(HOME)/.go-cache/mod $(HOME)/.go-cache/build
	docker run --platform $(GOOS)/$(GOARCH) --workdir /app --rm \
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
	docker run --platform $(GOOS)/$(GOARCH) --workdir /app --rm \
		-v ${PWD}:/app \
		-v ${HOME}/.go-cache/mod:/go/pkg/mod \
		-v ${HOME}/.go-cache/build:/root/.cache/go-build \
		-e CGO_ENABLED=1 -e CGO_CFLAGS="$(CGO_CFLAGS_EXTRA)" -e GOOS=$(GOOS) -e GOARCH=$(GOARCH) \
		golang:1.26-alpine \
		sh -c "apk add --no-cache gcc musl-dev && go build -ldflags='${LDFLAGS_STRING} -linkmode external -extldflags \"-static\"' -a -o dist/kubesolo ./cmd/kubesolo/main.go"

.PHONY: lint
lint:
	go mod tidy
	go fmt ./...

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

# Include custom make targets
-include $(wildcard .dev/*.make)
