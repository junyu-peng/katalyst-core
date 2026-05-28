/*
Copyright 2022 The Katalyst Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package qrm

import (
	"testing"

	"github.com/stretchr/testify/require"
	cliflag "k8s.io/component-base/cli/flag"

	qrmconfig "github.com/kubewharf/katalyst-core/pkg/config/agent/qrm"
)

func TestCPUOptions_AddFlags_ParseCPUWeight(t *testing.T) {
	t.Parallel()

	as := require.New(t)
	o := NewCPUOptions()

	fss := cliflag.NamedFlagSets{}
	o.AddFlags(&fss)
	fs := fss.FlagSet("cpu_resource_plugin")

	as.NotNil(fs.Lookup("enable-cpu-weight"))
	as.NotNil(fs.Lookup("cpu-weight-interval"))

	as.NoError(fs.Parse([]string{
		"--enable-cpu-weight=true",
		"--cpu-weight-interval=15s",
	}))

	as.True(o.EnableCPUWeight)
}

func TestCPUOptions_ApplyToCopiesCPUWeight(t *testing.T) {
	t.Parallel()

	as := require.New(t)
	o := NewCPUOptions()
	o.EnableCPUWeight = true

	conf := qrmconfig.NewCPUQRMPluginConfig()
	as.NoError(o.ApplyTo(conf))

	as.True(conf.EnableCPUWeight)
}
