package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	repo               = "UnitSense/agent"
	cosignOIDCIssuer   = "https://token.actions.githubusercontent.com"
	cosignBootstrapVer = "3.1.3"
)

// cosignBootstrapSHA256 mirrors install-agent.sh's pinned, hand-verified
// cosign checksums -- used only to bootstrap cosign on a machine that
// doesn't already have it on PATH. darwin/linux only: Windows installs
// verify via goreleaser's published checksums.txt instead, matching
// install-agent.ps1, since a signing-identity check isn't available there
// today.
func cosignBootstrapSHA256(osArch string) string {
	switch osArch {
	case "darwin-amd64":
		return "2347488e5d5b25336644024dfeca5601b190e91197a71a917bda44744aff106c"
	case "darwin-arm64":
		return "5cf948c2f4dfe59687bdd0b8523709067383e03982cc543475c8a7dc70e92a76"
	case "linux-amd64":
		return "4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71"
	case "linux-arm64":
		return "c5d324e091826b0d7a78eb16fef316450b4eb9aaec045611c08ba06f5e73220a"
	default:
		return ""
	}
}

// ReleaseFetcher downloads and verifies a UnitSense/agent release the same
// way install-agent.sh / install-agent.ps1 already do for a fresh install,
// reused here so a running agent can fetch and verify its own replacement.
type ReleaseFetcher struct {
	HTTPClient *http.Client
}

func NewReleaseFetcher() *ReleaseFetcher {
	return &ReleaseFetcher{HTTPClient: &http.Client{Timeout: 2 * time.Minute}}
}

func (f *ReleaseFetcher) client() *http.Client {
	if f.HTTPClient != nil {
		return f.HTTPClient
	}
	return http.DefaultClient
}

func (f *ReleaseFetcher) Fetch(version string) (string, error) {
	osName, arch, err := platformNames()
	if err != nil {
		return "", err
	}

	tag := "v" + version
	ext := "tar.gz"
	if osName == "windows" {
		ext = "zip"
	}
	archiveName := fmt.Sprintf("unitsense-agent_%s_%s_%s.%s", version, osName, arch, ext)
	baseURL := fmt.Sprintf("https://github.com/%s/releases/download/%s", repo, tag)

	dir, err := os.MkdirTemp("", "unitsense-agent-update-")
	if err != nil {
		return "", err
	}
	archivePath := filepath.Join(dir, archiveName)
	if err := f.download(baseURL+"/"+archiveName, archivePath); err != nil {
		os.RemoveAll(dir)
		return "", err
	}

	if osName == "windows" {
		if err := f.verifyChecksum(baseURL, archiveName, archivePath); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	} else {
		if err := f.verifyCosign(baseURL, archiveName, archivePath, tag, dir, osName, arch); err != nil {
			os.RemoveAll(dir)
			return "", err
		}
	}

	binName := "unitsense-agent"
	if osName == "windows" {
		binName = "unitsense-agent.exe"
	}
	binPath, err := extractBinary(archivePath, dir, binName)
	if err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	if err := os.Chmod(binPath, 0755); err != nil {
		os.RemoveAll(dir)
		return "", err
	}
	return binPath, nil
}

func platformNames() (osName, arch string, err error) {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		osName = runtime.GOOS
	default:
		return "", "", fmt.Errorf("unsupported OS: %s", runtime.GOOS)
	}
	switch runtime.GOARCH {
	case "amd64", "arm64":
		arch = runtime.GOARCH
	default:
		return "", "", fmt.Errorf("unsupported arch: %s", runtime.GOARCH)
	}
	if osName == "windows" && arch == "arm64" {
		return "", "", fmt.Errorf("no published release for windows/arm64")
	}
	return osName, arch, nil
}

func (f *ReleaseFetcher) download(url, dest string) error {
	resp, err := f.client().Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	out, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer out.Close()
	_, err = io.Copy(out, resp.Body)
	return err
}

func (f *ReleaseFetcher) downloadToString(url string) (string, error) {
	resp, err := f.client().Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("download %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

// verifyChecksum matches install-agent.ps1's Windows verification: compare
// the downloaded archive's SHA256 against goreleaser's published
// checksums.txt for this release.
func (f *ReleaseFetcher) verifyChecksum(baseURL, archiveName, archivePath string) error {
	checksums, err := f.downloadToString(baseURL + "/checksums.txt")
	if err != nil {
		return fmt.Errorf("fetch checksums.txt: %w", err)
	}
	var expected string
	for _, line := range strings.Split(checksums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == archiveName {
			expected = strings.ToLower(fields[0])
			break
		}
	}
	if expected == "" {
		return fmt.Errorf("no checksum entry for %s", archiveName)
	}
	actual, err := sha256File(archivePath)
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("checksum mismatch for %s: expected %s, got %s", archiveName, expected, actual)
	}
	return nil
}

func sha256File(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	h := sha256.New()
	if _, err := io.Copy(h, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// verifyCosign matches install-agent.sh's mac/linux verification: download
// the .sig/.crt siblings and run `cosign verify-blob` pinned to the
// UnitSense/agent release workflow identity. Bootstraps a pinned,
// checksum-verified cosign binary if one isn't already on PATH -- same
// bootstrap-only-if-missing behavior and pinned checksums as the shell
// installer.
func (f *ReleaseFetcher) verifyCosign(baseURL, archiveName, archivePath, tag, workDir, osName, arch string) error {
	sigPath := archivePath + ".sig"
	crtPath := archivePath + ".crt"
	if err := f.download(baseURL+"/"+archiveName+".sig", sigPath); err != nil {
		return fmt.Errorf("download signature: %w", err)
	}
	if err := f.download(baseURL+"/"+archiveName+".crt", crtPath); err != nil {
		return fmt.Errorf("download certificate: %w", err)
	}

	cosignBin, err := f.resolveCosign(workDir, osName, arch)
	if err != nil {
		return err
	}

	identity := fmt.Sprintf("https://github.com/%s/.github/workflows/release.yml@refs/tags/%s", repo, tag)
	cmd := exec.Command(cosignBin, "verify-blob",
		"--certificate-identity", identity,
		"--certificate-oidc-issuer", cosignOIDCIssuer,
		"--signature", sigPath,
		"--certificate", crtPath,
		archivePath,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("cosign verification failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (f *ReleaseFetcher) resolveCosign(workDir, osName, arch string) (string, error) {
	if path, err := exec.LookPath("cosign"); err == nil {
		return path, nil
	}

	key := osName + "-" + arch
	expected := cosignBootstrapSHA256(key)
	if expected == "" {
		return "", fmt.Errorf("cosign not found and no bootstrap binary available for %s", key)
	}
	url := fmt.Sprintf("https://github.com/sigstore/cosign/releases/download/v%s/cosign-%s-%s", cosignBootstrapVer, osName, arch)
	dest := filepath.Join(workDir, "cosign-bootstrap")
	if err := f.download(url, dest); err != nil {
		return "", fmt.Errorf("bootstrap cosign: %w", err)
	}
	actual, err := sha256File(dest)
	if err != nil {
		return "", err
	}
	if actual != expected {
		return "", fmt.Errorf("cosign bootstrap checksum mismatch: expected %s, got %s -- refusing to proceed", expected, actual)
	}
	if err := os.Chmod(dest, 0755); err != nil {
		return "", err
	}
	return dest, nil
}

// extractBinary pulls binName out of a tar.gz or zip archive into destDir,
// returning its path. The binary may be at the archive root or nested,
// same tolerance install-agent.ps1 already has for the zip case.
func extractBinary(archivePath, destDir, binName string) (string, error) {
	if strings.HasSuffix(archivePath, ".zip") {
		return extractFromZip(archivePath, destDir, binName)
	}
	return extractFromTarGz(archivePath, destDir, binName)
}

func extractFromTarGz(archivePath, destDir, binName string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	gz, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if filepath.Base(hdr.Name) != binName {
			continue
		}
		destPath := filepath.Join(destDir, binName)
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return "", err
		}
		out.Close()
		return destPath, nil
	}
	return "", fmt.Errorf("%s not found in archive", binName)
}

func extractFromZip(archivePath, destDir, binName string) (string, error) {
	zr, err := zip.OpenReader(archivePath)
	if err != nil {
		return "", err
	}
	defer zr.Close()
	for _, zf := range zr.File {
		if filepath.Base(zf.Name) != binName {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return "", err
		}
		destPath := filepath.Join(destDir, binName)
		out, err := os.OpenFile(destPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
		if err != nil {
			rc.Close()
			return "", err
		}
		_, copyErr := io.Copy(out, rc)
		rc.Close()
		out.Close()
		if copyErr != nil {
			return "", copyErr
		}
		return destPath, nil
	}
	return "", fmt.Errorf("%s not found in archive", binName)
}
