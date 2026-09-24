package doors

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"boar/internal/term"
)

// Drop file names, upper case as DOS programs expect.
const (
	DoorSys    = "DOOR.SYS"
	DorInfo    = "DORINFO1.DEF"
	levelUser  = 100
	levelSysop = 255
	baudRate   = 38400
)

// Caller describes who is entering a door.
type Caller struct {
	Node        int
	UserID      int64
	Handle      string
	Location    string
	Sysop       bool
	Calls       int
	LastLogin   time.Time
	MinutesLeft int
	ScreenRows  int
	ANSI        bool
	BBSName     string
	SysopName   string
}

// WriteDropFiles writes DOOR.SYS and DORINFO1.DEF into dir, in CP437 with
// DOS line endings. The caller's password is never written.
func WriteDropFiles(dir string, c Caller, now time.Time) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("doors: drop dir: %w", err)
	}
	files := map[string][]string{DoorSys: doorSys(c, now), DorInfo: dorInfo(c)}
	for name, lines := range files {
		data := term.Encode(term.CP437, strings.Join(lines, "\r\n")+"\r\n")
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return fmt.Errorf("doors: write %s: %w", name, err)
		}
	}
	return nil
}

func yn(b bool) string {
	if b {
		return "Y"
	}
	return "N"
}

func level(c Caller) int {
	if c.Sysop {
		return levelSysop
	}
	return levelUser
}

// clean keeps drop file values on one line.
func clean(s string) string {
	return strings.TrimSpace(term.StripControl(s))
}

// doorSys renders the 52-line GAP DOOR.SYS format.
func doorSys(c Caller, now time.Time) []string {
	last := c.LastLogin
	if last.IsZero() {
		last = now
	}
	graphics := "NG"
	if c.ANSI {
		graphics = "GR"
	}
	return []string{
		"COM1:",                        // 1 comm port (the door talks via stdio or a socket)
		fmt.Sprint(baudRate),           // 2 baud rate
		"8",                            // 3 parity/data bits
		fmt.Sprint(c.Node),             // 4 node number
		fmt.Sprint(baudRate),           // 5 locked DTE rate
		"Y",                            // 6 screen display
		"N",                            // 7 printer
		"Y",                            // 8 page bell
		"Y",                            // 9 caller alarm
		clean(c.Handle),                // 10 user's name
		clean(c.Location),              // 11 calling from
		"000-000-0000",                 // 12 home phone
		"000-000-0000",                 // 13 work phone
		"",                             // 14 password: deliberately blank
		fmt.Sprint(level(c)),           // 15 security level
		fmt.Sprint(c.Calls),            // 16 times on
		last.Format("01/02/06"),        // 17 last date called
		fmt.Sprint(c.MinutesLeft * 60), // 18 seconds remaining
		fmt.Sprint(c.MinutesLeft),      // 19 minutes remaining
		graphics,                       // 20 graphics mode
		fmt.Sprint(c.ScreenRows),       // 21 page length
		"N",                            // 22 expert mode
		"1",                            // 23 conferences registered
		"1",                            // 24 conference exited from
		"12/31/99",                     // 25 expiration date
		fmt.Sprint(c.UserID),           // 26 user record number
		"Z",                            // 27 default protocol
		"0",                            // 28 uploads
		"0",                            // 29 downloads
		"0",                            // 30 daily download K
		"0",                            // 31 max daily download K
		"01/01/80",                     // 32 birthdate
		".",                            // 33 path to main directory
		".",                            // 34 path to gen directory
		clean(c.SysopName),             // 35 sysop name
		clean(c.Handle),                // 36 alias
		"00:00",                        // 37 event time
		"Y",                            // 38 error-correcting connection
		yn(c.ANSI),                     // 39 ANSI supported
		"Y",                            // 40 record locking
		"7",                            // 41 default color
		"0",                            // 42 time credits
		last.Format("01/02/06"),        // 43 last new-files scan
		now.Format("15:04"),            // 44 time of this call
		last.Format("15:04"),           // 45 time of last call
		"32768",                        // 46 max daily files
		"0",                            // 47 files downloaded today
		"0",                            // 48 total K uploaded
		"0",                            // 49 total K downloaded
		"",                             // 50 comment
		"0",                            // 51 doors opened
		"0",                            // 52 messages left
	}
}

// dorInfo renders the 13-line DORINFO1.DEF (RBBS/QuickBBS) format.
func dorInfo(c Caller) []string {
	first, last := splitName(clean(c.Handle))
	sysFirst, sysLast := splitName(clean(c.SysopName))
	graphics := "0"
	if c.ANSI {
		graphics = "1"
	}
	return []string{
		clean(c.BBSName),
		sysFirst,
		sysLast,
		"COM1",
		fmt.Sprintf("%d BAUD,N,8,1", baudRate),
		"0",
		first,
		last,
		clean(c.Location),
		graphics,
		fmt.Sprint(level(c)),
		fmt.Sprint(c.MinutesLeft),
		"-1", // FOSSIL
	}
}

// splitName splits a handle into DOS-style first and last names.
func splitName(name string) (string, string) {
	first, last, found := strings.Cut(name, " ")
	if !found || strings.TrimSpace(last) == "" {
		return strings.ToUpper(first), "NLN" // "no last name", as BBSes wrote it
	}
	return strings.ToUpper(first), strings.ToUpper(strings.TrimSpace(last))
}
