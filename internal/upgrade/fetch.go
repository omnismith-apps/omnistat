package upgrade

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// Download limits (FR-010): today's archives are about 8 MiB.
const (
	archiveLimit   = 256 << 20
	binaryLimit    = 256 << 20
	archiveTimeout = 15 * time.Minute
)

// Errors of the verification (FR-009).
var (
	ErrChecksum = errors.New("the archive does not match its SHA-256 in checksums.txt")
	ErrNoBinary = errors.New("the archive holds no omnistat binary")
)

// BinaryName is the program inside the archive for goos.
func BinaryName(goos string) string {
	if goos == "windows" {
		return "omnistat.exe"
	}
	return "omnistat"
}

// Fetch downloads rel's archive into dir, verifies it against rel.SHA256 and
// unpacks only the binary, returning its path (FR-009, FR-010). The archive
// itself is removed. dir must be a staging directory only root can write
// (Stage, FR-011).
func Fetch(ctx context.Context, c *http.Client, rel Release, dir, goos string) (string, error) {
	archive := filepath.Join(dir, rel.Asset)
	defer os.Remove(archive)
	if err := download(ctx, c, rel, archive); err != nil {
		return "", err
	}
	bin := filepath.Join(dir, BinaryName(goos))
	var err error
	if goos == "windows" {
		err = unzip(archive, BinaryName(goos), bin)
	} else {
		err = untar(archive, BinaryName(goos), bin)
	}
	if err != nil {
		_ = os.Remove(bin)
		return "", fmt.Errorf("unpacking %s: %w", rel.Asset, err)
	}
	return bin, nil
}

// download streams the archive to path, hashing it on the way.
func download(ctx context.Context, c *http.Client, rel Release, path string) error {
	ctx, cancel := context.WithTimeout(ctx, archiveTimeout)
	defer cancel()
	body, err := get(ctx, c, rel.ArchiveURL, archiveLimit)
	if err != nil {
		return fmt.Errorf("downloading the archive: %w", err)
	}
	defer body.Close()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // our staging directory
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("downloading the archive: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != rel.SHA256 {
		return fmt.Errorf("%w (%s: expected %s, got %s)", ErrChecksum, rel.Asset, rel.SHA256, got)
	}
	return nil
}

// untar extracts the top-level regular file name from a .tar.gz to dst.
func untar(archive, name, dst string) error {
	f, err := os.Open(archive) //nolint:gosec // our staging file
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return ErrNoBinary
		}
		if err != nil {
			return err
		}
		if hdr.Typeflag == tar.TypeReg && filepath.Clean(hdr.Name) == name {
			return writeBinary(dst, tr)
		}
	}
}

// unzip extracts the top-level regular file name from a .zip to dst.
func unzip(archive, name, dst string) error {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return err
	}
	defer z.Close()
	for _, zf := range z.File {
		if zf.Name != name || !zf.Mode().IsRegular() {
			continue
		}
		r, err := zf.Open()
		if err != nil {
			return err
		}
		defer r.Close()
		return writeBinary(dst, r)
	}
	return ErrNoBinary
}

func writeBinary(dst string, r io.Reader) error {
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700) //nolint:gosec // an executable, in a root-only directory
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(r, binaryLimit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > binaryLimit {
		err = fmt.Errorf("the binary is larger than %d bytes", binaryLimit)
	}
	return err
}
