package bbs

import (
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"boar/internal/term"
)

// Screens a sysop can replace by dropping files into the art folder:
//
//	NAME.ans      CP437 ANSI art, as saved by PabloDraw or Moebius
//	NAME.txt      UTF-8 text with pipe color codes (like the built-in screens)
//	NAME.2.ans    ... any number of variants; one is picked at random per call
//
// @TOKENS@ (see screenTokens) are filled in in both formats.
var customScreens = []string{"welcome", "newuser", "logon", "goodbye"}

const maxArtBytes = 256 << 10

// artFiles lists the files that can stand in for a screen, sorted.
func artFiles(dir, screen string) []string {
	if dir == "" {
		return nil
	}
	var files []string
	for _, pattern := range []string{screen + ".ans", screen + ".*.ans", screen + ".txt", screen + ".*.txt"} {
		matches, err := filepath.Glob(filepath.Join(dir, pattern))
		if err == nil {
			files = append(files, matches...)
		}
	}
	slices.Sort(files)
	return slices.Compact(files)
}

// showCustomArt shows a random custom version of screen, reporting false
// when there is none (so the caller falls back to the built-in screen).
func (s *session) showCustomArt(screen string, vars map[string]string) (bool, error) {
	files := artFiles(s.srv.cfg.ArtDir, screen)
	if len(files) == 0 {
		return false, nil
	}
	path := files[rand.IntN(len(files))]
	if err := s.showArtFile(path, vars); err != nil {
		s.srv.log.Warn("custom art failed; using built-in screen", "file", path, "err", err)
		return false, nil
	}
	return true, nil
}

func (s *session) showArtFile(path string, vars map[string]string) error {
	data, err := readLimited(path, maxArtBytes)
	if err != nil {
		return err
	}
	if strings.EqualFold(filepath.Ext(path), ".txt") {
		text := string(data)
		for k, v := range vars {
			text = strings.ReplaceAll(text, "@"+k+"@", safe(v))
		}
		s.print(text)
		return nil
	}
	for k, v := range vars {
		// Tokens go in as CP437 so the converter can treat the file uniformly.
		data = []byte(strings.ReplaceAll(string(data), "@"+k+"@", string(term.Encode(term.CP437, term.StripControl(v)))))
	}
	s.write(term.ConvertANSIArt(data, s.cs, s.color))
	s.rend = term.NewRenderer(s.mode) // the art reset the colors
	return nil
}

func readLimited(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("art file larger than %d KB", limit>>10)
	}
	return data, nil
}

// screenTokens are the @TOKENS@ available on every custom screen.
func (s *session) screenTokens() map[string]string {
	members, err := s.srv.store.UserCount()
	if err != nil {
		s.srv.log.Warn("member count for art tokens", "err", err)
	}
	return map[string]string{
		"BBS":      s.srv.cfg.Name,
		"NODE":     fmt.Sprint(s.node.id),
		"NODES":    fmt.Sprint(s.srv.cfg.MaxNodes),
		"ONLINE":   fmt.Sprint(len(s.srv.nodes.online())),
		"MEMBERS":  fmt.Sprint(members),
		"HANDLE":   s.user.Handle,
		"LOCATION": s.user.Location,
		"CALLS":    fmt.Sprint(s.user.Calls),
		"TIME":     time.Now().Format("Monday, January 2 2006  15:04 MST"),
		"DURATION": shortDuration(time.Since(s.start)),
	}
}

// sysopArt lists custom screens and previews them.
func (s *session) sysopArt() error {
	for {
		s.header("Custom Art")
		dir := s.srv.cfg.ArtDir
		if dir == "" {
			s.printf("\n  %sNo art folder configured (start with -art DIR).\n\n", colDim)
			return s.pause()
		}
		var files []string
		screens := []string{colDim + "Folder: " + colLabel + safe(dir), separator}
		for _, screen := range customScreens {
			found := artFiles(dir, screen)
			note := colDim + "built-in"
			if len(found) > 0 {
				note = colOK + plural(len(found), "variant")
			}
			screens = append(screens, colHandle+term.Pad(screen, 10)+" "+note)
			files = append(files, found...)
		}
		s.box("Screens", screens...)
		rows := make([]string, 0, len(files))
		for i, f := range files {
			rows = append(rows, fmt.Sprintf("%s%4d  %s%s", colValue, i+1, colLabel, safe(filepath.Base(f))))
		}
		if err := s.table("Files", "", rows, "Drop NAME.ans or NAME.txt files into the folder to replace a screen."); err != nil {
			return err
		}
		if len(files) == 0 {
			return s.pause()
		}
		n, ok, err := s.pickNumber(len(files), "Preview")
		if err != nil || !ok {
			return err
		}
		s.print("|CL")
		if err := s.showArtFile(files[n-1], s.screenTokens()); err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				s.printf("\n%s%s\n", colAlert, safe(err.Error()))
			}
		}
		s.print("\n")
		if err := s.pause(); err != nil {
			return err
		}
	}
}
