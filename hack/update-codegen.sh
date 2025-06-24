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

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

# 1. Generate typed client, informers, listers
bash "${SCRIPT_ROOT}/hack/generate-groups.sh" "$@"

# 2. Regenerate CRDs manifests via controller-gen (installed via go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.15.0)
if ! command -v controller-gen &>/dev/null; then
  echo "controller-gen not found in PATH, installing..."
  go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.15.0
  export PATH=$(go env GOPATH)/bin:${PATH}
fi

controller-gen crd:crdVersions=v1 \
  paths="./..." \
  output:crd:dir="${SCRIPT_ROOT}/config/crd/bases" \
  rbac:roleName=manager-role

echo "Code generation completed." 