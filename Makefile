# Copyright 2018 The Kubernetes Authors.
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

ifdef GCE_PD_CSI_STAGING_VERSION
	REV=${GCE_PD_CSI_STAGING_VERSION}
endif

CMDS=gce-pd-csi-driver

all: build

include release-tools/build.make

test-k8s-integration:
	go build -mod=vendor -o bin/k8s-integration-test ./test/k8s-integration
