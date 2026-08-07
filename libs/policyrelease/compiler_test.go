package policyrelease_test

import (
	"context"
	"os/exec"
	"testing"

	"github.com/tishan-harischandra/cerbos-poc/libs/policyrelease"
)

func TestCLICompiler_ReturnsErrorWithOutputWhenBinaryFails(t *testing.T) {
	if _, err := exec.LookPath("false"); err != nil {
		t.Skip("no 'false' binary on PATH")
	}

	compiler := policyrelease.NewCLICompiler("false")
	err := compiler.Compile(context.Background(), "/nonexistent-policy-dir", "/nonexistent-policy-dir/tests")
	if err == nil {
		t.Fatal("Compile: want error when the compiler binary exits non-zero, got nil")
	}
}

func TestCLICompiler_SucceedsWhenBinaryExitsZero(t *testing.T) {
	if _, err := exec.LookPath("true"); err != nil {
		t.Skip("no 'true' binary on PATH")
	}

	compiler := policyrelease.NewCLICompiler("true")
	if err := compiler.Compile(context.Background(), "/policies", "/policies/tests"); err != nil {
		t.Fatalf("Compile: %v", err)
	}
}
