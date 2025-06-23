#!/usr/bin/env bash

# Copyright 2025 The quota-manager Authors.
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

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(dirname "${BASH_SOURCE[0]}")/..

# Create the pkg/generated directory structure
mkdir -p "${SCRIPT_ROOT}/pkg/generated/clientset"
mkdir -p "${SCRIPT_ROOT}/pkg/generated/informers"
mkdir -p "${SCRIPT_ROOT}/pkg/generated/listers"

# Disable vendor mode to use modules
export GOFLAGS="-mod=mod"

# Use go run to execute the code generator
go run k8s.io/code-generator/cmd/client-gen \
  --clientset-name "versioned" \
  --input-base "" \
  --input "github.com/zf930530/quota-manager/api/v1beta1" \
  --output-package "github.com/zf930530/quota-manager/pkg/generated/clientset" \
  --output-base "${SCRIPT_ROOT}" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt"

go run k8s.io/code-generator/cmd/lister-gen \
  --input-dirs "github.com/zf930530/quota-manager/api/v1beta1" \
  --output-package "github.com/zf930530/quota-manager/pkg/generated/listers" \
  --output-base "${SCRIPT_ROOT}" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt"

go run k8s.io/code-generator/cmd/informer-gen \
  --input-dirs "github.com/zf930530/quota-manager/api/v1beta1" \
  --versioned-clientset-package "github.com/zf930530/quota-manager/pkg/generated/clientset/versioned" \
  --listers-package "github.com/zf930530/quota-manager/pkg/generated/listers" \
  --output-package "github.com/zf930530/quota-manager/pkg/generated/informers" \
  --output-base "${SCRIPT_ROOT}" \
  --go-header-file "${SCRIPT_ROOT}/hack/boilerplate.go.txt"

# Move generated files from incorrect location to correct location
echo "Moving generated files to correct location..."
if [ -d "${SCRIPT_ROOT}/github.com/zf930530/quota-manager/pkg/generated" ]; then
  cp -r "${SCRIPT_ROOT}/github.com/zf930530/quota-manager/pkg/generated/"* "${SCRIPT_ROOT}/pkg/generated/"
  rm -rf "${SCRIPT_ROOT}/github.com"
  echo "Generated files moved successfully."
else
  echo "No files found in github.com directory, generation may have failed."
fi

# To use your own boilerplate text append:
#   --go-header-file "${SCRIPT_ROOT}"/hack/custom-boilerplate.go.txt 