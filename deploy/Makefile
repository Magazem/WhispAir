# Makefile for memoire
# Build and deploy the memoire server binary

SERVER ?= memoire@192.168.1.100
SERVER_DIR ?= /opt/memoire
BINARY_NAME ?= memoire
SSH_OPTS ?= -o StrictHostKeyChecking=no

.PHONY: all build deploy deploy-rollback logs restart test clean help

all: build ## Build binary only

build: ## Cross-compile for Linux (run from laptop/WSL)
	@echo "Building $(BINARY_NAME) for linux/amd64..."
	GOOS=linux GOARCH=amd64 go build -o $(BINARY_NAME) ./cmd/server
	@echo "Build complete: $(BINARY_NAME)"

deploy: build ## Deploy binary + prompts.yaml to server and restart
	@echo "Deploying to $(SERVER):$(SERVER_DIR)..."
	rsync -avz --progress \
		$(BINARY_NAME) \
		server/pipeline/prompts.yaml \
		$(SERVER):$(SERVER_DIR)/ \
		$(SSH_OPTS)
	@echo "Deployed. Restarting service..."
	ssh $(SSH_OPTS) $(SERVER) "sudo systemctl restart memoire"
	@echo "Done. Service restarted."

deploy-rollback: ## Restore previous binary and restart
	@echo "Rolling back on $(SERVER)..."
	ssh $(SSH_OPTS) $(SERVER) " \
		cd $(SERVER_DIR) && \
		if [ -f $(BINARY_NAME).prev ]; then \
			cp $(BINARY_NAME) $(BINARY_NAME).next && \
			cp $(BINARY_NAME).prev $(BINARY_NAME) && \
			sudo systemctl restart memoire && \
			echo 'Rolled back successfully.'; \
		else \
			echo 'No previous binary found!'; \
			exit 1; \
		fi"

logs: ## Tail server logs via SSH
	ssh $(SSH_OPTS) $(SERVER) "journalctl -u memoire -f -n 100"

restart: ## Restart the service on the server
	ssh $(SSH_OPTS) $(SERVER) "sudo systemctl restart memoire"
	@echo "Service restarted."

status: ## Check service status on the server
	ssh $(SSH_OPTS) $(SERVER) "sudo systemctl status memoire"

test: ## Run Go tests
	go test -v ./...

test-short: ## Run short tests
	go test -short -v ./...

clean: ## Remove build artifacts
	rm -f $(BINARY_NAME) $(BINARY_NAME).prev $(BINARY_NAME).next

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'
