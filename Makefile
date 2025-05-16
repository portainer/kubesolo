GOOS ?= linux
GOARCH ?= amd64

.PHONY: download-binaries build run clean

deps:
	./build/download-deps.sh --os=$(GOOS) --arch=$(GOARCH)

build: lint deps
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) go build \
		-ldflags="-s -w" \
		-o ./dist/kubesolo ./cmd/kubesolo/main.go

build-using-image:
	mkdir -p $(HOME)/.go-cache/mod $(HOME)/.go-cache/build
	docker run --platform $(GOOS)/$(GOARCH) --workdir /app --rm \
		-v ${PWD}:/app \
		-v ${HOME}/.go-cache/mod:/go/pkg/mod \
		-v ${HOME}/.go-cache/build:/root/.cache/go-build \
		-e CGO_ENABLED=1 -e GOOS=$(GOOS) -e GOARCH=$(GOARCH) \
		registry.k8s.io/build-image/kube-cross:v1.33.0-go1.24.2-bullseye.0 \
		make build
	
lint:
	go fmt ./...

run: build
	sudo ./dist/kubesolo

dev: 
	CGO_ENABLED=1 GOOS=$(GOOS) GOARCH=$(GOARCH) go run cmd/kubesolo/main.go

clean:
	rm -rf ./dist/kubesolo
	rm -rf ./internal/embedded/bin