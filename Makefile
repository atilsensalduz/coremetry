.PHONY: build build-ui build-go build-demo run dev-ui clean

build: build-ui build-go build-demo

build-ui:
	cd frontend && npm install && npm run build

build-go:
	go build -o coremetry .

build-demo:
	go build -o demo ./cmd/demo

run: build
	./coremetry

dev-ui:
	cd frontend && npm run dev

docker-up:
	docker compose --profile demo up -d --build

docker-down:
	docker compose --profile demo down

clean:
	rm -rf coremetry demo frontend/out frontend/.next frontend/node_modules
