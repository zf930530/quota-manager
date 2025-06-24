#!/usr/bin/env bash
# Copyright 2025.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# This script is a thin wrapper around k8s.io/code-generator's
# generate-groups.sh helper. It will generate the clientset, listers and
# informers for all API groups defined in our project and place the output
# under pkg/generated.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# Allow the CODEGEN_PKG to be overridden via env. This is handy when the module
# is downloaded to a different location (e.g. when using go workspaces).
if [[ -z "${CODEGEN_PKG:-}" ]]; then
  # try to pick the first matching code-generator dir under GOPATH for v0.29
  GOPATH_ROOT=$(go env GOPATH)
  CODEGEN_MATCHES=("${GOPATH_ROOT}/pkg/mod/k8s.io/code-generator@v0.29.*")
  CODEGEN_PKG=${CODEGEN_MATCHES[0]}
fi

if [[ ! -d "${CODEGEN_PKG}" ]]; then
  echo "Code-generator not found locally, downloading..."
  go mod download k8s.io/code-generator@v0.29.2
  CODEGEN_PKG=$(go env GOPATH)/pkg/mod/k8s.io/code-generator@v0.29.2
fi

# generate-groups.sh args: $1 - what to generate; $2 - output package; $3 - API package; $4 - group-version pairs.
bash "${CODEGEN_PKG}/generate-groups.sh" "client,informer,lister" \
  "github.com/zf930530/quota-manager/pkg/generated" \
  "github.com/zf930530/quota-manager/api" \
  "kmc.io:v1beta1" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt" "$@" 