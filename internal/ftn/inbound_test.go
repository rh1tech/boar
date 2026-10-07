// SPDX-License-Identifier: GPL-3.0-or-later

package ftn

import "testing"

func TestIsMailBundle(t *testing.T) {
	for name, want := range map[string]bool{
		"6ac47af8.pkt": true, "6AC47AF8.PKT": true,
		"00000000.mo0": true, "00000000.MO9": true,
		// After ten bundles in a day mailers go on with letters.
		"00000000.MOA": true, "00000000.tub": true, "00000000.SAZ": true,
		"00000000.mo_": false, "00000000.xx1": false, "readme.txt": false,
		"00000000.mo": false, "00000000.tic": false,
	} {
		if got := IsMailBundle(name); got != want {
			t.Errorf("IsMailBundle(%q) = %v, want %v", name, got, want)
		}
	}
}
