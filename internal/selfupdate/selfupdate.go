// Package selfupdate implements the agent's own auto-update: comparing
// the version it's currently running against the fleet-wide target the
// dashboard reports on check-in, and swapping itself over when they
// differ.
//
// Two failure tiers, matching the design decided with the product owner:
//   - A Fetch failure (download or cosign verification) never touches the
//     running binary -- nothing to roll back, just report and let the next
//     check-in retry.
//   - A SelfCheck failure happens AFTER the swap, so the previous binary
//     (kept at runningBinaryPath+".prev") is restored immediately, with no
//     human involved.
package selfupdate

import "os"

// Fetcher resolves a target version into a ready-to-install binary on
// disk, having already downloaded and cryptographically verified it (the
// same cosign verify-blob check install-agent.sh performs, pinned to the
// UnitSense/agent release identity). Returns the path to the verified
// binary.
type Fetcher interface {
	Fetch(version string) (binaryPath string, err error)
}

// SelfChecker runs a candidate binary and reports whether it starts up
// cleanly. The production implementation execs "<path> version" and
// checks for a clean exit.
type SelfChecker interface {
	Check(binaryPath string) error
}

// Outcome describes what Apply did, shaped to report directly on the
// agent's next check-in (dashboard/src/app/api/v1/agent/check-in/route.ts
// update_status field).
type Outcome struct {
	Attempted   bool
	Outcome     string // "ok" | "fail"; only meaningful when Attempted
	FromVersion string
	ToVersion   string
	Detail      string
}

// Apply compares currentVersion to targetVersion and, if they differ,
// fetches and swaps in the new binary at runningBinaryPath.
func Apply(currentVersion, targetVersion, runningBinaryPath string, fetcher Fetcher, checker SelfChecker) Outcome {
	if targetVersion == "" || targetVersion == currentVersion {
		return Outcome{Attempted: false}
	}

	newBinary, err := fetcher.Fetch(targetVersion)
	if err != nil {
		return Outcome{
			Attempted: true, Outcome: "fail",
			FromVersion: currentVersion, ToVersion: targetVersion,
			Detail: "fetch: " + err.Error(),
		}
	}

	backupPath := runningBinaryPath + ".prev"
	if err := os.Rename(runningBinaryPath, backupPath); err != nil {
		return Outcome{
			Attempted: true, Outcome: "fail",
			FromVersion: currentVersion, ToVersion: targetVersion,
			Detail: "backup current binary: " + err.Error(),
		}
	}
	if err := os.Rename(newBinary, runningBinaryPath); err != nil {
		// Couldn't get the new binary into place -- restore immediately so
		// the current version keeps running.
		_ = os.Rename(backupPath, runningBinaryPath)
		return Outcome{
			Attempted: true, Outcome: "fail",
			FromVersion: currentVersion, ToVersion: targetVersion,
			Detail: "install new binary: " + err.Error(),
		}
	}

	if err := checker.Check(runningBinaryPath); err != nil {
		// New binary is live but broken -- roll back right away.
		_ = os.Rename(backupPath, runningBinaryPath)
		return Outcome{
			Attempted: true, Outcome: "fail",
			FromVersion: currentVersion, ToVersion: targetVersion,
			Detail: "self-check failed, rolled back: " + err.Error(),
		}
	}

	return Outcome{
		Attempted: true, Outcome: "ok",
		FromVersion: currentVersion, ToVersion: targetVersion,
	}
}
