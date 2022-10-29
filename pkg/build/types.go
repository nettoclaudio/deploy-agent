// Copyright 2022 tsuru authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// NOTE: Copied from github.com/tsuru/tsuru/types/provision/provision.go in
// order to avoid circular dependency with github.com/tsuru/tsuru module.

package build

type TsuruYamlData struct {
	Hooks       *TsuruYamlHooks            `json:"hooks,omitempty" bson:",omitempty"`
	Healthcheck *TsuruYamlHealthcheck      `json:"healthcheck,omitempty" bson:",omitempty"`
	Kubernetes  *TsuruYamlKubernetesConfig `json:"kubernetes,omitempty" bson:",omitempty"`
}

type TsuruYamlHooks struct {
	Restart TsuruYamlRestartHooks `json:"restart" bson:",omitempty"`
	Build   []string              `json:"build" bson:",omitempty"`
}

type TsuruYamlRestartHooks struct {
	Before []string `json:"before" bson:",omitempty"`
	After  []string `json:"after" bson:",omitempty"`
}

type TsuruYamlHealthcheck struct {
	Headers              map[string]string `json:"headers,omitempty" bson:",omitempty"`
	Path                 string            `json:"path"`
	Method               string            `json:"method"`
	Scheme               string            `json:"scheme"`
	Match                string            `json:"match,omitempty" bson:",omitempty"`
	RouterBody           string            `json:"router_body,omitempty" yaml:"router_body" bson:"router_body,omitempty"`
	Command              []string          `json:"command,omitempty" bson:",omitempty"`
	Status               int               `json:"status"`
	AllowedFailures      int               `json:"allowed_failures,omitempty" yaml:"allowed_failures" bson:"allowed_failures,omitempty"`
	IntervalSeconds      int               `json:"interval_seconds,omitempty" yaml:"interval_seconds" bson:"interval_seconds,omitempty"`
	TimeoutSeconds       int               `json:"timeout_seconds,omitempty" yaml:"timeout_seconds" bson:"timeout_seconds,omitempty"`
	DeployTimeoutSeconds int               `json:"deploy_timeout_seconds,omitempty" yaml:"deploy_timeout_seconds" bson:"deploy_timeout_seconds,omitempty"`
	UseInRouter          bool              `json:"use_in_router,omitempty" yaml:"use_in_router" bson:"use_in_router,omitempty"`
	ForceRestart         bool              `json:"force_restart,omitempty" yaml:"force_restart" bson:"force_restart,omitempty"`
}

type TsuruYamlKubernetesConfig struct {
	Groups map[string]TsuruYamlKubernetesGroup `json:"groups,omitempty"`
}

func (in *TsuruYamlKubernetesConfig) DeepCopyInto(out *TsuruYamlKubernetesConfig) {
	if in.Groups == nil {
		return
	}
	if out.Groups == nil {
		out.Groups = make(map[string]TsuruYamlKubernetesGroup)
	}
	for k, v := range in.Groups {
		out.Groups[k] = v
	}
}

func (in *TsuruYamlKubernetesConfig) DeepCopy() *TsuruYamlKubernetesConfig {
	out := &TsuruYamlKubernetesConfig{}
	in.DeepCopyInto(out)
	return out
}

type TsuruYamlKubernetesGroup map[string]TsuruYamlKubernetesProcessConfig

type TsuruYamlKubernetesProcessConfig struct {
	Ports []TsuruYamlKubernetesProcessPortConfig `json:"ports"`
}

type TsuruYamlKubernetesProcessPortConfig struct {
	Name       string `json:"name,omitempty"`
	Protocol   string `json:"protocol,omitempty"`
	Port       int    `json:"port,omitempty"`
	TargetPort int    `json:"target_port,omitempty"`
}
