// Copyright 2016 Google Inc. All rights reserved.
// Use of this source code is governed by the Apache 2.0
// license that can be found in the LICENSE file.

package flexdev

import "testing"

func TestTwoFiles(t *testing.T) {
	ours := DirList{
		{Path: "a"},
	}
	theirs := DirList{
		{Path: "b"},
	}
	add, remove := ours.Diff(theirs)
	if want, got := 1, len(add); want != got {
		t.Fatalf("want len(add) = %d, got %d", want, got)
	}
	if want, got := 1, len(remove); want != got {
		t.Fatalf("want len(remove) = %d, got %d", want, got)
	}
	if want, got := "b", add[0].Path; want != got {
		t.Fatalf("want add[0] = %s, got %s", want, got)
	}
	if want, got := "a", remove[0].Path; want != got {
		t.Fatalf("want remove[0] = %s, got %s", want, got)
	}
}

func TestOnlyWant(t *testing.T) {
	ours := DirList{}
	theirs := DirList{
		{Path: "a"},
		{Path: "c"},
		{Path: "b", IsDir: true},
		{Path: "b/a"},
	}
	add, remove := ours.Diff(theirs)
	if want, got := 3, len(add); want != got {
		t.Fatalf("want len(add) = %d, got %d", want, got)
	}
	if want, got := 0, len(remove); want != got {
		t.Fatalf("want len(remove) = %d, got %d", want, got)
	}
}

func TestFileNowDir(t *testing.T) {
	ours := DirList{
		{Path: "a"},
	}
	theirs := DirList{
		{Path: "a", IsDir: true},
		{Path: "a/b"},
	}
	add, remove := ours.Diff(theirs)
	if want, got := 1, len(add); want != got {
		t.Fatalf("want len(add) = %d, got %d", want, got)
	}
	if want, got := 1, len(remove); want != got {
		t.Fatalf("want len(remove) = %d, got %d", want, got)
	}
}

func TestMismatchSHA(t *testing.T) {
	ours := DirList{
		{Path: "a", SHA1: "a"},
	}
	theirs := DirList{
		{Path: "a", SHA1: "b"},
	}
	add, remove := ours.Diff(theirs)
	if want, got := 1, len(add); want != got {
		t.Fatalf("want len(add) = %d, got %d", want, got)
	}
	if want, got := 0, len(remove); want != got {
		t.Fatalf("want len(remove) = %d, got %d", want, got)
	}
}

func TestRemoveDirs(t *testing.T) {
	ours := DirList{
		{Path: "a", IsDir: true},
		{Path: "a/a"},
		{Path: "a/b"},
	}
	theirs := DirList{}
	add, remove := ours.Diff(theirs)
	if want, got := 0, len(add); want != got {
		t.Fatalf("want len(add) = %d, got %d", want, got)
	}
	if want, got := 1, len(remove); want != got {
		t.Logf("remove = %v", remove)
		t.Fatalf("want len(remove) = %d, got %d", want, got)
	}
}
