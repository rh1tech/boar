// SPDX-License-Identifier: GPL-3.0-or-later

package ftn

import "strings"

// HiddenSubject stands in for a robot request's subject, which is the
// password of the link it is sent over.
const HiddenSubject = "(password hidden)"

// robots are the names Areafix-style robots answer netmail to.
var robots = map[string]bool{
	"areafix": true, "areamgr": true, "areamanager": true, "echomgr": true,
	"filefix": true, "filemgr": true, "filemanager": true, "allfix": true,
	"raid": true, "afix": true, "ffix": true,
}

// IsRobot reports whether netmail addressed to name is a robot request, whose
// subject is a password.
func IsRobot(name string) bool { return robots[strings.ToLower(strings.TrimSpace(name))] }
