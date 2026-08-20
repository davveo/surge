.PHONY: proto tidy test build run-im-core run-gateway up down logs smoke g200

export PATH := $(HOME)/go/bin:$(PATH)

proto:
	protoc \
		--go_out=. --go_opt=module=github.com/davveo/surge \
		--go-grpc_out=. --go-grpc_opt=module=github.com/davveo/surge \
		proto/im/v1/frame.proto proto/im/v1/core.proto

tidy:
	go mod tidy

test:
	go test ./pkg/... ./im-core/... ./gateway/...

smoke:
	go run ./tools/p0smoke

g200:
	go run ./tools/g200 -n 200 -msgs 10

build: proto tidy
	mkdir -p bin
	go build -o bin/im-core ./im-core
	go build -o bin/gateway ./gateway

run-im-core:
	go run ./im-core

run-gateway:
	go run ./gateway

up:
	docker compose up --build -d

down:
	docker compose down

logs:
	docker compose logs -f --tail=200
