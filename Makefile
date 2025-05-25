GOOS ?= linux
GOARCH ?= $(shell go env GOARCH)
OUTPUT ?= ./dist/kubesolo

VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT ?= $(shell git rev-parse --short HEAD)
BUILD_DATE ?= $(shell date -u +'%Y-%m-%dT%H:%M:%SZ')

LDFLAGS_STRING = -s -w -X main.Version=${VERSION} -X main.Commit=${COMMIT} -X main.BuildDate=${BUILD_DATE}

# Cross-compilation settings
CC_arm64 = aarch64-linux-gnu-gcc
CC_amd64 = x86_64-linux-gnu-gcc
CC_arm = arm-linux-gnueabihf-gcc  # ARM Hard Float (ARMHF) - targets ARMv7+ with hardware FPU

# Install cross-compilation toolchains
.PHONY: install-cross-compilers
install-cross-compilers:
	sudo apt-get update
	sudo apt-get install -y gcc-aarch64-linux-gnu gcc-x86-64-linux-gnu gcc-arm-linux-gnueabihf

.PHONY: deps
deps:
	./build/download-deps.sh --os=$(GOOS) --arch=$(GOARCH)

# Generic build function that uses the correct cross-compiler based on GOARCH
.PHONY: build
build: lint deps
	@mkdir -p $(dir $(OUTPUT))
ifeq ($(GOARCH),arm64)
	CC=$(CC_arm64) CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="${LDFLAGS_STRING}" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),amd64)
	CC=$(CC_amd64) CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="${LDFLAGS_STRING}" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else ifeq ($(GOARCH),arm)
	CC=$(CC_arm) CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="${LDFLAGS_STRING}" -a \
		-o $(OUTPUT) ./cmd/kubesolo/main.go
else
	@echo "Unsupported architecture: $(GOARCH)"
	@exit 1
endif

# Build for specific platforms
.PHONY: build-arm64
build-arm64: lint
	./build/download-deps.sh --os=linux --arch=arm64
	CC=$(CC_arm64) CGO_ENABLED=1 GOOS=linux GOARCH=arm64 go build \
		-ldflags="${LDFLAGS_STRING}" -a \
		-o ./dist/kubesolo-linux-arm64 ./cmd/kubesolo/main.go

.PHONY: build-amd64
build-amd64: lint
	./build/download-deps.sh --os=linux --arch=amd64
	CC=$(CC_amd64) CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build \
		-ldflags="${LDFLAGS_STRING}" -a \
		-o ./dist/kubesolo-linux-amd64 ./cmd/kubesolo/main.go

.PHONY: build-arm
build-arm: lint
	./build/download-deps.sh --os=linux --arch=arm
	CC=$(CC_arm) CGO_ENABLED=1 GOOS=linux GOARCH=arm go build \
		-ldflags="${LDFLAGS_STRING}" -a \
		-o ./dist/kubesolo-linux-arm ./cmd/kubesolo/main.go

# Build all supported platforms
.PHONY: build-all
build-all: build-arm64 build-amd64 build-arm
	@echo "Built binaries for all supported platforms:"
	@ls -la ./dist/kubesolo-linux-*

.PHONY: build-using-image
build-using-image:
	mkdir -p $(HOME)/.go-cache/mod $(HOME)/.go-cache/build
	docker run --platform $(GOOS)/$(GOARCH) --workdir /app --rm \
		-v ${PWD}:/app \
		-v ${HOME}/.go-cache/mod:/go/pkg/mod \
		-v ${HOME}/.go-cache/build:/root/.cache/go-build \
		-e CGO_ENABLED=1 -e GOOS=$(GOOS) -e GOARCH=$(GOARCH) \
		registry.k8s.io/build-image/kube-cross:v1.33.0-go1.24.2-bullseye.0 \
		make build
	
.PHONY: lint
lint:
	go fmt ./...

.PHONY: run
run: build
	sudo $(OUTPUT)

.PHONY: dev
dev: 
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) go run cmd/kubesolo/main.go

.PHONY: clean
clean:
	rm -rf ./dist/kubesolo*
	rm -rf ./internal/core/embedded/bin