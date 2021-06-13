// Copyright 2021 Tamás Gulácsi
//
// SPDX-License-Identifier: Apache-2.0

package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestGen(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "install")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd = exec.CommandContext(ctx, "protoc", "--wsdl_out=dij:"+filepath.Join(cwd, "testdata"), "-I", os.ExpandEnv("$HOME/src:."), "./testdata/db_pgw_ws.proto")
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %+v", b, err)
	}
}
