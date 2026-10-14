// Package syncer - on-disk layout of sites and clones
package syncer

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

const (
	reposDirName  = ".repos"
	tmpRepoPrefix = ".tmp-"
	oldRepoPrefix = ".old-"
	tmpLinkPrefix = ".tmp-link-"
)

// normalizeSubpath turns spec.path into a directory relative to the
// repository root. The empty string means the root itself.
func normalizeSubpath(p string) (string, error) {
	for _, segment := range strings.Split(p, "/") {
		if segment == ".." {
			return "", fmt.Errorf("path %q must not contain '..'", p)
		}
	}
	cleaned := strings.TrimPrefix(path.Clean("/"+p), "/")
	if strings.HasPrefix(cleaned, "-") {
		return "", fmt.Errorf("path %q must not start with '-'", p)
	}
	return cleaned, nil
}

// tempName returns "<prefix><site>-<8 hex>" for temporary entries.
func tempName(prefix, siteName string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return prefix + siteName + "-" + hex.EncodeToString(b)
}

// siteFromTempName returns the site a temporary entry belongs to.
func siteFromTempName(prefix, entry string) (string, bool) {
	rest, ok := strings.CutPrefix(entry, prefix)
	if !ok || len(rest) < 10 || rest[len(rest)-9] != '-' {
		return "", false
	}
	if _, err := hex.DecodeString(rest[len(rest)-8:]); err != nil {
		return "", false
	}
	return rest[:len(rest)-9], true
}

// replaceSymlink points link at target with an atomic rename. A directory at
// link (layout before sparse clones) is moved aside first and deleted only
// after the symlink is in place, so the outage does not last as long as the
// recursive delete. A failed delete is left for the leftover sweep.
func replaceSymlink(target, link string) error {
	dir := filepath.Dir(link)
	aside := ""
	if info, err := os.Lstat(link); err == nil && info.IsDir() {
		aside = filepath.Join(dir, tempName(tmpLinkPrefix, filepath.Base(link)))
		if err := os.Rename(link, aside); err != nil {
			return fmt.Errorf("move legacy directory %s aside: %w", link, err)
		}
	}
	tmp := filepath.Join(dir, tempName(tmpLinkPrefix, filepath.Base(link)))
	if err := os.Symlink(target, tmp); err != nil {
		restoreAside(aside, link)
		return err
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		restoreAside(aside, link)
		return err
	}
	if aside != "" {
		_ = os.RemoveAll(aside)
	}
	return nil
}

// restoreAside puts a moved-aside legacy directory back so it keeps serving.
func restoreAside(aside, link string) {
	if aside != "" {
		_ = os.Rename(aside, link)
	}
}
