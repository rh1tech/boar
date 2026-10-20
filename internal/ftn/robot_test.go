// SPDX-License-Identifier: GPL-3.0-or-later

package ftn

import "testing"

func TestIsRobot(t *testing.T) {
	for _, name := range []string{"AreaFix", "areafix", " AREAMGR ", "FileFix", "Filemgr", "AllFix", "Raid", "EchoMgr", "AreaManager"} {
		if !IsRobot(name) {
			t.Errorf("IsRobot(%q) = false", name)
		}
	}
	for _, name := range []string{"Mikhail Matveev", "Sysop", "All", "Areafixer", ""} {
		if IsRobot(name) {
			t.Errorf("IsRobot(%q) = true", name)
		}
	}
}
