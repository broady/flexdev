// Copyright 2016 Google Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

// Package flexdev contains code shared between the flexdev CLI and flexdev server.
package flexdev

import (
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const Version = "0.1"

type Build struct {
	ID    string
	State state
}

type state string

const (
	StateCreated  = state("created")
	StateFetching = state("fetching")
	StateBuilding = state("building")
	StateBuilt    = state("built")
	StateRunning  = state("running")
	StateStopped  = state("stopped")
)

type DirList []DirEntry

func (d DirList) Len() int {
	return len(d)
}

func (d DirList) Less(i, j int) bool {
	return d[i].Path < d[j].Path
}

func (d DirList) Swap(i, j int) {
	d[i], d[j] = d[j], d[i]
}

type DirEntry struct {
	Path  string
	IsDir bool
	SHA1  string
}

func (e DirEntry) InDir(dir DirEntry) bool {
	return dir.IsDir && strings.HasPrefix(e.Path+"/", dir.Path)
}

func FileSHA1(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	s1 := sha1.New()
	if _, err := io.Copy(s1, f); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", s1.Sum(nil)), nil
}

func ListDir(dirPath string) (DirList, error) {
	d := make(DirList, 0)
	err := filepath.Walk(dirPath, func(path string, fi os.FileInfo, err error) error {
		if dirPath != "." && !strings.HasPrefix(path, dirPath) {
			return fmt.Errorf("wanted %s to be in dir %s", path, dirPath)
		}
		e := DirEntry{}
		if p, err := filepath.Rel(dirPath, path); err != nil {
			return err
		} else {
			e.Path = p
		}
		if fi == nil {
			return fmt.Errorf("fi nil: %s", path)
		}
		e.IsDir = fi.IsDir()
		if !fi.IsDir() {
			sha, err := FileSHA1(path)
			if err != nil {
				return err
			}
			e.SHA1 = sha
		}
		d = append(d, e)
		return nil
	})
	return d, err
}

func (ours DirList) Diff(want DirList) (add, remove DirList) {
	sort.Sort(want)
	sort.Sort(ours)

	remove = make(DirList, 0)
	add = make(DirList, 0)

	appendEntry := func(l DirList, e DirEntry) DirList {
		if len(l) == 0 {
			return append(l, e)
		}
		if e.InDir(l[len(l)-1]) {
			return l
		}
		return append(l, e)
	}
	shift := func(d DirList) (DirEntry, DirList) {
		return d[0], d[1:]
	}

	var o, w DirEntry
	for len(ours) != 0 && len(want) != 0 {
		if ours[0].Path == want[0].Path {
			o, ours = shift(ours)
			w, want = shift(want)

			if o.IsDir != w.IsDir {
				// Mismatch in file type.
				remove = appendEntry(remove, o)
				add = appendEntry(add, w)
				continue
			}
			if o.SHA1 == w.SHA1 {
				// Nothing to change. Perfect!
				// Includes both being dirs (they don't have a SHA).
				continue
			}

			// Replace file.
			add = appendEntry(add, w)
			continue
		}
		if ours[0].Path > want[0].Path {
			w, want = shift(want)
			add = appendEntry(add, w)
			continue
		}
		if ours[0].Path < want[0].Path {
			o, ours = shift(ours)
			remove = appendEntry(remove, o)
			continue
		}
	}

	for len(want) != 0 {
		w, want = shift(want)
		add = appendEntry(add, w)
	}
	for len(ours) != 0 {
		o, ours = shift(ours)
		remove = appendEntry(remove, o)
	}

	return
}

type CreateBuildRequest struct {
	Config []byte
	Files  DirList
}
