// Copyright 2016 Google Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"golang.org/x/net/context"

	"google.golang.org/appengine"
	"google.golang.org/appengine/user"

	"gopkg.in/yaml.v2"

	"github.com/broady/flexdev/lib/flexdev"
)

var adminMux = http.NewServeMux()

var packageDir = filepath.Join(os.TempDir(), "flexdev-server")

var (
	buildMu sync.RWMutex
	build   *Build
)

func main() {
	http.HandleFunc("/", proxyHandler)

	http.HandleFunc("/_flexdev/build/", adminHandler)
	adminMux.HandleFunc("/_flexdev/build/create", createBuildHandler)
	adminMux.HandleFunc("/_flexdev/build/put", putFileHandler)
	adminMux.HandleFunc("/_flexdev/build/start", startBuildHandler)
	adminMux.HandleFunc("/_flexdev/build/status", statusHandler)

	log.Print("Server running.")

	appengine.Main()
}

func proxyHandler(w http.ResponseWriter, r *http.Request) {
	buildMu.RLock()
	defer buildMu.RUnlock()

	if build == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "No app to run. Use `flexdev deploy` to deploy the application code.")
		return
	}

	if build.State != flexdev.StateRunning {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprintf(w, "state: %s\n", build.State)
		fmt.Fprintf(w, "%s", build.output.String())
		return
	}
	target := &url.URL{
		Scheme: "http",
		Host:   build.addr,
	}

	w.Header().Set("X-FlexDev", flexdev.Version)
	httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r)
}

func adminHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-FlexDev", flexdev.Version)

	if h := r.Header.Get("X-Appengine-Https"); h != "" && h != "on" {
		Response{
			Code:  http.StatusBadRequest,
			Error: errors.New("This endpoint only works via HTTPS."),
		}.WriteTo(w)
		return
	}

	auth := r.Header.Get("Authorization")
	if auth == "" {
		Response{
			Code:  http.StatusUnauthorized,
			Error: errors.New("Missing auth token."),
		}.WriteTo(w)
		return
	}

	ctx := appengine.NewContext(r)
	if err := checkAuth(ctx, auth); err != nil {
		Response{
			Code:  http.StatusForbidden,
			Error: fmt.Errorf("Could not verify your auth: %v", err),
		}.WriteTo(w)
		return
	}

	adminMux.ServeHTTP(w, r)
}

func checkAuth(ctx context.Context, h string) error {
	if os.Getenv("FLEXDEV_NOAUTH") != "" {
		return nil
	}

	u, err := user.CurrentOAuth(ctx,
		"https://www.googleapis.com/auth/cloud-platform",
		"https://www.googleapis.com/auth/appengine.apis")
	if err != nil {
		return err
	}
	if !u.Admin {
		return errors.New("Must be an admin.")
	}
	return nil
}

func createBuildHandler(w http.ResponseWriter, r *http.Request) {
	buildMu.Lock()
	defer buildMu.Unlock()

	if build != nil && build.State == flexdev.StateRunning {
		if err := build.Stop(); err != nil {
			Response{Error: fmt.Errorf("Could not stop existing binary: %v", err)}.WriteTo(w)
			return
		}
	}

	var buildReq flexdev.CreateBuildRequest
	if err := json.NewDecoder(r.Body).Decode(&buildReq); err != nil {
		if err != io.EOF {
			Response{Error: fmt.Errorf("Could not read build req: %v", err)}.WriteTo(w)
			return
		}
	}
	if len(buildReq.Config) == 0 {
		Response{
			Error: errors.New("Missing config file."),
			Code:  http.StatusBadRequest,
		}.WriteTo(w)
		return
	}
	if len(buildReq.Files) == 0 {
		Response{
			Error: errors.New("Missing dir list."),
			Code:  http.StatusBadRequest,
		}.WriteTo(w)
		return
	}

	var config config
	if err := yaml.Unmarshal(buildReq.Config, &config); err != nil {
		Response{
			Error: fmt.Errorf("Could not parse yaml config: %v", err),
			Code:  http.StatusBadRequest,
		}.WriteTo(w)
	}

	// Ensure packageDir exists.
	if err := os.MkdirAll(packageDir, 0755); err != nil {
		Response{Error: err}.WriteTo(w)
		return
	}

	id := fmt.Sprintf("%d", time.Now().UnixNano())
	build = &Build{}
	build.ID = id
	build.State = flexdev.StateCreated
	build.dir = packageDir
	build.clientFiles = buildReq.Files
	build.config = &config

	log.Printf("Created build %s", build.ID)

	need, remove, err := build.filesNeeded()
	if err != nil {
		Response{Error: fmt.Errorf("Could not get needed files: %v", err)}.WriteTo(w)
		return
	}
	for _, f := range remove {
		log.Printf("Removing %s", f)
		if err := os.RemoveAll(filepath.Join(build.dir, f)); err != nil {
			Response{Error: err}.WriteTo(w)
			return
		}
	}

	Response{
		Message:   "Build created.",
		Build:     build,
		NeedFiles: need,
	}.WriteTo(w)
}

func putFileHandler(w http.ResponseWriter, r *http.Request) {
	buildMu.RLock()
	defer buildMu.RUnlock()

	buildID := r.FormValue("id")
	dest := r.FormValue("filename")
	hash := r.FormValue("sha1")

	if buildID == "" {
		Response{Code: http.StatusBadRequest, Error: errors.New("Missing build ID.")}.WriteTo(w)
		return
	}
	if buildID != build.ID {
		Response{Code: http.StatusBadRequest, Error: errors.New("Build ID does not match.")}.WriteTo(w)
		return
	}
	if dest == "" {
		Response{Code: http.StatusBadRequest, Error: errors.New("Missing destination filename.")}.WriteTo(w)
		return
	}
	if hash == "" {
		Response{Code: http.StatusBadRequest, Error: errors.New("Missing hash.")}.WriteTo(w)
		return
	}
	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		Response{Error: err}.WriteTo(w)
		return
	}
	if fmt.Sprintf("%x", sha1.Sum(b)) != hash {
		Response{Code: http.StatusBadRequest, Error: errors.New("sum did not match")}.WriteTo(w)
		return
	}

	if err := os.MkdirAll(filepath.Dir(filepath.Join(build.dir, dest)), 0755); err != nil {
		Response{Error: fmt.Errorf("Could not mkdir -p %s: %v", dest, err)}.WriteTo(w)
		return
	}

	log.Print("Writing to ", filepath.Join(build.dir, dest))
	if err := ioutil.WriteFile(filepath.Join(build.dir, dest), b, 0755); err != nil {
		Response{Error: fmt.Errorf("Could not write file to %s: %v", dest, err)}.WriteTo(w)
		return
	}

	Response{Message: fmt.Sprintf("Wrote %s", dest)}.WriteTo(w)
}

func startBuildHandler(w http.ResponseWriter, r *http.Request) {
	buildMu.Lock()
	defer buildMu.Unlock()

	if err := build.GoBuild(); err != nil {
		Response{
			Message: build.output.String(),
			Error:   fmt.Errorf("Build failed: %v", err),
		}.WriteTo(w)
		return
	}
	if err := build.Start(); err != nil {
		Response{Error: fmt.Errorf("Could not run binary: %v", err)}.WriteTo(w)
		return
	}
	Response{Message: "App is running."}.WriteTo(w)
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	buildMu.Lock()
	defer buildMu.Unlock()

	buf := &bytes.Buffer{}
	if build == nil {
		fmt.Fprintln(buf, "build=nil")
		Response{Message: buf.String()}.WriteTo(w)
		return
	}
	fmt.Fprintln(buf, build.ID)
	fmt.Fprintln(buf, build.State)
	fmt.Fprintln(buf, build.addr)
	fmt.Fprintln(buf, build.config)
	fmt.Fprintf(buf, "%s\n", build.output.Bytes())

	Response{Message: buf.String()}.WriteTo(w)
}

type Response struct {
	Code      int      `json:"code,omitempty"`
	Error     error    `json:"-"`
	Build     *Build   `json:"build,omitempty"`
	NeedFiles []string `json:"need_files,omitempty"`
	Message   string   `json:"message,omitempty"`

	// Used for serialization.
	ErrorJSON string `json:"error,omitempty"`
}

func (r Response) WriteTo(w http.ResponseWriter) {
	if r.Error != nil {
		log.Print(r.Error)
		r.ErrorJSON = r.Error.Error()
	}
	b, err := json.Marshal(r)
	if err != nil {
		w.WriteHeader(500)
		log.Printf("Could not marshal JSON for %#v: %v", r, err)
		fmt.Fprint(w, `{"error":"Could not marshal response JSON. Check server logs."}`)
		return
	}

	if r.Code != 0 {
		w.WriteHeader(r.Code)
	} else if r.Error != nil {
		w.WriteHeader(500)
	}
	w.Write(b)
}
