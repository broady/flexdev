// Copyright 2016 Google Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

// Command server implements the flexdev server. It should be deployed to App Engine via the flexdev CLI.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/broady/flexdev/lib/flexdev"
)

type Build struct {
	flexdev.Build

	clientFiles flexdev.DirList
	remove      []string
	dir         string
	cmd         *exec.Cmd
	output      bytes.Buffer
	addr        string
	config      *config
}

func (b *Build) Cleanup() error {
	if b == nil {
		return nil
	}
	if b.dir == "" {
		return errors.New("Empty build directory. Could not clean.")
	}
	log.Printf("Cleaning up build %s (rm %s)", b.ID, b.dir)
	return os.RemoveAll(b.dir)
}

func (b *Build) GoBuild() error {
	b.State = flexdev.StateBuilding

	cmd := exec.Command("go", "build",
		"-tags", "appenginevm",
		"-o", "a.out")
	cmd.Dir = b.dir
	cmd.Stdout, cmd.Stderr = &b.output, &b.output
	cmd.Env = env(os.Environ(), "GOPATH", os.Getenv("GOPATH")+":"+filepath.Join(b.dir, "_gopath"))

	if err := cmd.Run(); err != nil {
		return err
	}

	b.State = flexdev.StateBuilt
	return nil
}

func (b *Build) Start() error {
	l, err := net.Listen("tcp", ":0")
	if err != nil {
		return err
	}
	b.addr = l.Addr().String()
	l.Close()

	_, port, err := net.SplitHostPort(b.addr)
	if err != nil {
		return err
	}

	cmd := exec.Command("./a.out")
	cmd.Dir = b.dir
	cmd.Stdout, cmd.Stderr = &b.output, &b.output
	environ := env(os.Environ(), "PORT", fmt.Sprintf("%s", port))
	environ = env(environ, "GOPATH", filepath.Join(b.dir, "_gopath"))
	for k, v := range build.config.Env {
		environ = env(environ, k, v)
	}
	cmd.Env = environ
	b.cmd = cmd

	if err := cmd.Start(); err != nil {
		return err
	}
	b.State = flexdev.StateRunning

	return nil
}

func (b *Build) Stop() error {
	if b.State != flexdev.StateRunning {
		return errors.New("Tried to stop binary when not running")
	}
	if b.cmd.Process == nil {
		b.State = flexdev.StateStopped
		return errors.New("Tried to stop binary when process not running")
	}
	if err := b.cmd.Process.Kill(); err != nil {
		return err
	}
	b.State = flexdev.StateStopped
	return nil
}

func (b *Build) DirList() (flexdev.DirList, error) {
	return flexdev.ListDir(b.dir)
}

func (b *Build) filesNeeded() (need, remove []string, err error) {
	// TODO(cbro): find out our other files that need to be deleted.
	ours, err := flexdev.ListDir(b.dir)
	if err != nil {
		return nil, nil, err
	}
	add, del := ours.Diff(b.clientFiles)
	need = make([]string, len(add))
	remove = make([]string, len(del))
	for i, e := range add {
		need[i] = e.Path
	}
	for i, e := range del {
		remove[i] = e.Path
	}
	return
}

func env(env []string, k, v string) []string {
	e := make([]string, 0)
	for _, entry := range env {
		if strings.HasPrefix(entry, k+"=") {
			continue
		}
		e = append(e, entry)
	}
	return append(e, k+"="+v)
}

type config struct {
	Runtime string            `yaml:"runtime"`
	VM      string            `yaml:"vm"`
	Env     map[string]string `yaml:"env_variables"`
}
