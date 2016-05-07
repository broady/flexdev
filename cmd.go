// Copyright 2016 Google Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

// Command flexdev is a fast way to develop Go applications for App Engine flexible environment.
//
// Get the tools:
//
//    $ go get -u github.com/broady/flexdev
//    $ go get -u google.golang.org/appengine/cmd/aedeploy
//
// Ensure you are signed into gcloud:
//
//    $ gcloud auth login
//
// Deploy the flexdev server:
//
//    $ flexdev server deploy -project your-project -version flexdev
//
// Deploy your application code quickly.
//
//    $ aedeploy flexdev deploy -target=https://flexdev-dot-your-project.appspot.com app.yaml
//
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/build"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/broady/flexdev/lib/flexdev"
	"github.com/termie/go-shutil"
)

func main() {
	flag.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage:")
		fmt.Fprintln(os.Stderr, "  flexdev server deploy -project=... -version=... [-module=...]")
		fmt.Fprintln(os.Stderr, "  flexdev deploy -target=https://...-dot-...-dot-....appspot.com")
		fmt.Fprintln(os.Stderr, "  flexdev status -target=https://...-dot-...-dot-....appspot.com")
		fmt.Fprintln(os.Stderr, "")
		flag.PrintDefaults()
		os.Exit(1)
	}
	flag.Parse()

	switch flag.Arg(0) {
	case "server":
		if flag.Arg(1) != "deploy" {
			usage("Missing command.")
		}
		if err := doDeployServer(); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	case "deploy":
		if err := doDeploy(); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	case "status":
		if err := doStatus(); err != nil {
			fmt.Fprintf(os.Stderr, "%v\n", err)
			os.Exit(1)
		}
	default:
		usage("Missing command.")
	}
}

func usage(lines ...string) {
	for _, l := range lines {
		fmt.Fprintln(os.Stderr, l)
	}
	flag.Usage()
}

func doStatus() error {
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.Usage = func() {
		flag.Usage()
		flags.PrintDefaults()
	}
	target := flags.String("target", "", "Hostname of flexdev server. Required.")
	if err := flags.Parse(flag.Args()[1:]); err != nil {
		return err
	}
	if *target == "" {
		usage("Missing 'target' flag.")
	}
	req, err := http.NewRequest("POST", *target+"/_flexdev/build/status", nil)
	if err != nil {
		return err
	}
	_, err = doReq(req)
	return err
}

func doDeploy() error {
	flags := flag.NewFlagSet("deploy", flag.ContinueOnError)
	flags.Usage = func() {
		flag.Usage()
		flags.PrintDefaults()
	}
	target := flags.String("target", "", "Hostname of flexdev server. Required.")
	if err := flags.Parse(flag.Args()[1:]); err != nil {
		return err
	}
	if *target == "" {
		usage("Missing 'target' flag.")
	}

	yamlFile := flags.Arg(0)
	if yamlFile == "" {
		usage("Missing 'app.yaml' path.")
	}
	fi, err := os.Stat(yamlFile)
	if err != nil {
		return fmt.Errorf("Could not stat yaml file: %v", err)
	}
	if fi.IsDir() {
		usage("Config path must be a file, not a directory.")
	}
	yamlContents, err := ioutil.ReadFile(yamlFile)
	if err != nil {
		return err
	}

	appRoot := filepath.Dir(yamlFile)

	dirList, err := flexdev.ListDir(appRoot)
	if err != nil {
		return fmt.Errorf("Could not get dir list: %v", err)
	}

	buildReq := &flexdev.CreateBuildRequest{
		Config: yamlContents,
		Files:  dirList,
	}
	b, err := json.Marshal(buildReq)
	if err != nil {
		return fmt.Errorf("Could not marshal dir list: %v", err)
	}

	req, err := http.NewRequest("POST", *target+"/_flexdev/build/create", bytes.NewReader(b))
	if err != nil {
		return err
	}
	resp, err := doReq(req)
	if err != nil {
		return err
	}
	if resp.Build != nil {
		log.Printf("Build id: %s", resp.Build.ID)
	}

	sendFile := make(chan string)
	var sendErr error
	var errMu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < 15; i++ {
		go func() {
			for {
				skip := false
				errMu.Lock()
				if sendErr != nil {
					skip = true
				}
				errMu.Unlock()

				path := <-sendFile
				if skip {
					wg.Done()
					continue
				}
				if err := putFile(resp.Build.ID, *target, filepath.Join(appRoot, path), path); err != nil {
					errMu.Lock()
					sendErr = fmt.Errorf("Could not send %s: %v", path, err)
					errMu.Unlock()
				}
				wg.Done()
			}
		}()
	}

	for _, destFile := range resp.NeedFiles {
		fn := filepath.Join(appRoot, destFile)
		fi, err := os.Stat(fn)
		if err != nil {
			return err
		}
		if !fi.IsDir() {
			wg.Add(1)
			sendFile <- destFile
			continue
		}
		err = filepath.Walk(fn, func(subFile string, fi os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if fi.IsDir() {
				return nil
			}
			destFile, err := filepath.Rel(appRoot, subFile)
			if err != nil {
				return err
			}
			wg.Add(1)
			sendFile <- destFile
			return nil
		})
		if err != nil {
			return err
		}
	}

	wg.Wait()

	if sendErr != nil {
		return fmt.Errorf("Could not send file: %v", sendErr)
	}

	log.Printf("All files sent.")

	req, err = http.NewRequest("POST", *target+"/_flexdev/build/start", nil)
	if err != nil {
		return err
	}
	if _, err := doReq(req); err != nil {
		log.Print(err)
	}
	log.Print("Build successful. App is available at:\n\n")
	fmt.Fprintf(os.Stderr, "   %s\n\n", *target)
	return nil
}

func putFile(buildID, target, filePath, destFile string) error {
	hash, err := flexdev.FileSHA1(filePath)
	if err != nil {
		return err
	}
	v := url.Values{
		"id":       {buildID},
		"filename": {destFile},
		"sha1":     {hash},
	}
	r, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer r.Close()

	req, err := http.NewRequest("POST", target+"/_flexdev/build/put?"+v.Encode(), r)
	if err != nil {
		return err
	}
	_, err = doReq(req)
	return err
}

func doReq(req *http.Request) (*Response, error) {
	hc, err := google.DefaultClient(oauth2.NoContext,
		"https://www.googleapis.com/auth/appengine.apis",
		"https://www.googleapis.com/auth/userinfo.email",
		"https://www.googleapis.com/auth/cloud.platform")
	if err != nil {
		return nil, err
	}

	var payload Response
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Could not perform request: %v", err)
	}
	defer resp.Body.Close()
	if v := resp.Header.Get("X-FlexDev"); v != flexdev.Version {
		if v == "" {
			return nil, errors.New("Target doesn't look like a flexdev server. Use `flexdev server deploy` to deploy it.")
		}
		return nil, fmt.Errorf("Target should be flexdev version %s. Use `flexdev server deploy` to update it.", flexdev.Version)
	}
	b, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Could not read response body: %v", err)
	}
	if err := json.Unmarshal(b, &payload); err != nil {
		return nil, fmt.Errorf("Could not decode %q: %v", string(b), err)
	}
	if payload.Message != "" {
		log.Printf("Remote message: %s", payload.Message)
	}
	if payload.Error != "" {
		return nil, fmt.Errorf("Remote error: %s", payload.Error)
	}
	return &payload, nil
}

type DirList []DirEntry

type DirEntry struct {
	Path  string
	IsDir bool
	SHA1  string
}

type Build struct {
	ID string
}

type Response struct {
	Code      int      `json:"code,omitempty"`
	Error     string   `json:"error,omitempty"`
	Build     *Build   `json:"build,omitempty"`
	NeedFiles []string `json:"need_files,omitempty"`
	Message   string   `json:"message,omitempty"`
}

func doDeployServer() error {
	flags := flag.NewFlagSet("server deploy", flag.ContinueOnError)
	flags.Usage = func() {
		flag.Usage()
		flags.PrintDefaults()
	}
	project := flags.String("project", "", "Project to deploy to. Required.")
	module := flags.String("module", "", "Module to deploy to. Optional.")
	version := flags.String("version", "", "Version to deploy to. Required.")
	if err := flags.Parse(flag.Args()[2:]); err != nil {
		return err
	}
	if *project == "" {
		usage("Missing 'project' flag.")
	}
	if *version == "" {
		usage("Missing 'project' flag.")
	}
	if *module == "" {
		*module = "default"
	}

	ver := fmt.Sprintf("%s.%s.%s", *project, *module, *version)
	log.Printf("Deploying to %s", ver)

	pkg, err := build.Import("github.com/broady/flexdev/server", "", build.FindOnly)
	if err != nil {
		return fmt.Errorf("could not get server code: %v", err)
	}
	libPkg, err := build.Import("github.com/broady/flexdev/lib/flexdev", "", build.FindOnly)
	if err != nil {
		return fmt.Errorf("could not get server lib code: %v", err)
	}

	tmp, err := ioutil.TempDir("", "flexdev-server-")
	if err != nil {
		return fmt.Errorf("could not get a temp dir for deployment: %v", err)
	}
	defer os.RemoveAll(tmp)

	root := filepath.Join(tmp, "flexdev-server")

	if err := shutil.CopyTree(pkg.Dir, root, nil); err != nil {
		return fmt.Errorf("could not copy %s to %s: %v", pkg.Dir, root, err)
	}
	if err := shutil.CopyTree(libPkg.Dir, filepath.Join(root, "vendor", "github.com", "broady", "flexdev", "lib", "flexdev"), nil); err != nil {
		return fmt.Errorf("could not copy %s to %s: %v", pkg.Dir, root, err)
	}

	f, err := os.OpenFile(filepath.Join(root, "app.yaml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("could not edit app.yaml: %v", err)
	}
	if err := writeAppYaml(f, *module); err != nil {
		return fmt.Errorf("could not write app.yaml: %v", err)
	}

	cmd := exec.Command("gcloud")
	cmd.Args = []string{"-q", "preview", "app", "deploy", "app.yaml", "--no-promote"}
	if *version != "" {
		cmd.Args = append(cmd.Args, "--version", *version)
	}
	cmd.Dir = root
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr

	log.Print("Running `gcloud app deploy`")

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Deploy not successful: %v", err)
	}

	modulePart := ""
	if *module != "default" {
		modulePart = "-dot-" + *module
	}
	fmt.Fprintf(os.Stderr, "\n\nNow you can deploy with:\n\n")
	fmt.Fprintf(os.Stderr, "   aedeploy %s deploy -target=https://%s%s-dot-%s.appspot.com app.yaml\n\n", os.Args[0], *version, modulePart, *project)
	return nil
}

func writeAppYaml(f *os.File, module string) (err error) {
	defer func() {
		closeErr := f.Close()
		if err == nil {
			err = closeErr
		}
	}()
	if module != "" {
		_, err = fmt.Fprintf(f, "\nmodule: %s", module)
		if err != nil {
			return err
		}
	}
	return
}
