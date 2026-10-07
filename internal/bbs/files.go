// SPDX-License-Identifier: GPL-3.0-or-later

package bbs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"boar/internal/filelink"
	"boar/internal/store"
	"boar/internal/term"
)

const defaultFileLinkTTL = 24 * time.Hour

// filesMenu lists the FidoNet file echoes the BBS has received files in.
func (s *session) filesMenu() error {
	for {
		s.setActivity("File areas")
		areas, err := s.srv.store.FileAreas(s.user.ID)
		if err != nil {
			return err
		}
		s.header("File Areas")
		heading := fmt.Sprintf("%4s  %s %s %s", "#", term.Pad("Area", 22), term.Pad("Files", 14), "Newest")
		rows := make([]string, 0, len(areas))
		totalNew := 0
		for i, a := range areas {
			totalNew += a.New
			count := fmt.Sprintf("%d", a.Files)
			if a.New > 0 {
				count += fmt.Sprintf(" %s(%d new)", colAlert, a.New)
			}
			newest := "—"
			if !a.Newest.IsZero() {
				newest = shortDate(a.Newest)
			}
			rows = append(rows, fmt.Sprintf("%s%4d  %s%s %s %s%s  %s%s",
				colValue, i+1, colHandle, safe(term.Pad(a.Tag, 22)),
				fit(colLabel+count, 14), colDim, safe(newest), colInfo, safe(a.Description)))
		}
		empty := "No files yet. File echoes fill in as files arrive from FidoNet."
		if err := s.table(plural(len(areas), "file area"), heading, rows, empty); err != nil {
			return err
		}
		ans, err := s.prompt(fmt.Sprintf("\n |07Area # |08(%d new, Enter = back) %s» |15", totalNew, colBorder), 4)
		if err != nil || ans == "" {
			return err
		}
		n, convErr := strconv.Atoi(ans)
		if convErr != nil || n < 1 || n > len(areas) {
			s.printf("%sNo such area.\n", colAlert)
			if err := s.pause(); err != nil {
				return err
			}
			continue
		}
		if err := s.fileAreaView(areas[n-1].FileArea); err != nil {
			return err
		}
	}
}

func (s *session) fileAreaView(a store.FileArea) error {
	for {
		s.setActivity("Files in " + a.Tag)
		files, err := s.srv.store.FilesIn(a.ID)
		if err != nil {
			return err
		}
		seen, err := s.srv.store.FilesLastSeen(s.user.ID, a.ID)
		if err != nil {
			return err
		}
		s.header(a.Tag)
		descW := max(s.inner(s.width())-44, 10)
		heading := fmt.Sprintf("%s%4s    %s %s %s %s", colDim, "#", term.Pad("File", 16), term.Pad("Size", 8), term.Pad("Date", 9), "Description")
		rows := make([]string, 0, len(files))
		for i, f := range files {
			mark := "    "
			if f.ID > seen {
				mark = " " + colAlert + "*" + colLabel + "  "
			}
			desc, _, _ := strings.Cut(f.Description, "\n")
			rows = append(rows, fmt.Sprintf("%s%4d%s%s%s %s%s %s%s %s%s",
				colValue, i+1, mark, colHandle, safe(term.Pad(f.Name, 16)),
				colLabel, term.Pad(humanSize(f.Size), 8), colInfo, term.Pad(shortDate(f.ReceivedAt), 9),
				colLabel, safe(term.Pad(desc, descW))))
		}
		if err := s.table(plural(len(files), "file"), heading, rows, "No files in this area."); err != nil {
			return err
		}
		if len(files) > 0 {
			if err := s.srv.store.MarkFilesSeen(s.user.ID, a.ID, files[0].ID); err != nil {
				s.srv.log.Warn("mark files seen failed", "area", a.ID, "err", err)
			}
		}
		ans, err := s.prompt(fmt.Sprintf("\n |07File # |08(Enter = back) %s» |15", colBorder), 4)
		if err != nil || ans == "" {
			return err
		}
		n, convErr := strconv.Atoi(ans)
		if convErr != nil || n < 1 || n > len(files) {
			s.printf("%sNo such file.\n", colAlert)
			if err := s.pause(); err != nil {
				return err
			}
			continue
		}
		if err := s.fileView(a, files[n-1]); err != nil {
			return err
		}
	}
}

func (s *session) fileView(a store.FileArea, f store.File) error {
	for {
		s.setActivity("Looking at " + f.Name)
		body := f.Description
		if f.LDesc != "" {
			body += "\n\n" + f.LDesc
		}
		fields := []field{
			{"File", f.Name, colBright},
			{"Area", a.Tag, colLabel},
			{"Size", fmt.Sprintf("%s (%d bytes, CRC %s)", humanSize(f.Size), f.Size, f.CRC32), colLabel},
			{"Date", longDate(f.ReceivedAt), colLabel},
		}
		if f.Origin != "" {
			fields = append(fields, field{"From", f.Origin, colLabel})
		}
		if err := s.showLetter(letter{title: "File · " + a.Tag, body: body, fields: fields}); err != nil {
			return err
		}
		words := []string{"Zmodem"}
		if s.srv.cfg.FileLinks.URL != "" {
			words = append(words, "Link")
		}
		words = append(words, "Quit")
		s.print("\n" + s.actions(words...) + " " + colBorder + "» |15")
		k, err := s.choose(keysOf(words...) + "\r")
		if err != nil {
			return err
		}
		switch k {
		case 'Z':
			if stop, err := s.awaitingApproval("Downloads"); stop || err != nil {
				return err
			}
			if err := s.downloadZmodem(f); err != nil {
				return err
			}
		case 'L':
			if stop, err := s.awaitingApproval("Downloads"); stop || err != nil {
				return err
			}
			if err := s.downloadLink(f); err != nil {
				return err
			}
		case 'Q', keyEnter:
			return nil
		}
	}
}

// filePath is where f is on disk, kept inside the file root.
func (s *session) filePath(f store.File) (string, bool) {
	root := filepath.Clean(s.srv.cfg.Files)
	p := filepath.Join(root, f.Path)
	if root == "" || !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "", false
	}
	return p, true
}

func (s *session) downloadZmodem(f store.File) error {
	path, ok := s.filePath(f)
	if !ok {
		return s.fileMissing()
	}
	fh, err := os.Open(path)
	if err != nil {
		return s.fileMissing()
	}
	defer fh.Close()
	fi, err := fh.Stat()
	if err != nil {
		return s.fileMissing()
	}
	s.setActivity("Downloading " + f.Name)
	s.printf("\n%sSending %s%s%s by ZMODEM. Start your client's download if it does not start by itself;\n"+
		"%spress Ctrl-X five times to cancel.\n", colInfo, colBright, safe(f.Name), colInfo, colDim)
	time.Sleep(500 * time.Millisecond) // let that line reach the screen first
	sent, err := s.zmodemSend(f.Name, fh, fi.Size(), fi.ModTime())
	s.print("\r\n")
	if err != nil {
		s.srv.log.Info("zmodem download failed", "user", s.user.Handle, "file", f.Name, "sent", sent, "err", err)
		s.printf("%sThe transfer did not finish (%s).\n", colAlert, safe(err.Error()))
		return s.pause()
	}
	if err := s.srv.store.CountDownload(f.ID); err != nil {
		s.srv.log.Warn("count download failed", "file", f.ID, "err", err)
	}
	s.srv.log.Info("zmodem download", "user", s.user.Handle, "file", f.Name, "bytes", sent)
	s.printf("%sSent %s (%s).\n", colOK, safe(f.Name), humanSize(sent))
	return s.pause()
}

func (s *session) downloadLink(f store.File) error {
	cfg := s.srv.cfg.FileLinks
	ttl := cfg.TTL
	if ttl <= 0 {
		ttl = defaultFileLinkTTL
	}
	exp := time.Now().Add(ttl)
	link := cfg.URL + filelink.Sign(cfg.Key, filelink.Link{FileID: f.ID, UserID: s.user.ID, Expires: exp})
	s.srv.log.Info("download link", "user", s.user.Handle, "file", f.Name)
	s.printf("\n%sDownload %s on the web, until %s:\n\n %s%s\n\n%sThe link is yours; it stops working by itself.\n",
		colInfo, safe(f.Name), exp.Format("2 Jan 15:04 MST"), colBright, link, colDim)
	return s.pause()
}

func (s *session) fileMissing() error {
	s.printf("\n%sThat file is not on the disk any more.\n", colAlert)
	return s.pause()
}

// humanSize is a size the way a file listing shows it.
func humanSize(n int64) string {
	switch {
	case n < 1<<10:
		return fmt.Sprintf("%dB", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1fK", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1fM", float64(n)/(1<<20))
	default:
		return fmt.Sprintf("%.1fG", float64(n)/(1<<30))
	}
}
