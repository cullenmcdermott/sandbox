package sync

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
)

// ExecRunner is a Runner that invokes the mutagen CLI binary.
type ExecRunner struct {
	bin string
}

// NewExecRunner returns an ExecRunner using the given binary path. If bin is
// empty it defaults to "mutagen" (resolved via PATH).
func NewExecRunner(bin string) *ExecRunner {
	if bin == "" {
		bin = "mutagen"
	}
	return &ExecRunner{bin: bin}
}

// Output runs the mutagen binary with the given args, returning stdout. On
// failure it includes stderr in the error for diagnosis.
func (r *ExecRunner) Output(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, r.bin, args...)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := stderr.String()
		if msg == "" {
			msg = stdout.String()
		}
		return stdout.Bytes(), fmt.Errorf("mutagen %v: %w: %s", args, err, msg)
	}
	return stdout.Bytes(), nil
}

var _ Runner = (*ExecRunner)(nil)
