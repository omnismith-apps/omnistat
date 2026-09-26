// Package upgradetest fakes a releases location for tests of spec 009: an
// httptest server with GitHub's download layout, serving archives built in
// memory.
package upgradetest

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
)

// File is one archive entry.
type File struct {
	Name string
	Data []byte
}

// TarGz builds a .tar.gz of files.
func TarGz(files ...File) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, f := range files {
		_ = tw.WriteHeader(&tar.Header{Name: f.Name, Mode: 0o755, Size: int64(len(f.Data)), Typeflag: tar.TypeReg})
		_, _ = tw.Write(f.Data)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// Zip builds a .zip of files.
func Zip(files ...File) []byte {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, f := range files {
		w, _ := zw.Create(f.Name)
		_, _ = w.Write(f.Data)
	}
	_ = zw.Close()
	return buf.Bytes()
}

// Archive builds the release archive for goos holding binary as the program,
// with the other files the release ships.
func Archive(goos string, binary []byte) []byte {
	name := "omnistat"
	if goos == "windows" {
		name += ".exe"
		return Zip(File{"README.md", []byte("readme")}, File{name, binary}, File{"CHANGELOG.md", []byte("changes")})
	}
	return TarGz(File{"README.md", []byte("readme")}, File{name, binary}, File{"CHANGELOG.md", []byte("changes")})
}

// Sum is the hex SHA-256 of data.
func Sum(data []byte) string {
	s := sha256.Sum256(data)
	return hex.EncodeToString(s[:])
}

// Server is a fake releases location. Add releases before use; Latest names
// the tag latest/download resolves to.
type Server struct {
	*httptest.Server
	mu sync.Mutex
	// files by tag, then name
	files  map[string]map[string][]byte
	Latest string
	// BadSum makes checksums.txt of that tag list a wrong hash.
	BadSum map[string]bool
	hits   []string
}

// New starts an empty releases location.
func New() *Server {
	s := &Server{files: map[string]map[string][]byte{}, BadSum: map[string]bool{}}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	return s
}

// Add publishes a release tag whose archives for each "os/arch" hold binary.
func (s *Server) Add(tag string, binary []byte, platforms ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.files[tag] == nil {
		s.files[tag] = map[string][]byte{}
	}
	for _, p := range platforms {
		goos, goarch, _ := strings.Cut(p, "/")
		ext := ".tar.gz"
		if goos == "windows" {
			ext = ".zip"
		}
		s.files[tag]["omnistat_"+strings.TrimPrefix(tag, "v")+"_"+goos+"_"+goarch+ext] = Archive(goos, binary)
	}
}

// Hits lists the paths requested so far.
func (s *Server) Hits() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.hits...)
}

// Downloaded reports whether an archive was requested.
func (s *Server) Downloaded() bool {
	for _, h := range s.Hits() {
		if !strings.HasSuffix(h, "/checksums.txt") {
			return true
		}
	}
	return false
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hits = append(s.hits, r.URL.Path)
	var tag, name string
	if rest, ok := strings.CutPrefix(r.URL.Path, "/latest/download/"); ok {
		tag, name = s.Latest, rest
	} else if rest, ok := strings.CutPrefix(r.URL.Path, "/download/"); ok {
		tag, name, _ = strings.Cut(rest, "/")
	}
	files, ok := s.files[tag]
	if !ok {
		http.NotFound(w, r)
		return
	}
	if name == "checksums.txt" {
		names := make([]string, 0, len(files))
		for n := range files {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			sum := Sum(files[n])
			if s.BadSum[tag] {
				sum = Sum([]byte("tampered"))
			}
			fmt.Fprintf(w, "%s  %s\n", sum, n)
		}
		return
	}
	data, ok := files[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	_, _ = w.Write(data)
}
