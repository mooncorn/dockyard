#!/bin/bash

# Script to generate protobuf definitions for control and worker projects
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

# Project root directory
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PROTO_DIR="${PROJECT_ROOT}/proto"
PROTO_FILE="${PROTO_DIR}/dockyard.proto"

echo -e "${YELLOW}Generating protobuf definitions...${NC}"

# Check if proto file exists
if [ ! -f "$PROTO_FILE" ]; then
    echo -e "${RED}Error: Proto file not found at $PROTO_FILE${NC}"
    exit 1
fi

# Check if required tools are installed
for tool in protoc protoc-gen-go protoc-gen-go-grpc; do
    if ! command -v "$tool" &> /dev/null; then
        echo -e "${RED}Error: $tool is not installed${NC}"
        exit 1
    fi
done

# Create output directory for generated files
OUTPUT_DIR="${PROTO_DIR}/pb"
mkdir -p "$OUTPUT_DIR"

echo -e "${YELLOW}Generating Go protobuf files...${NC}"

# Generate protobuf files
protoc \
    --proto_path="$PROTO_DIR" \
    --go_out="$OUTPUT_DIR" \
    --go_opt=paths=source_relative \
    --go-grpc_out="$OUTPUT_DIR" \
    --go-grpc_opt=paths=source_relative \
    "$PROTO_FILE"

echo -e "${GREEN}✓ Generated protobuf files in $OUTPUT_DIR${NC}"

# Generate for control project
echo -e "${YELLOW}Updating control project dependencies...${NC}"
cd "${PROJECT_ROOT}/control"

# Add required dependencies to control project
if ! grep -q "google.golang.org/grpc" go.mod; then
    go get google.golang.org/grpc
fi

if ! grep -q "google.golang.org/protobuf" go.mod; then
    go get google.golang.org/protobuf/types/known/timestamppb
    go get google.golang.org/protobuf/reflect/protoreflect
    go get google.golang.org/protobuf/runtime/protoimpl
fi

# Replace the proto module path with local reference
go mod edit -replace github.com/mooncorn/dockyard/proto="../proto"
go mod tidy

echo -e "${GREEN}✓ Updated control project dependencies${NC}"

# Generate for worker project
echo -e "${YELLOW}Updating worker project dependencies...${NC}"
cd "${PROJECT_ROOT}/worker"

# Add required dependencies to worker project
if ! grep -q "google.golang.org/grpc" go.mod; then
    go get google.golang.org/grpc
fi

if ! grep -q "google.golang.org/protobuf" go.mod; then
    go get google.golang.org/protobuf/types/known/timestamppb
    go get google.golang.org/protobuf/reflect/protoreflect
    go get google.golang.org/protobuf/runtime/protoimpl
fi

# Replace the proto module path with local reference
go mod edit -replace github.com/mooncorn/dockyard/proto="../proto"
go mod tidy

echo -e "${GREEN}✓ Updated worker project dependencies${NC}"

# Create go.mod for proto directory if it doesn't exist
cd "$PROTO_DIR"
if [ ! -f go.mod ]; then
    echo -e "${YELLOW}Creating go.mod for proto package...${NC}"
    go mod init github.com/mooncorn/dockyard/proto
    go get google.golang.org/grpc
    go get google.golang.org/protobuf/types/known/timestamppb
    go get google.golang.org/protobuf/reflect/protoreflect
    go get google.golang.org/protobuf/runtime/protoimpl
    go mod tidy
    echo -e "${GREEN}✓ Created proto go.mod${NC}"
fi

echo -e "${GREEN}🎉 Protobuf generation complete!${NC}"
echo -e "${YELLOW}Generated files:${NC}"
find "$OUTPUT_DIR" -name "*.go" -exec echo "  - {}" \;