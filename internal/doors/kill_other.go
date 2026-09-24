// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !unix

package doors

import "os/exec"

// configureKill uses the default: kill the door process itself.
func configureKill(cmd *exec.Cmd) {}
