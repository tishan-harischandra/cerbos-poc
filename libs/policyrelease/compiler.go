package policyrelease

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// CLICompiler runs the cerbos binary's compile subcommand, mirroring the
// `cerbos compile --tests=<testsDir> <policyDir>` invocation this repository
// already runs from the Makefile's policy-test target.
type CLICompiler struct {
	binary string
}

// NewCLICompiler builds a CLICompiler that invokes binary. In the
// policy-controller image this is the cerbos binary copied in from the same
// image the served PDP runs, so a release can never validate against a
// different cerbos build than the one that will serve it.
func NewCLICompiler(binary string) *CLICompiler {
	return &CLICompiler{binary: binary}
}

// Compile implements Compiler.
func (c *CLICompiler) Compile(ctx context.Context, policyDir, testsDir string) error {
	cmd := exec.CommandContext(ctx, c.binary, "compile", "--tests="+testsDir, policyDir)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s compile --tests=%s %s: %w\n%s", c.binary, testsDir, policyDir, err, output.String())
	}
	return nil
}
