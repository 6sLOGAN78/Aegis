#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

echo "=== Generating Protobuf Go Stubs ==="
mkdir -p "${ROOT_DIR}/pkg/api"

INCLUDE_ARGS=()
if [ -d "${HOME}/.local/include" ]; then
  INCLUDE_ARGS+=("-I" "${HOME}/.local/include")
fi

protoc --proto_path="${ROOT_DIR}/api/proto" "${INCLUDE_ARGS[@]}" \
  --go_out="${ROOT_DIR}/pkg/api" --go_opt=paths=source_relative \
  --go-grpc_out="${ROOT_DIR}/pkg/api" --go-grpc_opt=paths=source_relative \
  "${ROOT_DIR}/api/proto/snapshot/v1/snapshot.proto"

echo "=== Generating OpenAPI Go Types ==="
mkdir -p "${ROOT_DIR}/pkg/api/control/v1"
oapi-codegen -generate types -package controlv1 \
  "${ROOT_DIR}/api/openapi/control-v1.yaml" > "${ROOT_DIR}/pkg/api/control/v1/types.gen.go"

echo "=== Verifying Generated Code ==="
cd "${ROOT_DIR}"
go mod tidy
go vet ./pkg/api/...
echo "=== Code Generation Succeeded ==="
