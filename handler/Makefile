#include .env

MIGRATION_INIT_FILE=data/init.sql
MIGRATION_FILE=
PG_CONN=PGPASSWORD=$(DB_PASSWORD) psql -h $(DB_HOST) -p $(DB_PORT) -U $(DB_USER) -d $(DB_NAME)

install:
	curl -sSfL https://raw.githubusercontent.com/cosmtrek/air/master/install.sh | sh -s
	curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s v1.61.0
	GOBIN=$(shell pwd)/bin go install github.com/daixiang0/gci@latest
	GOBIN=$(shell pwd)/bin go install mvdan.cc/gofumpt@latest

fmt:
	./bin/gci write --skip-generated -s standard -s default .
	./bin/gofumpt -w .
	go fmt ./...

lint:
	./bin/golangci-lint run ./... --build-tags development

lint.fast:
	./bin/golangci-lint run ./... --build-tags development --fast

update:
	make install
	go get -u ./...

server:
	#docker compose up -d
	./bin/air

rebuild-schema:
	docker rm -f postgres-go
	docker volume rm reels-platform_pgdata
	docker compose up

.PHONY: migrate-init
migrate-init:
	@for file in $$(ls -1 $(MIGRATION_INIT_FILE) | sort); do \
    		echo "Applying migration $$file..."; \
    		$(PG_CONN) -f $$file; \
    	done

build:
	go build -o bin/app cmd/app/main.go

migrate:
	migrate create -ext sql -dir handler/migrations -seq $(MIGRATION_FILE)

build.version:
	go build -tags development \-ldflags "-X 'main.Version=${VERSION}' -X 'main.BuildTime=${BUILD_TIME}'" \-o admin-service \cmd/app/main.go
