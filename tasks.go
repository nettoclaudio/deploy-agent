// Copyright 2017 deploy-agent authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/ghodss/yaml"
	"github.com/tsuru/deploy-agent/internal/tsuru"
	"github.com/tsuru/tsuru/app/bind"
	"github.com/tsuru/tsuru/exec"
)

var (
	defaultWorkingDir = "/home/application/current"
	tsuruYamlFiles    = []string{"tsuru.yml", "tsuru.yaml", "app.yml", "app.yaml"}
	configDirs        = []string{defaultWorkingDir, "/app/user", "/"}
)

func execScript(cmds []string, envs []bind.EnvVar, w io.Writer, fs Filesystem, executor exec.Executor) error {
	if w == nil {
		w = ioutil.Discard
	}
	workingDir := defaultWorkingDir
	exists, err := fs.CheckFile(defaultWorkingDir)
	if err != nil {
		return err
	}
	if !exists {
		workingDir = "/"
	}
	formatedEnvs := []string{}
	for _, env := range envs {
		formatedEnv := fmt.Sprintf("%s=%s", env.Name, env.Value)
		formatedEnvs = append(formatedEnvs, formatedEnv)
	}
	if isR, ok := executor.(interface {
		IsRemote() bool
	}); !ok || !isR.IsRemote() {
		// local environment variables do not make sense on a remote executor
		// since it runs commands in a different container
		formatedEnvs = append(formatedEnvs, os.Environ()...)
	}
	for _, cmd := range cmds {
		execOpts := exec.ExecuteOptions{
			Cmd:    "/bin/sh",
			Args:   []string{"-lc", cmd},
			Dir:    workingDir,
			Envs:   formatedEnvs,
			Stdout: os.Stdout,
			Stderr: os.Stderr,
		}
		fmt.Fprintf(w, " ---> Running %q\n", cmd)
		err := executor.Execute(execOpts)
		if err != nil {
			return fmt.Errorf("error running %q: %s\n", cmd, err)
		}
	}
	return nil
}

func loadTsuruYamlRaw(fs Filesystem) []byte {
	for _, yamlFile := range tsuruYamlFiles {
		for _, dir := range configDirs {
			path := filepath.Join(dir, yamlFile)
			tsuruYaml, err := fs.ReadFile(path)
			if err == nil {
				return tsuruYaml
			}
		}
	}
	return nil
}

func parseTsuruYaml(data []byte) (tsuru.TsuruYaml, error) {
	var tsuruYamlData tsuru.TsuruYaml
	err := yaml.Unmarshal(data, &tsuruYamlData)
	return tsuruYamlData, err
}

func parseAllTsuruYaml(data []byte) (map[string]interface{}, error) {
	var tsuruYamlData map[string]interface{}
	err := yaml.Unmarshal(data, &tsuruYamlData)
	if tsuruYamlData == nil {
		tsuruYamlData = map[string]interface{}{}
	}
	return tsuruYamlData, err
}

func buildHooks(yamlData tsuru.TsuruYaml, envs []bind.EnvVar, fs Filesystem, executor exec.Executor) error {
	cmds := append([]string{}, yamlData.Hooks.BuildHooks...)
	fmt.Fprintln(os.Stdout, "---- Running build hooks ----")
	return execScript(cmds, envs, os.Stdout, fs, executor)
}

func readProcfile(fs Filesystem) (string, error) {
	var err error
	for _, dir := range configDirs {
		path := filepath.Join(dir, "Procfile")
		var procfile []byte
		procfile, err = fs.ReadFile(path)
		if err == nil {
			return string(bytes.Replace(procfile, []byte("\r\n"), []byte("\n"), -1)), nil
		}
	}
	return "", err
}

var procfileRegex = regexp.MustCompile(`^([\w-]+):\s*(\S.+)$`)

func loadProcesses(t *tsuru.TsuruYaml, fs Filesystem) error {
	procfile, err := readProcfile(fs)
	if err != nil {
		return err
	}
	processList := strings.Split(procfile, "\n")
	processes := make(map[string]string, len(processList))
	for _, proc := range processList {
		if p := procfileRegex.FindStringSubmatch(proc); p != nil {
			processes[p[1]] = strings.Trim(p[2], " ")
		}
	}
	if len(processes) == 0 {
		return fmt.Errorf("invalid Procfile, no processes found in %q", procfile)
	}
	t.Processes = processes
	return nil
}

func readDiffDeploy(fs Filesystem) (string, bool, error) {
	filePath := fmt.Sprintf("%s/%s", defaultWorkingDir, "diff")
	deployDiff, err := fs.ReadFile(filePath)
	if err != nil {
		return "", true, err
	}
	defer fs.RemoveFile(filePath)
	return string(deployDiff), false, nil
}
