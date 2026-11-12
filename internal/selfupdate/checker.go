package selfupdate

import (
	"fmt"
	"os/exec"
	"strings"
)

// ExecSelfChecker runs the candidate binary's "version" subcommand and
// treats a clean exit as "starts up okay." Deliberately minimal -- this
// only needs to catch a binary that's corrupt or fails to execute at all,
// not validate behavior.
type ExecSelfChecker struct{}

func (ExecSelfChecker) Check(path string) error {
	cmd := exec.Command(path, "version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
